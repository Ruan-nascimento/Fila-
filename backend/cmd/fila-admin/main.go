// Comando fila-admin: processos controlados do operador (ADR-0013). Não há rota HTTP para eles.
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/fila-admin gerar-chave
//	go -C backend run ./cmd/fila-admin bootstrap -nome "Barbearia X" -slug barbearia-x -email dona@exemplo.com
//	go -C backend run ./cmd/fila-admin eventos
//	go -C backend run ./cmd/fila-admin nova-epoca
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"
	"time"
	_ "time/tzdata" // fusos horários embutidos (o Windows não traz a base IANA)

	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/identidade"
	"fila/internal/platform/app"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
	"fila/internal/platform/outbox"
)

const usage = `uso: fila-admin <comando> [opções]

comandos:
  gerar-chave   imprime uma chave secreta nova para FILA_SECRET_KEY
  bootstrap     cria a loja e envia o convite do dono inicial (uso único, 24 h)
                opções: -nome "Nome da loja" -slug endereco-da-loja -email dono@exemplo.com
                        [-fuso America/Sao_Paulo] [-motivo "texto para a auditoria"]
  transfer-owner     convida um novo dono para a loja (transferência da administração, T-OP-02)
                     opções: -slug endereco-da-loja -email novo-dono@exemplo.com -motivo "texto"
  close-establishment  encerra a loja (só sem ninguém na fila e sem comanda pendente)
                     opções: -slug endereco-da-loja -motivo "texto"
  eventos       mostra o atraso da publicação de eventos (outbox) e a posição do fluxo
  nova-epoca    troca a época do fluxo de eventos (a conciliação da restauração já faz isso)
  verificar     indicadores e alertas (saída 0 = sem alerta, 3 = alerta ativo)
                opções: [-backups /pasta/dos/backups] [-json]
  manutencao iniciar -motivo "texto"   trava o worker (nenhuma tarefa roda) para manutenção
  manutencao encerrar                  solta a trava de manutenção
  restauracao status                   mostra a trava e a conciliação
  restauracao travar -motivo "texto"   trava o worker por restauração de backup
  restauracao reconciliar [-desde AAAA-MM-DD] (-diario /pasta | -sem-diario) [-sem-pix]
                concilia o banco restaurado com o provedor de Pix, encerra sessões, troca a época
                do fluxo e reaplica as exclusões do diário
  restauracao liberar                  solta a trava (só depois de reconciliar)
  (passo a passo: docs/operacao/backup-restauracao.md)`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	switch args[0] {
	case "gerar-chave":
		if len(args) != 1 {
			fmt.Fprintln(stderr, usage)
			return app.ExitConfigError
		}
		return generateKey(stdout, stderr)
	case "bootstrap":
		return bootstrap(ctx, args[1:], environ, stdout, stderr)
	case "transfer-owner", "close-establishment":
		return ownerProcess(ctx, args[0], args[1:], environ, stdout, stderr)
	case "verificar":
		return verify(ctx, args[1:], environ, stdout, stderr)
	case "manutencao":
		return maintenance(ctx, args[1:], environ, stdout, stderr)
	case "restauracao":
		return restore(ctx, args[1:], environ, stdout, stderr)
	case "eventos", "nova-epoca":
		if len(args) != 1 {
			fmt.Fprintln(stderr, usage)
			return app.ExitConfigError
		}
		return events(ctx, args[0], environ, stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
}

// events mostra o atraso da outbox (monitoramento) ou troca a época do fluxo (restauração).
func events(ctx context.Context, cmd string, environ []string, stdout, stderr io.Writer) int {
	rt, code := app.Start("fila-admin", environ, stderr, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	if err := rt.CheckDatabase(ctx, rt.Config.Database.ConnectTimeout); err != nil {
		rt.Log.Error("banco indisponível ou sem migrações", "error", err.Error())
		return app.ExitFailure
	}
	if cmd == "nova-epoca" {
		epoch, err := outbox.NewEpoch(ctx, rt.Pool)
		if err != nil {
			rt.Log.Error("não foi possível trocar a época", "error", err.Error())
			return app.ExitFailure
		}
		fmt.Fprintf(stdout, "nova época do fluxo de eventos: %s\n", epoch)
		return app.ExitOK
	}
	lag, err := outbox.MeasureLag(ctx, rt.Pool, time.Now())
	if err != nil {
		rt.Log.Error("não foi possível medir o atraso", "error", err.Error())
		return app.ExitFailure
	}
	head, err := outbox.ReadHead(ctx, rt.Pool)
	if err != nil {
		rt.Log.Error("não foi possível ler o fluxo", "error", err.Error())
		return app.ExitFailure
	}
	fmt.Fprintf(stdout, "eventos esperando publicação: %d (mais antigo há %ds)\n", lag.Pending, int(lag.Oldest.Seconds()))
	fmt.Fprintf(stdout, "última posição publicada: %d · mais antiga guardada: %d · época: %s\n", head.Last, head.Oldest, head.Epoch)
	return app.ExitOK
}

// ownerProcess: processos controlados do dono (T-OP-02, ADR-0013), pedidos ao operador com
// identidade confirmada fora do sistema. Auditados com ator operator e motivo.
func ownerProcess(ctx context.Context, cmd string, args, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	slug := fs.String("slug", "", "endereço da loja")
	email := fs.String("email", "", "e-mail do novo dono (transfer-owner)")
	motivo := fs.String("motivo", "", "motivo registrado na auditoria")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *slug == "" || *motivo == "" || (cmd == "transfer-owner" && *email == "") {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	rt, code := app.Start("fila-admin", environ, stderr, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	if err := rt.CheckDatabase(ctx, rt.Config.Database.ConnectTimeout); err != nil {
		rt.Log.Error("banco indisponível ou sem migrações", "error", err.Error())
		return app.ExitFailure
	}
	estID, status, err := estabelecimento.BySlug(ctx, rt.Pool, *slug)
	if err != nil || status != "active" {
		fmt.Fprintln(stderr, "erro: loja não encontrada ou já encerrada")
		return app.ExitConfigError
	}
	if cmd == "transfer-owner" {
		ring, err := keys.NewRotating(rt.Config.Security.KeyMaterial())
		if err != nil {
			rt.Log.Error("chave do servidor inválida", "error", err.Error())
			return app.ExitConfigError
		}
		var name string
		_ = rt.Pool.QueryRow(ctx, `SELECT name FROM establishment WHERE id = $1`, estID).Scan(&name)
		svc := identidade.New(identidade.Deps{Pool: rt.Pool, Clock: clock.System{}, Ring: ring,
			Hasher: identidade.NewHasher(identidade.DefaultPasswordParams, 1), Log: rt.Log,
			Config: identidade.Config{CookieSecure: rt.Config.Security.CookieSecure, PublicBaseURL: rt.Config.Security.PublicBaseURL}})
		expires, err := svc.OperatorInviteOwner(ctx, estID, name, *email, *motivo)
		if err != nil {
			fmt.Fprintln(stderr, "erro:", err.Error())
			return app.ExitConfigError
		}
		fmt.Fprintf(stdout, "convite de dono enviado ao e-mail informado; vale até %s (UTC)\n", expires.Format("02/01/2006 15:04"))
		fmt.Fprintln(stdout, "depois que o novo dono aceitar, o dono atual pode excluir a própria conta")
		return app.ExitOK
	}
	now := time.Now().UTC()
	refused := ""
	err = database.InTx(ctx, rt.Pool, func(tx pgx.Tx) error {
		if err := estabelecimento.LockAllQueues(ctx, tx, estID); err != nil {
			return err
		}
		active, err := fila.ActiveInEstablishment(ctx, tx, estID)
		if err != nil {
			return err
		}
		pending, err := atendimento.PendingInEstablishment(ctx, tx, estID)
		if err != nil {
			return err
		}
		if active > 0 || pending > 0 {
			refused = fmt.Sprintf("há %d pessoa(s) na fila e %d comanda(s) pendente(s); resolva antes de encerrar", active, pending)
			return nil
		}
		if _, err := estabelecimento.CloseEstablishment(ctx, tx, estID, now); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOperator,
			Action: "T-OP-02", TargetType: "establishment", TargetID: estID, FromState: "active", ToState: "closed", Reason: *motivo})
	})
	if err != nil {
		rt.Log.Error("encerramento falhou; nada mudou", "error", err.Error())
		return app.ExitFailure
	}
	if refused != "" {
		fmt.Fprintln(stderr, "erro:", refused)
		return app.ExitConfigError
	}
	fmt.Fprintln(stdout, "loja encerrada")
	return app.ExitOK
}

// generateKey imprime 32 bytes aleatórios em base64url. A chave aparece só na saída do
// terminal de quem rodou o comando; nada é gravado.
func generateKey(stdout, stderr io.Writer) int {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintln(stderr, "não foi possível gerar números aleatórios:", err)
		return app.ExitFailure
	}
	fmt.Fprintln(stdout, base64.RawURLEncoding.EncodeToString(b))
	return app.ExitOK
}

// bootstrap cria a loja e o convite do dono inicial (T-OP-01). O token do convite nunca é
// impresso: ele só vai no e-mail (entregue pelo worker).
func bootstrap(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nome := fs.String("nome", "", "nome da loja")
	slug := fs.String("slug", "", "endereço público da loja (ex.: barbearia-centro)")
	email := fs.String("email", "", "e-mail de quem será o dono")
	fuso := fs.String("fuso", "America/Sao_Paulo", "fuso horário IANA da loja")
	motivo := fs.String("motivo", "", "motivo registrado na auditoria (opcional)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *nome == "" || *slug == "" || *email == "" {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}

	rt, code := app.Start("fila-admin", environ, stderr, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	if err := rt.CheckDatabase(ctx, rt.Config.Database.ConnectTimeout); err != nil {
		rt.Log.Error("banco indisponível ou sem migrações", "error", err.Error())
		return app.ExitFailure
	}
	if rt.Config.Mail.Mode == config.MailDisabled {
		rt.Log.Warn("envio de e-mail desativado: o convite será registrado, mas não chegará ao dono")
	}
	ring, err := keys.NewRotating(rt.Config.Security.KeyMaterial())
	if err != nil {
		rt.Log.Error("chave do servidor inválida", "error", err.Error())
		return app.ExitConfigError
	}
	svc := identidade.New(identidade.Deps{Pool: rt.Pool, Clock: clock.System{}, Ring: ring,
		Hasher: identidade.NewHasher(identidade.DefaultPasswordParams, 1), Log: rt.Log,
		Config: identidade.Config{CookieSecure: rt.Config.Security.CookieSecure, PublicBaseURL: rt.Config.Security.PublicBaseURL}})

	res, err := svc.Bootstrap(ctx, identidade.BootstrapInput{Name: *nome, Slug: *slug, Timezone: *fuso, Email: *email, Reason: *motivo})
	var herr *httpserver.Error
	switch {
	case errors.Is(err, identidade.ErrSlugTaken):
		fmt.Fprintln(stderr, "erro: já existe uma loja com esse -slug; escolha outro")
		return app.ExitConfigError
	case errors.As(err, &herr):
		fmt.Fprintln(stderr, "erro: dados inválidos:")
		for _, f := range herr.Fields {
			fmt.Fprintf(stderr, "  - %s: %s %s\n", f.Field, f.Code, f.Message)
		}
		return app.ExitConfigError
	case err != nil:
		rt.Log.Error("bootstrap falhou; nada foi criado", "error", err.Error())
		return app.ExitFailure
	}
	fmt.Fprintf(stdout, "loja criada: %s\n", res.EstablishmentID)
	fmt.Fprintf(stdout, "convite de dono enviado ao e-mail informado; vale até %s (UTC)\n", res.InviteExpiresAt.Format("02/01/2006 15:04"))
	fmt.Fprintln(stdout, "o worker precisa estar rodando para entregar o e-mail")
	return app.ExitOK
}
