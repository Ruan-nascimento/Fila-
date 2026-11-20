package analitico

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/database"
)

// Consultas agregadas. Regras comuns (docs/desktop/metricas.md):
//   - dinheiro em centavos (bigint), somado no banco;
//   - intervalos [início, fim) com instantes já convertidos do fuso da loja;
//   - recebimentos e estornos vêm das próprias tabelas (uma linha = um registro financeiro);
//     a comanda só entra (EXISTS, 1:1) quando há filtro de profissional: nada se duplica e, sem
//     filtro, a consulta lê só o índice da loja e do período (docs/desktop/desempenho.md);
//   - itens da comanda nunca entram no mesmo JOIN que recebimentos (evita multiplicar valores);
//   - profissional = o da comanda/fila FINAL (a transferência move os dois para o destino).
//
// Parâmetros posicionais comuns: $1 loja, $2 granularidade, $3 fuso, $4 início, $5 fim,
// $6 profissional (uuid ou NULL), $7 serviço (uuid ou NULL), $8 meio de pagamento ('' = todos).

type args struct {
	est   uuid.UUID
	gran  string
	tz    string
	start time.Time
	end   time.Time
	f     Filters
}

func (a args) list() []any {
	return []any{a.est, a.gran, a.tz, a.start, a.end, a.f.ProfessionalID, a.f.ServiceID, a.f.Method}
}

// ---------------------------------------------------------------------------------------------
// Caixa

// Recebimentos confirmados pela data de registro (Pix só existe aqui depois de pago; dinheiro e
// cartão, ao registrar). amount_cents é o valor recebido pela loja: o troco não entra.
const sqlReceipts = `
SELECT date_trunc($2, r.registered_at AT TIME ZONE $3) AS b, coalesce(sum(r.amount_cents), 0)::bigint, count(*)
FROM receipt r
WHERE r.establishment_id = $1 AND r.registered_at >= $4 AND r.registered_at < $5
  AND ($6::uuid IS NULL OR EXISTS (SELECT 1 FROM service_order o WHERE o.id = r.order_id AND o.professional_id = $6)) AND ($7::uuid IS NULL OR true) -- serviço: inaplicável (sem rateio)
  AND ($8 = '' OR r.method = $8)
GROUP BY 1`

// Estornos efetivados (status confirmed) pela data de efetivação. Solicitado, em análise ou
// falho não reduz o líquido.
const sqlRefunds = `
SELECT date_trunc($2, f.resolved_at AT TIME ZONE $3) AS b, coalesce(sum(f.amount_cents), 0)::bigint, count(*)
FROM refund f
WHERE f.establishment_id = $1 AND f.status = 'confirmed' AND f.resolved_at >= $4 AND f.resolved_at < $5
  AND ($6::uuid IS NULL OR EXISTS (SELECT 1 FROM service_order o WHERE o.id = f.order_id AND o.professional_id = $6)) AND ($7::uuid IS NULL OR true) -- serviço: inaplicável (sem rateio)
  AND ($8 = '' OR f.method = $8)
GROUP BY 1`

// Ticket: comandas liquidadas (settled) pela data de liquidação; total_cents é o valor cobrado
// atual (correções posteriores do dono já refletidas, na data original de liquidação).
const sqlTicket = `
SELECT date_trunc($2, o.closed_at AT TIME ZONE $3) AS b, coalesce(sum(o.total_cents), 0)::bigint, count(*)
FROM service_order o
WHERE o.establishment_id = $1 AND o.status = 'settled' AND o.closed_at >= $4 AND o.closed_at < $5
  AND ($6::uuid IS NULL OR o.professional_id = $6)
  AND ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = o.id AND i.service_id = $7 AND i.charged))
  AND ($8 = '' OR o.payment_method = $8)
GROUP BY 1`

// ---------------------------------------------------------------------------------------------
// Fila e atendimento (profissional = fila final; serviço = participação cuja comanda tem o item)

const partFilter = `
  AND ($6::uuid IS NULL OR q.professional_id = $6)
  AND ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = p.order_id AND i.service_id = $7 AND i.status <> 'removed'))
  AND ($8::text IS NULL OR true) -- meio de pagamento: inaplicável`

// Entradas na fila pelo horário de entrada.
const sqlEntries = `
SELECT date_trunc($2, p.created_at AT TIME ZONE $3) AS b, 0::bigint, count(*)
FROM participation p JOIN queue q ON q.id = p.queue_id
WHERE p.establishment_id = $1 AND p.created_at >= $4 AND p.created_at < $5` + partFilter + `
GROUP BY 1`

// Atendimentos concluídos pelo horário de conclusão.
const sqlCompleted = `
SELECT date_trunc($2, p.finished_at AT TIME ZONE $3) AS b, 0::bigint, count(*)
FROM participation p JOIN queue q ON q.id = p.queue_id
WHERE p.establishment_id = $1 AND p.status = 'completed' AND p.finished_at >= $4 AND p.finished_at < $5` + partFilter + `
GROUP BY 1`

// Espera = chamada − entrada, para participações chamadas no período (sem chamada: fora).
// Transferência não reinicia o relógio: conta desde a entrada original.
const sqlWait = `
SELECT date_trunc($2, p.called_at AT TIME ZONE $3) AS b,
       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM p.called_at - p.created_at)),
       percentile_cont(0.9) WITHIN GROUP (ORDER BY extract(epoch FROM p.called_at - p.created_at)),
       count(*)
FROM participation p JOIN queue q ON q.id = p.queue_id
WHERE p.establishment_id = $1 AND p.called_at IS NOT NULL AND p.called_at >= p.created_at
  AND p.called_at >= $4 AND p.called_at < $5` + partFilter + `
GROUP BY ROLLUP (1)`

// Duração = fim − início, só concluídos (interrompidos ficam à parte).
const sqlDuration = `
SELECT date_trunc($2, p.finished_at AT TIME ZONE $3) AS b,
       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM p.finished_at - p.started_at)) FILTER (WHERE p.status = 'completed'),
       count(*) FILTER (WHERE p.status = 'completed'),
       count(*) FILTER (WHERE p.status = 'interrupted')
FROM participation p JOIN queue q ON q.id = p.queue_id
WHERE p.establishment_id = $1 AND p.status IN ('completed', 'interrupted') AND p.started_at IS NOT NULL
  AND p.finished_at >= p.started_at AND p.finished_at >= $4 AND p.finished_at < $5` + partFilter + `
GROUP BY ROLLUP (1)`

// ---------------------------------------------------------------------------------------------
// Leitura

// sumRow é um bucket aditivo (valor em centavos + quantidade).
type sumRow struct {
	Cents int64
	Count int64
}

func sumByBucket(ctx context.Context, q database.Querier, sql string, a args) (map[string]sumRow, error) {
	rows, err := q.Query(ctx, sql, a.list()...)
	if err != nil {
		return nil, fmt.Errorf("consulta analítica: %w", err)
	}
	out := map[string]sumRow{}
	defer rows.Close()
	for rows.Next() {
		var b time.Time
		var r sumRow
		if err := rows.Scan(&b, &r.Cents, &r.Count); err != nil {
			return nil, err
		}
		out[localKey(b)] = r
	}
	return out, rows.Err()
}

// pctRow é um bucket de distribuição (mediana, p90, amostra, e um contador extra).
type pctRow struct {
	Median *float64
	P90    *float64
	Sample int64
	Extra  int64
}

// pctByBucket lê consultas com ROLLUP: a linha com bucket NULL é o total do período.
func pctByBucket(ctx context.Context, q database.Querier, sql string, a args, withP90 bool) (map[string]pctRow, pctRow, error) {
	rows, err := q.Query(ctx, sql, a.list()...)
	if err != nil {
		return nil, pctRow{}, fmt.Errorf("consulta analítica: %w", err)
	}
	defer rows.Close()
	out := map[string]pctRow{}
	var total pctRow
	for rows.Next() {
		var b *time.Time
		var r pctRow
		var err error
		if withP90 {
			err = rows.Scan(&b, &r.Median, &r.P90, &r.Sample)
		} else {
			err = rows.Scan(&b, &r.Median, &r.Sample, &r.Extra)
		}
		if err != nil {
			return nil, total, err
		}
		if b == nil {
			total = r
			continue
		}
		out[localKey(*b)] = r
	}
	return out, total, rows.Err()
}

func sumTotal(m map[string]sumRow) sumRow {
	var t sumRow
	for _, r := range m {
		t.Cents += r.Cents
		t.Count += r.Count
	}
	return t
}

// collectionStart é o início da coleta da loja (criação no sistema).
func collectionStart(ctx context.Context, q database.Querier, est uuid.UUID) (time.Time, string, string, error) {
	var created time.Time
	var tz, name string
	err := q.QueryRow(ctx, `SELECT created_at, timezone, name FROM establishment WHERE id = $1`, est).Scan(&created, &tz, &name)
	return created, tz, name, err
}
