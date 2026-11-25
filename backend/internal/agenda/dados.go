package agenda

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/platform/database"
)

// Leitura da agenda de um dia em consultas por conjunto (nunca uma consulta por horário).

// weekly é a versão de jornada vigente de um sujeito (loja ou profissional) numa data.
type weekly struct {
	ID            uuid.UUID
	Professional  *uuid.UUID
	EffectiveFrom time.Time
	Inherits      bool
	Shifts        []ShiftDef
}

// schedulesOn devolve a jornada vigente na data da loja (chave uuid.Nil) e de cada profissional
// que tem jornada própria (os outros herdam a da loja).
func schedulesOn(ctx context.Context, q database.Querier, estID uuid.UUID, day Day) (map[uuid.UUID]weekly, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (professional_id) id, professional_id, effective_from, inherits_store
		FROM agenda_schedule WHERE establishment_id = $1 AND effective_from <= $2
		ORDER BY professional_id, effective_from DESC, created_at DESC, id DESC`, estID, day.Date())
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (weekly, error) {
		var w weekly
		err := r.Scan(&w.ID, &w.Professional, &w.EffectiveFrom, &w.Inherits)
		return w, err
	})
	if err != nil {
		return nil, err
	}
	if list, err = withShifts(ctx, q, list); err != nil {
		return nil, err
	}
	out := map[uuid.UUID]weekly{}
	for _, w := range list {
		key := uuid.Nil
		if w.Professional != nil {
			key = *w.Professional
		}
		out[key] = w
	}
	return out, nil
}

// withShifts preenche os turnos de cada versão (uma consulta para todas).
func withShifts(ctx context.Context, q database.Querier, list []weekly) ([]weekly, error) {
	ids := make([]uuid.UUID, len(list))
	for i, w := range list {
		ids[i] = w.ID
	}
	rows, err := q.Query(ctx, `SELECT schedule_id, weekday, start_minute, end_minute, modality FROM agenda_shift
		WHERE schedule_id = ANY($1) ORDER BY weekday, start_minute`, ids)
	if err != nil {
		return nil, err
	}
	defs := map[uuid.UUID][]ShiftDef{}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var d ShiftDef
			if err := rows.Scan(&id, &d.Weekday, &d.StartMinute, &d.EndMinute, &d.Modality); err != nil {
				return err
			}
			defs[id] = append(defs[id], d)
		}
		return rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Shifts = defs[list[i].ID]
		if list[i].Shifts == nil {
			list[i].Shifts = []ShiftDef{}
		}
	}
	return list, nil
}

// professionalShifts: turnos do dia do profissional (próprios ou herdados), limitados à loja.
func professionalShifts(sched map[uuid.UUID]weekly, profID uuid.UUID, day Day, loc *time.Location) []Shift {
	store, ok := sched[uuid.Nil]
	if !ok {
		return nil
	}
	storeShifts := ShiftsOn(day, store.Shifts, loc)
	own, has := sched[profID]
	if !has || own.Inherits {
		return storeShifts
	}
	return Intersect(ShiftsOn(day, own.Shifts, loc), storeShifts)
}

// exceptionRow é uma folga/feriado/bloqueio com data.
type exceptionRow struct {
	ID           uuid.UUID  `json:"id"`
	Professional *uuid.UUID `json:"professional_id"`
	Date         time.Time  `json:"-"`
	StartMinute  *int       `json:"start_minute"`
	EndMinute    *int       `json:"end_minute"`
	Reason       string     `json:"reason"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (e exceptionRow) interval(loc *time.Location) Interval {
	d := Day{e.Date.Year(), e.Date.Month(), e.Date.Day()}
	if e.StartMinute == nil {
		return Interval{edge(d, 0, loc), edge(d, 1440, loc)}
	}
	return Interval{edge(d, *e.StartMinute, loc), edge(d, *e.EndMinute, loc)}
}

func exceptionsBetween(ctx context.Context, q database.Querier, estID uuid.UUID, from, to Day) ([]exceptionRow, error) {
	rows, err := q.Query(ctx, `
		SELECT id, professional_id, local_date, start_minute, end_minute, reason, created_at FROM agenda_exception
		WHERE establishment_id = $1 AND local_date BETWEEN $2 AND $3 AND removed_at IS NULL
		ORDER BY local_date, start_minute NULLS FIRST, id`, estID, from.Date(), to.Date())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (exceptionRow, error) {
		var e exceptionRow
		err := r.Scan(&e.ID, &e.Professional, &e.Date, &e.StartMinute, &e.EndMinute, &e.Reason, &e.CreatedAt)
		return e, err
	})
}

// closuresFor: intervalos fechados do profissional (exceções da loja inteira e as dele).
func closuresFor(exc []exceptionRow, profID uuid.UUID, loc *time.Location) []Interval {
	var out []Interval
	for _, e := range exc {
		if e.Professional == nil || *e.Professional == profID {
			out = append(out, e.interval(loc))
		}
	}
	return out
}

// occupied é uma ocupação ativa (compromisso ou bloqueio) de um profissional.
type occupied struct {
	Professional uuid.UUID
	Appointment  *uuid.UUID
	Interval     Interval
}

// occupancies lê as ocupações ativas; retenção de pagamento vencida (hold_until <= now) não conta.
func occupancies(ctx context.Context, q database.Querier, profIDs []uuid.UUID, span Interval, exclude *uuid.UUID, now time.Time) (map[uuid.UUID][]occupied, error) {
	rows, err := q.Query(ctx, `
		SELECT professional_id, appointment_id, lower(period), upper(period) FROM agenda_occupancy
		WHERE professional_id = ANY($1) AND released_at IS NULL AND period && tstzrange($2, $3, '[)')
		  AND ($4::uuid IS NULL OR appointment_id IS DISTINCT FROM $4) AND (hold_until IS NULL OR hold_until > $5)
		ORDER BY lower(period)`, profIDs, span.Start, span.End, exclude, now)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]occupied{}
	defer rows.Close()
	for rows.Next() {
		var o occupied
		if err := rows.Scan(&o.Professional, &o.Appointment, &o.Interval.Start, &o.Interval.End); err != nil {
			return nil, err
		}
		out[o.Professional] = append(out[o.Professional], o)
	}
	return out, rows.Err()
}

func intervalsOf(list []occupied) []Interval {
	out := make([]Interval, len(list))
	for i, o := range list {
		out[i] = o.Interval
	}
	return out
}

// dayPlan é a agenda calculada de um profissional num dia.
type dayPlan struct {
	Shifts     []Shift
	Busy       []Interval // tudo o que a reserva não pode invadir
	Projection *Projection
	// QueueOnly: há turno hoje mas nenhum trecho aceita fila (modo "os dois").
	QueueTime []Interval
	// ChairUntil: fim previsto do uso atual da cadeira (zero = livre).
	ChairUntil time.Time
}

// planInput reúne o que planDay precisa (lido antes, em lote).
type planInput struct {
	Day      Day
	Loc      *time.Location
	Now      time.Time
	Mode     string
	Settings estabelecimento.Settings
	Sched    map[uuid.UUID]weekly
	Exc      []exceptionRow
	Occ      map[uuid.UUID][]occupied
}

// planDay monta turnos e ocupados de um profissional. Hoje, soma a cadeira em uso e a fila
// projetada (FIFO nos trechos de fila) — é a mesma projeção que decide a entrada na fila.
func (s *Service) planDay(ctx context.Context, q database.Querier, in planInput, prof estabelecimento.ProfessionalRef, extraWaiting int) (dayPlan, error) {
	var p dayPlan
	p.Shifts = professionalShifts(in.Sched, prof.ID, in.Day, in.Loc)
	p.Busy = append(closuresFor(in.Exc, prof.ID, in.Loc), intervalsOf(in.Occ[prof.ID])...)
	p.QueueTime = eligible(p.Shifts, ForQueue(in.Mode))
	if DayOf(in.Now, in.Loc) != in.Day || s.Visits == nil {
		return p, nil
	}
	load, err := fila.QueueLoad(ctx, q, prof.QueueID, in.Now)
	if err != nil {
		return p, err
	}
	buffer := time.Duration(in.Settings.BufferMinutes) * time.Minute
	var qi QueueInput
	for _, e := range load {
		switch e.Status {
		case fila.InService, fila.Called:
			if until := e.BusyUntil.Add(buffer); until.After(qi.ChairBusyUntil) {
				qi.ChairBusyUntil = until
			}
		case fila.Waiting:
			qi.Waiting = append(qi.Waiting, e.Minutes+in.Settings.BufferMinutes)
		}
	}
	if extraWaiting > 0 {
		qi.Waiting = append(qi.Waiting, extraWaiting+in.Settings.BufferMinutes)
	}
	p.ChairUntil = qi.ChairBusyUntil
	proj := ProjectQueue(p.QueueTime, p.Busy, in.Now, qi)
	p.Projection = &proj
	if qi.ChairBusyUntil.After(in.Now) {
		p.Busy = append(p.Busy, Interval{in.Now, qi.ChairBusyUntil})
	}
	p.Busy = append(p.Busy, proj.Placed...)
	return p, nil
}

// slotsFor lista os inícios oferecidos num plano.
func slotsFor(in planInput, p dayPlan, totalMinutes int) []time.Time {
	return Slots(SlotInput{Day: in.Day, Loc: in.Loc, Mode: in.Mode, Shifts: p.Shifts, Busy: p.Busy,
		Step: in.Settings.SlotStepMinutes, Total: totalMinutes,
		Earliest: in.Now.Add(time.Duration(in.Settings.MinLeadMinutes) * time.Minute)})
}

// loadPlanInput lê jornada, exceções e ocupações do dia para os profissionais (em lote).
func loadPlanInput(ctx context.Context, q database.Querier, estID uuid.UUID, loc *time.Location, day Day, now time.Time,
	st estabelecimento.Settings, mode string, profIDs []uuid.UUID, exclude *uuid.UUID) (planInput, error) {
	in := planInput{Day: day, Loc: loc, Now: now, Mode: mode, Settings: st}
	var err error
	if in.Sched, err = schedulesOn(ctx, q, estID, day); err != nil {
		return in, err
	}
	if in.Exc, err = exceptionsBetween(ctx, q, estID, day, day); err != nil {
		return in, err
	}
	span := Interval{edge(day, 0, loc), edge(day, 1440, loc)}
	in.Occ, err = occupancies(ctx, q, profIDs, span, exclude, now)
	return in, err
}
