package atendimento

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
	"fila/internal/auditoria"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
)

// VisitState é o que a comanda precisa saber da participação (módulo fila).
type VisitState struct {
	ParticipationID uuid.UUID
	QueueID         uuid.UUID
	Status          string // ParticipationStatus
}

// Estados da participação usados aqui.
const (
	visitWaiting   = "waiting"
	visitCalled    = "called"
	visitInService = "in_service"
)

// Visits é implementado pelo módulo fila. A comanda não lê a tabela de participação: pede ao
// dono dela, respeitando a ordem de travas fila → participação → comanda (ADR-0019/0020).
type Visits interface {
	// LockVisit trava a fila e a participação (nesta ordem) e devolve o estado atual.
	LockVisit(ctx context.Context, tx pgx.Tx, estID, participationID uuid.UUID) (VisitState, error)
	// QueueChanged grava na outbox os eventos da fila depois de mudar serviços de alguém nela
	// (nomes dos serviços, estimativa e aviso de solicitação pendente).
	QueueChanged(ctx context.Context, tx pgx.Tx, estID, queueID uuid.UUID) error
}

// Situações de cobrança Pix que impedem corrigir uma comanda fechada (R-CMD-12).
const (
	ChargeNone    = ""
	ChargeActive  = "active"  // creating/pending
	ChargeUnknown = "unknown" // resultado desconhecido
)

// ChargeGuard informa se a comanda tem cobrança Pix ativa ou de resultado desconhecido. As
// cobranças chegam na etapa 08; até lá não existe cobrança (nil = nenhuma).
type ChargeGuard interface {
	ChargeState(ctx context.Context, q database.Querier, orderID uuid.UUID) (string, error)
}

// Deps são as dependências do módulo.
type Deps struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Log       *slog.Logger
	Auth      acesso.Authorizer
	Customers acesso.Customers
	Visits    Visits
	Charges   ChargeGuard
	// VisitClosed avisa o módulo identidade que a visita do visitante terminou (R-VIS-03).
	VisitClosed func(ctx context.Context, q database.Querier, visitorID uuid.UUID, at time.Time) error
}

// Service implementa as rotas de comanda.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço.
func New(d Deps) *Service {
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	const ord = "/v1/establishments/{establishmentId}/orders"
	const one = ord + "/{orderId}"
	visitOwner := []hs.Grant{{Actor: "visitor", Scope: "visit_owner"}, {Actor: "customer", Scope: "visit_owner"}}
	staff := []hs.Grant{{Actor: "professional", Scope: "own_attendance"}, hs.GrantOwner}
	all := append(append([]hs.Grant{}, visitOwner...), staff...)
	return []hs.Route{
		{Operation: "listOrders", Method: http.MethodGet, Path: ord, Grants: staff, Handler: s.listOrders},
		{Operation: "getOrder", Method: http.MethodGet, Path: one, Grants: all, Handler: s.getOrder},
		{Operation: "replaceOrderItems", Method: http.MethodPut, Path: one + "/items", Grants: all, Handler: s.replaceOrderItems},
		{Operation: "markItemPerformed", Method: http.MethodPost, Path: one + "/items/{itemId}/mark-performed", Grants: staff, Handler: s.markItemPerformed},
		{Operation: "createChangeRequest", Method: http.MethodPost, Path: one + "/change-requests", Grants: visitOwner, Handler: s.createChangeRequest},
		{Operation: "approveChangeRequest", Method: http.MethodPost, Path: one + "/change-requests/{changeRequestId}/approve", Grants: staff, Handler: s.approveChangeRequest},
		{Operation: "rejectChangeRequest", Method: http.MethodPost, Path: one + "/change-requests/{changeRequestId}/reject", Grants: staff, Handler: s.rejectChangeRequest},
		{Operation: "closeInterruptedOrder", Method: http.MethodPost, Path: one + "/close-interruption", Grants: []hs.Grant{hs.GrantOwner}, Handler: s.closeInterruptedOrder},
		{Operation: "adjustClosedOrder", Method: http.MethodPost, Path: one + "/adjustments", Grants: []hs.Grant{hs.GrantOwner}, Handler: s.adjustClosedOrder},
	}
}

// ---------------------------------------------------------------------------------------------
// Autorização por comanda (R-PERM-01/04/05)

// who é como o pedido age sobre a comanda.
type who struct {
	customer bool
	staff    acesso.Actor
	isStaff  bool
	key      string // escopo da Idempotency-Key
}

type allow struct{ customer, professional, owner bool }

func (w who) audit(ctx context.Context, o orderRow) auditoria.Event {
	e := auditoria.Event{RequestID: hs.RequestIDFrom(ctx)}
	switch {
	case w.isStaff && w.staff.IsOwner():
		e.ActorKind, e.ActorID = auditoria.ActorOwner, auditoria.Ptr(w.staff.AccountID)
	case w.isStaff:
		e.ActorKind, e.ActorID = auditoria.ActorProfessional, auditoria.Ptr(w.staff.AccountID)
	case o.Identity.AccountID != nil:
		e.ActorKind, e.ActorID = auditoria.ActorCustomer, o.Identity.AccountID
	default:
		e.ActorKind, e.ActorID = auditoria.ActorVisitor, o.Identity.VisitorID
	}
	return e
}

func (w who) role() string {
	if w.staff.IsOwner() {
		return acesso.RoleOwner
	}
	return acesso.RoleProfessional
}

func owns(o orderRow, id *acesso.Identity) bool {
	if id == nil {
		return false
	}
	if id.Kind == acesso.IdentityAccount {
		return o.Identity.AccountID != nil && *o.Identity.AccountID == id.AccountID
	}
	return o.Identity.VisitorID != nil && *o.Identity.VisitorID == id.VisitorID
}

// authorize decide o papel do pedido sobre a comanda o:
//   - dono da visita (cookie de visitante/conta da própria visita) → cliente;
//   - dono da loja → equipe; profissional do atendimento (own_attendance) → equipe;
//   - outro profissional, outro cliente ou visitante → 404 (não revela que existe);
//   - equipe com papel insuficiente para a ação → 403.
func (s *Service) authorize(r *http.Request, o orderRow, a allow) (who, error) {
	account, visitor := s.Customers.CustomerIdentities(r, o.EstablishmentID)
	if a.customer {
		if owns(o, account) {
			return who{customer: true, key: "conta:" + account.AccountID.String()}, nil
		}
		if owns(o, visitor) {
			return who{customer: true, key: "visitante:" + visitor.VisitorID.String()}, nil
		}
	}
	actor, err := s.Auth.Member(r, o.EstablishmentID)
	if err != nil {
		if owns(o, account) || owns(o, visitor) {
			return who{}, hs.ErrForbidden("Esta ação é feita pela equipe da loja.")
		}
		var he *hs.Error
		if errors.As(err, &he) && he.Status == 401 && visitor != nil {
			return who{}, hs.ErrNotFound()
		}
		return who{}, err
	}
	w := who{staff: actor, isStaff: true, key: "conta:" + actor.AccountID.String()}
	if actor.IsOwner() {
		if a.owner {
			return w, nil
		}
		return who{}, hs.ErrForbidden("Esta ação é feita pelo cliente da visita.")
	}
	if actor.ProfessionalID == nil || *actor.ProfessionalID != o.ProfessionalID {
		return who{}, hs.ErrNotFound() // atendimento de outro profissional (C-CMD-07)
	}
	if !a.professional {
		return who{}, hs.ErrForbidden("Somente o dono da loja pode fazer isso.")
	}
	return w, nil
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

// orderCommand é a parte comum dos comandos sobre uma comanda: lê a comanda, autoriza, valida o
// corpo e roda fn na transação idempotente. fn trava o que precisa (via lock).
func (s *Service) orderCommand(w http.ResponseWriter, r *http.Request, op string, a allow, body any,
	check func(who) error, fn func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error)) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	orderID, err := pathID(r, "orderId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	o, err := getOrder(ctx, s.Pool, estID, orderID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return hs.ErrNotFound()
	}
	if err != nil {
		return err
	}
	by, err := s.authorize(r, o, a)
	if err != nil {
		return err
	}
	raw, err := hs.DecodeJSON(r, body)
	if err != nil {
		return err
	}
	if check != nil {
		if err := check(by); err != nil {
			return err
		}
	}
	return s.idem.Run(w, r, idempotency.Command{ActorKey: by.key, Operation: op, Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
		return fn(tx, o, by)
	})
}

// lockWithVisit trava fila → participação → comanda e confere a versão esperada da comanda.
func (s *Service) lockWithVisit(ctx context.Context, tx pgx.Tx, o orderRow, expected int64) (orderRow, VisitState, error) {
	visit, err := s.Visits.LockVisit(ctx, tx, o.EstablishmentID, o.ParticipationID)
	if err != nil {
		return o, visit, err
	}
	cur, err := s.lockOrder(ctx, tx, o, expected)
	return cur, visit, err
}

// lockOrder trava só a comanda (comandos sobre comanda já fora da fila) e confere a versão.
func (s *Service) lockOrder(ctx context.Context, tx pgx.Tx, o orderRow, expected int64) (orderRow, error) {
	cur, err := getOrder(ctx, tx, o.EstablishmentID, o.ID, true)
	if err != nil {
		return cur, err
	}
	if cur.Version != expected {
		return cur, s.orderConflict(ctx, tx, cur, "version_conflict", "A comanda mudou",
			"Outra pessoa alterou esta comanda. Confira os dados atualizados.")
	}
	return cur, nil
}

// orderConflict devolve 409 com a comanda atual em current.
func (s *Service) orderConflict(ctx context.Context, q database.Querier, o orderRow, code, title, detail string) error {
	view, err := LoadOrder(ctx, q, o.EstablishmentID, o.ID)
	if err != nil {
		return err
	}
	return conflict(code, title, detail, view)
}

// done devolve a comanda atualizada como resposta do comando.
func (s *Service) done(ctx context.Context, tx pgx.Tx, o orderRow, status int) (idempotency.Result, error) {
	view, err := LoadOrder(ctx, tx, o.EstablishmentID, o.ID)
	if err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: status, Body: hs.Data[OrderView]{Data: view}}, nil
}

// bumpOpenTotal recalcula o total da comanda aberta (soma dos itens não retirados) e sobe a versão.
func bumpOpenTotal(ctx context.Context, q database.Querier, orderID uuid.UUID, now time.Time) (int64, error) {
	var version int64
	err := q.QueryRow(ctx, `
		UPDATE service_order SET total_cents = (SELECT coalesce(sum(unit_price_cents), 0) FROM order_item
		                                        WHERE order_id = $1 AND status <> 'removed'),
		       version = version + 1, updated_at = $2
		WHERE id = $1 RETURNING version`, orderID, now).Scan(&version)
	return version, err
}

// bumpVersion sobe a versão da comanda (mudança que não altera o total).
func bumpVersion(ctx context.Context, q database.Querier, orderID uuid.UUID, now time.Time) (int64, error) {
	var version int64
	err := q.QueryRow(ctx, `UPDATE service_order SET version = version + 1, updated_at = $2 WHERE id = $1 RETURNING version`,
		orderID, now).Scan(&version)
	return version, err
}
