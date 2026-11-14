package main

// Comandos de operação (etapa 12, ADR-0026): verificar, manutencao e restauracao.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fila/internal/api"
	"fila/internal/exclusao"
	"fila/internal/fidelidade"
	"fila/internal/fila"
	"fila/internal/identidade"
	"fila/internal/operacao"
	"fila/internal/pagamento"
	"fila/internal/painel"
	"fila/internal/platform/app"
	"fila/internal/platform/clock"
	"fila/internal/platform/keys"
)

// ExitAlerts: `verificar` encontrou alerta ativo (diferente de falha ao rodar, que é 1).
const ExitAlerts = 3

// BackupMarker é o arquivo que o script de backup grava na pasta dos backups depois de um backup
// conferido (deploy/backup/backup.sh). Contém o instante UTC em RFC 3339.
const BackupMarker = "ULTIMO_BACKUP_OK"

// BackupMaxAge: sem backup conferido há mais que isso = alerta (backup diário + folga).
const BackupMaxAge = 26 * time.Hour

func startDB(ctx context.Context, environ []string, stderr io.Writer) (*app.Runtime, int) {
	rt, code := app.Start("fila-admin", environ, stderr, stderr)
	if rt == nil {
		return nil, code
	}
	if err := rt.CheckDatabase(ctx, rt.Config.Database.ConnectTimeout); err != nil {
		rt.Log.Error("banco indisponível ou sem migrações", "error", err.Error())
		rt.Close()
		return nil, app.ExitFailure
	}
	return rt, app.ExitOK
}

// verify: indicadores + alertas (para cron/monitor simples). Saída 0 = sem alerta, 3 = alerta.
func verify(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verificar", flag.ContinueOnError)
	fs.SetOutput(stderr)
	backups := fs.String("backups", "", "pasta dos backups (confere a idade do último backup conferido)")
	asJSON := fs.Bool("json", false, "saída em JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	rt, code := startDB(ctx, environ, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	now := time.Now().UTC()
	ind, err := operacao.Collect(ctx, rt.Pool, now)
	if err != nil {
		rt.Log.Error("não foi possível ler os indicadores", "error", err.Error())
		return app.ExitFailure
	}
	alerts := operacao.Evaluate(ind)
	if *backups != "" {
		alerts = append(alerts, backupAlerts(*backups, now)...)
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"at": now, "alerts": alerts, "indicators": ind})
	} else {
		printIndicators(stdout, ind)
		if len(alerts) == 0 {
			fmt.Fprintln(stdout, "\nnenhum alerta ativo")
		} else {
			fmt.Fprintf(stdout, "\n%d alerta(s) ativo(s):\n", len(alerts))
			for _, a := range alerts {
				fmt.Fprintf(stdout, "  [%s] %s — %s\n         responsável: %s · o que fazer: docs/operacao/rotinas.md#%s\n",
					a.Severity, a.Name, a.Summary, a.Owner, a.Runbook)
			}
		}
	}
	if len(alerts) > 0 {
		return ExitAlerts
	}
	return app.ExitOK
}

func backupAlerts(dir string, now time.Time) []operacao.Alert {
	raw, err := os.ReadFile(filepath.Join(dir, BackupMarker))
	if err != nil {
		return []operacao.Alert{{Name: "FilaBackupAusente", Severity: operacao.Critical, Owner: operacao.OwnerOperator,
			Runbook: "backup", Summary: "nenhum backup conferido encontrado na pasta informada"}}
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		return []operacao.Alert{{Name: "FilaBackupAusente", Severity: operacao.Critical, Owner: operacao.OwnerOperator,
			Runbook: "backup", Summary: "marcador do último backup ilegível"}}
	}
	if age := now.Sub(at); age > BackupMaxAge {
		return []operacao.Alert{{Name: "FilaBackupAtrasado", Severity: operacao.Critical, Owner: operacao.OwnerOperator,
			Runbook: "backup", Summary: fmt.Sprintf("último backup conferido há %s (máximo %s)", age.Round(time.Minute), BackupMaxAge)}}
	}
	return nil
}

func printIndicators(w io.Writer, ind operacao.Indicators) {
	st := ind.State
	state := "liberado"
	if st.Held() {
		state = "TRAVADO (" + reasonText(st) + ")"
	}
	fmt.Fprintf(w, "worker: %s", state)
	if st.WorkerSeenAt != nil {
		fmt.Fprintf(w, " · último sinal de vida há %s", ind.At.Sub(*st.WorkerSeenAt).Round(time.Second))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "eventos esperando publicação: %d (mais antigo há %s)\n", ind.OutboxLag.Pending, ind.OutboxLag.Oldest.Round(time.Second))
	p := ind.Payments
	fmt.Fprintf(w, "Pix: %d desconhecida(s), %d há mais de 15 min · estornos parados: %d · em revisão: %d\n",
		p.ChargesUnknown, p.ChargesStuck, p.RefundsStuck, p.RefundsUnderReview)
	fmt.Fprintf(w, "pendências financeiras abertas: %v\n", p.OpenIssues)
	d := ind.Deletions
	fmt.Fprintf(w, "exclusões em processamento: %d (mais antiga há %s; %d perto da meta de 30 dias)\n",
		d.Processing, d.Oldest.Round(time.Minute), d.NearTarget)
	r := ind.Reservations
	fmt.Fprintf(w, "reservas de prêmio: %d ativa(s), %d vencida(s) sem liberar\n", r.ActiveReservations, r.OverdueReservations)
	m := ind.Mail
	fmt.Fprintf(w, "e-mails: %d esperando (mais antigo há %s), %d desistido(s) em 24 h\n", m.Pending, m.OldestPending.Round(time.Second), m.FailedLast24h)
}

func reasonText(st operacao.State) string {
	switch {
	case st.HoldReason != nil && *st.HoldReason == operacao.HoldMaintenance:
		return "manutenção"
	case st.RestoreHold():
		return "restauração de backup"
	}
	return "?"
}

// maintenance: manutencao iniciar -motivo "…" | manutencao encerrar.
func maintenance(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "iniciar" && args[0] != "encerrar") {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	fs := flag.NewFlagSet("manutencao", flag.ContinueOnError)
	fs.SetOutput(stderr)
	motivo := fs.String("motivo", "", "motivo (auditoria)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || (args[0] == "iniciar" && len(strings.TrimSpace(*motivo)) < 3) {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	rt, code := startDB(ctx, environ, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	now := time.Now().UTC()
	if args[0] == "iniciar" {
		st, err := operacao.Hold(ctx, rt.Pool, operacao.HoldMaintenance, *motivo, now)
		if err != nil {
			rt.Log.Error("não foi possível travar", "error", err.Error())
			return app.ExitFailure
		}
		fmt.Fprintf(stdout, "worker travado (%s): as tarefas param em até 10 s, terminando a rodada atual\n", reasonText(st))
		return app.ExitOK
	}
	st, err := operacao.Read(ctx, rt.Pool)
	if err == nil && st.RestoreHold() {
		fmt.Fprintln(stderr, "erro: a trava é de RESTAURAÇÃO; use fila-admin restauracao (conciliar e liberar)")
		return app.ExitConfigError
	}
	if _, err := operacao.Release(ctx, rt.Pool, now); err != nil {
		fmt.Fprintln(stderr, "erro:", err.Error())
		return app.ExitConfigError
	}
	fmt.Fprintln(stdout, "worker liberado: as tarefas voltam em até 10 s")
	return app.ExitOK
}

// restore: restauracao status | travar -motivo | reconciliar [...] | liberar.
func restore(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	sub := args[0]
	fs := flag.NewFlagSet("restauracao", flag.ContinueOnError)
	fs.SetOutput(stderr)
	motivo := fs.String("motivo", "", "motivo (auditoria)")
	desde := fs.String("desde", "", "início da conciliação com o provedor (AAAA-MM-DD ou RFC 3339); padrão: último sinal de vida do worker no backup, menos 1 dia")
	diario := fs.String("diario", "", "pasta do diário de exclusões (padrão: FILA_DELETION_JOURNAL_DIR)")
	semDiario := fs.Bool("sem-diario", false, "confirmo que NÃO há diário de exclusões (exclusões pedidas depois do backup não serão reaplicadas)")
	semPix := fs.Bool("sem-pix", false, "confirmo que este sistema nunca teve Pix ligado (pula a conciliação com o provedor)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	switch sub {
	case "status", "travar", "reconciliar", "liberar":
	default:
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	if sub == "travar" && len(strings.TrimSpace(*motivo)) < 3 {
		fmt.Fprintln(stderr, "erro: informe -motivo (ex.: \"restaurado do backup de 14/11 03:00\")")
		return app.ExitConfigError
	}
	rt, code := startDB(ctx, environ, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	now := time.Now().UTC()
	st, err := operacao.Read(ctx, rt.Pool)
	if err != nil {
		rt.Log.Error("não foi possível ler o estado", "error", err.Error())
		return app.ExitFailure
	}
	switch sub {
	case "status":
		printState(stdout, st)
		return app.ExitOK
	case "travar":
		st, err := operacao.Hold(ctx, rt.Pool, operacao.HoldRestore, *motivo, now)
		if err != nil {
			rt.Log.Error("não foi possível travar", "error", err.Error())
			return app.ExitFailure
		}
		printState(stdout, st)
		return app.ExitOK
	case "liberar":
		if _, err := operacao.Release(ctx, rt.Pool, now); err != nil {
			fmt.Fprintln(stderr, "erro:", err.Error())
			return app.ExitConfigError
		}
		fmt.Fprintln(stdout, "worker liberado: as tarefas voltam em até 10 s")
		return app.ExitOK
	}

	// reconciliar
	if !st.RestoreHold() {
		fmt.Fprintln(stderr, "erro: "+operacao.ErrNoRestoreHold.Error())
		return app.ExitConfigError
	}
	var since time.Time
	if *desde != "" {
		if since, err = parseSince(*desde); err != nil {
			fmt.Fprintln(stderr, "erro:", err.Error())
			return app.ExitConfigError
		}
	}
	journalDir := *diario
	if journalDir == "" {
		journalDir = rt.Config.Ops.DeletionJournalDir
	}
	ring, err := keys.NewRotating(rt.Config.Security.KeyMaterial())
	if err != nil {
		rt.Log.Error("chave do servidor inválida", "error", err.Error())
		return app.ExitConfigError
	}
	clk := clock.System{}
	loyalty := fidelidade.New(fidelidade.Deps{Pool: rt.Pool, Clock: clk, Log: rt.Log})
	d := operacao.RestoreDeps{Pool: rt.Pool, Clock: clk, Log: rt.Log, NoPix: *semPix, JournalDir: journalDir,
		NoJournal: *semDiario, Since: since}
	if prov := api.PixProvider(rt.Config.Payments, nil); prov != nil {
		d.Payments = pagamento.New(pagamento.Deps{Pool: rt.Pool, Clock: clk, Log: rt.Log, Provider: prov, Loyalty: loyalty,
			ExpiresIn: rt.Config.Payments.ExpiresIn, Timeout: rt.Config.Payments.Timeout, Pendencies: painel.Record})
	}
	ident := identidade.New(identidade.Deps{Pool: rt.Pool, Clock: clk, Ring: ring, Log: rt.Log,
		Hasher: identidade.NewHasher(identidade.DefaultPasswordParams, 1),
		Config: identidade.Config{CookieSecure: rt.Config.Security.CookieSecure, PublicBaseURL: rt.Config.Security.PublicBaseURL}})
	d.Deletion = exclusao.New(exclusao.Deps{Pool: rt.Pool, Clock: clk, Log: rt.Log, Ring: ring, Identity: ident,
		Queue: fila.New(fila.Deps{Pool: rt.Pool, Clock: clk, Log: rt.Log}), Benefits: loyalty})
	if err := operacao.Reconcile(ctx, d, func(l string) { fmt.Fprintln(stdout, "· "+l) }); err != nil {
		fmt.Fprintln(stderr, "erro: "+err.Error())
		fmt.Fprintln(stderr, "a trava continua; corrija e rode de novo (cada passo é repetível)")
		return app.ExitFailure
	}
	fmt.Fprintln(stdout, "conciliação concluída. Confira as pendências no painel do dono e então rode: fila-admin restauracao liberar")
	return app.ExitOK
}

func parseSince(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("-desde: use AAAA-MM-DD ou RFC 3339")
}

func printState(w io.Writer, st operacao.State) {
	if !st.Held() {
		fmt.Fprintln(w, "worker: liberado")
	} else {
		fmt.Fprintf(w, "worker: TRAVADO (%s)\n", reasonText(st))
		if st.HoldNote != nil {
			fmt.Fprintf(w, "motivo: %s\n", *st.HoldNote)
		}
		if st.HoldSince != nil {
			fmt.Fprintf(w, "desde: %s UTC\n", st.HoldSince.Format("2006-01-02 15:04:05"))
		}
		if st.RestoreHold() {
			if st.ReconciledAt != nil && st.HoldSince != nil && !st.ReconciledAt.Before(*st.HoldSince) {
				fmt.Fprintf(w, "conciliação: feita em %s UTC (pode liberar)\n", st.ReconciledAt.Format("2006-01-02 15:04:05"))
			} else {
				fmt.Fprintln(w, "conciliação: PENDENTE (fila-admin restauracao reconciliar)")
			}
		}
	}
	if st.BoundMismatch {
		fmt.Fprintln(w, "banco: diferente do vinculado (restaurado ou copiado)")
	}
	if st.WorkerSeenAt != nil {
		fmt.Fprintf(w, "último sinal de vida do worker gravado neste banco: %s UTC\n", st.WorkerSeenAt.Format("2006-01-02 15:04:05"))
	}
}
