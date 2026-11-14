package mail

import (
	"context"
	"time"

	"fila/internal/platform/database"
)

// Health são os indicadores da caixa de saída de e-mail (etapa 12).
type Health struct {
	Pending       int
	OldestPending time.Duration
	FailedLast24h int
}

// ReadHealth lê os indicadores da caixa de saída.
func ReadHealth(ctx context.Context, q database.Querier, now time.Time) (Health, error) {
	var h Health
	var oldest *time.Time
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE sent_at IS NULL AND failed_at IS NULL),
		       min(created_at) FILTER (WHERE sent_at IS NULL AND failed_at IS NULL),
		       count(*) FILTER (WHERE failed_at >= $1::timestamptz - interval '24 hours')
		FROM mail_outbox`, now).Scan(&h.Pending, &oldest, &h.FailedLast24h)
	if oldest != nil {
		h.OldestPending = now.Sub(*oldest)
	}
	return h, err
}
