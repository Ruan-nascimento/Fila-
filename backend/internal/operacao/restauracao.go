package operacao

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/exclusao"
	"fila/internal/identidade"
	"fila/internal/pagamento"
	"fila/internal/platform/clock"
	"fila/internal/platform/outbox"
)

// RestoreDeps são as peças da conciliação pós-restauração.
type RestoreDeps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	// Payments com provedor configurado; nil só com NoPix (o operador confirma que nunca houve Pix).
	Payments *pagamento.Service
	NoPix    bool
	// Deletion + JournalDir reaplicam as exclusões do diário; NoJournal confirma que não há diário.
	Deletion   *exclusao.Service
	JournalDir string
	NoJournal  bool
	// Since: desde quando listar cobranças no provedor (zero = último sinal de vida do worker − 1 dia).
	Since time.Time
}

// ErrNoRestoreHold: conciliação só com o worker travado por restauração.
var ErrNoRestoreHold = errors.New("o worker não está travado por restauração; trave antes (fila-admin restauracao travar)")

// Reconcile executa a conciliação pós-restauração, na ordem segura, e registra a conclusão.
// Cada passo é repetível; se um falhar, nada é marcado como conciliado e a trava continua.
//  1. época nova do fluxo SSE (cursor antigo → retrato novo);
//  2. sessões e links de e-mail encerrados (saídas e trocas de senha depois do backup);
//  3. pagamentos com o provedor (o banco restaurado não "desfaz" pagamento recebido);
//  4. exclusões do diário reaplicadas.
func Reconcile(ctx context.Context, d RestoreDeps, report func(string)) error {
	st, err := Read(ctx, d.Pool)
	if err != nil {
		return err
	}
	if !st.RestoreHold() {
		return ErrNoRestoreHold
	}
	since := d.Since
	if since.IsZero() {
		if st.WorkerSeenAt == nil {
			return errors.New("o backup não tem sinal de vida do worker; informe a data do backup (-desde)")
		}
		// Margem de 1 dia: o filtro do provedor é por dia e o sinal de vida pode ser de minutos antes do backup.
		since = st.WorkerSeenAt.Add(-24 * time.Hour)
	}
	if d.Payments == nil && !d.NoPix {
		return errors.New("pix desligado nesta configuração: configure o provedor para conciliar, ou confirme que nunca houve Pix (-sem-pix)")
	}
	if d.JournalDir == "" && !d.NoJournal {
		return errors.New("informe o diário de exclusões (-diario) ou confirme que não há (-sem-diario)")
	}
	var lines []string
	step := func(format string, a ...any) {
		l := fmt.Sprintf(format, a...)
		lines = append(lines, l)
		if report != nil {
			report(l)
		}
	}
	now := d.Clock.Now()

	epoch, err := outbox.NewEpoch(ctx, d.Pool)
	if err != nil {
		return fmt.Errorf("época do fluxo: %w", err)
	}
	step("época nova do fluxo de eventos: %s", epoch)

	sess, toks, err := identidade.RevokeAllAfterRestore(ctx, d.Pool, now)
	if err != nil {
		return fmt.Errorf("encerrar sessões: %w", err)
	}
	step("sessões encerradas: %d · links de e-mail invalidados: %d", sess, toks)

	if d.Payments != nil {
		rep, err := d.Payments.ReconcileAfterRestore(ctx, since)
		if err != nil {
			return fmt.Errorf("conciliação com o provedor (rode de novo quando ele responder): %w", err)
		}
		step("Pix desde %s: %d pago(s) e %d estornado(s) no provedor; %d tentativa(s) do banco consultada(s), %d seguem em verificação",
			since.Format("2006-01-02"), rep.ProviderPaid, rep.ProviderRefunded, rep.LocalChecked, rep.LocalUnresolved)
		step("pagamentos registrados agora: %d · recriadas do provedor: %d · órfãs: %d · estornos não registrados: %d",
			rep.PaymentsApplied, rep.Recreated, rep.Orphans, rep.RefundIssues)
		if len(rep.Unlinked) > 0 {
			step("ATENÇÃO: %d cobrança(s) do provedor sem vínculo (tratar à mão): %s", len(rep.Unlinked), strings.Join(rep.Unlinked, ", "))
		}
	} else {
		h, err := pagamento.ReadHealth(ctx, d.Pool, now)
		if err != nil {
			return err
		}
		if h.ChargesUnknown > 0 || h.ChargesCreated24h > 0 {
			return errors.New("há cobranças Pix no banco: configure o provedor para conciliar")
		}
		step("Pix: nunca ligado (confirmado pelo operador)")
	}

	if d.JournalDir != "" {
		entries, err := exclusao.ReadJournal(d.JournalDir)
		if err != nil {
			return fmt.Errorf("diário de exclusões: %w", err)
		}
		rep, err := d.Deletion.Reapply(ctx, entries, "reaplicada após restauração de backup")
		if err != nil {
			return fmt.Errorf("reaplicar exclusões: %w", err)
		}
		step("diário de exclusões: %d pedido(s); %d já no banco; %d reaplicado(s); %d de conta inexistente no backup",
			rep.InJournal, rep.AlreadyInDB, rep.Reapplied, rep.AccountAbsent)
		if len(rep.NeedsManual) > 0 {
			step("ATENÇÃO: %d exclusão(ões) precisam de ação manual: %s", len(rep.NeedsManual), strings.Join(rep.NeedsManual, ", "))
		}
	} else {
		step("diário de exclusões: não usado (confirmado pelo operador)")
	}

	sum := strings.Join(lines, " | ")
	if len(sum) > 500 {
		sum = sum[:497] + "..."
	}
	return MarkReconciled(ctx, d.Pool, sum, d.Clock.Now())
}
