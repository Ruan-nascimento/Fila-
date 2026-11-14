package operacao

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/exclusao"
	"fila/internal/fidelidade"
	"fila/internal/pagamento"
	"fila/internal/platform/mail"
	"fila/internal/platform/metrics"
	"fila/internal/platform/outbox"
)

// Indicators reúne os indicadores de negócio e de tarefas lidos do banco (sem dado pessoal).
type Indicators struct {
	At           time.Time
	State        State
	OutboxLag    outbox.Lag
	Payments     pagamento.Health
	Deletions    exclusao.Health
	Reservations fidelidade.Health
	Mail         mail.Health
}

// Collect lê todos os indicadores numa leitura consistente o bastante para alertas.
func Collect(ctx context.Context, pool *pgxpool.Pool, now time.Time) (Indicators, error) {
	ind := Indicators{At: now}
	var err error
	if ind.State, err = Read(ctx, pool); err != nil {
		return ind, fmt.Errorf("estado: %w", err)
	}
	if ind.OutboxLag, err = outbox.MeasureLag(ctx, pool, now); err != nil {
		return ind, fmt.Errorf("outbox: %w", err)
	}
	if ind.Payments, err = pagamento.ReadHealth(ctx, pool, now); err != nil {
		return ind, fmt.Errorf("pagamentos: %w", err)
	}
	if ind.Deletions, err = exclusao.ReadHealth(ctx, pool, now); err != nil {
		return ind, fmt.Errorf("exclusões: %w", err)
	}
	if ind.Reservations, err = fidelidade.ReadHealth(ctx, pool, now); err != nil {
		return ind, fmt.Errorf("reservas: %w", err)
	}
	if ind.Mail, err = mail.ReadHealth(ctx, pool, now); err != nil {
		return ind, fmt.Errorf("e-mail: %w", err)
	}
	return ind, nil
}

// Severidades e responsáveis dos alertas (docs/operacao/monitoramento.md).
const (
	Critical = "critico" // agir agora (dinheiro, dados ou tela parada)
	Warning  = "aviso"   // agir no mesmo dia

	OwnerOperator = "operador"     // quem cuida do servidor
	OwnerShop     = "dono da loja" // vê no painel de pendências e decide
	OwnerBoth     = "operador e dono da loja"
)

// Alert é um alerta acionável: o que está errado, quem age e onde está o passo a passo.
type Alert struct {
	Name     string
	Severity string
	Owner    string
	Summary  string
	Runbook  string // seção em docs/operacao/rotinas.md
}

// Limiares dos alertas (os mesmos de deploy/monitoramento/alertas.yml).
const (
	OutboxLagMax          = time.Minute
	WorkerSilenceMax      = 2 * time.Minute
	MaintenanceHoldMax    = 2 * time.Hour
	IssueAgeMax           = 72 * time.Hour
	DeletionUncheckedMax  = 15 * time.Minute
	MailPendingMax        = 15 * time.Minute
	ReservationOverdueMax = 5 * time.Minute
)

// Evaluate aplica os limiares e devolve os alertas ativos (mais graves primeiro).
func Evaluate(ind Indicators) []Alert {
	var out []Alert
	add := func(name, sev, owner, runbook, format string, args ...any) {
		out = append(out, Alert{Name: name, Severity: sev, Owner: owner, Runbook: runbook, Summary: fmt.Sprintf(format, args...)})
	}
	st := ind.State
	switch {
	case st.RestoreHold():
		add("FilaWorkerTravadoRestauracao", Critical, OwnerOperator, "restauracao",
			"worker travado por restauração de backup: concilie e libere (fila-admin restauracao)")
	case st.Held() && st.HoldSince != nil && ind.At.Sub(*st.HoldSince) > MaintenanceHoldMax:
		add("FilaWorkerEmManutencaoLonga", Warning, OwnerOperator, "manutencao",
			"worker em manutenção há %s", round(ind.At.Sub(*st.HoldSince)))
	case !st.Held() && (st.WorkerSeenAt == nil || ind.At.Sub(*st.WorkerSeenAt) > WorkerSilenceMax):
		add("FilaWorkerSemSinal", Critical, OwnerOperator, "worker-parado",
			"worker sem sinal de vida há %s (nada de Pix, e-mail, exclusão ou vencimento está rodando)", silence(st, ind.At))
	}
	if ind.OutboxLag.Pending > 0 && ind.OutboxLag.Oldest > OutboxLagMax {
		add("FilaOutboxAtrasada", Critical, OwnerOperator, "worker-parado",
			"%d eventos sem publicar, o mais antigo há %s: as telas não se atualizam sozinhas", ind.OutboxLag.Pending, round(ind.OutboxLag.Oldest))
	}
	p := ind.Payments
	if p.ChargesStuck > 0 {
		add("FilaPixDesconhecido", Warning, OwnerBoth, "provedor-indisponivel",
			"%d cobrança(s) Pix com resultado desconhecido há mais de 15 min (provedor fora ou lento?)", p.ChargesStuck)
	}
	if total := sum(p.OpenIssues); total > 0 && p.OldestOpenIssue > IssueAgeMax {
		add("FilaDivergenciaFinanceiraAntiga", Warning, OwnerShop, "divergencia-financeira",
			"%d pendência(s) financeira(s) aberta(s), a mais antiga há %s", total, round(p.OldestOpenIssue))
	}
	if n := p.OpenIssues[pagamento.IssueRestoredOrphan] + p.OpenIssues[pagamento.IssueRestoredRefundUnrecorded]; n > 0 {
		add("FilaDivergenciaPosRestauracao", Critical, OwnerBoth, "restauracao",
			"%d pendência(s) da conciliação pós-restauração (pagamento ou estorno que o backup não tinha)", n)
	}
	if p.RefundsStuck > 0 {
		add("FilaEstornoParado", Warning, OwnerOperator, "provedor-indisponivel",
			"%d estorno(s) pedido(s) há mais de 24 h sem confirmação do provedor", p.RefundsStuck)
	}
	d := ind.Deletions
	if d.Processing > 0 && d.NotCheckedSince > DeletionUncheckedMax {
		add("FilaExclusaoSemProcessamento", Critical, OwnerOperator, "worker-parado",
			"exclusões de conta sem reavaliação há %s (tarefa de exclusão parada?)", round(d.NotCheckedSince))
	}
	if d.NearTarget > 0 {
		add("FilaExclusaoPertoDaMeta", Warning, OwnerBoth, "exclusao-atrasada",
			"%d exclusão(ões) em processamento há 25 dias ou mais (meta operacional de 30 dias)", d.NearTarget)
	}
	if r := ind.Reservations; r.OverdueReservations > 0 {
		add("FilaReservaVencidaPresa", Critical, OwnerOperator, "worker-parado",
			"%d reserva(s) de prêmio vencida(s) e não liberada(s) (pontos presos)", r.OverdueReservations)
	}
	m := ind.Mail
	if m.Pending > 0 && m.OldestPending > MailPendingMax {
		add("FilaEmailParado", Warning, OwnerOperator, "email",
			"%d e-mail(s) esperando envio, o mais antigo há %s", m.Pending, round(m.OldestPending))
	}
	if m.FailedLast24h > 0 {
		add("FilaEmailFalhou", Warning, OwnerOperator, "email", "%d e-mail(s) desistido(s) nas últimas 24 h", m.FailedLast24h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity == Critical && out[j].Severity != Critical })
	return out
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func round(d time.Duration) time.Duration { return d.Round(time.Second) }

func silence(st State, now time.Time) string {
	if st.WorkerSeenAt == nil {
		return "sempre (nunca rodou)"
	}
	return round(now.Sub(*st.WorkerSeenAt)).String()
}

// ---------------------------------------------------------------------------------------------
// Métricas do worker (lidas a cada rodada de Collect e servidas em FILA_METRICS_ADDR).

// Gauges são os medidores de negócio do worker.
type Gauges struct {
	g          map[string]*metrics.Gauge
	issues     *metrics.Gauge
	lastOK     *metrics.Gauge
	collectErr *metrics.Counter
}

// NewGauges registra os medidores.
func NewGauges(reg *metrics.Registry) *Gauges {
	names := map[string]string{
		"fila_outbox_pending":                  "Eventos esperando publicação.",
		"fila_outbox_oldest_seconds":           "Idade do evento mais antigo esperando publicação.",
		"fila_pix_charges_unknown":             "Tentativas Pix criando ou com resultado desconhecido.",
		"fila_pix_charges_stuck":               "Tentativas Pix desconhecidas há mais de 15 min.",
		"fila_pix_unknown_oldest_seconds":      "Idade da tentativa Pix desconhecida mais antiga.",
		"fila_financial_issue_oldest_seconds":  "Idade da pendência financeira aberta mais antiga.",
		"fila_refunds_stuck":                   "Estornos sem confirmação há mais de 24 h.",
		"fila_refunds_under_review":            "Estornos em revisão do dono.",
		"fila_deletions_processing":            "Exclusões de conta em processamento.",
		"fila_deletions_oldest_seconds":        "Idade da exclusão em processamento mais antiga.",
		"fila_deletions_near_target":           "Exclusões em processamento há 25 dias ou mais.",
		"fila_deletions_unchecked_seconds":     "Tempo desde a última reavaliação da exclusão mais esquecida.",
		"fila_reservations_active":             "Reservas de prêmio ativas.",
		"fila_reservations_overdue":            "Reservas vencidas há mais de 5 min e ainda não liberadas.",
		"fila_mail_pending":                    "E-mails esperando envio.",
		"fila_mail_oldest_pending_seconds":     "Idade do e-mail esperando há mais tempo.",
		"fila_mail_failed_24h":                 "E-mails desistidos nas últimas 24 h.",
		"fila_worker_hold":                     "Worker travado (1) ou liberado (0), por manutenção ou restauração.",
		"fila_worker_restore_hold":             "Worker travado por restauração de backup (1) ou não (0).",
		"fila_worker_hold_seconds":             "Há quanto tempo o worker está travado.",
		"fila_worker_last_seen_age_seconds":    "Idade do último sinal de vida do worker gravado no banco.",
		"fila_payments_received_24h":           "Recebimentos registrados nas últimas 24 h.",
		"fila_pix_charges_created_24h":         "Tentativas Pix criadas nas últimas 24 h.",
		"fila_pix_charges_failed_1h":           "Tentativas Pix que falharam na última hora.",
		"fila_alerts_active":                   "Alertas ativos pela avaliação interna (fila-admin verificar).",
		"fila_reservations_overdue_oldest_sec": "Há quanto tempo venceu a reserva mais atrasada.",
	}
	g := &Gauges{g: map[string]*metrics.Gauge{}}
	for n, help := range names {
		g.g[n] = reg.NewGauge(n, help)
	}
	g.issues = reg.NewGauge("fila_financial_issues_open", "Pendências financeiras abertas por tipo.", "kind")
	g.lastOK = reg.NewGauge("fila_indicators_last_success_timestamp_seconds", "Última leitura bem-sucedida dos indicadores (Unix).")
	g.collectErr = reg.NewCounter("fila_indicators_errors_total", "Falhas ao ler os indicadores do banco.")
	return g
}

// Update grava a leitura nos medidores.
func (g *Gauges) Update(ind Indicators) {
	set := func(n string, v float64) { g.g[n].Set(v) }
	b := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	set("fila_outbox_pending", float64(ind.OutboxLag.Pending))
	set("fila_outbox_oldest_seconds", ind.OutboxLag.Oldest.Seconds())
	p := ind.Payments
	set("fila_pix_charges_unknown", float64(p.ChargesUnknown))
	set("fila_pix_charges_stuck", float64(p.ChargesStuck))
	set("fila_pix_unknown_oldest_seconds", p.OldestUnknown.Seconds())
	set("fila_financial_issue_oldest_seconds", p.OldestOpenIssue.Seconds())
	set("fila_refunds_stuck", float64(p.RefundsStuck))
	set("fila_refunds_under_review", float64(p.RefundsUnderReview))
	set("fila_payments_received_24h", float64(p.PaymentsLast24h))
	set("fila_pix_charges_created_24h", float64(p.ChargesCreated24h))
	set("fila_pix_charges_failed_1h", float64(p.ChargesFailedLast1h))
	for _, k := range issueKinds {
		g.issues.Set(float64(p.OpenIssues[k]), k)
	}
	d := ind.Deletions
	set("fila_deletions_processing", float64(d.Processing))
	set("fila_deletions_oldest_seconds", d.Oldest.Seconds())
	set("fila_deletions_near_target", float64(d.NearTarget))
	set("fila_deletions_unchecked_seconds", d.NotCheckedSince.Seconds())
	r := ind.Reservations
	set("fila_reservations_active", float64(r.ActiveReservations))
	set("fila_reservations_overdue", float64(r.OverdueReservations))
	set("fila_reservations_overdue_oldest_sec", r.OldestOverdue.Seconds())
	m := ind.Mail
	set("fila_mail_pending", float64(m.Pending))
	set("fila_mail_oldest_pending_seconds", m.OldestPending.Seconds())
	set("fila_mail_failed_24h", float64(m.FailedLast24h))
	st := ind.State
	set("fila_worker_hold", b(st.Held()))
	set("fila_worker_restore_hold", b(st.RestoreHold()))
	hold := 0.0
	if st.HoldSince != nil {
		hold = ind.At.Sub(*st.HoldSince).Seconds()
	}
	set("fila_worker_hold_seconds", hold)
	seen := -1.0
	if st.WorkerSeenAt != nil {
		seen = ind.At.Sub(*st.WorkerSeenAt).Seconds()
	}
	set("fila_worker_last_seen_age_seconds", seen)
	set("fila_alerts_active", float64(len(Evaluate(ind))))
	g.lastOK.Set(float64(ind.At.Unix()))
}

// Failed conta uma leitura que falhou.
func (g *Gauges) Failed() { g.collectErr.Inc() }

// issueKinds: todos os tipos de pendência (séries sempre presentes, mesmo zeradas).
var issueKinds = []string{"duplicate_receipt", "late_receipt", "amount_mismatch", "charge_outcome_unresolved",
	"refund_under_review", "active_pix_on_method_switch", "adjustment_excess", pagamento.IssueRestoredOrphan,
	pagamento.IssueRestoredRefundUnrecorded}
