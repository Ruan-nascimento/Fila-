package agenda

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/pagamento"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/mail"
	"fila/internal/platform/ratelimit"
)

// Limites do contrato (consultas delimitadas; nenhuma viagem HTTP por horário).
const (
	maxProfessionalsPerQuery = 10
	maxExceptionRangeDays    = 62
	maxAgendaDays            = 7
	maxShiftsPerWeek         = 28
	// visitorHorizonDays: o visitante (sem conta) reserva até 7 dias à frente, o prazo em que a
	// credencial dele continua valendo sem uso. A recuperação por e-mail vem na parte 2.
	visitorHorizonDays = 7
	// counterHorizonDays: reserva de balcão até 29 dias (o código de recuperação vale 30).
	counterHorizonDays = 29
	pageSize           = 20
)

var (
	ruleBookIdentity = ratelimit.Rule{Name: "agenda_reserva_identidade", Limit: 20, Window: time.Hour}
	ruleBookIP       = ratelimit.Rule{Name: "agenda_reserva_ip", Limit: 60, Window: time.Hour}
)

// Deps são as dependências do módulo.
type Deps struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Log       *slog.Logger
	Auth      acesso.Authorizer
	Customers acesso.Customers
	Limiter   *ratelimit.Limiter
	// Visits abre a visita da reserva chamada e informa a carga da fila (módulo fila).
	Visits *fila.Service
	// Enabled: agenda liberada neste servidor (FILA_AGENDA). Desligada, nenhuma reserva NOVA; as
	// existentes continuam consultáveis, canceláveis e atendidas.
	Enabled bool
	// CounterVisitor cria a identidade recuperável de um cliente de balcão (módulo identidade):
	// um visitante desta loja com código de recuperação mostrado uma vez ao dono.
	CounterVisitor func(ctx context.Context, tx pgx.Tx, estID uuid.UUID, nick string, now time.Time) (visitorID uuid.UUID, code string, validUntil time.Time, err error)
	// TrustForwardedFor: IP do cliente pelo X-Forwarded-For (só atrás de proxy próprio).
	TrustForwardedFor bool
	// Payments é a cobrança da reserva (módulo pagamento). nil = reservas sempre gratuitas.
	Payments *pagamento.Service
	// Charge: cobrança da reserva liberada neste servidor (FILA_AGENDA_COBRANCA).
	Charge bool
	// Mail grava os avisos da reserva na caixa de saída cifrada; NoticeEmail resolve o endereço
	// (conta ou contato verificado do visitante; módulo identidade). nil = sem avisos.
	Mail          *mail.Outbox
	NoticeEmail   func(ctx context.Context, q database.Querier, accountID, visitorID *uuid.UUID) (string, bool, error)
	PublicBaseURL string
}

// Service implementa a agenda.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço.
func New(d Deps) *Service {
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

var _ fila.ChairGuard = (*Service)(nil)

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	const est = "/v1/establishments/{establishmentId}"
	const appt = est + "/appointments/{appointmentId}"
	owner := []hs.Grant{hs.GrantOwner}
	staff := []hs.Grant{{Actor: "professional", Scope: "own_queue"}, hs.GrantOwner}
	self := []hs.Grant{{Actor: "visitor", Scope: "self"}, {Actor: "customer", Scope: "self"}}
	bookingOwner := []hs.Grant{{Actor: "visitor", Scope: "visit_owner"}, {Actor: "customer", Scope: "visit_owner"}}
	with := func(a, b []hs.Grant) []hs.Grant { return append(append([]hs.Grant{}, a...), b...) }
	return []hs.Route{
		{Operation: "getAgendaHours", Method: http.MethodGet, Path: est + "/agenda/hours", Grants: staff, Handler: s.getHours},
		{Operation: "setAgendaHours", Method: http.MethodPut, Path: est + "/agenda/hours", Grants: owner, Handler: s.setHours},
		{Operation: "listAgendaExceptions", Method: http.MethodGet, Path: est + "/agenda/exceptions", Grants: staff, Handler: s.listExceptions},
		{Operation: "createAgendaException", Method: http.MethodPost, Path: est + "/agenda/exceptions", Grants: staff, Handler: s.createException},
		{Operation: "removeAgendaException", Method: http.MethodPost, Path: est + "/agenda/exceptions/{exceptionId}/remove", Grants: staff, Handler: s.removeException},
		{Operation: "listAgenda", Method: http.MethodGet, Path: est + "/agenda", Grants: staff, Handler: s.listAgenda},
		{Operation: "getAvailability", Method: http.MethodGet, Path: "/v1/public/establishments/{establishmentId}/availability",
			Grants: []hs.Grant{hs.GrantPublic}, Handler: s.getAvailability},
		{Operation: "getAvailabilityDays", Method: http.MethodGet, Path: "/v1/public/establishments/{establishmentId}/availability/days",
			Grants: []hs.Grant{hs.GrantPublic}, Handler: s.getAvailabilityDays},
		{Operation: "createAppointment", Method: http.MethodPost, Path: est + "/appointments", Grants: with(self, owner), Handler: s.createAppointment},
		{Operation: "listMyAppointments", Method: http.MethodGet, Path: "/v1/my-appointments", Grants: self, Handler: s.listMyAppointments},
		{Operation: "getAppointment", Method: http.MethodGet, Path: appt, Grants: with(bookingOwner, staff), Handler: s.getAppointment},
		{Operation: "cancelAppointment", Method: http.MethodPost, Path: appt + "/cancel", Grants: with(bookingOwner, staff), Handler: s.cancelAppointment},
		{Operation: "rescheduleAppointment", Method: http.MethodPost, Path: appt + "/reschedule", Grants: with(bookingOwner, staff), Handler: s.rescheduleAppointment},
		{Operation: "confirmAppointmentArrival", Method: http.MethodPost, Path: appt + "/arrival", Grants: staff, Handler: s.confirmArrival},
		{Operation: "signalAppointmentArrival", Method: http.MethodPost, Path: appt + "/arrival-signal", Grants: bookingOwner, Handler: s.signalArrival},
		{Operation: "callAppointment", Method: http.MethodPost, Path: appt + "/call", Grants: staff, Handler: s.callAppointment},
		{Operation: "createAppointmentPixCharge", Method: http.MethodPost, Path: appt + "/pix-charges", Grants: bookingOwner, Handler: s.createAppointmentPixCharge},
	}
}

// ---------------------------------------------------------------------------------------------
// Utilitários comuns

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, hs.ErrNotFound()
	}
	return id, nil
}

func conflict(code, title, detail string, current any) error {
	return &hs.Error{Status: http.StatusConflict, Code: code, Title: title, Detail: detail, Current: current}
}

func actorKey(a acesso.Actor) string { return "conta:" + a.AccountID.String() }

func identityKey(id acesso.Identity) string {
	if id.Kind == acesso.IdentityAccount {
		return "conta:" + id.AccountID.String()
	}
	return "visitante:" + id.VisitorID.String()
}

// lockIdentity serializa comandos da MESMA identidade na loja, com a mesma chave da entrada na
// fila (fila-entrada|loja|identidade): reservar, entrar na fila e ser chamado não correm juntos.
func lockIdentity(ctx context.Context, tx pgx.Tx, estID uuid.UUID, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "fila-entrada|"+estID.String()+"|"+key)
	return err
}

// staffActor autoriza equipe da loja: dono vê tudo; profissional só a própria agenda.
func (s *Service) staffActor(r *http.Request, estID uuid.UUID, professionalID *uuid.UUID) (acesso.Actor, error) {
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return actor, err
	}
	if actor.IsOwner() {
		return actor, nil
	}
	if actor.ProfessionalID == nil {
		return actor, hs.ErrForbidden("Somente a equipe da loja pode ver a agenda.")
	}
	if professionalID != nil && *professionalID != *actor.ProfessionalID {
		return actor, hs.ErrForbidden("Você só pode operar a sua própria agenda.")
	}
	return actor, nil
}

// storeLoc devolve a loja ativa e o fuso dela.
func storeLoc(ctx context.Context, q database.Querier, estID uuid.UUID) (estabelecimento.Public, *time.Location, error) {
	e, err := estabelecimento.ActiveByID(ctx, q, estID)
	if errors.Is(err, estabelecimento.ErrNotFound) {
		return e, nil, hs.ErrNotFound()
	}
	if err != nil {
		return e, nil, err
	}
	loc, err := time.LoadLocation(e.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return e, loc, nil
}

// mode é a forma de atendimento efetiva (sem a agenda liberada no servidor, é fila).
func (s *Service) mode(st estabelecimento.Settings) string {
	if !s.Enabled {
		return estabelecimento.ModeQueue
	}
	return st.ServiceMode
}

func (s *Service) allow(ctx context.Context, rule ratelimit.Rule, subject string) error {
	if s.Limiter == nil {
		return nil
	}
	ok, retry, err := s.Limiter.Allow(ctx, rule, subject)
	if err != nil {
		return err
	}
	if !ok {
		return hs.ErrRateLimited(retry)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
