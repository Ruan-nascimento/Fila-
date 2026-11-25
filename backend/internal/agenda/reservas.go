package agenda

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/pagamento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// ---------------------------------------------------------------------------------------------
// Disponibilidade (getAvailability, público)

type slotView struct {
	StartsAt  time.Time `json:"starts_at"`
	LocalTime string    `json:"local_time"`
}

type queueEstimate struct {
	// NextStart: início ESTIMADO de quem entrar agora na fila (hoje, modo "os dois").
	NextStart   *time.Time `json:"next_start"`
	HasCapacity bool       `json:"has_capacity"`
}

type professionalSlots struct {
	ProfessionalID uuid.UUID      `json:"professional_id"`
	DisplayName    string         `json:"display_name"`
	Eligible       bool           `json:"eligible"`
	Slots          []slotView     `json:"slots"`
	Queue          *queueEstimate `json:"queue"`
}

type availabilityView struct {
	Date            string `json:"date"`
	Timezone        string `json:"timezone"`
	Mode            string `json:"mode"`
	FirstDate       string `json:"first_date"`
	LastDate        string `json:"last_date"`
	VisitorLastDate string `json:"visitor_last_date"`
	StepMinutes     int    `json:"step_minutes"`
	ServiceMinutes  int    `json:"service_minutes"`
	BufferMinutes   int    `json:"buffer_minutes"`
	TotalMinutes    int    `json:"total_minutes"`
	Price           money  `json:"price"`
	// Payment: o que se paga agora e na barbearia por esta escolha (calculado no servidor; a
	// confirmação refaz a conta).
	Payment       pagamento.Quote     `json:"payment"`
	Professionals []professionalSlots `json:"professionals"`
}

// parseItemsQuery lê items=<serviço ou produto>[:quantidade] (repetido).
func parseItemsQuery(v *validate.Collector, raw []string) *[]atendimento.LineRequest {
	if len(raw) == 0 {
		v.Required("items", true)
		return nil
	}
	if len(raw) > 20 {
		v.Add(&hs.FieldError{Field: "items", Code: hs.FieldTooLong, Message: "no máximo 20 itens"})
		return nil
	}
	out := make([]atendimento.LineRequest, 0, len(raw))
	for i, s := range raw {
		idPart, qty, _ := strings.Cut(s, ":")
		id, err := uuid.Parse(idPart)
		if err != nil {
			v.Add(&hs.FieldError{Field: "items/" + strconv.Itoa(i), Code: hs.FieldInvalidFormat, Message: "use <id> ou <id>:<quantidade>"})
			continue
		}
		var q *int64
		if qty != "" {
			n, err := strconv.ParseInt(qty, 10, 64)
			if err != nil {
				v.Add(&hs.FieldError{Field: "items/" + strconv.Itoa(i), Code: hs.FieldInvalidFormat, Message: "quantidade inteira"})
				continue
			}
			q = &n
		}
		// O mesmo id serve de serviço ou produto: o catálogo decide (produto tem duração zero).
		out = append(out, atendimento.LineRequest{ServiceID: &id, Quantity: q})
	}
	return &out
}

// resolveForProfessional resolve os itens (serviço OU produto) no catálogo, com preço e duração
// do servidor, para o profissional. Ids que não são serviço são tentados como produto.
func resolveForProfessional(ctx context.Context, q database.Querier, estID uuid.UUID, prof estabelecimento.ProfessionalRef,
	lines []atendimento.LineRequest, st estabelecimento.Settings) ([]atendimento.Line, error) {
	ids := make([]uuid.UUID, 0, len(lines))
	for _, l := range lines {
		if l.ServiceID != nil {
			ids = append(ids, *l.ServiceID)
		}
		if l.ProductID != nil {
			ids = append(ids, *l.ProductID)
		}
	}
	svc, err := catalogo.ServicesForEntry(ctx, q, estID, ids)
	if err != nil {
		return nil, err
	}
	fixed := make([]atendimento.LineRequest, len(lines))
	for i, l := range lines {
		fixed[i] = l
		if l.ServiceID != nil {
			if _, ok := svc[*l.ServiceID]; !ok {
				fixed[i] = atendimento.LineRequest{ProductID: l.ServiceID, Quantity: l.Quantity}
			}
		}
	}
	var v validate.Collector
	wants := atendimento.ParseLines(&v, "/items", &fixed, 1)
	if err := v.Err(); err != nil {
		return nil, err
	}
	return atendimento.ResolveNewLines(ctx, q, estID, prof.ID, prof.Name, wants, st)
}

func totals(lines []atendimento.Line) (minutes int, cents int64) {
	for _, l := range lines {
		cents += l.Snap.PriceCents * int64(l.Quantity)
		if l.Snap.Kind == catalogo.KindService {
			minutes += l.Snap.DurationMinutes * l.Quantity
		}
	}
	return
}

func (s *Service) getAvailability(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	qs := r.URL.Query()
	var v validate.Collector
	day, ok := ParseDay(qs.Get("date"))
	if !ok {
		v.Add(&hs.FieldError{Field: "date", Code: hs.FieldInvalidFormat, Message: "use AAAA-MM-DD"})
	}
	lines := parseItemsQuery(&v, qs["items"])
	var only []uuid.UUID
	for i, s := range qs["professional_id"] {
		id, err := uuid.Parse(s)
		if err != nil {
			v.Add(&hs.FieldError{Field: "professional_id/" + strconv.Itoa(i), Code: hs.FieldInvalidFormat})
			continue
		}
		only = append(only, id)
	}
	if len(only) > maxProfessionalsPerQuery {
		v.Add(&hs.FieldError{Field: "professional_id", Code: hs.FieldTooLong, Message: fmt.Sprintf("no máximo %d profissionais por consulta", maxProfessionalsPerQuery)})
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	var out availabilityView
	// Transação comum (não somente leitura): o catálogo é lido com FOR SHARE, como na confirmação.
	err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		out, err = s.availability(ctx, tx, estID, day, *lines, only, nil)
		return err
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	hs.WriteJSON(w, http.StatusOK, hs.Data[availabilityView]{Data: out})
	return nil
}

// availability calcula os horários livres do dia (mesma regra usada ao confirmar, sob trava).
// exclude: reserva sendo reagendada (a ocupação dela não conta).
func (s *Service) availability(ctx context.Context, q database.Querier, estID uuid.UUID, day Day, lines []atendimento.LineRequest,
	only []uuid.UUID, exclude *uuid.UUID) (availabilityView, error) {
	e, loc, err := storeLoc(ctx, q, estID)
	if err != nil {
		return availabilityView{}, err
	}
	st, err := estabelecimento.LoadSettings(ctx, q, estID)
	if err != nil {
		return availabilityView{}, err
	}
	mode := s.mode(st)
	if mode != estabelecimento.ModeSchedule && mode != estabelecimento.ModeHybrid {
		return availabilityView{}, conflict("schedule_disabled", "Esta barbearia não está marcando horário",
			"No momento o atendimento é só por ordem de chegada.", nil)
	}
	now := s.Clock.Now()
	today := DayOf(now, loc)
	last := today.AddDays(st.HorizonDays)
	out := availabilityView{Date: day.String(), Timezone: e.Timezone, Mode: mode, FirstDate: today.String(), LastDate: last.String(),
		VisitorLastDate: minDay(last, today.AddDays(visitorHorizonDays)).String(), StepMinutes: st.SlotStepMinutes,
		BufferMinutes: st.BufferMinutes, Professionals: []professionalSlots{}}
	if day.Before(today) || last.Before(day) {
		return out, hs.ErrValidation(hs.FieldError{Field: "date", Code: "out_of_window",
			Message: fmt.Sprintf("escolha uma data entre %s e %s", today.String(), last.String())})
	}
	profs, err := estabelecimento.ActiveProfessionals(ctx, q, estID)
	if err != nil {
		return out, err
	}
	if len(only) > 0 {
		profs = slices.DeleteFunc(profs, func(p estabelecimento.ProfessionalRef) bool { return !slices.Contains(only, p.ID) })
	}
	if len(profs) > maxProfessionalsPerQuery {
		profs = profs[:maxProfessionalsPerQuery]
	}
	ids := make([]uuid.UUID, len(profs))
	for i, p := range profs {
		ids[i] = p.ID
	}
	in, err := loadPlanInput(ctx, q, estID, loc, day, now, st, mode, ids, exclude)
	if err != nil {
		return out, err
	}
	for _, p := range profs {
		ps := professionalSlots{ProfessionalID: p.ID, DisplayName: p.Name, Slots: []slotView{}}
		resolved, err := resolveForProfessional(ctx, q, estID, p, lines, st)
		var he *hs.Error
		switch {
		case errors.As(err, &he) && (he.Code == "service_not_eligible"):
			out.Professionals = append(out.Professionals, ps) // não faz algum dos serviços
			continue
		case err != nil:
			return out, err
		}
		ps.Eligible = true
		mins, cents := totals(resolved)
		out.ServiceMinutes, out.TotalMinutes, out.Price = mins, mins+st.BufferMinutes, brl(cents)
		plan, err := s.planDay(ctx, q, in, p, 0)
		if err != nil {
			return out, err
		}
		for _, t := range slotsFor(in, plan, mins+st.BufferMinutes) {
			ps.Slots = append(ps.Slots, slotView{StartsAt: t.UTC(), LocalTime: t.In(loc).Format("15:04")})
		}
		if plan.Projection != nil && mode == estabelecimento.ModeHybrid && len(plan.Shifts) > 0 {
			est := &queueEstimate{}
			if iv, ok := plan.Projection.NextQueueStart(now, mins+st.BufferMinutes); ok {
				est.NextStart, est.HasCapacity = utc(&iv.Start), true
			}
			ps.Queue = est
		}
		out.Professionals = append(out.Professionals, ps)
	}
	out.Payment = s.quote(st, out.Price.AmountCents)
	return out, nil
}

func minDay(a, b Day) Day {
	if a.Before(b) {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------------------------
// Criar reserva (T-AP-01)

type counterCustomer struct {
	Nick *string `json:"nick"`
}

type createRequest struct {
	ProfessionalID  *uuid.UUID                 `json:"professional_id"`
	StartsAt        *time.Time                 `json:"starts_at"`
	Items           *[]atendimento.LineRequest `json:"items"`
	CounterCustomer *counterCustomer           `json:"counter_customer"`
}

type createdView struct {
	appointmentView
	// CounterRecoveryCode: só na reserva de balcão, uma única vez (o dono entrega ao cliente).
	CounterRecoveryCode *string    `json:"counter_recovery_code,omitempty"`
	CounterCodeValid    *time.Time `json:"counter_recovery_code_valid_until,omitempty"`
}

// createAppointment (T-AP-01): reserva gratuita confirmada na hora. Cliente (conta verificada ou,
// se a loja aceita, visitante) para si; ou o dono no balcão, com identidade recuperável própria do
// cliente (visitante + código de recuperação), nunca uma identidade compartilhada. Preço, duração,
// margem, tolerância e janela vêm do servidor e ficam congelados na reserva.
func (s *Service) createAppointment(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	var req createRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	ctx := r.Context()
	// Quem reserva (antes de validar o conteúdo: anônimo recebe 401, não 400): cliente (cookies)
	// ou dono no balcão.
	var who *acesso.Identity
	var staff *acesso.Actor
	if req.CounterCustomer != nil {
		a, err := s.Auth.Owner(r, estID)
		if err != nil {
			return err
		}
		staff = &a
	} else {
		account, visitor := s.Customers.CustomerIdentities(r, estID)
		who = account
		if who == nil {
			who = visitor
		}
		if who == nil {
			return hs.ErrUnauthenticated()
		}
	}
	var v validate.Collector
	v.Required("/professional_id", req.ProfessionalID == nil)
	v.Required("/starts_at", req.StartsAt == nil)
	v.Required("/items", req.Items == nil)
	counterNick := ""
	if req.CounterCustomer != nil {
		v.Required("/counter_customer/nick", req.CounterCustomer.Nick == nil)
		if req.CounterCustomer.Nick != nil {
			var fe *hs.FieldError
			counterNick, fe = validate.Text("/counter_customer/nick", *req.CounterCustomer.Nick, 1, 30)
			v.Add(fe)
		}
	}
	if req.Items != nil && (len(*req.Items) == 0 || len(*req.Items) > 20) {
		v.Add(&hs.FieldError{Field: "/items", Code: hs.FieldNotAllowed, Message: "de 1 a 20 itens"})
	}
	if err := v.Err(); err != nil {
		return err
	}
	if who != nil {
		if err := s.allow(ctx, ruleBookIdentity, estID.String()+"|"+identityKey(*who)); err != nil {
			return err
		}
		if err := s.allow(ctx, ruleBookIP, estID.String()+"|"+hs.ClientIP(r, s.TrustForwardedFor)); err != nil {
			return err
		}
	}
	key := ""
	if staff != nil {
		key = actorKey(*staff)
	} else {
		key = identityKey(*who)
	}
	cmd := idempotency.Command{ActorKey: key, Operation: "createAppointment", Body: body}
	var counter struct {
		code  string
		until time.Time
	}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.Clock.Now()
		st, err := estabelecimento.LoadSettings(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if m := s.mode(st); m != estabelecimento.ModeSchedule && m != estabelecimento.ModeHybrid {
			return idempotency.Result{}, conflict("schedule_disabled", "Esta barbearia não está marcando horário",
				"No momento o atendimento é só por ordem de chegada.", nil)
		}
		_, loc, err := storeLoc(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if who != nil && who.Kind == acesso.IdentityVisitor && !st.VisitorsEnabled {
			return idempotency.Result{}, conflict("visitors_disabled", "Reserva como visitante desligada",
				"Esta barbearia não aceita reservas sem conta no momento. Entre com a sua conta.", nil)
		}
		start := req.StartsAt.UTC()
		day := DayOf(start, loc)
		today := DayOf(now, loc)
		horizon := today.AddDays(st.HorizonDays)
		if who != nil && who.Kind == acesso.IdentityVisitor {
			horizon = minDay(horizon, today.AddDays(visitorHorizonDays))
		} else if staff != nil {
			horizon = minDay(horizon, today.AddDays(counterHorizonDays))
		}
		if horizon.Before(day) {
			return idempotency.Result{}, conflict("outside_booking_window", "Data fora da janela de reservas",
				fmt.Sprintf("Escolha uma data até %s.", horizon.Date().Format("02/01")), nil)
		}
		// Balcão: o cliente ganha a própria identidade recuperável (visitante desta loja).
		if staff != nil {
			if s.CounterVisitor == nil {
				return idempotency.Result{}, errors.New("reserva de balcão indisponível")
			}
			vid, code, until, err := s.CounterVisitor(ctx, tx, estID, counterNick, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			who = &acesso.Identity{Kind: acesso.IdentityVisitor, VisitorID: vid, Name: counterNick}
			counter.code, counter.until = code, until
		}
		if err := lockIdentity(ctx, tx, estID, identityKey(*who)); err != nil {
			return idempotency.Result{}, err
		}
		prof, err := professionalOf(ctx, tx, estID, *req.ProfessionalID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if err := s.lockChairs(ctx, tx, estID, []estabelecimento.ProfessionalRef{prof}); err != nil {
			return idempotency.Result{}, err
		}
		lines, err := resolveForProfessional(ctx, tx, estID, prof, *req.Items, st)
		if err != nil {
			return idempotency.Result{}, err
		}
		mins, cents := totals(lines)
		// Limite de reservas futuras por identidade e loja (contado sob a trava da identidade).
		var future int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM appointment WHERE establishment_id = $1 AND status IN ('held', 'confirmed')
			AND (account_id = $2 OR visitor_id = $2) AND starts_at > $3`, estID, who.ID(), now).Scan(&future); err != nil {
			return idempotency.Result{}, err
		}
		if future >= st.MaxFutureAppointments {
			return idempotency.Result{}, conflict("appointment_limit_reached", "Limite de reservas",
				fmt.Sprintf("Você já tem %d horário(s) marcado(s) nesta barbearia. Cancele um para marcar outro.", future), nil)
		}
		if err := s.checkSlot(ctx, tx, estID, loc, day, now, st, prof, mins, start, nil); err != nil {
			return idempotency.Result{}, err
		}
		// Cobrança da reserva (ADR-0039): online e com a cobrança disponível; a de balcão é
		// gratuita (o cliente paga na barbearia). O valor e a política vêm do servidor.
		q := pagamento.Quote{Mode: estabelecimento.BookingFree}
		if staff == nil {
			q = s.quote(st, cents)
		}
		n := newAppointment{estID: estID, prof: prof, who: *who, origin: originOf(staff),
			start: start, day: day, loc: loc, mins: mins, cents: cents, st: st, lines: lines, now: now}
		if q.Mode != estabelecimento.BookingFree {
			hold := now.Add(s.holdDuration())
			if !hold.Before(start) {
				// Sem tempo para pagar antes do horário: a retenção nunca passa do início.
				return idempotency.Result{}, conflict("booking_too_close", "Horário muito próximo para pagar antes",
					"Esta barbearia cobra a reserva pelo Pix e não há tempo para pagar antes deste horário. Escolha um horário mais tarde.", nil)
			}
			n.hold = &hold
		}
		id, err := insertAppointment(ctx, tx, n)
		if err != nil {
			return idempotency.Result{}, err
		}
		if n.hold != nil {
			if _, err := s.Payments.CreateObligation(ctx, tx, pagamento.Obligation{EstablishmentID: estID, AppointmentID: id, Quote: q,
				HoldExpiresAt: *n.hold, PolicyVersion: st.AgendaVersion, Now: now}); err != nil {
				return idempotency.Result{}, err
			}
		}
		actor := customerActorInfo(*who)
		if staff != nil {
			actor = staffActorInfo(*staff)
		}
		if err := s.afterChange(ctx, tx, estID, id, "T-AP-01", "", actor, "", now); err != nil {
			return idempotency.Result{}, err
		}
		a, err := loadAppointment(ctx, tx, estID, id, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		av, err := view(ctx, tx, a, staff != nil)
		if err != nil {
			return idempotency.Result{}, err
		}
		res := idempotency.Result{Status: http.StatusCreated, Body: hs.Data[createdView]{Data: createdView{appointmentView: av}}}
		if staff != nil {
			// O código aparece uma única vez; a repetição devolve a reserva sem ele.
			res.Stored = res.Body
			res.Body = hs.Data[createdView]{Data: createdView{appointmentView: av, CounterRecoveryCode: &counter.code, CounterCodeValid: &counter.until}}
		}
		return res, nil
	})
}

func originOf(staff *acesso.Actor) string {
	if staff != nil {
		return "counter"
	}
	return "online"
}

func professionalOf(ctx context.Context, q database.Querier, estID, profID uuid.UUID) (estabelecimento.ProfessionalRef, error) {
	profs, err := estabelecimento.ActiveProfessionals(ctx, q, estID)
	if err != nil {
		return estabelecimento.ProfessionalRef{}, err
	}
	for _, p := range profs {
		if p.ID == profID {
			return p, nil
		}
	}
	return estabelecimento.ProfessionalRef{}, conflict("professional_unavailable", "Profissional indisponível",
		"Este profissional não está atendendo. Escolha outro.", nil)
}

// checkSlot refaz, SOB A TRAVA da cadeira, a mesma conta da disponibilidade: o início precisa ser
// um dos oferecidos agora. Se outra pessoa acabou de ocupar, 409 slot_unavailable com os horários
// atuais (a tela troca o horário sem perder os itens escolhidos).
func (s *Service) checkSlot(ctx context.Context, tx pgx.Tx, estID uuid.UUID, loc *time.Location, day Day, now time.Time,
	st estabelecimento.Settings, prof estabelecimento.ProfessionalRef, mins int, start time.Time, exclude *uuid.UUID) error {
	in, err := loadPlanInput(ctx, tx, estID, loc, day, now, st, s.mode(st), []uuid.UUID{prof.ID}, exclude)
	if err != nil {
		return err
	}
	plan, err := s.planDay(ctx, tx, in, prof, 0)
	if err != nil {
		return err
	}
	free := slotsFor(in, plan, mins+st.BufferMinutes)
	if slices.ContainsFunc(free, func(t time.Time) bool { return t.Equal(start) }) {
		return nil
	}
	cur := make([]slotView, len(free))
	for i, t := range free {
		cur[i] = slotView{StartsAt: t.UTC(), LocalTime: t.In(loc).Format("15:04")}
	}
	return conflict("slot_unavailable", "Horário indisponível",
		"Este horário acabou de ser ocupado ou não está mais disponível. Escolha outro; os serviços escolhidos continuam.",
		map[string]any{"professional_id": prof.ID, "date": day.String(), "slots": cur})
}

type newAppointment struct {
	estID  uuid.UUID
	prof   estabelecimento.ProfessionalRef
	who    acesso.Identity
	origin string
	start  time.Time
	day    Day
	loc    *time.Location
	mins   int
	cents  int64
	st     estabelecimento.Settings
	lines  []atendimento.Line
	now    time.Time
	// hold: reserva paga aguardando o Pix até este instante (nil = confirmada na hora).
	hold *time.Time
}

func insertAppointment(ctx context.Context, tx pgx.Tx, n newAppointment) (uuid.UUID, error) {
	id, _ := uuid.NewV7()
	ends := n.start.Add(time.Duration(n.mins+n.st.BufferMinutes) * time.Minute)
	var acc, vis *uuid.UUID
	if n.who.Kind == acesso.IdentityAccount {
		acc = &n.who.AccountID
	} else {
		vis = &n.who.VisitorID
	}
	nick := n.who.Name
	if r := []rune(nick); len(r) > 60 {
		nick = string(r[:60])
	} else if len(r) == 0 {
		nick = "Cliente"
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO appointment (id, establishment_id, professional_id, account_id, visitor_id, nick, origin, local_date, timezone,
		                         starts_at, ends_at, service_minutes, buffer_minutes, tolerance_minutes, checkin_window_minutes,
		                         policy_version, price_cents, created_at, updated_at, status, hold_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $18, $19, $20)`,
		id, n.estID, n.prof.ID, acc, vis, nick, n.origin, n.day.Date(), n.loc.String(), n.start, ends, max(n.mins, 1),
		n.st.BufferMinutes, n.st.AppointmentTolerance(), n.st.CheckinWindowMinutes, n.st.AgendaVersion, n.cents, n.now,
		statusFor(n.hold), n.hold)
	if isOverlapOn(err, "appointment_account_overlap") || isOverlapOn(err, "appointment_visitor_overlap") {
		return id, conflict("appointment_overlaps_own", "Você já tem horário nesse período",
			"Você já tem outra reserva que se sobrepõe a este horário nesta barbearia (com qualquer profissional).", nil)
	}
	if err != nil {
		return id, fmt.Errorf("criar reserva: %w", err)
	}
	for _, l := range n.lines {
		var sid, pid *uuid.UUID
		dur := l.Snap.DurationMinutes
		if l.Snap.Kind == catalogo.KindProduct {
			pid, dur = &l.Snap.ID, 0
		} else {
			sid = &l.Snap.ID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO appointment_item (establishment_id, appointment_id, kind, service_id, product_id, name,
			unit_price_cents, duration_minutes, points, quantity) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			n.estID, id, l.Snap.Kind, sid, pid, l.Snap.Name, l.Snap.PriceCents, dur, l.Snap.Points, l.Quantity); err != nil {
			return id, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO agenda_occupancy (establishment_id, professional_id, kind, appointment_id, period, hold_until)
		VALUES ($1, $2, 'appointment', $3, tstzrange($4, $5, '[)'), $6)`, n.estID, n.prof.ID, id, n.start, ends, n.hold)
	if isOverlap(err) {
		return id, conflict("slot_unavailable", "Horário indisponível",
			"Este horário acabou de ser ocupado. Escolha outro; os serviços escolhidos continuam.", nil)
	}
	return id, err
}

func statusFor(hold *time.Time) string {
	if hold != nil {
		return StatusHeld
	}
	return StatusConfirmed
}

func isOverlap(err error) bool { return isOverlapOn(err, "agenda_occupancy_no_overlap") }

// isOverlapOn reconhece a violação de uma restrição de exclusão (23P01) pelo nome.
func isOverlapOn(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23P01" && pe.ConstraintName == constraint
}

func recordAudit(ctx context.Context, q database.Querier, estID uuid.UUID, actor actorInfo, action, target string, id uuid.UUID,
	to, reason string, now time.Time) error {
	return auditoria.Record(ctx, q, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: actor.kind, ActorID: actor.id,
		Action: action, TargetType: target, TargetID: id, ToState: to, Reason: reason, RequestID: hs.RequestIDFrom(ctx)})
}

// ---------------------------------------------------------------------------------------------
// Minhas reservas (listMyAppointments) e detalhe (getAppointment)

type myAppointment struct {
	Establishment struct {
		ID   uuid.UUID `json:"id"`
		Slug string    `json:"slug"`
		Name string    `json:"name"`
	} `json:"establishment"`
	Appointment appointmentView `json:"appointment"`
}

// listMyAppointments: as reservas da pessoa (conta e visitantes deste navegador), em todas as
// lojas. scope=upcoming (ativas, mais próximas primeiro) ou past (encerradas, mais recentes
// primeiro), paginadas por cursor.
func (s *Service) listMyAppointments(w http.ResponseWriter, r *http.Request) error {
	account, visitors := s.Customers.AllCustomerIdentities(r)
	if account == nil && len(visitors) == 0 {
		return hs.ErrUnauthenticated()
	}
	ids := []uuid.UUID{}
	if account != nil {
		ids = append(ids, account.AccountID)
	}
	for _, v := range visitors {
		ids = append(ids, v.VisitorID)
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "upcoming"
	}
	if scope != "upcoming" && scope != "past" {
		return hs.ErrValidation(hs.FieldError{Field: "scope", Code: hs.FieldNotAllowed, Message: "use upcoming ou past"})
	}
	var afterT time.Time
	var afterID uuid.UUID
	if c := r.URL.Query().Get("cursor"); c != "" {
		ts, id, ok := strings.Cut(c, "_")
		n, err1 := strconv.ParseInt(ts, 10, 64)
		u, err2 := uuid.Parse(id)
		if !ok || err1 != nil || err2 != nil {
			return hs.ErrValidation(hs.FieldError{Field: "cursor", Code: hs.FieldInvalidFormat})
		}
		afterT, afterID = time.UnixMicro(n), u
	}
	ctx := r.Context()
	sql := appointmentSelect + ` WHERE (account_id = ANY($1) OR visitor_id = ANY($1))`
	args := []any{ids}
	if scope == "upcoming" {
		sql += ` AND status IN ('held', 'confirmed', 'called', 'in_service')`
		if !afterT.IsZero() {
			sql += ` AND (starts_at, id) > ($2, $3)`
			args = append(args, afterT, afterID)
		}
		sql += ` ORDER BY starts_at, id`
	} else {
		sql += ` AND status NOT IN ('held', 'confirmed', 'called', 'in_service')`
		if !afterT.IsZero() {
			sql += ` AND (starts_at, id) < ($2, $3)`
			args = append(args, afterT, afterID)
		}
		sql += ` ORDER BY starts_at DESC, id DESC`
	}
	sql += fmt.Sprintf(` LIMIT %d`, pageSize+1)
	out := []myAppointment{}
	var next *string
	err := database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (appointment, error) { return scanAppointment(r) })
		if err != nil {
			return err
		}
		if len(list) > pageSize {
			list = list[:pageSize]
			c := fmt.Sprintf("%d_%s", list[pageSize-1].StartsAt.UnixMicro(), list[pageSize-1].ID)
			next = &c
		}
		vs, err := views(ctx, tx, list, false)
		if err != nil {
			return err
		}
		stores := map[uuid.UUID]estabelecimento.Public{}
		for i, a := range list {
			e, ok := stores[a.EstablishmentID]
			if !ok {
				if e, err = estabelecimento.ActiveByID(ctx, tx, a.EstablishmentID); errors.Is(err, estabelecimento.ErrNotFound) {
					continue
				} else if err != nil {
					return err
				}
				stores[a.EstablishmentID] = e
			}
			var m myAppointment
			m.Establishment.ID, m.Establishment.Slug, m.Establishment.Name = e.ID, e.Slug, e.Name
			m.Appointment = vs[i]
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return err
	}
	type page struct {
		NextCursor *string `json:"next_cursor"`
		HasMore    bool    `json:"has_more"`
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []myAppointment `json:"data"`
		Page page            `json:"page"`
	}{out, page{next, next != nil}})
	return nil
}

// who decide o papel do pedido sobre a reserva: titular (cliente/visitante), equipe autorizada
// (dono; profissional só da própria agenda) ou 404 para qualquer outro cliente.
type who struct {
	customer *acesso.Identity
	staff    *acesso.Actor
}

func (w who) key() string {
	if w.staff != nil {
		return actorKey(*w.staff)
	}
	return identityKey(*w.customer)
}

func (w who) actor() actorInfo {
	if w.staff != nil {
		return staffActorInfo(*w.staff)
	}
	return customerActorInfo(*w.customer)
}

func (s *Service) authorize(r *http.Request, a appointment, customer, staff bool) (who, error) {
	account, visitor := s.Customers.CustomerIdentities(r, a.EstablishmentID)
	if customer {
		if a.owns(account) {
			return who{customer: account}, nil
		}
		if a.owns(visitor) {
			return who{customer: visitor}, nil
		}
	}
	actor, err := s.Auth.Member(r, a.EstablishmentID)
	if err != nil {
		var he *hs.Error
		if errors.As(err, &he) && he.Status == http.StatusUnauthorized && (visitor != nil || account != nil) {
			return who{}, hs.ErrNotFound()
		}
		return who{}, err
	}
	if !staff {
		return who{}, hs.ErrForbidden("Só o titular da reserva pode fazer isso.")
	}
	if actor.IsOwner() || (actor.ProfessionalID != nil && *actor.ProfessionalID == a.ProfessionalID) {
		return who{staff: &actor}, nil
	}
	return who{}, hs.ErrForbidden("Você só pode operar a sua própria agenda.")
}

func (s *Service) getAppointment(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	id, err := pathID(r, "appointmentId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	a, err := loadAppointment(ctx, s.Pool, estID, id, false)
	if err != nil {
		return err
	}
	by, err := s.authorize(r, a, true, true)
	if err != nil {
		return err
	}
	v, err := view(ctx, s.Pool, a, by.staff != nil)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[appointmentView]{Data: v})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Comandos sobre uma reserva

// command é a parte comum: lê, autoriza, decodifica, e na transação trava identidade → cadeira(s)
// → reserva (ordem do ADR-0038), confere a versão e roda fn.
func (s *Service) command(w http.ResponseWriter, r *http.Request, op string, customer, staff bool, body any,
	expected func() *int64, extraProf func() *uuid.UUID,
	fn func(tx pgx.Tx, a appointment, by who, loc *time.Location) (idempotency.Result, error)) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	id, err := pathID(r, "appointmentId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	before, err := loadAppointment(ctx, s.Pool, estID, id, false)
	if err != nil {
		return err
	}
	by, err := s.authorize(r, before, customer, staff)
	if err != nil {
		return err
	}
	raw, err := hs.DecodeJSON(r, body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Version("/expected_version", expected())
	if err := v.Err(); err != nil {
		return err
	}
	cmd := idempotency.Command{ActorKey: by.key(), Operation: op, Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		if err := lockIdentity(ctx, tx, estID, identityKey(before.identity())); err != nil {
			return idempotency.Result{}, err
		}
		profs := []estabelecimento.ProfessionalRef{}
		all, err := estabelecimento.ActiveProfessionals(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		for _, p := range all {
			if p.ID == before.ProfessionalID || (extraProf != nil && extraProf() != nil && *extraProf() == p.ID) {
				profs = append(profs, p)
			}
		}
		if err := s.lockChairs(ctx, tx, estID, profs); err != nil {
			return idempotency.Result{}, err
		}
		a, err := loadAppointment(ctx, tx, estID, id, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if a.Version != *expected() || a.ProfessionalID != before.ProfessionalID {
			cur, err := view(ctx, tx, a, by.staff != nil)
			if err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{}, conflict("version_conflict", "A reserva mudou",
				"Outra pessoa alterou esta reserva. Confira os dados atualizados.", cur)
		}
		_, loc, err := storeLoc(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		return fn(tx, a, by, loc)
	})
}

func (s *Service) invalid(ctx context.Context, q database.Querier, a appointment, by who, detail string) (idempotency.Result, error) {
	cur, err := view(ctx, q, a, by.staff != nil)
	if err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{}, conflict("invalid_transition", "Ação não permitida agora", detail, cur)
}

func (s *Service) done(ctx context.Context, q database.Querier, a appointment, by who) (idempotency.Result, error) {
	cur, err := loadAppointment(ctx, q, a.EstablishmentID, a.ID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	v, err := view(ctx, q, cur, by.staff != nil)
	if err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: http.StatusOK, Body: hs.Data[appointmentView]{Data: v}}, nil
}

func reasonOf(b *string) (string, error) {
	if b == nil {
		return "", nil
	}
	r, fe := validate.Text("/reason", *b, 3, 500)
	if fe != nil {
		return "", hs.ErrValidation(*fe)
	}
	return r, nil
}

// cancelAppointment (T-AP-03): o titular cancela até o horário; a equipe, com motivo, a qualquer
// momento antes da chamada. Libera o intervalo na hora. Depois de chamada, cancela-se a visita.
type cancelRequest struct {
	ExpectedVersion *int64  `json:"expected_version"`
	Reason          *string `json:"reason"`
	// AcceptRetention: o cliente leu que, fora da janela, o valor pago fica com a barbearia.
	AcceptRetention *bool `json:"accept_retention"`
}

func (s *Service) cancelAppointment(w http.ResponseWriter, r *http.Request) error {
	var body cancelRequest
	return s.command(w, r, "cancelAppointment", true, true, &body, func() *int64 { return body.ExpectedVersion }, nil,
		func(tx pgx.Tx, a appointment, by who, _ *time.Location) (idempotency.Result, error) {
			ctx := r.Context()
			reason, err := reasonOf(body.Reason)
			if err != nil {
				return idempotency.Result{}, err
			}
			if by.staff != nil && reason == "" {
				return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/reason", Code: hs.FieldRequired, Message: "informe o motivo do cancelamento"})
			}
			if a.Status != StatusConfirmed && a.Status != StatusHeld {
				detail := "Esta reserva não pode mais ser cancelada."
				if a.Status == StatusCalled || a.Status == StatusInService {
					detail = "O cliente já foi chamado: cancele ou encerre pela visita, em Filas."
				}
				return s.invalid(ctx, tx, a, by, detail)
			}
			now := s.Clock.Now()
			if by.customer != nil && !now.Before(a.StartsAt) {
				return s.invalid(ctx, tx, a, by, "O horário já começou. Fale com a barbearia.")
			}
			cancelledBy, end := "customer", pagamento.EndCancelledByCust
			if by.staff != nil {
				cancelledBy, end = "store", pagamento.EndCancelledByStore
			}
			if by.customer != nil {
				// Fora da janela com a política "reter": o cliente confirma que leu antes.
				bv, err := pagamento.BookingViews(ctx, tx, []uuid.UUID{a.ID})
				if err != nil {
					return idempotency.Result{}, err
				}
				if b, ok := bv[a.ID]; ok && b.Status == pagamento.BookingPaid && b.LateCancelPolicy == "retain" &&
					now.After(a.StartsAt.Add(-time.Duration(b.CancelWindowHours)*time.Hour)) &&
					(body.AcceptRetention == nil || !*body.AcceptRetention) {
					return idempotency.Result{}, conflict("cancel_retains_payment", "O valor pago não será devolvido",
						fmt.Sprintf("Faltam menos de %d h para o horário: pela política aceita na reserva, cancelando agora o valor de %s fica com a barbearia. Confirme para cancelar mesmo assim.",
							b.CancelWindowHours, reaisBR(b.Amount.AmountCents)), nil)
				}
			}
			var why *string
			if reason != "" {
				why = &reason
			}
			if _, err := tx.Exec(ctx, `UPDATE appointment SET status = 'cancelled', cancelled_by = $2, end_reason = $3, finished_at = $4,
				version = version + 1, updated_at = $4 WHERE id = $1`, a.ID, cancelledBy, why, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := releaseOccupancy(ctx, tx, a.ID, now); err != nil {
				return idempotency.Result{}, err
			}
			if s.Payments != nil {
				if err := s.Payments.BookingEnded(ctx, tx, a.EstablishmentID, a.ID, end, a.StartsAt, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			if err := s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-03", a.Status, by.actor(), reason, now); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, a, by)
		})
}

type rescheduleRequest struct {
	ExpectedVersion *int64     `json:"expected_version"`
	StartsAt        *time.Time `json:"starts_at"`
	ProfessionalID  *uuid.UUID `json:"professional_id"`
	Reason          *string    `json:"reason"`
}

// rescheduleAppointment (T-AP-02): mesma loja, mesmos itens e valores aceitos. Valida o destino
// ANTES de soltar a origem, na mesma transação: se o novo horário falhar, o anterior continua.
func (s *Service) rescheduleAppointment(w http.ResponseWriter, r *http.Request) error {
	var body rescheduleRequest
	return s.command(w, r, "rescheduleAppointment", true, true, &body, func() *int64 { return body.ExpectedVersion },
		func() *uuid.UUID { return body.ProfessionalID },
		func(tx pgx.Tx, a appointment, by who, loc *time.Location) (idempotency.Result, error) {
			ctx := r.Context()
			if body.StartsAt == nil {
				return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/starts_at", Code: hs.FieldRequired})
			}
			reason, err := reasonOf(body.Reason)
			if err != nil {
				return idempotency.Result{}, err
			}
			if by.staff != nil && reason == "" {
				return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/reason", Code: hs.FieldRequired, Message: "informe o motivo"})
			}
			if a.Status != StatusConfirmed {
				return s.invalid(ctx, tx, a, by, "Só é possível remarcar uma reserva confirmada que ainda não foi chamada.")
			}
			now := s.Clock.Now()
			if by.customer != nil && !now.Before(a.StartsAt) {
				return s.invalid(ctx, tx, a, by, "O horário já começou. Fale com a barbearia.")
			}
			st, err := estabelecimento.LoadSettings(ctx, tx, a.EstablishmentID)
			if err != nil {
				return idempotency.Result{}, err
			}
			profID := a.ProfessionalID
			if body.ProfessionalID != nil {
				profID = *body.ProfessionalID
			}
			prof, err := professionalOf(ctx, tx, a.EstablishmentID, profID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if profID != a.ProfessionalID {
				enabled, err := catalogo.EnabledServiceIDs(ctx, tx, a.EstablishmentID, profID)
				if err != nil {
					return idempotency.Result{}, err
				}
				items, err := itemsOf(ctx, tx, []uuid.UUID{a.ID})
				if err != nil {
					return idempotency.Result{}, err
				}
				for _, it := range items[a.ID] {
					if it.ServiceID != nil && !enabled[*it.ServiceID] {
						return idempotency.Result{}, conflict("service_not_eligible", "Profissional não faz estes serviços",
							prof.Name+" não faz todos os serviços desta reserva. Escolha outro profissional.", nil)
					}
				}
			}
			start := body.StartsAt.UTC()
			day := DayOf(start, loc)
			today := DayOf(now, loc)
			if today.AddDays(st.HorizonDays).Before(day) {
				return idempotency.Result{}, conflict("outside_booking_window", "Data fora da janela de reservas",
					"Escolha uma data dentro da janela de reservas da barbearia.", nil)
			}
			if err := s.checkSlot(ctx, tx, a.EstablishmentID, loc, day, now, st, prof, a.ServiceMinutes, start, &a.ID); err != nil {
				return idempotency.Result{}, err
			}
			ends := start.Add(a.EndsAt.Sub(a.StartsAt))
			if _, err := tx.Exec(ctx, `UPDATE appointment SET starts_at = $2, ends_at = $3, professional_id = $4, local_date = $5,
				store_late_at = NULL, arrival_signal_at = NULL, revision = revision + 1, version = version + 1, updated_at = $6 WHERE id = $1`,
				a.ID, start, ends, profID, day.Date(), now); err != nil {
				if isOverlapOn(err, "appointment_account_overlap") || isOverlapOn(err, "appointment_visitor_overlap") {
					return idempotency.Result{}, conflict("appointment_overlaps_own", "Você já tem horário nesse período",
						"Há outra reserva sua que se sobrepõe ao novo horário. A reserva atual continua como estava.", nil)
				}
				return idempotency.Result{}, err
			}
			if _, err := tx.Exec(ctx, `UPDATE agenda_occupancy SET professional_id = $2, period = tstzrange($3, $4, '[)')
				WHERE appointment_id = $1 AND released_at IS NULL`, a.ID, profID, start, ends); err != nil {
				if isOverlap(err) {
					return idempotency.Result{}, conflict("slot_unavailable", "Horário indisponível",
						"O novo horário acabou de ser ocupado. A reserva atual continua como estava.", nil)
				}
				return idempotency.Result{}, err
			}
			if err := s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-02", a.Status, by.actor(), reason, now); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, a, by)
		})
}

type versionOnly struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

// confirmAppointmentArrival (T-AP-04): a equipe confere a presença física (a partir da janela de
// chegada). Presença confirmada impede a falta automática; aguardando, não ocupa outra cadeira.
func (s *Service) confirmArrival(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	return s.command(w, r, "confirmAppointmentArrival", false, true, &body, func() *int64 { return body.ExpectedVersion }, nil,
		func(tx pgx.Tx, a appointment, by who, loc *time.Location) (idempotency.Result, error) {
			ctx := r.Context()
			if a.Status != StatusConfirmed && a.Status != StatusCalled {
				return s.invalid(ctx, tx, a, by, "Só é possível registrar a chegada de uma reserva confirmada ou chamada.")
			}
			if a.ArrivedAt != nil {
				return s.invalid(ctx, tx, a, by, "A chegada já foi registrada.")
			}
			now := s.Clock.Now()
			opens := a.StartsAt.Add(-time.Duration(a.CheckinWindow) * time.Minute)
			if a.Status == StatusConfirmed && now.Before(opens) {
				return s.invalid(ctx, tx, a, by, "A chegada pode ser registrada a partir das "+hora(opens, loc)+".")
			}
			if _, err := tx.Exec(ctx, `UPDATE appointment SET arrived_at = $2, version = version + 1, updated_at = $2 WHERE id = $1`, a.ID, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-04", a.Status, by.actor(), "", now); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, a, by)
		})
}

// signalAppointmentArrival (T-AP-05): "Cheguei" no celular. É só um aviso para a equipe; não
// confirma presença (quem confirma é a equipe).
func (s *Service) signalArrival(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	return s.command(w, r, "signalAppointmentArrival", true, false, &body, func() *int64 { return body.ExpectedVersion }, nil,
		func(tx pgx.Tx, a appointment, by who, loc *time.Location) (idempotency.Result, error) {
			ctx := r.Context()
			if a.Status != StatusConfirmed && a.Status != StatusCalled {
				return s.invalid(ctx, tx, a, by, "Esta reserva não está aguardando a sua chegada.")
			}
			if a.ArrivalSignalAt != nil || a.ArrivedAt != nil {
				return s.invalid(ctx, tx, a, by, "A barbearia já foi avisada da sua chegada.")
			}
			now := s.Clock.Now()
			opens := a.StartsAt.Add(-time.Duration(a.CheckinWindow) * time.Minute)
			if now.Before(opens) {
				return s.invalid(ctx, tx, a, by, "Você poderá avisar que chegou a partir das "+hora(opens, loc)+".")
			}
			if _, err := tx.Exec(ctx, `UPDATE appointment SET arrival_signal_at = $2, version = version + 1, updated_at = $2 WHERE id = $1`, a.ID, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-05", a.Status, by.actor(), "", now); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, a, by)
		})
}

// callAppointment (T-AP-06): a equipe da cadeira chama o horário marcado. Na mesma transação abre
// a visita (participação já chamada + comanda com os itens e preços congelados) e grava o prazo de
// chegada: max(horário marcado, chamada) + tolerância congelada. Cadeira ocupada, cliente com outra
// visita ativa ou chamada cedo demais → 409, e a reserva continua valendo. Fila pausada/fechada não
// impede: a agenda opera a cadeira.
func (s *Service) callAppointment(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	return s.command(w, r, "callAppointment", false, true, &body, func() *int64 { return body.ExpectedVersion }, nil,
		func(tx pgx.Tx, a appointment, by who, loc *time.Location) (idempotency.Result, error) {
			ctx := r.Context()
			if a.Status != StatusConfirmed {
				if a.Status == StatusCalled || a.Status == StatusInService {
					return s.invalid(ctx, tx, a, by, "Este horário já foi chamado.")
				}
				return s.invalid(ctx, tx, a, by, "Esta reserva não pode ser chamada.")
			}
			now := s.Clock.Now()
			early := max(a.StartsAt.Sub(now), 0)
			lead := int(math.Ceil(early.Minutes()))
			tolerance := a.Tolerance + lead
			// O prazo (tolerância gravada na visita) cabe em 60 min; a janela de chegada limita a
			// chamada antecipada. Com presença confirmada, pode chamar antes (o prazo é limitado).
			maxEarly := min(a.CheckinWindow, 60-a.Tolerance)
			if a.ArrivedAt == nil && lead > maxEarly {
				opens := a.StartsAt.Add(-time.Duration(maxEarly) * time.Minute)
				return idempotency.Result{}, conflict("appointment_too_early", "Ainda é cedo para chamar",
					"A chamada deste horário abre às "+hora(opens, loc)+". Se o cliente já chegou, registre a chegada antes.", nil)
			}
			tolerance = min(tolerance, 60)
			queueID, err := estabelecimento.QueueOfProfessional(ctx, tx, a.ProfessionalID)
			if err != nil {
				return idempotency.Result{}, err
			}
			load, err := fila.QueueLoad(ctx, tx, queueID, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			for _, e := range load {
				if e.Status == fila.Called || e.Status == fila.InService {
					return idempotency.Result{}, conflict("professional_busy", "Cadeira ocupada",
						"Já há um cliente chamado ou em atendimento nesta cadeira. Conclua ou encerre antes de chamar o horário marcado.", nil)
				}
			}
			qi, err := estabelecimento.GetQueue(ctx, tx, a.EstablishmentID, queueID)
			if err != nil {
				return idempotency.Result{}, err
			}
			lines, err := frozenLines(ctx, tx, a.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			actor := by.actor()
			audit := auditoria.Event{ActorKind: actor.kind, ActorID: actor.id, RequestID: hs.RequestIDFrom(ctx)}
			orderID, _ := uuid.NewV7()
			partID, err := s.Visits.OpenScheduledVisit(ctx, tx, fila.ScheduledVisit{EstablishmentID: a.EstablishmentID, Queue: qi,
				AccountID: a.AccountID, VisitorID: a.VisitorID, Nick: a.Nick, Lines: lines, ToleranceMinutes: tolerance, Now: now, Audit: audit,
				OrderID: orderID})
			if err != nil {
				return idempotency.Result{}, err
			}
			// Valor pago da reserva: fica ligado à comanda e é abatido quando o total fechar.
			if err := pagamento.AttachOrder(ctx, tx, a.EstablishmentID, a.ID, orderID, now); err != nil {
				return idempotency.Result{}, err
			}
			deadline := now.Add(time.Duration(tolerance) * time.Minute)
			if _, err := tx.Exec(ctx, `UPDATE appointment SET status = 'called', called_at = $2, deadline_at = $3, participation_id = $4,
				version = version + 1, updated_at = $2 WHERE id = $1`, a.ID, now, deadline, partID); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-06", a.Status, actor, "", now); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, a, by)
		})
}

// frozenLines converte os itens congelados da reserva em linhas da comanda (preço e duração aceitos).
func frozenLines(ctx context.Context, q database.Querier, id uuid.UUID) ([]atendimento.Line, error) {
	rows, err := q.Query(ctx, `SELECT kind, coalesce(service_id, product_id), name, unit_price_cents, duration_minutes, points, quantity
		FROM appointment_item WHERE appointment_id = $1 ORDER BY kind DESC, name`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (atendimento.Line, error) {
		var l atendimento.Line
		err := r.Scan(&l.Snap.Kind, &l.Snap.ID, &l.Snap.Name, &l.Snap.PriceCents, &l.Snap.DurationMinutes, &l.Snap.Points, &l.Quantity)
		l.Snap.Active = true
		return l, err
	})
}

// ---------------------------------------------------------------------------------------------
// Agenda da equipe (listAgenda): um dia (ou até 7) por profissional, com turnos, bloqueios e reservas.

type agendaShift struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Modality string    `json:"modality"`
}

type agendaDay struct {
	Date           string            `json:"date"`
	ProfessionalID uuid.UUID         `json:"professional_id"`
	DisplayName    string            `json:"display_name"`
	Shifts         []agendaShift     `json:"shifts"`
	Blocks         []exceptionView   `json:"blocks"`
	Appointments   []appointmentView `json:"appointments"`
}

type agendaView struct {
	Timezone string      `json:"timezone"`
	Mode     string      `json:"mode"`
	Today    string      `json:"today"`
	Days     []agendaDay `json:"days"`
}

func (s *Service) listAgenda(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	var filter *uuid.UUID
	if p := r.URL.Query().Get("professional_id"); p != "" {
		id, err := uuid.Parse(p)
		if err != nil {
			return hs.ErrValidation(hs.FieldError{Field: "professional_id", Code: hs.FieldInvalidFormat})
		}
		filter = &id
	}
	actor, err := s.staffActor(r, estID, filter)
	if err != nil {
		return err
	}
	if !actor.IsOwner() {
		filter = actor.ProfessionalID
	}
	ctx := r.Context()
	var out agendaView
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		e, loc, err := storeLoc(ctx, tx, estID)
		if err != nil {
			return err
		}
		st, err := estabelecimento.LoadSettings(ctx, tx, estID)
		if err != nil {
			return err
		}
		today := DayOf(s.Clock.Now(), loc)
		from, to, err := dayRange(r, today, maxAgendaDays, 1)
		if err != nil {
			return err
		}
		out = agendaView{Timezone: e.Timezone, Mode: s.mode(st), Today: today.String(), Days: []agendaDay{}}
		profs, err := estabelecimento.ActiveProfessionals(ctx, tx, estID)
		if err != nil {
			return err
		}
		if filter != nil {
			profs = slices.DeleteFunc(profs, func(p estabelecimento.ProfessionalRef) bool { return p.ID != *filter })
		}
		ids := make([]uuid.UUID, len(profs))
		for i, p := range profs {
			ids[i] = p.ID
		}
		rows, err := tx.Query(ctx, appointmentSelect+` WHERE establishment_id = $1 AND professional_id = ANY($2)
			AND local_date BETWEEN $3 AND $4 ORDER BY starts_at, id LIMIT 1000`, estID, ids, from.Date(), to.Date())
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (appointment, error) { return scanAppointment(r) })
		if err != nil {
			return err
		}
		vs, err := views(ctx, tx, list, true)
		if err != nil {
			return err
		}
		exc, err := exceptionsBetween(ctx, tx, estID, from, to)
		if err != nil {
			return err
		}
		for d := from; !to.Before(d); d = d.AddDays(1) {
			sched, err := schedulesOn(ctx, tx, estID, d)
			if err != nil {
				return err
			}
			for _, p := range profs {
				day := agendaDay{Date: d.String(), ProfessionalID: p.ID, DisplayName: p.Name, Shifts: []agendaShift{},
					Blocks: []exceptionView{}, Appointments: []appointmentView{}}
				for _, sh := range professionalShifts(sched, p.ID, d, loc) {
					day.Shifts = append(day.Shifts, agendaShift{sh.Start.UTC(), sh.End.UTC(), sh.Modality})
				}
				for _, ev := range exceptionViews(exc, &p.ID) {
					if ev.Date == d.String() {
						day.Blocks = append(day.Blocks, ev)
					}
				}
				for i, a := range list {
					if a.ProfessionalID == p.ID && a.LocalDate.Format(time.DateOnly) == d.String() {
						day.Appointments = append(day.Appointments, vs[i])
					}
				}
				out.Days = append(out.Days, day)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[agendaView]{Data: out})
	return nil
}
