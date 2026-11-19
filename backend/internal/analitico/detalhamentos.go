package analitico

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
)

// GET …/breakdowns/{dimension}: agregados por categoria (meio de pagamento, desfecho,
// profissional, serviço, dia/hora, faixas, pendências, prêmios, recorrência).

type breakdownDef struct {
	inapplicable []string
	run          func(ctx context.Context, q database.Querier, sc *scope) (any, error)
}

var breakdowns = map[string]breakdownDef{
	"payment_methods": {inapplicable: []string{"service_id"}, run: bdPaymentMethods},
	"outcomes":        {inapplicable: []string{"method"}, run: bdOutcomes},
	"professionals":   {inapplicable: []string{"method"}, run: bdProfessionals},
	"services":        {inapplicable: []string{"method"}, run: bdServices},
	"peak_hours":      {inapplicable: []string{"method"}, run: bdPeakHours},
	"wait_bands":      {inapplicable: []string{"method"}, run: bdWaitBands},
	"pending":         {inapplicable: []string{"professional_id", "service_id", "method"}, run: bdPending},
	"rewards":         {inapplicable: []string{"professional_id", "service_id", "method"}, run: bdRewards},
	"recurrence":      {inapplicable: []string{"method"}, run: bdRecurrence},
}

type breakdownView struct {
	Dimension string `json:"dimension"`
	// Status: ok, not_applicable (filtro sem atribuição), not_collected (antes da coleta).
	Status string `json:"status"`
	Rows   any    `json:"rows"`
}

func (s *Service) breakdown(w http.ResponseWriter, r *http.Request) error {
	dim := r.PathValue("dimension")
	def, known := breakdowns[dim]
	if !known {
		return hs.ErrNotFound()
	}
	sc, err := s.authorize(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := breakdownView{Dimension: dim, Status: "ok"}
	for _, f := range def.inapplicable {
		if sc.active(f) {
			out.Status = "not_applicable"
		}
	}
	if out.Status == "ok" && sc.beforeCollection(sc.q.Period) && dim != "pending" {
		out.Status = "not_collected"
	}
	if out.Status == "ok" {
		err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
			var err error
			out.Rows, err = def.run(ctx, tx, sc)
			return err
		})
		if err != nil {
			return err
		}
	}
	if out.Rows == nil {
		out.Rows = map[string]any{}
	}
	m := sc.meta(def.inapplicable...)
	if dim == "pending" {
		now := sc.now.UTC()
		m.SnapshotAt = &now
	}
	write(w, out, m)
	return nil
}

// ---------------------------------------------------------------------------------------------

type methodRow struct {
	Method        string `json:"method"`
	ReceivedCents int64  `json:"received_cents"`
	Receipts      int64  `json:"receipts"`
	RefundedCents int64  `json:"refunded_cents"`
	Refunds       int64  `json:"refunds"`
	NetCents      int64  `json:"net_cents"`
}

func bdPaymentMethods(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	rows, err := q.Query(ctx, `
		WITH m(method) AS (VALUES ('pix'), ('cash'), ('card')),
		rec AS (
		  SELECT r.method, sum(r.amount_cents)::bigint AS cents, count(*) AS n
		  FROM receipt r
		  WHERE r.establishment_id = $1 AND r.registered_at >= $2 AND r.registered_at < $3
		    AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM service_order o WHERE o.id = r.order_id AND o.professional_id = $4)) AND ($5 = '' OR r.method = $5)
		  GROUP BY r.method),
		ref AS (
		  SELECT f.method, sum(f.amount_cents)::bigint AS cents, count(*) AS n
		  FROM refund f
		  WHERE f.establishment_id = $1 AND f.status = 'confirmed' AND f.resolved_at >= $2 AND f.resolved_at < $3
		    AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM service_order o WHERE o.id = f.order_id AND o.professional_id = $4)) AND ($5 = '' OR f.method = $5)
		  GROUP BY f.method)
		SELECT m.method, coalesce(rec.cents, 0), coalesce(rec.n, 0), coalesce(ref.cents, 0), coalesce(ref.n, 0)
		FROM m LEFT JOIN rec USING (method) LEFT JOIN ref USING (method)
		WHERE $5 = '' OR m.method = $5
		ORDER BY coalesce(rec.cents, 0) DESC, m.method`,
		sc.est, p.Start, p.EffectiveEnd, sc.q.Filters.ProfessionalID, sc.q.Filters.Method)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (methodRow, error) {
		var m methodRow
		err := r.Scan(&m.Method, &m.ReceivedCents, &m.Receipts, &m.RefundedCents, &m.Refunds)
		m.NetCents = m.ReceivedCents - m.RefundedCents
		return m, err
	})
}

// Desfechos das participações que ENTRARAM no período (coorte por entrada). Taxas sobre as já
// encerradas (denominador explícito); as ainda abertas aparecem à parte, sem taxa.
type outcomeRow struct {
	Status  string   `json:"status"`
	Count   int64    `json:"count"`
	Percent *float64 `json:"percent"`
	Open    bool     `json:"open"`
}

type outcomesView struct {
	Denominator      string       `json:"denominator"`
	DenominatorCount int64        `json:"denominator_count"`
	OpenCount        int64        `json:"open_count"`
	Items            []outcomeRow `json:"items"`
}

func bdOutcomes(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	rows, err := q.Query(ctx, `
		WITH st(status, ord) AS (VALUES ('completed', 1), ('interrupted', 2), ('no_show', 3), ('cancelled', 4),
		                                ('waiting', 5), ('called', 6), ('in_service', 7))
		SELECT st.status, count(p.id)
		FROM st LEFT JOIN (
		  SELECT p.id, p.status FROM participation p JOIN queue q ON q.id = p.queue_id
		  WHERE p.establishment_id = $1 AND p.created_at >= $2 AND p.created_at < $3
		    AND ($4::uuid IS NULL OR q.professional_id = $4)
		    AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = p.order_id AND i.service_id = $5 AND i.status <> 'removed'))
		) p ON p.status = st.status
		GROUP BY st.status, st.ord ORDER BY st.ord`,
		sc.est, p.Start, p.EffectiveEnd, sc.q.Filters.ProfessionalID, sc.q.Filters.ServiceID)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (outcomeRow, error) {
		var o outcomeRow
		err := r.Scan(&o.Status, &o.Count)
		o.Open = o.Status == "waiting" || o.Status == "called" || o.Status == "in_service"
		return o, err
	})
	if err != nil {
		return nil, err
	}
	v := outcomesView{Denominator: "participações que entraram no período e já foram encerradas (concluídas, interrompidas, ausências e cancelamentos)", Items: items}
	for _, o := range items {
		if o.Open {
			v.OpenCount += o.Count
		} else {
			v.DenominatorCount += o.Count
		}
	}
	for i := range v.Items {
		if !v.Items[i].Open && v.DenominatorCount > 0 {
			pct := float64(v.Items[i].Count) * 100 / float64(v.DenominatorCount)
			v.Items[i].Percent = &pct
		}
	}
	return v, nil
}

// Profissionais: atribuição ao profissional da fila FINAL (destino da última transferência).
type professionalRow struct {
	ProfessionalID    uuid.UUID `json:"professional_id"`
	Name              string    `json:"name"`
	Active            bool      `json:"active"`
	Completed         int64     `json:"completed"`
	WaitMedianSeconds Value     `json:"wait_median_seconds"`
	DurationMedian    Value     `json:"duration_median_seconds"`
	ServicesPerformed int64     `json:"services_performed"`
	TransferredIn     int64     `json:"transferred_in"`
}

func bdProfessionals(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	rows, err := q.Query(ctx, `
		SELECT pr.id, pr.display_name, pr.status = 'active',
		  (SELECT count(*) FROM participation x WHERE x.queue_id = qu.id AND x.status = 'completed'
		     AND x.finished_at >= $2 AND x.finished_at < $3 AND ($4::uuid IS NULL OR EXISTS (
		       SELECT 1 FROM order_item i WHERE i.order_id = x.order_id AND i.service_id = $4 AND i.status <> 'removed'))),
		  (SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM x.called_at - x.created_at))
		     FROM participation x WHERE x.queue_id = qu.id AND x.called_at >= $2 AND x.called_at < $3 AND x.called_at >= x.created_at
		     AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = x.order_id AND i.service_id = $4 AND i.status <> 'removed'))),
		  (SELECT count(*) FROM participation x WHERE x.queue_id = qu.id AND x.called_at >= $2 AND x.called_at < $3 AND x.called_at >= x.created_at
		     AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = x.order_id AND i.service_id = $4 AND i.status <> 'removed'))),
		  (SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM x.finished_at - x.started_at))
		     FROM participation x WHERE x.queue_id = qu.id AND x.status = 'completed' AND x.finished_at >= $2 AND x.finished_at < $3
		     AND x.finished_at >= x.started_at
		     AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = x.order_id AND i.service_id = $4 AND i.status <> 'removed'))),
		  (SELECT count(*) FROM participation x WHERE x.queue_id = qu.id AND x.status = 'completed' AND x.finished_at >= $2 AND x.finished_at < $3
		     AND x.finished_at >= x.started_at
		     AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = x.order_id AND i.service_id = $4 AND i.status <> 'removed'))),
		  (SELECT count(*) FROM order_item i JOIN service_order o ON o.id = i.order_id
		     WHERE o.establishment_id = $1 AND o.professional_id = pr.id AND i.status = 'performed'
		       AND i.performed_at >= $2 AND i.performed_at < $3 AND ($4::uuid IS NULL OR i.service_id = $4)),
		  (SELECT count(*) FROM participation_transfer t WHERE t.to_queue_id = qu.id AND t.at >= $2 AND t.at < $3)
		FROM professional pr JOIN queue qu ON qu.professional_id = pr.id
		WHERE pr.establishment_id = $1 AND ($5::uuid IS NULL OR pr.id = $5)
		ORDER BY pr.display_name, pr.id`,
		sc.est, p.Start, p.EffectiveEnd, sc.q.Filters.ServiceID, sc.q.Filters.ProfessionalID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (professionalRow, error) {
		var x professionalRow
		var wm, dm *float64
		var wn, dn int64
		err := r.Scan(&x.ProfessionalID, &x.Name, &x.Active, &x.Completed, &wm, &wn, &dm, &dn, &x.ServicesPerformed, &x.TransferredIn)
		x.WaitMedianSeconds, x.DurationMedian = fromPtr(wm, wn), fromPtr(dm, dn)
		return x, err
	})
}

// Serviços: solicitados (itens incluídos, por data de inclusão, sem os removidos) e realizados
// (por data de realização). Valor = preço registrado no item cobrado, não dinheiro recebido.
type serviceRow struct {
	ServiceID          uuid.UUID `json:"service_id"`
	Name               string    `json:"name"`
	Requested          int64     `json:"requested"`
	Performed          int64     `json:"performed"`
	PerformedValue     int64     `json:"performed_value_cents"`
	ReceivedAttributed *int64    `json:"received_cents_attributed"` // sempre null: sem rateio
}

func bdServices(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	rows, err := q.Query(ctx, `
		SELECT i.service_id, (array_agg(i.name ORDER BY i.added_at DESC))[1],
		  count(*) FILTER (WHERE i.status <> 'removed' AND i.added_at >= $2 AND i.added_at < $3),
		  count(*) FILTER (WHERE i.status = 'performed' AND i.performed_at >= $2 AND i.performed_at < $3),
		  coalesce(sum(i.unit_price_cents) FILTER (WHERE i.status = 'performed' AND i.charged AND i.performed_at >= $2 AND i.performed_at < $3), 0)::bigint
		FROM order_item i JOIN service_order o ON o.establishment_id = i.establishment_id AND o.id = i.order_id
		WHERE i.establishment_id = $1
		  AND ((i.added_at >= $2 AND i.added_at < $3) OR (i.performed_at >= $2 AND i.performed_at < $3))
		  AND ($4::uuid IS NULL OR o.professional_id = $4) AND ($5::uuid IS NULL OR i.service_id = $5)
		GROUP BY i.service_id
		ORDER BY 4 DESC, 3 DESC, 2, 1`,
		sc.est, p.Start, p.EffectiveEnd, sc.q.Filters.ProfessionalID, sc.q.Filters.ServiceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (serviceRow, error) {
		var x serviceRow
		err := r.Scan(&x.ServiceID, &x.Name, &x.Requested, &x.Performed, &x.PerformedValue)
		return x, err
	})
}

// Horários de pico: entradas por dia da semana ISO (1 = segunda) e hora local.
type peakCell struct {
	DOW     int   `json:"dow"`
	Hour    int   `json:"hour"`
	Entries int64 `json:"entries"`
}

func bdPeakHours(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	rows, err := q.Query(ctx, `
		SELECT extract(isodow FROM p.created_at AT TIME ZONE $4)::int, extract(hour FROM p.created_at AT TIME ZONE $4)::int, count(*)
		FROM participation p JOIN queue q ON q.id = p.queue_id
		WHERE p.establishment_id = $1 AND p.created_at >= $2 AND p.created_at < $3
		  AND ($5::uuid IS NULL OR q.professional_id = $5)
		  AND ($6::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = p.order_id AND i.service_id = $6 AND i.status <> 'removed'))
		GROUP BY 1, 2 ORDER BY 1, 2`,
		sc.est, p.Start, p.EffectiveEnd, sc.tz, sc.q.Filters.ProfessionalID, sc.q.Filters.ServiceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (peakCell, error) {
		var c peakCell
		err := r.Scan(&c.DOW, &c.Hour, &c.Entries)
		return c, err
	})
}

// Faixas de espera (minutos) das participações chamadas no período.
var waitBands = []struct {
	Key      string
	From, To int // minutos [From, To); To = 0 → sem teto
}{{"0-5", 0, 5}, {"5-15", 5, 15}, {"15-30", 15, 30}, {"30-60", 30, 60}, {"60+", 60, 0}}

type bandRow struct {
	Band  string `json:"band"`
	From  int    `json:"from_minutes"`
	To    *int   `json:"to_minutes"`
	Count int64  `json:"count"`
}

func bdWaitBands(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	rows, err := q.Query(ctx, `
		SELECT floor(extract(epoch FROM p.called_at - p.created_at) / 60)::int AS m, count(*)
		FROM participation p JOIN queue q ON q.id = p.queue_id
		WHERE p.establishment_id = $1 AND p.called_at IS NOT NULL AND p.called_at >= p.created_at
		  AND p.called_at >= $2 AND p.called_at < $3
		  AND ($4::uuid IS NULL OR q.professional_id = $4)
		  AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = p.order_id AND i.service_id = $5 AND i.status <> 'removed'))
		GROUP BY 1`, sc.est, p.Start, p.EffectiveEnd, sc.q.Filters.ProfessionalID, sc.q.Filters.ServiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make([]int64, len(waitBands))
	for rows.Next() {
		var m int
		var n int64
		if err := rows.Scan(&m, &n); err != nil {
			return nil, err
		}
		counts[bandIndex(m)] += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]bandRow, len(waitBands))
	for i, b := range waitBands {
		out[i] = bandRow{Band: b.Key, From: b.From, Count: counts[i]}
		if b.To > 0 {
			to := b.To
			out[i].To = &to
		}
	}
	return out, nil
}

func bandIndex(minutes int) int {
	for i, b := range waitBands {
		if b.To == 0 || minutes < b.To {
			return i
		}
	}
	return len(waitBands) - 1
}

// Pendências: "abertas agora" (fotografia) separadas de "abertas/resolvidas no período".
type pendingKindRow struct {
	Kind        string `json:"kind"`
	Count       int64  `json:"count"`
	AmountCents *int64 `json:"amount_cents"`
}

type pendingView struct {
	OpenNow        []pendingKindRow `json:"open_now"`
	AgeBands       []bandRow        `json:"age_bands_now"`
	OpenedPeriod   int64            `json:"opened_in_period"`
	ResolvedPeriod int64            `json:"resolved_in_period"`
}

var ageBands = []struct {
	Key      string
	From, To int // horas
}{{"<1h", 0, 1}, {"1-24h", 1, 24}, {"1-7d", 24, 168}, {">7d", 168, 0}}

func bdPending(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	v := pendingView{OpenNow: []pendingKindRow{}}
	rows, err := q.Query(ctx, `
		SELECT kind, count(*), sum(amount_cents)::bigint FROM financial_issue WHERE establishment_id = $1 AND status = 'open' GROUP BY kind
		UNION ALL
		SELECT 'order_' || status, count(*), sum(total_cents)::bigint FROM service_order
		WHERE establishment_id = $1 AND status IN ('awaiting_payment', 'awaiting_confirmation') GROUP BY status
		UNION ALL
		SELECT 'pix_charge_unknown', count(*), sum(amount_cents)::bigint FROM pix_charge WHERE establishment_id = $1 AND status = 'unknown'
		HAVING count(*) > 0
		ORDER BY 2 DESC, 1`, sc.est)
	if err != nil {
		return nil, err
	}
	if v.OpenNow, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (pendingKindRow, error) {
		var x pendingKindRow
		err := r.Scan(&x.Kind, &x.Count, &x.AmountCents)
		return x, err
	}); err != nil {
		return nil, err
	}
	ages, err := q.Query(ctx, `
		SELECT floor(extract(epoch FROM $2::timestamptz - created_at) / 3600)::int, count(*) FROM (
		  SELECT created_at FROM financial_issue WHERE establishment_id = $1 AND status = 'open'
		  UNION ALL
		  SELECT coalesce(total_frozen_at, updated_at) FROM service_order
		  WHERE establishment_id = $1 AND status IN ('awaiting_payment', 'awaiting_confirmation')
		) x(created_at) GROUP BY 1`, sc.est, sc.now)
	if err != nil {
		return nil, err
	}
	counts := make([]int64, len(ageBands))
	for ages.Next() {
		var h int
		var n int64
		if err := ages.Scan(&h, &n); err != nil {
			ages.Close()
			return nil, err
		}
		for i, b := range ageBands {
			if b.To == 0 || h < b.To {
				counts[i] += n
				break
			}
		}
	}
	ages.Close()
	for i, b := range ageBands {
		row := bandRow{Band: b.Key, From: b.From, Count: counts[i]}
		if b.To > 0 {
			to := b.To
			row.To = &to
		}
		v.AgeBands = append(v.AgeBands, row)
	}
	err = q.QueryRow(ctx, `SELECT
		  count(*) FILTER (WHERE created_at >= $2 AND created_at < $3),
		  count(*) FILTER (WHERE resolved_at >= $2 AND resolved_at < $3)
		FROM financial_issue WHERE establishment_id = $1`, sc.est, p.Start, p.EffectiveEnd).Scan(&v.OpenedPeriod, &v.ResolvedPeriod)
	return v, err
}

// Prêmios: reservas por criação; entregas, cancelamentos e expirações pela resolução.
type rewardRow struct {
	RewardID   uuid.UUID `json:"reward_id"`
	Name       string    `json:"name"`
	Reserved   int64     `json:"reserved"`
	Delivered  int64     `json:"delivered"`
	Cancelled  int64     `json:"cancelled"`
	Expired    int64     `json:"expired"`
	PointsUsed int64     `json:"points_used"`
}

type rewardsView struct {
	ProgramEnabledNow bool        `json:"program_enabled_now"`
	Items             []rewardRow `json:"items"`
}

func bdRewards(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	var v rewardsView
	if err := q.QueryRow(ctx, `SELECT coalesce((SELECT enabled FROM loyalty_program WHERE establishment_id = $1), false)`, sc.est).
		Scan(&v.ProgramEnabledNow); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT w.id, w.name,
		  count(*) FILTER (WHERE r.created_at >= $2 AND r.created_at < $3),
		  count(*) FILTER (WHERE r.status IN ('delivered', 'delivery_reverted') AND r.resolved_at >= $2 AND r.resolved_at < $3),
		  count(*) FILTER (WHERE r.status = 'cancelled' AND r.resolved_at >= $2 AND r.resolved_at < $3),
		  count(*) FILTER (WHERE r.status = 'expired' AND r.resolved_at >= $2 AND r.resolved_at < $3),
		  coalesce(sum(r.accepted_cost_points) FILTER (WHERE r.status = 'delivered' AND r.resolved_at >= $2 AND r.resolved_at < $3), 0)::bigint
		FROM reward w JOIN reward_reservation r ON r.establishment_id = w.establishment_id AND r.reward_id = w.id
		WHERE w.establishment_id = $1 AND ((r.created_at >= $2 AND r.created_at < $3) OR (r.resolved_at >= $2 AND r.resolved_at < $3))
		GROUP BY w.id, w.name ORDER BY 3 DESC, 2, 1`, sc.est, p.Start, p.EffectiveEnd)
	if err != nil {
		return nil, err
	}
	v.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (rewardRow, error) {
		var x rewardRow
		err := r.Scan(&x.RewardID, &x.Name, &x.Reserved, &x.Delivered, &x.Cancelled, &x.Expired, &x.PointsUsed)
		return x, err
	})
	return v, err
}

// Recorrência: contas identificadas (nunca visitantes nem apelidos) com 2+ visitas concluídas
// dentro do período escolhido (a janela é o próprio período).
type recurrenceView struct {
	Window             string `json:"window"`
	IdentifiedAccounts int64  `json:"identified_accounts"`
	RecurringAccounts  int64  `json:"recurring_accounts"`
	Rate               Value  `json:"rate_percent"`
	AnonymousVisits    int64  `json:"anonymous_visits_excluded"`
}

func bdRecurrence(ctx context.Context, q database.Querier, sc *scope) (any, error) {
	p := sc.q.Period
	v := recurrenceView{Window: fmt.Sprintf("%s a %s (visitas concluídas)", p.From, p.To)}
	err := q.QueryRow(ctx, `
		WITH v AS (
		  SELECT p.account_id, p.visitor_id FROM participation p JOIN queue q ON q.id = p.queue_id
		  WHERE p.establishment_id = $1 AND p.status = 'completed' AND p.finished_at >= $2 AND p.finished_at < $3
		    AND ($4::uuid IS NULL OR q.professional_id = $4)
		    AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = p.order_id AND i.service_id = $5 AND i.status <> 'removed')))
		SELECT
		  (SELECT count(DISTINCT account_id) FROM v WHERE account_id IS NOT NULL),
		  (SELECT count(*) FROM (SELECT account_id FROM v WHERE account_id IS NOT NULL GROUP BY account_id HAVING count(*) > 1) r),
		  (SELECT count(*) FROM v WHERE visitor_id IS NOT NULL)`,
		sc.est, p.Start, p.EffectiveEnd, sc.q.Filters.ProfessionalID, sc.q.Filters.ServiceID).
		Scan(&v.IdentifiedAccounts, &v.RecurringAccounts, &v.AnonymousVisits)
	if err != nil {
		return nil, err
	}
	if v.IdentifiedAccounts == 0 {
		v.Rate = empty(0)
	} else {
		v.Rate = okN(float64(v.RecurringAccounts)*100/float64(v.IdentifiedAccounts), v.IdentifiedAccounts)
	}
	return v, nil
}
