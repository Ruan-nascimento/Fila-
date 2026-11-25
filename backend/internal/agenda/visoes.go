package agenda

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/pagamento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// Estados da reserva (AppointmentStatus). held/expired: cobrança da reserva (cobranca.go).
const (
	StatusConfirmed   = "confirmed"
	StatusCalled      = "called"
	StatusInService   = "in_service"
	StatusCompleted   = "completed"
	StatusInterrupted = "interrupted"
	StatusNoShow      = "no_show"
	StatusCancelled   = "cancelled"
)

func terminal(s string) bool {
	return s == StatusCompleted || s == StatusInterrupted || s == StatusNoShow || s == StatusCancelled || s == StatusExpired
}

// Tipo de evento (EventType) e recurso.
const evAppointment = "appointment.updated"

var errNoAppointment = errors.New("reserva inexistente")

// appointment é uma reserva lida do banco.
type appointment struct {
	ID               uuid.UUID
	EstablishmentID  uuid.UUID
	ProfessionalID   uuid.UUID
	ProfessionalName string
	AccountID        *uuid.UUID
	VisitorID        *uuid.UUID
	Nick             string
	Origin           string
	Status           string
	CancelledBy      *string
	LocalDate        time.Time
	Timezone         string
	StartsAt         time.Time
	EndsAt           time.Time
	ServiceMinutes   int
	BufferMinutes    int
	Tolerance        int
	CheckinWindow    int
	PolicyVersion    int64
	PriceCents       int64
	CalledAt         *time.Time
	DeadlineAt       *time.Time
	ArrivedAt        *time.Time
	ArrivalSignalAt  *time.Time
	StoreLateAt      *time.Time
	ParticipationID  *uuid.UUID
	EndReason        *string
	Revision         int
	Version          int64
	CreatedAt        time.Time
	FinishedAt       *time.Time
	HoldExpiresAt    *time.Time
}

const appointmentSelect = `
	SELECT id, establishment_id, professional_id, account_id, visitor_id, nick, origin, status, cancelled_by,
	       local_date, timezone, starts_at, ends_at, service_minutes, buffer_minutes, tolerance_minutes,
	       checkin_window_minutes, policy_version, price_cents, called_at, deadline_at, arrived_at,
	       arrival_signal_at, store_late_at, participation_id, end_reason, revision, version, created_at, finished_at,
	       hold_expires_at
	FROM appointment`

func scanAppointment(r pgx.Row) (appointment, error) {
	var a appointment
	err := r.Scan(&a.ID, &a.EstablishmentID, &a.ProfessionalID, &a.AccountID, &a.VisitorID, &a.Nick, &a.Origin, &a.Status,
		&a.CancelledBy, &a.LocalDate, &a.Timezone, &a.StartsAt, &a.EndsAt, &a.ServiceMinutes, &a.BufferMinutes, &a.Tolerance,
		&a.CheckinWindow, &a.PolicyVersion, &a.PriceCents, &a.CalledAt, &a.DeadlineAt, &a.ArrivedAt, &a.ArrivalSignalAt,
		&a.StoreLateAt, &a.ParticipationID, &a.EndReason, &a.Revision, &a.Version, &a.CreatedAt, &a.FinishedAt,
		&a.HoldExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, errNoAppointment
	}
	return a, err
}

// loadAppointmentBy lê uma reserva pelo filtro. lock trava a linha.
func loadAppointmentBy(ctx context.Context, q database.Querier, where string, lock bool, args ...any) (appointment, error) {
	sql := appointmentSelect + ` WHERE ` + where
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanAppointment(q.QueryRow(ctx, sql, args...))
}

// loadAppointment lê a reserva DESTA loja (outra loja → 404).
func loadAppointment(ctx context.Context, q database.Querier, estID, id uuid.UUID, lock bool) (appointment, error) {
	a, err := loadAppointmentBy(ctx, q, `establishment_id = $1 AND id = $2`, lock, estID, id)
	if errors.Is(err, errNoAppointment) {
		return a, hs.ErrNotFound()
	}
	return a, err
}

func (a appointment) owns(id *acesso.Identity) bool {
	if id == nil {
		return false
	}
	if id.Kind == acesso.IdentityAccount {
		return a.AccountID != nil && *a.AccountID == id.AccountID
	}
	return a.VisitorID != nil && *a.VisitorID == id.VisitorID
}

func (a appointment) identity() acesso.Identity {
	if a.AccountID != nil {
		return acesso.Identity{Kind: acesso.IdentityAccount, AccountID: *a.AccountID, Name: a.Nick}
	}
	return acesso.Identity{Kind: acesso.IdentityVisitor, VisitorID: *a.VisitorID, Name: a.Nick}
}

// ---------------------------------------------------------------------------------------------
// Visões (Appointment)

type money struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

func brl(c int64) money { return money{AmountCents: c, Currency: "BRL"} }

type itemView struct {
	Kind            string     `json:"kind"`
	ServiceID       *uuid.UUID `json:"service_id"`
	ProductID       *uuid.UUID `json:"product_id"`
	Name            string     `json:"name"`
	UnitPrice       money      `json:"unit_price"`
	Quantity        int        `json:"quantity"`
	DurationMinutes int        `json:"duration_minutes"`
}

type professionalRef struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
}

// paymentView: o que se paga agora e na barbearia. Booking: obrigação da reserva paga (sinal ou
// taxa), com o Pix, a devolução e o crédito aplicado; nulo na reserva gratuita.
type paymentView struct {
	Mode         string                 `json:"mode"`
	DueNow       money                  `json:"due_now"`
	DueAtStore   money                  `json:"due_at_store"`
	EstimatedFor money                  `json:"estimated_total"`
	Booking      *pagamento.BookingView `json:"booking"`
	// FreeCancelUntil: até este instante o cancelamento pelo cliente devolve o valor (janela
	// aceita na reserva). Nulo na reserva gratuita.
	FreeCancelUntil *time.Time `json:"free_cancel_until"`
}

type appointmentView struct {
	ID                   uuid.UUID       `json:"id"`
	EstablishmentID      uuid.UUID       `json:"establishment_id"`
	Professional         professionalRef `json:"professional"`
	Status               string          `json:"status"`
	CancelledBy          *string         `json:"cancelled_by"`
	Origin               string          `json:"origin"`
	StartsAt             time.Time       `json:"starts_at"`
	EndsAt               time.Time       `json:"ends_at"`
	LocalDate            string          `json:"local_date"`
	LocalTime            string          `json:"local_time"`
	Timezone             string          `json:"timezone"`
	ServiceMinutes       int             `json:"service_minutes"`
	BufferMinutes        int             `json:"buffer_minutes"`
	ToleranceMinutes     int             `json:"tolerance_minutes"`
	CheckinWindowMinutes int             `json:"checkin_window_minutes"`
	Items                []itemView      `json:"items"`
	Price                money           `json:"price"`
	Payment              paymentView     `json:"payment"`
	Terms                string          `json:"terms"`
	CalledAt             *time.Time      `json:"called_at"`
	DeadlineAt           *time.Time      `json:"deadline_at"`
	ArrivedAt            *time.Time      `json:"arrived_at"`
	ArrivalSignalAt      *time.Time      `json:"arrival_signal_at"`
	StoreLate            bool            `json:"store_late"`
	ParticipationID      *uuid.UUID      `json:"participation_id"`
	Revision             int             `json:"revision"`
	Version              int64           `json:"version"`
	CreatedAt            time.Time       `json:"created_at"`
	FinishedAt           *time.Time      `json:"finished_at"`
	HoldExpiresAt        *time.Time      `json:"hold_expires_at"`
	// Só para a equipe.
	Nick         string  `json:"nick,omitempty"`
	IdentityKind string  `json:"identity_kind,omitempty"`
	EndReason    *string `json:"end_reason,omitempty"`
}

const termsFree = "Reserva gratuita. Você paga na barbearia o valor dos serviços feitos. " +
	"Chegue até o fim da tolerância depois do horário; se não for, cancele pelo app para liberar o horário."

func itemsOf(ctx context.Context, q database.Querier, ids []uuid.UUID) (map[uuid.UUID][]itemView, error) {
	rows, err := q.Query(ctx, `SELECT appointment_id, kind, service_id, product_id, name, unit_price_cents, quantity, duration_minutes
		FROM appointment_item WHERE appointment_id = ANY($1) ORDER BY appointment_id, kind DESC, name`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]itemView{}
	for rows.Next() {
		var id uuid.UUID
		var it itemView
		var cents int64
		if err := rows.Scan(&id, &it.Kind, &it.ServiceID, &it.ProductID, &it.Name, &cents, &it.Quantity, &it.DurationMinutes); err != nil {
			return nil, err
		}
		it.UnitPrice = brl(cents)
		out[id] = append(out[id], it)
	}
	return out, rows.Err()
}

// views monta as visões de várias reservas (itens e nomes em lote).
func views(ctx context.Context, q database.Querier, list []appointment, forStaff bool) ([]appointmentView, error) {
	ids := make([]uuid.UUID, len(list))
	profIDs := make([]uuid.UUID, len(list))
	for i, a := range list {
		ids[i], profIDs[i] = a.ID, a.ProfessionalID
	}
	items, err := itemsOf(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	profs, err := estabelecimento.ProfessionalNames(ctx, q, profIDs)
	if err != nil {
		return nil, err
	}
	bookings, err := pagamento.BookingViews(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := make([]appointmentView, len(list))
	for i, a := range list {
		loc, err := time.LoadLocation(a.Timezone)
		if err != nil {
			loc = time.UTC
		}
		v := appointmentView{ID: a.ID, EstablishmentID: a.EstablishmentID, Professional: professionalRef{a.ProfessionalID, profs[a.ProfessionalID]},
			Status: a.Status, CancelledBy: a.CancelledBy, Origin: a.Origin, StartsAt: a.StartsAt, EndsAt: a.EndsAt,
			LocalDate: a.LocalDate.Format(time.DateOnly), LocalTime: a.StartsAt.In(loc).Format("15:04"), Timezone: a.Timezone,
			ServiceMinutes: a.ServiceMinutes, BufferMinutes: a.BufferMinutes, ToleranceMinutes: a.Tolerance,
			CheckinWindowMinutes: a.CheckinWindow, Items: items[a.ID], Price: brl(a.PriceCents),
			Payment: paymentView{Mode: "free", DueNow: brl(0), DueAtStore: brl(a.PriceCents), EstimatedFor: brl(a.PriceCents)},
			Terms:   termsFree, CalledAt: a.CalledAt, DeadlineAt: a.DeadlineAt, ArrivedAt: a.ArrivedAt, ArrivalSignalAt: a.ArrivalSignalAt,
			StoreLate: a.StoreLateAt != nil && a.Status == StatusConfirmed, ParticipationID: a.ParticipationID, Revision: a.Revision,
			Version: a.Version, CreatedAt: a.CreatedAt, FinishedAt: a.FinishedAt, HoldExpiresAt: utc(a.HoldExpiresAt)}
		if b, ok := bookings[a.ID]; ok {
			v.Payment, v.Terms = bookingPayment(a, b), bookingTerms(a, b)
		}
		// Instantes sempre em UTC (o contrato usa o sufixo Z); a hora local vem em local_time.
		v.StartsAt, v.EndsAt, v.CreatedAt = v.StartsAt.UTC(), v.EndsAt.UTC(), v.CreatedAt.UTC()
		v.CalledAt, v.DeadlineAt, v.ArrivedAt = utc(v.CalledAt), utc(v.DeadlineAt), utc(v.ArrivedAt)
		v.ArrivalSignalAt, v.FinishedAt = utc(v.ArrivalSignalAt), utc(v.FinishedAt)
		if v.Items == nil {
			v.Items = []itemView{}
		}
		if forStaff {
			v.Nick, v.EndReason = a.Nick, a.EndReason
			v.IdentityKind = acesso.IdentityVisitor
			if a.AccountID != nil {
				v.IdentityKind = acesso.IdentityAccount
			}
		}
		out[i] = v
	}
	return out, nil
}

func view(ctx context.Context, q database.Querier, a appointment, forStaff bool) (appointmentView, error) {
	v, err := views(ctx, q, []appointment{a}, forStaff)
	if err != nil {
		return appointmentView{}, err
	}
	return v[0], nil
}

// ---------------------------------------------------------------------------------------------
// Histórico, auditoria e eventos (mesma transação do comando)

type actorInfo struct {
	kind string // customer | visitor | owner | professional | system
	id   *uuid.UUID
}

func staffActorInfo(a acesso.Actor) actorInfo {
	if a.IsOwner() {
		return actorInfo{kind: "owner", id: &a.AccountID}
	}
	return actorInfo{kind: "professional", id: &a.AccountID}
}

func customerActorInfo(id acesso.Identity) actorInfo {
	if id.Kind == acesso.IdentityAccount {
		return actorInfo{kind: "customer", id: &id.AccountID}
	}
	return actorInfo{kind: "visitor", id: &id.VisitorID}
}

// afterChange grava o histórico da reserva (appointment_event), a auditoria e os eventos para o
// titular, o profissional e o dono. Relê a reserva (já alterada) para o estado final.
func (s *Service) afterChange(ctx context.Context, q database.Querier, estID, id uuid.UUID, action, from string,
	actor actorInfo, reason string, at time.Time) error {
	a, err := loadAppointment(ctx, q, estID, id, false)
	if err != nil {
		return err
	}
	var why *string
	if reason != "" {
		why = &reason
	}
	var fromPtr *string
	if from != "" {
		fromPtr = &from
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO appointment_event (establishment_id, appointment_id, action, from_status, to_status, actor_kind, actor_id,
		                               reason, starts_at, professional_id, revision, at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		estID, a.ID, action, fromPtr, a.Status, actor.kind, actor.id, why, a.StartsAt, a.ProfessionalID, a.Revision, at); err != nil {
		return fmt.Errorf("histórico da reserva: %w", err)
	}
	if err := auditoria.Record(ctx, q, auditoria.Event{EstablishmentID: &estID, OccurredAt: at, ActorKind: actor.kind,
		ActorID: actor.id, Action: action, TargetType: "appointment", TargetID: a.ID, FromState: from, ToState: a.Status,
		Reason: reason, RequestID: hs.RequestIDFrom(ctx)}); err != nil {
		return err
	}
	if err := s.planNotices(ctx, q, a, action, at); err != nil {
		return err
	}
	if freesOrTakesSlot(action, a.Status) {
		day := a.LocalDate.Format(time.DateOnly)
		if err := availabilityChanged(ctx, q, estID, &a.ProfessionalID, &day, at); err != nil {
			return err
		}
	}
	forCustomer, err := view(ctx, q, a, false)
	if err != nil {
		return err
	}
	forStaff, err := view(ctx, q, a, true)
	if err != nil {
		return err
	}
	base := outbox.Event{EstablishmentID: estID, Type: evAppointment, ResourceType: "appointment", ResourceID: a.ID,
		ResourceVersion: a.Version, OccurredAt: at}
	aud := outbox.Audience{Kind: outbox.Visit, AccountID: a.AccountID, VisitorID: a.VisitorID}
	return outbox.Record(ctx, q, outbox.ForVisitAndStaff(base, aud, a.ProfessionalID, forCustomer, forStaff)...)
}

// utc normaliza instantes opcionais para UTC (o contrato usa o sufixo Z).
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// hora formata "15h" / "15h30" no fuso da loja (mensagens).
func hora(t time.Time, loc *time.Location) string {
	l := t.In(loc)
	if l.Minute() == 0 {
		return fmt.Sprintf("%dh", l.Hour())
	}
	return fmt.Sprintf("%dh%02d", l.Hour(), l.Minute())
}

// Evento público de disponibilidade (ADR-0038/0039, parte 3): só loja, profissional e data, nunca
// quem reservou. As telas públicas abertas releem o dia visível (agrupando eventos em sequência).
const evAvailability = "availability.updated"

type availabilityHint struct {
	ProfessionalID *uuid.UUID `json:"professional_id"`
	Date           *string    `json:"date"`
}

func availabilityChanged(ctx context.Context, q database.Querier, estID uuid.UUID, prof *uuid.UUID, day *string, at time.Time) error {
	// Recurso = (loja, profissional, data): remarcação gera dois eventos distintos (dia antigo e novo).
	key := "loja"
	if prof != nil {
		key = prof.String()
	}
	if day != nil {
		key += "|" + *day
	}
	rid := uuid.NewSHA1(estID, []byte(key))
	return outbox.Record(ctx, q, outbox.Event{EstablishmentID: estID, Type: evAvailability, ResourceType: "availability",
		ResourceID: rid, ResourceVersion: at.UnixMicro(), OccurredAt: at, Audience: outbox.Audience{Kind: outbox.Public},
		Data: availabilityHint{ProfessionalID: prof, Date: day}})
}

// freesOrTakesSlot: a transição ocupa ou libera um intervalo da cadeira.
func freesOrTakesSlot(action, status string) bool {
	switch action {
	case "T-AP-01", "T-AP-02", "T-AP-03", "T-AP-09":
		return true
	case "T-AP-07":
		return terminal(status)
	}
	return false
}
