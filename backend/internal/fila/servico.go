package fila

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
	"fila/internal/atendimento"
	"fila/internal/estabelecimento"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/ratelimit"
)

// Estados da participação (ParticipationStatus).
const (
	Waiting     = "waiting"
	Called      = "called"
	InService   = "in_service"
	Cancelled   = "cancelled"
	NoShow      = "no_show"
	Tolerance   = 10 * time.Minute // tolerância fixa após a chamada (R-FILA-09)
	maxServices = 10
)

// activeStatuses ocupam a fila; chairStatuses ocupam a cadeira do profissional.
var activeStatuses = []string{Waiting, Called, InService}

// Deps são as dependências do módulo.
type Deps struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Log       *slog.Logger
	Auth      acesso.Authorizer
	Customers acesso.Customers
	Limiter   *ratelimit.Limiter
	// TrustForwardedFor: IP do cliente pelo X-Forwarded-For (só atrás de proxy próprio).
	TrustForwardedFor bool
	// VisitStarted/VisitClosed avisam o módulo identidade sobre a visita do visitante (R-VIS-03).
	VisitStarted func(ctx context.Context, q database.Querier, visitorID uuid.UUID) error
	VisitClosed  func(ctx context.Context, q database.Querier, visitorID uuid.UUID, at time.Time) error
}

// Service implementa a fila transacional.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço.
func New(d Deps) *Service {
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

// row é uma participação lida do banco.
type row struct {
	ID              uuid.UUID
	EstablishmentID uuid.UUID
	QueueID         uuid.UUID
	OrderID         uuid.UUID
	AccountID       *uuid.UUID
	VisitorID       *uuid.UUID
	Nick            string
	LoyaltyEligible bool
	Status          string
	Position        int64
	TicketCode      string
	CalledAt        *time.Time
	ToleranceEndsAt *time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	EndReason       *string
	Version         int64
	CreatedAt       time.Time
}

const rowSelect = `
	SELECT id, establishment_id, queue_id, order_id, account_id, visitor_id, nick, loyalty_eligible, status, position,
	       ticket_code, called_at, tolerance_ends_at, started_at, finished_at, end_reason, version, created_at
	FROM participation`

func scanRow(r pgx.Row) (row, error) {
	var p row
	err := r.Scan(&p.ID, &p.EstablishmentID, &p.QueueID, &p.OrderID, &p.AccountID, &p.VisitorID, &p.Nick, &p.LoyaltyEligible,
		&p.Status, &p.Position, &p.TicketCode, &p.CalledAt, &p.ToleranceEndsAt, &p.StartedAt, &p.FinishedAt, &p.EndReason,
		&p.Version, &p.CreatedAt)
	return p, err
}

// load lê a participação DESTA loja (de outra loja → 404). lock trava a linha.
func load(ctx context.Context, q database.Querier, estID, id uuid.UUID, lock bool) (row, error) {
	sql := rowSelect + ` WHERE establishment_id = $1 AND id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	p, err := scanRow(q.QueryRow(ctx, sql, estID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, hs.ErrNotFound()
	}
	return p, err
}

func (p row) owns(id *acesso.Identity) bool {
	if id == nil {
		return false
	}
	if id.Kind == acesso.IdentityAccount {
		return p.AccountID != nil && *p.AccountID == id.AccountID
	}
	return p.VisitorID != nil && *p.VisitorID == id.VisitorID
}

func (p row) active() bool { return p.Status == Waiting || p.Status == Called || p.Status == InService }

// ---------------------------------------------------------------------------------------------
// Autorização por participação (R-PERM-01/04/05)

// role é como o pedido age sobre a participação.
type role struct {
	customer bool // dono da visita (visitante ou conta)
	staff    acesso.Actor
	isStaff  bool
}

func (r role) auditKind() string {
	switch {
	case r.customer:
		return ""
	case r.staff.IsOwner():
		return "owner"
	default:
		return "professional"
	}
}

type allow struct{ customer, professional, owner bool }

// authorize decide o papel do pedido sobre a participação p (fila queueProfessional).
//   - dono da visita (cookie de visitante/conta da própria visita) → cliente;
//   - equipe: dono da loja, ou profissional da fila da participação (own_queue);
//   - qualquer outro cliente/visitante → 404 (não revela que existe);
//   - equipe sem o papel exigido → 403.
func (s *Service) authorize(r *http.Request, p row, queueProfessional uuid.UUID, a allow) (role, error) {
	account, visitor := s.Customers.CustomerIdentities(r, p.EstablishmentID)
	if a.customer && (p.owns(account) || p.owns(visitor)) {
		return role{customer: true}, nil
	}
	actor, err := s.Auth.Member(r, p.EstablishmentID)
	if err != nil {
		var he *hs.Error
		if errors.As(err, &he) && he.Status == 401 && visitor != nil {
			return role{}, hs.ErrNotFound()
		}
		return role{}, err
	}
	if actor.IsOwner() && a.owner {
		return role{staff: actor, isStaff: true}, nil
	}
	if a.professional && actor.ProfessionalID != nil && *actor.ProfessionalID == queueProfessional {
		return role{staff: actor, isStaff: true}, nil
	}
	if actor.IsOwner() || a.professional {
		return role{}, hs.ErrForbidden("Esta ação não é permitida para o seu papel nesta fila.")
	}
	return role{}, hs.ErrForbidden("Somente o dono da loja pode fazer isso.")
}

func actorKey(a acesso.Actor) string { return "conta:" + a.AccountID.String() }

func identityKey(id acesso.Identity) string {
	if id.Kind == acesso.IdentityAccount {
		return "conta:" + id.AccountID.String()
	}
	return "visitante:" + id.VisitorID.String()
}

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, hs.ErrNotFound()
	}
	return id, nil
}

func conflict(code, title, detail string, current any) error {
	return &hs.Error{Status: 409, Code: code, Title: title, Detail: detail, Current: current}
}

// orderIdentity converte a identidade do cliente para a comanda.
func orderIdentity(id acesso.Identity) atendimento.Identity {
	if id.Kind == acesso.IdentityAccount {
		a := id.AccountID
		return atendimento.Identity{AccountID: &a}
	}
	v := id.VisitorID
	return atendimento.Identity{VisitorID: &v}
}

var _ estabelecimento.Occupancy = (*Service)(nil)
var _ estabelecimento.QueueActivity = (*Service)(nil)
