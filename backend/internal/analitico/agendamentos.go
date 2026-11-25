package analitico

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/agenda"
	"fila/internal/platform/database"
)

// GET …/appointments: área "Agendamentos" do Dashboard (Fila 2.0, ADR-0039).
//
// Datas de referência (documentadas no dicionário do Dashboard):
//   - contagens e desfechos: data/hora do HORÁRIO MARCADO (starts_at), no fuso da loja; "previstos"
//     inclui o resto do período (futuro), os desfechos só o que já passou;
//   - remarcações: instante da remarcação (transição T-AP-02);
//   - dinheiro: instante em que entrou (recebimento), foi aplicado (crédito) ou confirmado (devolução);
//   - pendências (devoluções em aberto, saldo de comandas de reserva): fotografia de agora.
//
// Profissional: o da reserva hoje (remarcação para outro profissional leva a reserva junto); na
// utilização real, o da fila final da visita (transferência atribui ao último profissional).
// Reserva vira visita só ao ser chamada: comparecimentos contam UMA vez (a visita da reserva tem
// origem "appointment" e não entra nas entradas da fila).
//
// Taxa de falta = faltas / (faltas + comparecimentos), só entre reservas com desfecho; futuros,
// retenções vencidas (não pagas) e cancelamentos ficam fora. Denominador zero → "Sem dados".

type apptCounts struct {
	Scheduled         Value `json:"scheduled"`
	Completed         Value `json:"completed"`
	Interrupted       Value `json:"interrupted"`
	NoShow            Value `json:"no_show"`
	CancelledCustomer Value `json:"cancelled_by_customer"`
	CancelledStore    Value `json:"cancelled_by_store"`
	Expired           Value `json:"expired_unpaid"`
	StoreLate         Value `json:"store_late"`
	Reschedules       Value `json:"reschedules"`
	NoShowRate        Value `json:"no_show_rate"`
	StartDelayMedian  Value `json:"start_delay_median_seconds"`
	StartDelayP90     Value `json:"start_delay_p90_seconds"`
}

type apptMoney struct {
	Deposits         Value `json:"deposits_received_cents"`
	Fees             Value `json:"fees_received_cents"`
	LatePayments     Value `json:"late_payments_cents"`
	CreditsApplied   Value `json:"credits_applied_cents"`
	RefundsConfirmed Value `json:"refunds_confirmed_cents"`
}

type apptPending struct {
	SnapshotAt          time.Time `json:"snapshot_at"`
	RefundsPending      int64     `json:"refunds_pending"`
	RefundsPendingCents int64     `json:"refunds_pending_cents"`
	OpenBalanceCents    int64     `json:"open_balance_cents"`
	OpenBalanceOrders   int64     `json:"open_balance_orders"`
	AwaitingPayment     int64     `json:"awaiting_payment"`
}

type modalityRow struct {
	Origin    string `json:"origin"` // queue | appointment
	Completed int64  `json:"completed"`
	NoShow    int64  `json:"no_show"`
}

type occupancy struct {
	AvailableSeconds Value `json:"available_seconds"`
	CommittedSeconds Value `json:"committed_seconds"`
	Planned          Value `json:"planned"`
	InServiceSeconds Value `json:"in_service_seconds"`
	Real             Value `json:"real"`
	OvertimeSeconds  Value `json:"overtime_seconds"`
	// DaysWithSchedule / Days: cobertura (dias sem jornada cadastrada ficam fora do denominador).
	DaysWithSchedule int `json:"days_with_schedule"`
	Days             int `json:"days"`
}

type apptDay struct {
	Date      string    `json:"date"`
	Start     time.Time `json:"start"`
	Scheduled int64     `json:"scheduled"`
	Completed int64     `json:"completed"`
	NoShow    int64     `json:"no_show"`
	Cancelled int64     `json:"cancelled"`
}

type apptPart struct {
	Counts    apptCounts    `json:"counts"`
	Money     apptMoney     `json:"money"`
	Modality  []modalityRow `json:"modality"`
	Occupancy occupancy     `json:"occupancy"`
}

type appointmentsView struct {
	Current apptPart    `json:"current"`
	Compare *apptPart   `json:"compare"`
	Daily   []apptDay   `json:"daily"`
	Pending apptPending `json:"pending_now"`
}

// apptFilter: profissional da reserva e serviço da reserva ($4, $5).
const apptFilter = `
  AND ($4::uuid IS NULL OR a.professional_id = $4)
  AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM appointment_item i WHERE i.appointment_id = a.id AND i.service_id = $5))`

func (s *Service) appointments(w http.ResponseWriter, r *http.Request) error {
	sc, err := s.authorize(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out appointmentsView
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		if out.Current, err = s.apptFor(ctx, tx, sc, sc.q.Period); err != nil {
			return err
		}
		if sc.q.Compare != nil {
			c, err := s.apptFor(ctx, tx, sc, *sc.q.Compare)
			if err != nil {
				return err
			}
			out.Compare = &c
		}
		if out.Daily, err = apptDaily(ctx, tx, sc); err != nil {
			return err
		}
		out.Pending, err = apptPendingNow(ctx, tx, sc)
		return err
	})
	if err != nil {
		return err
	}
	m := sc.meta("method")
	snap := out.Pending.SnapshotAt
	m.SnapshotAt = &snap
	write(w, out, m)
	return nil
}

func (s *Service) apptFor(ctx context.Context, q database.Querier, sc *scope, p Period) (apptPart, error) {
	var out apptPart
	if sc.beforeCollection(p) {
		nc := status("not_collected")
		out.Counts = apptCounts{nc, nc, nc, nc, nc, nc, nc, nc, nc, nc, nc, nc}
		out.Money = apptMoney{nc, nc, nc, nc, nc}
		out.Modality = []modalityRow{}
		out.Occupancy = occupancy{AvailableSeconds: nc, CommittedSeconds: nc, Planned: nc, InServiceSeconds: nc, Real: nc, OvertimeSeconds: nc}
		return out, nil
	}
	prof, svc := sc.q.Filters.ProfessionalID, sc.q.Filters.ServiceID
	var sched, done, intr, noShow, cc, cs, exp, late int64
	if err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE a.status NOT IN ('held', 'expired', 'cancelled')),
		       count(*) FILTER (WHERE a.status = 'completed'),
		       count(*) FILTER (WHERE a.status = 'interrupted'),
		       count(*) FILTER (WHERE a.status = 'no_show'),
		       count(*) FILTER (WHERE a.status = 'cancelled' AND a.cancelled_by = 'customer'),
		       count(*) FILTER (WHERE a.status = 'cancelled' AND a.cancelled_by = 'store'),
		       count(*) FILTER (WHERE a.status = 'expired'),
		       count(*) FILTER (WHERE a.store_late_at IS NOT NULL)
		FROM appointment a WHERE a.establishment_id = $1 AND a.starts_at >= $2 AND a.starts_at < $3`+apptFilter,
		sc.est, p.Start, p.End, prof, svc).Scan(&sched, &done, &intr, &noShow, &cc, &cs, &exp, &late); err != nil {
		return out, err
	}
	c := &out.Counts
	c.Scheduled, c.Completed, c.Interrupted, c.NoShow = ok(float64(sched)), ok(float64(done)), ok(float64(intr)), ok(float64(noShow))
	c.CancelledCustomer, c.CancelledStore, c.Expired, c.StoreLate = ok(float64(cc)), ok(float64(cs)), ok(float64(exp)), ok(float64(late))
	if den := noShow + done + intr; den > 0 {
		c.NoShowRate = okN(float64(noShow)/float64(den), den)
	} else {
		c.NoShowRate = empty(0)
	}
	var resch int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM appointment_event e JOIN appointment a ON a.id = e.appointment_id
		WHERE e.establishment_id = $1 AND e.action = 'T-AP-02' AND e.at >= $2 AND e.at < $3`+apptFilter,
		sc.est, p.Start, p.EffectiveEnd, prof, svc).Scan(&resch); err != nil {
		return out, err
	}
	c.Reschedules = ok(float64(resch))
	var med, p90 *float64
	var n int64
	if err := q.QueryRow(ctx, `
		SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY d), percentile_cont(0.9) WITHIN GROUP (ORDER BY d), count(*)
		FROM (SELECT greatest(0, extract(epoch FROM min(e.at) - a.starts_at)) AS d
		      FROM appointment a JOIN appointment_event e ON e.appointment_id = a.id AND e.to_status = 'in_service'
		      WHERE a.establishment_id = $1 AND a.starts_at >= $2 AND a.starts_at < $3`+apptFilter+`
		      GROUP BY a.id, a.starts_at) x`,
		sc.est, p.Start, p.EffectiveEnd, prof, svc).Scan(&med, &p90, &n); err != nil {
		return out, err
	}
	c.StartDelayMedian, c.StartDelayP90 = fromPtr(med, n), fromPtr(p90, n)

	var dep, fee, latePay, credit, refunded int64
	if err := q.QueryRow(ctx, `
		SELECT coalesce(sum(r.amount_cents) FILTER (WHERE b.mode = 'deposit' AND r.classification = 'principal'), 0)::bigint,
		       coalesce(sum(r.amount_cents) FILTER (WHERE b.mode = 'fee' AND r.classification = 'principal'), 0)::bigint,
		       coalesce(sum(r.amount_cents) FILTER (WHERE r.classification = 'excess'), 0)::bigint
		FROM receipt r JOIN booking_payment b ON b.id = r.booking_payment_id JOIN appointment a ON a.id = b.appointment_id
		WHERE r.establishment_id = $1 AND r.registered_at >= $2 AND r.registered_at < $3`+apptFilter,
		sc.est, p.Start, p.EffectiveEnd, prof, svc).Scan(&dep, &fee, &latePay); err != nil {
		return out, err
	}
	if err := q.QueryRow(ctx, `
		SELECT coalesce(sum(k.amount_cents), 0)::bigint FROM booking_credit k
		JOIN booking_payment b ON b.id = k.booking_payment_id JOIN appointment a ON a.id = b.appointment_id
		WHERE k.establishment_id = $1 AND k.applied_at >= $2 AND k.applied_at < $3`+apptFilter,
		sc.est, p.Start, p.EffectiveEnd, prof, svc).Scan(&credit); err != nil {
		return out, err
	}
	if err := q.QueryRow(ctx, `
		SELECT coalesce(sum(f.amount_cents), 0)::bigint FROM refund f
		JOIN booking_payment b ON b.id = f.booking_payment_id JOIN appointment a ON a.id = b.appointment_id
		WHERE f.establishment_id = $1 AND f.status = 'confirmed' AND f.resolved_at >= $2 AND f.resolved_at < $3`+apptFilter,
		sc.est, p.Start, p.EffectiveEnd, prof, svc).Scan(&refunded); err != nil {
		return out, err
	}
	out.Money = apptMoney{ok(float64(dep)), ok(float64(fee)), ok(float64(latePay)), ok(float64(credit)), ok(float64(refunded))}

	rows, err := q.Query(ctx, `
		WITH o(origin) AS (VALUES ('queue'), ('appointment'))
		SELECT o.origin, count(p.id) FILTER (WHERE p.status = 'completed'), count(p.id) FILTER (WHERE p.status = 'no_show')
		FROM o LEFT JOIN (
		  SELECT p.id, p.origin, p.status FROM participation p JOIN queue q ON q.id = p.queue_id
		  WHERE p.establishment_id = $1 AND p.finished_at >= $2 AND p.finished_at < $3
		    AND ($4::uuid IS NULL OR q.professional_id = $4)
		    AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = p.order_id AND i.service_id = $5 AND i.status <> 'removed'))
		) p ON p.origin = o.origin
		GROUP BY o.origin ORDER BY o.origin DESC`, sc.est, p.Start, p.EffectiveEnd, prof, svc)
	if err != nil {
		return out, err
	}
	if out.Modality, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (modalityRow, error) {
		var m modalityRow
		err := r.Scan(&m.Origin, &m.Completed, &m.NoShow)
		return m, err
	}); err != nil {
		return out, err
	}
	out.Occupancy, err = occupancyFor(ctx, q, sc, p)
	return out, err
}

// occupancyFor: ocupação planejada (minutos de reserva comprometidos ÷ minutos de jornada líquidos de
// pausas, folgas e bloqueios) e utilização real (minutos em atendimento DENTRO da jornada ÷ minutos
// de jornada já decorridos). Atendimento fora da jornada aparece como hora extra, fora da razão.
// Interrompido conta o tempo realmente em atendimento. Serviço (filtro) não se aplica.
func occupancyFor(ctx context.Context, q database.Querier, sc *scope, p Period) (occupancy, error) {
	var out occupancy
	if sc.active("service_id") {
		na := status("not_applicable")
		return occupancy{AvailableSeconds: na, CommittedSeconds: na, Planned: na, InServiceSeconds: na, Real: na, OvertimeSeconds: na}, nil
	}
	rows, err := q.Query(ctx, `SELECT id FROM professional WHERE establishment_id = $1 AND ($2::uuid IS NULL OR id = $2)`,
		sc.est, sc.q.Filters.ProfessionalID)
	if err != nil {
		return out, err
	}
	profs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return out, err
	}
	from, to := agenda.DayOf(p.Start, sc.loc), agenda.DayOf(p.End.Add(-time.Nanosecond), sc.loc)
	work, err := agenda.WorkIntervals(ctx, q, sc.est, profs, from, to, sc.loc)
	if err != nil {
		return out, err
	}
	days := map[string]bool{}
	var avail, availPast time.Duration
	elapsed := agenda.Interval{Start: p.Start, End: p.EffectiveEnd}
	for _, list := range work {
		for _, iv := range list {
			days[agenda.DayOf(iv.Start, sc.loc).String()] = true
			avail += iv.End.Sub(iv.Start)
			availPast += agenda.Clip(elapsed, []agenda.Interval{iv})
		}
	}
	for d := from; !to.Before(d); d = d.AddDays(1) {
		out.Days++
	}
	out.DaysWithSchedule = len(days)
	// Comprometido: reservas não canceladas nem vencidas (falta também ocupou o horário).
	rows, err = q.Query(ctx, `SELECT a.professional_id, a.starts_at, a.ends_at FROM appointment a
		WHERE a.establishment_id = $1 AND a.starts_at < $3 AND a.ends_at > $2
		  AND a.status IN ('confirmed', 'called', 'in_service', 'completed', 'interrupted', 'no_show')
		  AND ($4::uuid IS NULL OR a.professional_id = $4)`, sc.est, p.Start, p.End, sc.q.Filters.ProfessionalID)
	if err != nil {
		return out, err
	}
	var committed time.Duration
	err = forEachInterval(rows, func(prof uuid.UUID, iv agenda.Interval) { committed += agenda.Clip(iv, work[prof]) })
	if err != nil {
		return out, err
	}
	rows, err = q.Query(ctx, `SELECT q.professional_id, p.started_at, p.finished_at FROM participation p JOIN queue q ON q.id = p.queue_id
		WHERE p.establishment_id = $1 AND p.status IN ('completed', 'interrupted') AND p.started_at IS NOT NULL
		  AND p.started_at < $3 AND p.finished_at > $2 AND ($4::uuid IS NULL OR q.professional_id = $4)`,
		sc.est, p.Start, p.EffectiveEnd, sc.q.Filters.ProfessionalID)
	if err != nil {
		return out, err
	}
	var inShift, total time.Duration
	err = forEachInterval(rows, func(prof uuid.UUID, iv agenda.Interval) {
		if iv.Start.Before(p.Start) {
			iv.Start = p.Start
		}
		if iv.End.After(p.EffectiveEnd) {
			iv.End = p.EffectiveEnd
		}
		if iv.End.After(iv.Start) {
			total += iv.End.Sub(iv.Start)
			inShift += agenda.Clip(iv, work[prof])
		}
	})
	if err != nil {
		return out, err
	}
	out.AvailableSeconds, out.CommittedSeconds = ok(avail.Seconds()), ok(committed.Seconds())
	out.InServiceSeconds, out.OvertimeSeconds = ok(inShift.Seconds()), ok((total - inShift).Seconds())
	if avail > 0 {
		out.Planned = okN(committed.Seconds()/avail.Seconds(), int64(len(days)))
	} else {
		out.Planned = empty(0)
	}
	if availPast > 0 {
		out.Real = okN(inShift.Seconds()/availPast.Seconds(), int64(len(days)))
	} else {
		out.Real = empty(0)
	}
	return out, nil
}

func forEachInterval(rows pgx.Rows, f func(uuid.UUID, agenda.Interval)) error {
	defer rows.Close()
	for rows.Next() {
		var prof uuid.UUID
		var iv agenda.Interval
		if err := rows.Scan(&prof, &iv.Start, &iv.End); err != nil {
			return err
		}
		f(prof, iv)
	}
	return rows.Err()
}

// apptDaily: série diária pelo horário marcado (dias sem reservas aparecem com zero).
func apptDaily(ctx context.Context, q database.Querier, sc *scope) ([]apptDay, error) {
	p := sc.q.Period
	rows, err := q.Query(ctx, `
		SELECT (a.starts_at AT TIME ZONE $6)::date,
		       count(*) FILTER (WHERE a.status NOT IN ('held', 'expired', 'cancelled')),
		       count(*) FILTER (WHERE a.status = 'completed'),
		       count(*) FILTER (WHERE a.status = 'no_show'),
		       count(*) FILTER (WHERE a.status = 'cancelled')
		FROM appointment a WHERE a.establishment_id = $1 AND a.starts_at >= $2 AND a.starts_at < $3`+apptFilter+`
		GROUP BY 1`, sc.est, p.Start, p.End, sc.q.Filters.ProfessionalID, sc.q.Filters.ServiceID, sc.tz)
	if err != nil {
		return nil, err
	}
	got := map[string]apptDay{}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var d time.Time
			var x apptDay
			if err := rows.Scan(&d, &x.Scheduled, &x.Completed, &x.NoShow, &x.Cancelled); err != nil {
				return err
			}
			got[d.Format(time.DateOnly)] = x
		}
		return rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	out := []apptDay{}
	for _, b := range bucketStarts(p, GranDay) {
		key := b.In(sc.loc).Format(time.DateOnly)
		x := got[key]
		x.Date, x.Start = key, b.UTC()
		out = append(out, x)
	}
	return out, nil
}

// apptPendingNow: fotografia de agora (não depende do período).
func apptPendingNow(ctx context.Context, q database.Querier, sc *scope) (apptPending, error) {
	out := apptPending{SnapshotAt: sc.now.UTC()}
	err := q.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM refund f WHERE f.establishment_id = $1 AND f.booking_payment_id IS NOT NULL
		     AND f.status NOT IN ('confirmed', 'failed')),
		  (SELECT coalesce(sum(f.amount_cents), 0)::bigint FROM refund f WHERE f.establishment_id = $1
		     AND f.booking_payment_id IS NOT NULL AND f.status NOT IN ('confirmed', 'failed')),
		  (SELECT coalesce(sum(o.total_cents - o.credit_cents), 0)::bigint FROM service_order o
		     JOIN participation p ON p.order_id = o.id JOIN queue q ON q.id = p.queue_id
		     WHERE o.establishment_id = $1 AND p.origin = 'appointment' AND o.status = 'awaiting_payment'
		       AND ($2::uuid IS NULL OR q.professional_id = $2)),
		  (SELECT count(*) FROM service_order o JOIN participation p ON p.order_id = o.id JOIN queue q ON q.id = p.queue_id
		     WHERE o.establishment_id = $1 AND p.origin = 'appointment' AND o.status = 'awaiting_payment'
		       AND ($2::uuid IS NULL OR q.professional_id = $2)),
		  (SELECT count(*) FROM appointment a WHERE a.establishment_id = $1 AND a.status = 'held'
		       AND ($2::uuid IS NULL OR a.professional_id = $2))`, sc.est, sc.q.Filters.ProfessionalID).
		Scan(&out.RefundsPending, &out.RefundsPendingCents, &out.OpenBalanceCents, &out.OpenBalanceOrders, &out.AwaitingPayment)
	return out, err
}
