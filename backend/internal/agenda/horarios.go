package agenda

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// ---------------------------------------------------------------------------------------------
// Jornadas (getAgendaHours / setAgendaHours)

type scheduleView struct {
	ID            uuid.UUID  `json:"id"`
	EffectiveFrom string     `json:"effective_from"`
	Inherits      bool       `json:"inherits_store"`
	Shifts        []ShiftDef `json:"shifts"`
}

type subjectHours struct {
	ProfessionalID *uuid.UUID    `json:"professional_id"`
	DisplayName    string        `json:"display_name"`
	Current        *scheduleView `json:"current"`
	// Upcoming: versões já gravadas que passam a valer depois de hoje (vigência futura).
	Upcoming []scheduleView `json:"upcoming"`
}

type hoursView struct {
	Timezone      string         `json:"timezone"`
	Today         string         `json:"today"`
	Store         subjectHours   `json:"store"`
	Professionals []subjectHours `json:"professionals"`
}

func toView(w weekly) *scheduleView {
	return &scheduleView{ID: w.ID, EffectiveFrom: w.EffectiveFrom.Format(time.DateOnly), Inherits: w.Inherits, Shifts: w.Shifts}
}

func (s *Service) hours(ctx context.Context, q database.Querier, estID uuid.UUID, only *uuid.UUID) (hoursView, error) {
	e, loc, err := storeLoc(ctx, q, estID)
	if err != nil {
		return hoursView{}, err
	}
	today := DayOf(s.Clock.Now(), loc)
	cur, err := schedulesOn(ctx, q, estID, today)
	if err != nil {
		return hoursView{}, err
	}
	rows, err := q.Query(ctx, `SELECT id, professional_id, effective_from, inherits_store FROM agenda_schedule
		WHERE establishment_id = $1 AND effective_from > $2 ORDER BY effective_from, created_at, id`, estID, today.Date())
	if err != nil {
		return hoursView{}, err
	}
	future, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (weekly, error) {
		var w weekly
		err := r.Scan(&w.ID, &w.Professional, &w.EffectiveFrom, &w.Inherits)
		return w, err
	})
	if err != nil {
		return hoursView{}, err
	}
	// Uma versão por data (a última gravada vale).
	byDate := map[string]weekly{}
	for _, w := range future {
		k := w.EffectiveFrom.Format(time.DateOnly)
		if w.Professional != nil {
			k += w.Professional.String()
		}
		byDate[k] = w
	}
	future = future[:0]
	for _, w := range byDate {
		future = append(future, w)
	}
	slices.SortFunc(future, func(a, b weekly) int { return a.EffectiveFrom.Compare(b.EffectiveFrom) })
	if future, err = withShifts(ctx, q, future); err != nil {
		return hoursView{}, err
	}
	upcoming := func(prof *uuid.UUID) []scheduleView {
		out := []scheduleView{}
		for _, w := range future {
			if (prof == nil) == (w.Professional == nil) && (prof == nil || *prof == *w.Professional) {
				out = append(out, *toView(w))
			}
		}
		return out
	}
	v := hoursView{Timezone: e.Timezone, Today: today.String(), Store: subjectHours{DisplayName: e.Name, Upcoming: upcoming(nil)},
		Professionals: []subjectHours{}}
	if w, ok := cur[uuid.Nil]; ok {
		v.Store.Current = toView(w)
	}
	profs, err := estabelecimento.ActiveProfessionals(ctx, q, estID)
	if err != nil {
		return hoursView{}, err
	}
	for _, p := range profs {
		if only != nil && *only != p.ID {
			continue
		}
		id := p.ID
		sh := subjectHours{ProfessionalID: &id, DisplayName: p.Name, Upcoming: upcoming(&id)}
		if w, ok := cur[p.ID]; ok {
			sh.Current = toView(w)
		}
		v.Professionals = append(v.Professionals, sh)
	}
	return v, nil
}

func (s *Service) getHours(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.staffActor(r, estID, nil)
	if err != nil {
		return err
	}
	var only *uuid.UUID
	if !actor.IsOwner() {
		only = actor.ProfessionalID
	}
	v, err := s.hours(r.Context(), s.Pool, estID, only)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[hoursView]{Data: v})
	return nil
}

type setHoursRequest struct {
	ProfessionalID *uuid.UUID  `json:"professional_id"`
	EffectiveFrom  *string     `json:"effective_from"`
	InheritsStore  *bool       `json:"inherits_store"`
	Shifts         *[]ShiftDef `json:"shifts"`
}

// validateShifts confere formato e sobreposição dentro do mesmo dia da semana. Um turno que
// atravessa a meia-noite é informado em dois trechos (ex.: 22:00–24:00 e 00:00–02:00 do dia seguinte).
func validateShifts(v *validate.Collector, shifts []ShiftDef) {
	if len(shifts) > maxShiftsPerWeek {
		v.Add(&hs.FieldError{Field: "/shifts", Code: hs.FieldTooLong, Message: fmt.Sprintf("no máximo %d turnos por semana", maxShiftsPerWeek)})
		return
	}
	for i, sh := range shifts {
		f := fmt.Sprintf("/shifts/%d", i)
		switch {
		case sh.Weekday < 0 || sh.Weekday > 6:
			v.Add(&hs.FieldError{Field: f + "/weekday", Code: hs.FieldNotAllowed, Message: "dia da semana de 0 (domingo) a 6 (sábado)"})
		case sh.StartMinute < 0 || sh.StartMinute > 1439 || sh.EndMinute < 1 || sh.EndMinute > 1440:
			v.Add(&hs.FieldError{Field: f, Code: hs.FieldNotAllowed, Message: "horário fora do dia (00:00 a 24:00)"})
		case sh.StartMinute >= sh.EndMinute:
			v.Add(&hs.FieldError{Field: f, Code: "invalid_range", Message: "o fim precisa ser depois do início; turno que passa da meia-noite vira dois trechos"})
		case sh.Modality != ModalityQueue && sh.Modality != ModalitySchedule && sh.Modality != ModalityBoth:
			v.Add(&hs.FieldError{Field: f + "/modality", Code: hs.FieldNotAllowed, Message: "use queue, schedule ou both"})
		}
		for j := range i {
			o := shifts[j]
			if o.Weekday == sh.Weekday && sh.StartMinute < o.EndMinute && o.StartMinute < sh.EndMinute {
				v.Add(&hs.FieldError{Field: f, Code: "overlap", Message: fmt.Sprintf("turnos sobrepostos no mesmo dia (com o turno %d)", j)})
			}
		}
	}
}

// setAgendaHours (T-AG-01): o dono grava uma nova versão da jornada (loja ou profissional) com data
// de vigência. Turnos do profissional ficam dentro da abertura da loja. Se a mudança deixar alguma
// reserva confirmada fora do horário, nada é gravado: 409 com a lista (remaneje ou cancele antes).
func (s *Service) setHours(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	var req setHoursRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/effective_from", req.EffectiveFrom == nil)
	inherits := req.InheritsStore != nil && *req.InheritsStore
	var from Day
	if req.EffectiveFrom != nil {
		var ok bool
		if from, ok = ParseDay(*req.EffectiveFrom); !ok {
			v.Add(&hs.FieldError{Field: "/effective_from", Code: hs.FieldInvalidFormat, Message: "use AAAA-MM-DD"})
		}
	}
	switch {
	case inherits && req.ProfessionalID == nil:
		v.Add(&hs.FieldError{Field: "/inherits_store", Code: hs.FieldNotAllowed, Message: "só um profissional herda a jornada da loja"})
	case inherits && req.Shifts != nil && len(*req.Shifts) > 0:
		v.Add(&hs.FieldError{Field: "/shifts", Code: hs.FieldNotAllowed, Message: "quem herda a jornada da loja não tem turnos próprios"})
	case !inherits:
		v.Required("/shifts", req.Shifts == nil)
		if req.Shifts != nil {
			validateShifts(&v, *req.Shifts)
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "setAgendaHours", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		_, loc, err := storeLoc(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		now := s.Clock.Now()
		if from.Before(DayOf(now, loc)) {
			return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/effective_from", Code: hs.FieldNotAllowed,
				Message: "a vigência começa hoje ou depois (o passado não é reescrito)"})
		}
		profs, err := estabelecimento.ActiveProfessionals(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		// Trava as cadeiras afetadas (ordem por id; mesma ordem de todos os escritores).
		affected := profs
		if req.ProfessionalID != nil {
			i := slices.IndexFunc(profs, func(p estabelecimento.ProfessionalRef) bool { return p.ID == *req.ProfessionalID })
			if i < 0 {
				return idempotency.Result{}, hs.ErrNotFound()
			}
			affected = profs[i : i+1]
		}
		if err := s.lockChairs(ctx, tx, estID, affected); err != nil {
			return idempotency.Result{}, err
		}
		shifts := []ShiftDef{}
		if req.Shifts != nil && !inherits {
			shifts = *req.Shifts
		}
		// Turnos do profissional dentro da abertura da loja vigente na data.
		if req.ProfessionalID != nil && !inherits {
			sched, err := schedulesOn(ctx, tx, estID, from)
			if err != nil {
				return idempotency.Result{}, err
			}
			store := sched[uuid.Nil].Shifts
			for i, sh := range shifts {
				if !coveredByStore(sh, store) {
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: fmt.Sprintf("/shifts/%d", i), Code: "outside_store_hours",
						Message: "o turno do profissional precisa ficar dentro do horário de abertura da loja"})
				}
			}
		}
		id, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `INSERT INTO agenda_schedule (id, establishment_id, professional_id, effective_from, inherits_store, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, estID, req.ProfessionalID, from.Date(), inherits, actor.AccountID, now); err != nil {
			return idempotency.Result{}, err
		}
		for _, sh := range shifts {
			if _, err := tx.Exec(ctx, `INSERT INTO agenda_shift (schedule_id, weekday, start_minute, end_minute, modality) VALUES ($1, $2, $3, $4, $5)`,
				id, sh.Weekday, sh.StartMinute, sh.EndMinute, sh.Modality); err != nil {
				return idempotency.Result{}, err
			}
		}
		// Com a versão nova já gravada (na transação), nenhuma reserva confirmada pode ficar fora.
		st, err := estabelecimento.LoadSettings(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		out, err := s.outsideHours(ctx, tx, estID, loc, from, affected, st)
		if err != nil {
			return idempotency.Result{}, err
		}
		if len(out) > 0 {
			return idempotency.Result{}, conflict("schedule_conflicts_appointments", "Há reservas fora do novo horário",
				fmt.Sprintf("%d reserva(s) confirmada(s) ficariam fora do novo horário. Remaneje ou cancele com motivo antes; nada foi alterado.", len(out)),
				map[string]any{"appointments": out})
		}
		if err := s.recordAudit(ctx, tx, estID, actor, "T-AG-01", "agenda_schedule", id, "", "", now); err != nil {
			return idempotency.Result{}, err
		}
		var only *uuid.UUID
		v, err := s.hours(ctx, tx, estID, only)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: hs.Data[hoursView]{Data: v}}, nil
	})
}

// coveredByStore: o turno do profissional cabe inteiro dentro de turnos contíguos da loja no dia.
func coveredByStore(sh ShiftDef, store []ShiftDef) bool {
	cur := sh.StartMinute
	for {
		advanced := false
		for _, s := range store {
			if s.Weekday == sh.Weekday && s.StartMinute <= cur && cur < s.EndMinute {
				cur, advanced = s.EndMinute, true
			}
		}
		if cur >= sh.EndMinute {
			return true
		}
		if !advanced {
			return false
		}
	}
}

// lockChairs trava as filas (cadeiras) dos profissionais, ordenadas por id.
func lockChairs(ctx context.Context, tx pgx.Tx, estID uuid.UUID, profs []estabelecimento.ProfessionalRef) error {
	ids := make([]uuid.UUID, len(profs))
	for i, p := range profs {
		ids[i] = p.QueueID
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := estabelecimento.LockQueues(ctx, tx, estID, ids...)
	if errors.Is(err, estabelecimento.ErrNotFound) {
		return hs.ErrNotFound()
	}
	return err
}

// conflictItem é uma reserva afetada (resposta de conflito para a equipe; sem dados de contato).
type conflictItem struct {
	ID             uuid.UUID `json:"id"`
	ProfessionalID uuid.UUID `json:"professional_id"`
	StartsAt       time.Time `json:"starts_at"`
	Nick           string    `json:"nick"`
}

// outsideHours lista as reservas confirmadas a partir de from que não cabem mais nos turnos de agenda.
func (s *Service) outsideHours(ctx context.Context, tx pgx.Tx, estID uuid.UUID, loc *time.Location, from Day,
	profs []estabelecimento.ProfessionalRef, st estabelecimento.Settings) ([]conflictItem, error) {
	ids := make([]uuid.UUID, len(profs))
	for i, p := range profs {
		ids[i] = p.ID
	}
	rows, err := tx.Query(ctx, appointmentSelect+` WHERE establishment_id = $1 AND professional_id = ANY($2)
		AND status IN ('held', 'confirmed') AND local_date >= $3 ORDER BY starts_at LIMIT 500`, estID, ids, from.Date())
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (appointment, error) { return scanAppointment(r) })
	if err != nil {
		return nil, err
	}
	out := []conflictItem{}
	cache := map[string]map[uuid.UUID]weekly{}
	for _, a := range list {
		d := DayOf(a.StartsAt, loc)
		sched, ok := cache[d.String()]
		if !ok {
			if sched, err = schedulesOn(ctx, tx, estID, d); err != nil {
				return nil, err
			}
			cache[d.String()] = sched
		}
		free := eligible(professionalShifts(sched, a.ProfessionalID, d, loc), ForSchedule(s.mode(st)))
		if !inside(free, Interval{a.StartsAt, a.EndsAt}) {
			out = append(out, conflictItem{ID: a.ID, ProfessionalID: a.ProfessionalID, StartsAt: a.StartsAt.UTC(), Nick: a.Nick})
		}
	}
	return out, nil
}

func (s *Service) recordAudit(ctx context.Context, q database.Querier, estID uuid.UUID, actor acesso.Actor, action, target string,
	id uuid.UUID, to, reason string, now time.Time) error {
	return recordAudit(ctx, q, estID, staffActorInfo(actor), action, target, id, to, reason, now)
}

// ---------------------------------------------------------------------------------------------
// Exceções: folga, feriado e bloqueio (T-AG-02 / T-AG-03)

type exceptionView struct {
	ID             uuid.UUID  `json:"id"`
	ProfessionalID *uuid.UUID `json:"professional_id"`
	Date           string     `json:"date"`
	StartMinute    *int       `json:"start_minute"`
	EndMinute      *int       `json:"end_minute"`
	Reason         string     `json:"reason"`
	CreatedAt      time.Time  `json:"created_at"`
}

func exceptionViews(list []exceptionRow, only *uuid.UUID) []exceptionView {
	out := []exceptionView{}
	for _, e := range list {
		if only != nil && e.Professional != nil && *e.Professional != *only {
			continue
		}
		out = append(out, exceptionView{ID: e.ID, ProfessionalID: e.Professional, Date: e.Date.Format(time.DateOnly),
			StartMinute: e.StartMinute, EndMinute: e.EndMinute, Reason: e.Reason, CreatedAt: e.CreatedAt.UTC()})
	}
	return out
}

func (s *Service) listExceptions(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.staffActor(r, estID, nil)
	if err != nil {
		return err
	}
	ctx := r.Context()
	_, loc, err := storeLoc(ctx, s.Pool, estID)
	if err != nil {
		return err
	}
	from, to, err := dayRange(r, DayOf(s.Clock.Now(), loc), maxExceptionRangeDays, 30)
	if err != nil {
		return err
	}
	list, err := exceptionsBetween(ctx, s.Pool, estID, from, to)
	if err != nil {
		return err
	}
	var only *uuid.UUID
	if !actor.IsOwner() {
		only = actor.ProfessionalID
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]exceptionView]{Data: exceptionViews(list, only)})
	return nil
}

// dayRange lê ?from=&to= (datas locais), com padrão e limite de dias.
func dayRange(r *http.Request, today Day, maxDays, defaultDays int) (Day, Day, error) {
	from, to := today, today.AddDays(defaultDays-1)
	var v validate.Collector
	if s := r.URL.Query().Get("from"); s != "" {
		d, ok := ParseDay(s)
		if !ok {
			v.Add(&hs.FieldError{Field: "from", Code: hs.FieldInvalidFormat, Message: "use AAAA-MM-DD"})
		}
		from, to = d, d.AddDays(defaultDays-1)
	}
	if s := r.URL.Query().Get("to"); s != "" {
		d, ok := ParseDay(s)
		if !ok {
			v.Add(&hs.FieldError{Field: "to", Code: hs.FieldInvalidFormat, Message: "use AAAA-MM-DD"})
		}
		to = d
	}
	if err := v.Err(); err != nil {
		return from, to, err
	}
	if to.Before(from) || from.AddDays(maxDays-1).Before(to) {
		return from, to, hs.ErrValidation(hs.FieldError{Field: "to", Code: "invalid_range",
			Message: fmt.Sprintf("intervalo de 1 a %d dias", maxDays)})
	}
	return from, to, nil
}

type exceptionRequest struct {
	ProfessionalID *uuid.UUID `json:"professional_id"`
	Date           *string    `json:"date"`
	StartMinute    *int64     `json:"start_minute"`
	EndMinute      *int64     `json:"end_minute"`
	Reason         *string    `json:"reason"`
}

// createAgendaException (T-AG-02): folga/feriado (dia inteiro) ou bloqueio de um trecho, para a loja
// inteira ou um profissional. Dono sempre; profissional só na própria agenda e só com a permissão
// "profissionais podem bloquear" ligada. Se invadir reserva ativa, nada é gravado (409 com a lista).
func (s *Service) createException(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	var req exceptionRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	actor, err := s.staffActor(r, estID, req.ProfessionalID)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/date", req.Date == nil)
	v.Required("/reason", req.Reason == nil)
	var day Day
	if req.Date != nil {
		var ok bool
		if day, ok = ParseDay(*req.Date); !ok {
			v.Add(&hs.FieldError{Field: "/date", Code: hs.FieldInvalidFormat, Message: "use AAAA-MM-DD"})
		}
	}
	reason := ""
	if req.Reason != nil {
		var fe *hs.FieldError
		reason, fe = validate.Text("/reason", *req.Reason, 3, 200)
		v.Add(fe)
	}
	if (req.StartMinute == nil) != (req.EndMinute == nil) {
		v.Add(&hs.FieldError{Field: "/end_minute", Code: hs.FieldRequired, Message: "informe início e fim, ou nenhum (dia inteiro)"})
	}
	if req.StartMinute != nil && req.EndMinute != nil {
		v.IntRange("/start_minute", req.StartMinute, 0, 1439)
		v.IntRange("/end_minute", req.EndMinute, 1, 1440)
		if *req.StartMinute >= *req.EndMinute {
			v.Add(&hs.FieldError{Field: "/end_minute", Code: "invalid_range", Message: "o fim precisa ser depois do início"})
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	if !actor.IsOwner() && req.ProfessionalID == nil {
		return hs.ErrForbidden("Só o dono fecha a loja inteira.")
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "createAgendaException", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		if !actor.IsOwner() {
			st, err := estabelecimento.LoadSettings(ctx, tx, estID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if !st.ProfessionalsCanBlock {
				return idempotency.Result{}, hs.ErrForbidden("O dono não liberou bloqueios na agenda pelos profissionais.")
			}
		}
		_, loc, err := storeLoc(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		now := s.Clock.Now()
		if day.Before(DayOf(now, loc)) {
			return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/date", Code: hs.FieldNotAllowed, Message: "a data já passou"})
		}
		profs, err := estabelecimento.ActiveProfessionals(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if req.ProfessionalID != nil {
			i := slices.IndexFunc(profs, func(p estabelecimento.ProfessionalRef) bool { return p.ID == *req.ProfessionalID })
			if i < 0 {
				return idempotency.Result{}, hs.ErrNotFound()
			}
			profs = profs[i : i+1]
		}
		if err := s.lockChairs(ctx, tx, estID, profs); err != nil {
			return idempotency.Result{}, err
		}
		var sm, em *int
		if req.StartMinute != nil {
			sm, em = ptr(int(*req.StartMinute)), ptr(int(*req.EndMinute))
		}
		ex := exceptionRow{Professional: req.ProfessionalID, Date: day.Date(), StartMinute: sm, EndMinute: em}
		iv := ex.interval(loc)
		ids := make([]uuid.UUID, len(profs))
		for i, p := range profs {
			ids[i] = p.ID
		}
		hits, err := activeAppointmentsIn(ctx, tx, estID, ids, iv)
		if err != nil {
			return idempotency.Result{}, err
		}
		if len(hits) > 0 {
			return idempotency.Result{}, conflict("exception_conflicts_appointments", "Há reservas neste período",
				fmt.Sprintf("%d reserva(s) ativa(s) neste período. Remaneje ou cancele com motivo antes de bloquear; nada foi alterado.", len(hits)),
				map[string]any{"appointments": hits})
		}
		id, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `INSERT INTO agenda_exception (id, establishment_id, professional_id, local_date, start_minute, end_minute,
			reason, created_by, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id, estID, req.ProfessionalID, day.Date(), sm, em, reason, actor.AccountID, now); err != nil {
			return idempotency.Result{}, err
		}
		for _, p := range profs {
			if _, err := tx.Exec(ctx, `INSERT INTO agenda_occupancy (establishment_id, professional_id, kind, exception_id, period)
				VALUES ($1, $2, 'block', $3, tstzrange($4, $5, '[)'))`, estID, p.ID, id, iv.Start, iv.End); err != nil {
				if isOverlap(err) {
					return idempotency.Result{}, conflict("exception_conflicts_appointments", "Há reservas neste período",
						"Outra reserva ocupou este período agora. Confira a agenda; nada foi alterado.", nil)
				}
				return idempotency.Result{}, err
			}
		}
		if err := s.recordAudit(ctx, tx, estID, actor, "T-AG-02", "agenda_exception", id, "active", reason, now); err != nil {
			return idempotency.Result{}, err
		}
		list, err := exceptionsBetween(ctx, tx, estID, day, day)
		if err != nil {
			return idempotency.Result{}, err
		}
		for _, e := range exceptionViews(list, nil) {
			if e.ID == id {
				return idempotency.Result{Status: http.StatusCreated, Body: hs.Data[exceptionView]{Data: e}}, nil
			}
		}
		return idempotency.Result{}, errors.New("exceção recém-criada não encontrada")
	})
}

// activeAppointmentsIn lista reservas ativas (confirmadas, chamadas ou em atendimento) no período.
func activeAppointmentsIn(ctx context.Context, q database.Querier, estID uuid.UUID, profIDs []uuid.UUID, iv Interval) ([]conflictItem, error) {
	rows, err := q.Query(ctx, `SELECT id, professional_id, starts_at, nick FROM appointment
		WHERE establishment_id = $1 AND professional_id = ANY($2) AND status IN ('held', 'confirmed', 'called', 'in_service')
		  AND tstzrange(starts_at, ends_at, '[)') && tstzrange($3, $4, '[)') ORDER BY starts_at LIMIT 200`,
		estID, profIDs, iv.Start, iv.End)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (conflictItem, error) {
		var c conflictItem
		err := r.Scan(&c.ID, &c.ProfessionalID, &c.StartsAt, &c.Nick)
		c.StartsAt = c.StartsAt.UTC()
		return c, err
	})
}

// removeAgendaException (T-AG-03): desfaz uma exceção (o histórico fica; a ocupação é liberada).
func (s *Service) removeException(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	exID, err := pathID(r, "exceptionId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var prof *uuid.UUID
	err = s.Pool.QueryRow(ctx, `SELECT professional_id FROM agenda_exception WHERE establishment_id = $1 AND id = $2 AND removed_at IS NULL`,
		estID, exID).Scan(&prof)
	if errors.Is(err, pgx.ErrNoRows) {
		return hs.ErrNotFound()
	}
	if err != nil {
		return err
	}
	actor, err := s.staffActor(r, estID, prof)
	if err != nil {
		return err
	}
	if !actor.IsOwner() && prof == nil {
		return hs.ErrForbidden("Só o dono reabre a loja inteira.")
	}
	var empty struct{}
	body, err := hs.DecodeJSON(r, &empty)
	if err != nil {
		return err
	}
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "removeAgendaException", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.Clock.Now()
		tag, err := tx.Exec(ctx, `UPDATE agenda_exception SET removed_at = $3, removed_by = $4
			WHERE establishment_id = $1 AND id = $2 AND removed_at IS NULL`, estID, exID, now, actor.AccountID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if tag.RowsAffected() == 0 {
			return idempotency.Result{}, hs.ErrNotFound()
		}
		if _, err := tx.Exec(ctx, `UPDATE agenda_occupancy SET released_at = $2 WHERE exception_id = $1 AND released_at IS NULL`, exID, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := s.recordAudit(ctx, tx, estID, actor, "T-AG-03", "agenda_exception", exID, "removed", "", now); err != nil {
			return idempotency.Result{}, err
		}
		type removed struct {
			ID        uuid.UUID `json:"id"`
			RemovedAt time.Time `json:"removed_at"`
		}
		return idempotency.Result{Status: http.StatusOK, Body: hs.Data[removed]{Data: removed{exID, now}}}, nil
	})
}
