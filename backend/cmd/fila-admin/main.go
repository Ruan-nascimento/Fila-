// Comando fila-admin: processos controlados do operador (ADR-0013). Não há rota HTTP para eles.
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/fila-admin gerar-chave
//	go -C backend run ./cmd/fila-admin bootstrap -nome "Barbearia X" -slug barbearia-x -email dona@exemplo.com
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
	_ "time/tzdata" // fusos horários embutidos (o Windows não traz a base IANA)

	"fila/internal/identidade"
	"fila/internal/platform/app"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
)

const usage = `uso: fila-admin <comando> [opções]

comandos:
  gerar-chave   imprime uma chave secreta nova para FILA_SECRET_KEY
  bootstrap     cria a loja e envia o convite do dono inicial (uso único, 24 h)
                opções: -nome "Nome da loja" -slug endereco-da-loja -email dono@exemplo.com
                        [-fuso America/Sao_Paulo] [-motivo "texto para a auditoria"]`

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
	default:
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
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
	ring, err := keys.New([]byte(rt.Config.Security.SecretKey.Reveal()))
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
