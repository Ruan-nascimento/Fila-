package exclusao

import (
	"context"
	"time"

	"fila/internal/platform/database"
)

// Health são os indicadores das exclusões de conta (etapa 12).
type Health struct {
	Processing      int           // pedidos em processamento
	Oldest          time.Duration // idade do pedido em processamento mais antigo
	NearTarget      int           // em processamento há 25 dias ou mais (meta operacional de 30 dias)
	NotCheckedSince time.Duration // há quanto tempo o worker não reavalia o pedido mais esquecido (job parado?)
}

// ReadHealth lê os indicadores das exclusões.
func ReadHealth(ctx context.Context, q database.Querier, now time.Time) (Health, error) {
	var h Health
	var oldest, oldestCheck *time.Time
	err := q.QueryRow(ctx, `
		SELECT count(*), min(requested_at), count(*) FILTER (WHERE requested_at < $1::timestamptz - interval '25 days'),
		       min(coalesce(checked_at, requested_at))
		FROM deletion_request WHERE status = 'processing'`, now).Scan(&h.Processing, &oldest, &h.NearTarget, &oldestCheck)
	if oldest != nil {
		h.Oldest = now.Sub(*oldest)
	}
	if oldestCheck != nil {
		h.NotCheckedSince = now.Sub(*oldestCheck)
	}
	return h, err
}
