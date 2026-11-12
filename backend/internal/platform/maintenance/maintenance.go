// Package maintenance apaga registros técnicos vencidos (sem valor de negócio nem de auditoria).
package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/clock"
	"fila/internal/platform/database"
)

// MailRetention: e-mails entregues ou abandonados (já sem conteúdo) ficam este tempo para diagnóstico.
const MailRetention = 7 * 24 * time.Hour

// UndeliveredMailLimit: e-mail não entregue depois disso perde o sentido (os links vencem em até 24 h);
// o conteúdo cifrado é apagado.
const UndeliveredMailLimit = 48 * time.Hour

// Cleaner apaga contadores de tentativa vencidos, respostas de idempotência vencidas e
// e-mails antigos.
type Cleaner struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
}

// RunOnce faz uma rodada de limpeza e devolve quantas linhas foram afetadas.
func (c *Cleaner) RunOnce(ctx context.Context) (int64, error) {
	now := c.Clock.Now()
	var total int64
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM rate_limit WHERE expires_at <= $1`, []any{now}},
		{`DELETE FROM idempotency_record WHERE expires_at <= $1`, []any{now}},
		{`DELETE FROM mail_outbox WHERE (sent_at IS NOT NULL OR failed_at IS NOT NULL) AND created_at <= $1`, []any{now.Add(-MailRetention)}},
		{`UPDATE mail_outbox SET failed_at = $1, payload = NULL, last_error = 'vencido sem entrega'
		  WHERE sent_at IS NULL AND failed_at IS NULL AND created_at <= $2`, []any{now, now.Add(-UndeliveredMailLimit)}},
	} {
		tag, err := c.Pool.Exec(ctx, q.sql, q.args...)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// Run limpa periodicamente até ctx terminar.
func (c *Cleaner) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := c.RunOnce(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("limpeza falhou", "error", database.Describe(err))
		} else if n > 0 {
			c.Log.Info("registros vencidos removidos", "quantidade", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
