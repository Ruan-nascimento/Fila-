package pagamento

import (
	"context"
	"time"

	"fila/internal/platform/database"
)

// Health são os indicadores financeiros para monitoramento (etapa 12). Só contagens e idades.
type Health struct {
	ChargesUnknown      int            // tentativas criando/desconhecidas (todas)
	ChargesStuck        int            // criando/desconhecidas há mais de 15 min
	OldestUnknown       time.Duration  // idade da tentativa desconhecida mais antiga
	OpenIssues          map[string]int // pendências abertas por tipo (divergências)
	OldestOpenIssue     time.Duration
	RefundsStuck        int // estorno pedido/processando/desconhecido há mais de 24 h
	RefundsUnderReview  int
	PaymentsLast24h     int
	ChargesCreated24h   int
	ChargesFailedLast1h int
}

// ReadHealth lê os indicadores financeiros de todas as lojas.
func ReadHealth(ctx context.Context, q database.Querier, now time.Time) (Health, error) {
	h := Health{OpenIssues: map[string]int{}}
	var oldestUnknown, oldestIssue *time.Time
	err := q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pix_charge WHERE status IN ('creating', 'unknown')),
		       (SELECT count(*) FROM pix_charge WHERE status IN ('creating', 'unknown') AND created_at < $1::timestamptz - interval '15 minutes'),
		       (SELECT min(created_at) FROM pix_charge WHERE status IN ('creating', 'unknown')),
		       (SELECT min(created_at) FROM financial_issue WHERE status = 'open'),
		       (SELECT count(*) FROM refund WHERE status IN ('requested', 'processing', 'unknown') AND requested_at < $1::timestamptz - interval '24 hours'),
		       (SELECT count(*) FROM refund WHERE status = 'under_review'),
		       (SELECT count(*) FROM receipt WHERE registered_at >= $1::timestamptz - interval '24 hours'),
		       (SELECT count(*) FROM pix_charge WHERE created_at >= $1::timestamptz - interval '24 hours'),
		       (SELECT count(*) FROM pix_charge WHERE status = 'failed' AND updated_at >= $1::timestamptz - interval '1 hour')`, now).
		Scan(&h.ChargesUnknown, &h.ChargesStuck, &oldestUnknown, &oldestIssue, &h.RefundsStuck, &h.RefundsUnderReview,
			&h.PaymentsLast24h, &h.ChargesCreated24h, &h.ChargesFailedLast1h)
	if err != nil {
		return h, err
	}
	if oldestUnknown != nil {
		h.OldestUnknown = now.Sub(*oldestUnknown)
	}
	if oldestIssue != nil {
		h.OldestOpenIssue = now.Sub(*oldestIssue)
	}
	rows, err := q.Query(ctx, `SELECT kind, count(*) FROM financial_issue WHERE status = 'open' GROUP BY kind`)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return h, err
		}
		h.OpenIssues[k] = n
	}
	return h, rows.Err()
}
