package agenda

import (
	"context"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/validate"
)

// GET /v1/public/establishments/{id}/availability/days: quais dias de um intervalo (até 62) têm
// ao menos um horário para a seleção. Consulta agregada e limitada: jornada (todas as versões),
// exceções e ocupações do intervalo inteiro em 4 leituras, e o cálculo de cada dia em memória com a
// MESMA regra da confirmação (planDay/slotsFor). Os horários detalhados continuam vindo do GET do
// dia escolhido. Nunca diz quem ocupa a cadeira.

type dayFlag struct {
	Date     string `json:"date"`
	HasSlots bool   `json:"has_slots"`
}

type daysView struct {
	Timezone       string    `json:"timezone"`
	ProfessionalID uuid.UUID `json:"professional_id"`
	Days           []dayFlag `json:"days"`
}

// scheduleIndex carrega todas as versões de jornada da loja até "to" e devolve a vigente por dia.
func scheduleIndex(ctx context.Context, q database.Querier, estID uuid.UUID, to Day) (func(Day) map[uuid.UUID]weekly, error) {
	rows, err := q.Query(ctx, `
		SELECT id, professional_id, effective_from, inherits_store FROM agenda_schedule
		WHERE establishment_id = $1 AND effective_from <= $2 ORDER BY effective_from, created_at, id`, estID, to.Date())
	if err != nil {
		return nil, err
	}
	versions, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (weekly, error) {
		var w weekly
		err := r.Scan(&w.ID, &w.Professional, &w.EffectiveFrom, &w.Inherits)
		return w, err
	})
	if err != nil {
		return nil, err
	}
	if versions, err = withShifts(ctx, q, versions); err != nil {
		return nil, err
	}
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].EffectiveFrom.Before(versions[j].EffectiveFrom) })
	return func(d Day) map[uuid.UUID]weekly {
		out := map[uuid.UUID]weekly{}
		for _, w := range versions { // em ordem de vigência: a última até a data vale
			if w.EffectiveFrom.After(d.Date()) {
				break
			}
			key := uuid.Nil
			if w.Professional != nil {
				key = *w.Professional
			}
			out[key] = w
		}
		return out
	}, nil
}

func (s *Service) getAvailabilityDays(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	qs := r.URL.Query()
	var v validate.Collector
	from, okFrom := ParseDay(qs.Get("from"))
	to, okTo := ParseDay(qs.Get("to"))
	if !okFrom {
		v.Add(&hs.FieldError{Field: "from", Code: hs.FieldInvalidFormat, Message: "use AAAA-MM-DD"})
	}
	if !okTo {
		v.Add(&hs.FieldError{Field: "to", Code: hs.FieldInvalidFormat, Message: "use AAAA-MM-DD"})
	}
	profID, perr := uuid.Parse(qs.Get("professional_id"))
	if perr != nil {
		v.Add(&hs.FieldError{Field: "professional_id", Code: hs.FieldRequired, Message: "um profissional"})
	}
	lines := parseItemsQuery(&v, qs["items"])
	if okFrom && okTo && (to.Before(from) || from.AddDays(maxExceptionRangeDays).Before(to)) {
		v.Add(&hs.FieldError{Field: "to", Code: "out_of_window", Message: "até 62 dias a partir de from"})
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	var out daysView
	err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		out, err = s.availableDays(ctx, tx, estID, profID, from, to, *lines)
		return err
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	hs.WriteJSON(w, http.StatusOK, hs.Data[daysView]{Data: out})
	return nil
}

func (s *Service) availableDays(ctx context.Context, q database.Querier, estID, profID uuid.UUID, from, to Day,
	lines []atendimento.LineRequest) (daysView, error) {
	e, loc, err := storeLoc(ctx, q, estID)
	if err != nil {
		return daysView{}, err
	}
	st, err := estabelecimento.LoadSettings(ctx, q, estID)
	if err != nil {
		return daysView{}, err
	}
	mode := s.mode(st)
	if mode != estabelecimento.ModeSchedule && mode != estabelecimento.ModeHybrid {
		return daysView{}, conflict("schedule_disabled", "Esta barbearia não está marcando horário",
			"No momento o atendimento é só por ordem de chegada.", nil)
	}
	prof, err := professionalOf(ctx, q, estID, profID)
	if err != nil {
		return daysView{}, err
	}
	out := daysView{Timezone: e.Timezone, ProfessionalID: profID, Days: []dayFlag{}}
	byProf, err := resolveEach(ctx, q, estID, []estabelecimento.ProfessionalRef{prof}, lines, st)
	if err != nil {
		return out, err
	}
	resolved, eligible := byProf[prof.ID]
	if !eligible {
		return out, conflict("service_not_eligible", "Serviço não feito por este profissional",
			prof.Name+" não faz algum dos serviços escolhidos. Escolha outro profissional.", nil)
	}
	mins, _ := totals(resolved)
	now := s.Clock.Now()
	today := DayOf(now, loc)
	last := today.AddDays(st.HorizonDays)
	if from.Before(today) {
		from = today
	}
	if last.Before(to) {
		to = last
	}
	if to.Before(from) {
		return out, nil
	}
	idx, err := scheduleIndex(ctx, q, estID, to)
	if err != nil {
		return out, err
	}
	exc, err := exceptionsBetween(ctx, q, estID, from, to)
	if err != nil {
		return out, err
	}
	span := Interval{edge(from, 0, loc), edge(to, 1440, loc)}
	occ, err := occupancies(ctx, q, []uuid.UUID{prof.ID}, span, nil, now)
	if err != nil {
		return out, err
	}
	for d := from; !to.Before(d); d = d.AddDays(1) {
		in := planInput{Day: d, Loc: loc, Now: now, Mode: mode, Settings: st, Sched: idx(d), Exc: exc, Occ: occ}
		plan, err := s.planDay(ctx, q, in, prof, 0) // só "hoje" lê a fila (uma consulta)
		if err != nil {
			return out, err
		}
		out.Days = append(out.Days, dayFlag{Date: d.String(), HasSlots: len(slotsFor(in, plan, mins+st.BufferMinutes)) > 0})
	}
	return out, nil
}
