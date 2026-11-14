package fidelidade

import (
	"context"
	"time"

	"fila/internal/platform/database"
)

// Health são os indicadores de prêmios (etapa 12).
type Health struct {
	ActiveReservations  int
	OverdueReservations int           // ativas com validade vencida há mais de 5 min (worker de vencimento parado?)
	OldestOverdue       time.Duration // há quanto tempo a mais atrasada venceu
	ExpiredLast24h      int
}

// ReadHealth lê os indicadores das reservas.
func ReadHealth(ctx context.Context, q database.Querier, now time.Time) (Health, error) {
	var h Health
	var oldest *time.Time
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'active'),
		       count(*) FILTER (WHERE status = 'active' AND expires_at < $1::timestamptz - interval '5 minutes'),
		       min(expires_at) FILTER (WHERE status = 'active' AND expires_at < $1::timestamptz),
		       count(*) FILTER (WHERE status = 'expired' AND expires_at >= $1::timestamptz - interval '24 hours')
		FROM reward_reservation`, now).Scan(&h.ActiveReservations, &h.OverdueReservations, &oldest, &h.ExpiredLast24h)
	if oldest != nil {
		h.OldestOverdue = now.Sub(*oldest)
	}
	return h, err
}
