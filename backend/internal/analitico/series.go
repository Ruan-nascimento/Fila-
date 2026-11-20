package analitico

import (
	"context"
	"fmt"
	"time"

	"fila/internal/platform/database"
)

// bucketValues: bucket (hora local) → série → valor.
type bucketValues map[string]map[string]Value

type seriesDefinition struct {
	series []seriesDef
	// inapplicable: filtros sem atribuição confiável nesta métrica. Com qualquer um deles ativo,
	// os valores saem not_applicable (nunca o total sem filtro disfarçado de filtrado).
	inapplicable []string
	ignored      []string // mesmo conjunto, listado em meta.ignored_filters
	compute      func(ctx context.Context, q database.Querier, sc *scope, p Period, gran string) (bucketValues, map[string]Value, error)
	// zeroIsEmpty: sem observação num bucket = "empty" (mediana), senão 0 real (contagens/somas).
	zeroIsEmpty map[string]bool
}

func (d seriesDefinition) notApplicable(sc *scope) bool {
	for _, f := range d.inapplicable {
		if sc.active(f) {
			return true
		}
	}
	return false
}

func (d seriesDefinition) zero(key string) Value {
	if d.zeroIsEmpty[key] {
		return empty(0)
	}
	return ok(0)
}

var seriesDefs map[string]seriesDefinition

func init() {
	seriesDefs = map[string]seriesDefinition{
		"cash": {
			series: []seriesDef{{"received_cents", "Recebido", "cents"}, {"refunded_cents", "Estornado", "cents"},
				{"net_cents", "Líquido", "cents"}},
			inapplicable: []string{"service_id"}, ignored: []string{"service_id"},
			compute: computeCash,
		},
		"ticket": {
			series: []seriesDef{{"average_cents", "Ticket médio", "cents"}, {"settled_total_cents", "Valor cobrado", "cents"},
				{"settled_orders", "Comandas liquidadas", "count"}},
			compute: computeTicket, zeroIsEmpty: map[string]bool{"average_cents": true},
		},
		"movement": {
			series: []seriesDef{{"entries", "Entradas na fila (por horário de entrada)", "count"},
				{"completed", "Atendimentos concluídos (por horário de conclusão)", "count"}},
			inapplicable: []string{"method"}, ignored: []string{"method"},
			compute: computeMovement,
		},
		"wait": {
			series:       []seriesDef{{"median_seconds", "Espera mediana", "seconds"}, {"p90_seconds", "Espera (percentil 90)", "seconds"}},
			inapplicable: []string{"method"}, ignored: []string{"method"},
			compute: computeWait, zeroIsEmpty: map[string]bool{"median_seconds": true, "p90_seconds": true},
		},
		"duration": {
			series: []seriesDef{{"median_seconds", "Duração mediana (concluídos)", "seconds"},
				{"interrupted", "Atendimentos interrompidos", "count"}},
			inapplicable: []string{"method"}, ignored: []string{"method"},
			compute: computeDuration, zeroIsEmpty: map[string]bool{"median_seconds": true},
		},
		"loyalty": {
			series: []seriesDef{{"credited", "Pontos creditados", "points"}, {"used", "Pontos utilizados", "points"},
				{"returned", "Pontos devolvidos", "points"}, {"adjusted", "Ajustes (estorno de crédito e encerramento)", "points"},
				{"reservations", "Reservas criadas", "count"}, {"delivered", "Prêmios entregues", "count"}},
			inapplicable: []string{"professional_id", "service_id", "method"},
			ignored:      []string{"professional_id", "service_id", "method"},
			compute:      computeLoyalty,
		},
	}
}

func setVal(bv bucketValues, key, series string, v Value) {
	if bv[key] == nil {
		bv[key] = map[string]Value{}
	}
	bv[key][series] = v
}

func computeCash(ctx context.Context, q database.Querier, sc *scope, p Period, gran string) (bucketValues, map[string]Value, error) {
	a := sc.args(p, gran)
	rec, err := sumByBucket(ctx, q, sqlReceipts, a)
	if err != nil {
		return nil, nil, err
	}
	ref, err := sumByBucket(ctx, q, sqlRefunds, a)
	if err != nil {
		return nil, nil, err
	}
	bv := bucketValues{}
	keys := map[string]bool{}
	for k := range rec {
		keys[k] = true
	}
	for k := range ref {
		keys[k] = true
	}
	for k := range keys {
		r, f := rec[k], ref[k]
		setVal(bv, k, "received_cents", okN(float64(r.Cents), r.Count))
		setVal(bv, k, "refunded_cents", okN(float64(f.Cents), f.Count))
		setVal(bv, k, "net_cents", ok(float64(r.Cents-f.Cents)))
	}
	rt, ft := sumTotal(rec), sumTotal(ref)
	totals := map[string]Value{"received_cents": okN(float64(rt.Cents), rt.Count), "refunded_cents": okN(float64(ft.Cents), ft.Count),
		"net_cents": ok(float64(rt.Cents - ft.Cents))}
	return bv, applyScope(sc, p, totals, []string{"service_id"}), nil
}

// applyScope troca os totais por not_collected/not_applicable quando for o caso.
func applyScope(sc *scope, p Period, totals map[string]Value, inapplicable []string) map[string]Value {
	for _, f := range inapplicable {
		if sc.active(f) {
			for k := range totals {
				totals[k] = status("not_applicable")
			}
			return totals
		}
	}
	if sc.beforeCollection(p) {
		for k := range totals {
			totals[k] = status("not_collected")
		}
	}
	return totals
}

func computeTicket(ctx context.Context, q database.Querier, sc *scope, p Period, gran string) (bucketValues, map[string]Value, error) {
	rows, err := sumByBucket(ctx, q, sqlTicket, sc.args(p, gran))
	if err != nil {
		return nil, nil, err
	}
	bv := bucketValues{}
	for k, r := range rows {
		setVal(bv, k, "settled_total_cents", ok(float64(r.Cents)))
		setVal(bv, k, "settled_orders", ok(float64(r.Count)))
		setVal(bv, k, "average_cents", avg(r))
	}
	t := sumTotal(rows)
	totals := map[string]Value{"settled_total_cents": ok(float64(t.Cents)), "settled_orders": ok(float64(t.Count)), "average_cents": avg(t)}
	return bv, applyScope(sc, p, totals, nil), nil
}

// avg: divisão por zero → indisponível (empty), nunca 0.
func avg(r sumRow) Value {
	if r.Count == 0 {
		return empty(0)
	}
	return okN(float64(r.Cents)/float64(r.Count), r.Count)
}

func computeMovement(ctx context.Context, q database.Querier, sc *scope, p Period, gran string) (bucketValues, map[string]Value, error) {
	a := sc.args(p, gran)
	ent, err := sumByBucket(ctx, q, sqlEntries, a)
	if err != nil {
		return nil, nil, err
	}
	done, err := sumByBucket(ctx, q, sqlCompleted, a)
	if err != nil {
		return nil, nil, err
	}
	bv := bucketValues{}
	for k, r := range ent {
		setVal(bv, k, "entries", ok(float64(r.Count)))
	}
	for k, r := range done {
		setVal(bv, k, "completed", ok(float64(r.Count)))
	}
	totals := map[string]Value{"entries": ok(float64(sumTotal(ent).Count)), "completed": ok(float64(sumTotal(done).Count))}
	return bv, applyScope(sc, p, totals, []string{"method"}), nil
}

func computeWait(ctx context.Context, q database.Querier, sc *scope, p Period, gran string) (bucketValues, map[string]Value, error) {
	rows, total, err := pctByBucket(ctx, q, sqlWait, sc.args(p, gran), true)
	if err != nil {
		return nil, nil, err
	}
	bv := bucketValues{}
	for k, r := range rows {
		setVal(bv, k, "median_seconds", fromPtr(r.Median, r.Sample))
		setVal(bv, k, "p90_seconds", fromPtr(r.P90, r.Sample))
	}
	totals := map[string]Value{"median_seconds": fromPtr(total.Median, total.Sample), "p90_seconds": fromPtr(total.P90, total.Sample)}
	return bv, applyScope(sc, p, totals, []string{"method"}), nil
}

func computeDuration(ctx context.Context, q database.Querier, sc *scope, p Period, gran string) (bucketValues, map[string]Value, error) {
	rows, total, err := pctByBucket(ctx, q, sqlDuration, sc.args(p, gran), false)
	if err != nil {
		return nil, nil, err
	}
	bv := bucketValues{}
	for k, r := range rows {
		setVal(bv, k, "median_seconds", fromPtr(r.Median, r.Sample))
		setVal(bv, k, "interrupted", ok(float64(r.Extra)))
	}
	totals := map[string]Value{"median_seconds": fromPtr(total.Median, total.Sample), "interrupted": ok(float64(total.Extra))}
	return bv, applyScope(sc, p, totals, []string{"method"}), nil
}

// Fidelidade: tipos reais do extrato (loyalty_ledger.kind) e eventos datados das reservas.
//   - creditados: credit; utilizados: consume (|delta|); devolvidos: consume_reversal;
//   - ajustes: credit_reversal (estorno de crédito, |delta|) + account_closure (|delta|);
//   - hold/hold_release só reservam pontos e não entram (o consumo é o que conta);
//   - reservas pela criação; entregas pela resolução com status delivered.
func computeLoyalty(ctx context.Context, q database.Querier, sc *scope, p Period, gran string) (bucketValues, map[string]Value, error) {
	rows, err := q.Query(ctx, `
		SELECT date_trunc($2, occurred_at AT TIME ZONE $3) AS b,
		       coalesce(sum(balance_delta) FILTER (WHERE kind = 'credit'), 0)::bigint,
		       coalesce(sum(-balance_delta) FILTER (WHERE kind = 'consume'), 0)::bigint,
		       coalesce(sum(balance_delta) FILTER (WHERE kind = 'consume_reversal'), 0)::bigint,
		       coalesce(sum(abs(balance_delta)) FILTER (WHERE kind IN ('credit_reversal', 'account_closure')), 0)::bigint
		FROM loyalty_ledger WHERE establishment_id = $1 AND occurred_at >= $4 AND occurred_at < $5
		GROUP BY 1`, sc.est, gran, sc.tz, p.Start, p.EffectiveEnd)
	if err != nil {
		return nil, nil, fmt.Errorf("fidelidade: %w", err)
	}
	bv := bucketValues{}
	tot := map[string]int64{}
	add := func(k, s string, n int64) {
		setVal(bv, k, s, ok(float64(n)))
		tot[s] += n
	}
	for rows.Next() {
		var b time.Time
		var c, u, r, a int64
		if err := rows.Scan(&b, &c, &u, &r, &a); err != nil {
			rows.Close()
			return nil, nil, err
		}
		k := localKey(b)
		add(k, "credited", c)
		add(k, "used", u)
		add(k, "returned", r)
		add(k, "adjusted", a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	res, err := q.Query(ctx, `
		SELECT date_trunc($2, created_at AT TIME ZONE $3), count(*), 0 FROM reward_reservation
		WHERE establishment_id = $1 AND created_at >= $4 AND created_at < $5 GROUP BY 1
		UNION ALL
		SELECT date_trunc($2, resolved_at AT TIME ZONE $3), 0, count(*) FROM reward_reservation
		WHERE establishment_id = $1 AND status IN ('delivered', 'delivery_reverted') AND resolved_at >= $4 AND resolved_at < $5 GROUP BY 1`,
		sc.est, gran, sc.tz, p.Start, p.EffectiveEnd)
	if err != nil {
		return nil, nil, fmt.Errorf("reservas: %w", err)
	}
	defer res.Close()
	for res.Next() {
		var b time.Time
		var cr, dl int64
		if err := res.Scan(&b, &cr, &dl); err != nil {
			return nil, nil, err
		}
		k := localKey(b)
		if cr > 0 {
			prev := int64(0)
			if v, has := bv[k]["reservations"]; has && v.Value != nil {
				prev = int64(*v.Value)
			}
			setVal(bv, k, "reservations", ok(float64(prev+cr)))
			tot["reservations"] += cr
		}
		if dl > 0 {
			prev := int64(0)
			if v, has := bv[k]["delivered"]; has && v.Value != nil {
				prev = int64(*v.Value)
			}
			setVal(bv, k, "delivered", ok(float64(prev+dl)))
			tot["delivered"] += dl
		}
	}
	if err := res.Err(); err != nil {
		return nil, nil, err
	}
	totals := map[string]Value{}
	for _, k := range []string{"credited", "used", "returned", "adjusted", "reservations", "delivered"} {
		totals[k] = ok(float64(tot[k]))
	}
	return bv, applyScope(sc, p, totals, []string{"professional_id", "service_id", "method"}), nil
}
