package atendimento

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
)

// Estados de comanda usados pelo pagamento.
const (
	StatusAwaitingConfirmation = "awaiting_confirmation"
	StatusSettled              = "settled"
)

// PaymentInfo é o que o módulo pagamento precisa saber da comanda (sem tocar na tabela dela).
type PaymentInfo struct {
	ID              uuid.UUID
	EstablishmentID uuid.UUID
	ProfessionalID  uuid.UUID
	Identity        Identity
	Status          string
	TotalCents      int64
	Version         int64
	Method          *string
}

// DueCents é o valor devido: o total fechado, só quando a comanda está a pagar.
func (p PaymentInfo) DueCents() int64 {
	if payable(p.Status) {
		return p.TotalCents
	}
	return 0
}

// ErrOrderNotFound indica comanda inexistente nesta loja.
var ErrOrderNotFound = errors.New("comanda não encontrada")

func paymentInfo(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, lock bool) (PaymentInfo, error) {
	sql := `SELECT id, establishment_id, professional_id, account_id, visitor_id, status, total_cents, version, payment_method
		FROM service_order WHERE establishment_id = $1 AND id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	var p PaymentInfo
	err := q.QueryRow(ctx, sql, estID, orderID).Scan(&p.ID, &p.EstablishmentID, &p.ProfessionalID, &p.Identity.AccountID,
		&p.Identity.VisitorID, &p.Status, &p.TotalCents, &p.Version, &p.Method)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrOrderNotFound
	}
	return p, err
}

// GetPaymentInfo lê a comanda DESTA loja (sem travar).
func GetPaymentInfo(ctx context.Context, q database.Querier, estID, orderID uuid.UUID) (PaymentInfo, error) {
	return paymentInfo(ctx, q, estID, orderID, false)
}

// LockForPayment trava a comanda (FOR UPDATE) para um comando de pagamento.
func LockForPayment(ctx context.Context, tx pgx.Tx, estID, orderID uuid.UUID) (PaymentInfo, error) {
	return paymentInfo(ctx, tx, estID, orderID, true)
}

func bumpWith(ctx context.Context, q database.Querier, orderID uuid.UUID, set string, now time.Time, args ...any) (int64, error) {
	var version int64
	all := append([]any{orderID, now}, args...)
	err := q.QueryRow(ctx, `UPDATE service_order SET `+set+`version = version + 1, updated_at = $2 WHERE id = $1 RETURNING version`, all...).Scan(&version)
	return version, err
}

// SetPaymentMethod registra o meio escolhido (T-O-12).
func SetPaymentMethod(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, method string, now time.Time) (int64, error) {
	return bumpWith(ctx, tx, orderID, `payment_method = $3, `, now, method)
}

// MarkAwaitingConfirmation (T-O-09): Pix recebido cobre o devido; falta a conferência da equipe.
func MarkAwaitingConfirmation(ctx context.Context, tx pgx.Tx, p PaymentInfo, now time.Time, audit auditoria.Event) error {
	version, err := bumpWith(ctx, tx, p.ID, `status = 'awaiting_confirmation', `, now)
	if err != nil {
		return err
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action, audit.TargetType, audit.TargetID = &p.EstablishmentID, now, "T-O-09", "order", p.ID
	audit.FromState, audit.ToState = versionTag(p.Status, version-1), versionTag(StatusAwaitingConfirmation, version)
	return auditoria.Record(ctx, tx, audit)
}

// Settle (T-O-10): recebimento integral E conferido → comanda paga. O banco confere a soma no
// COMMIT (check_order_payment). visitClosed encerra a visita do visitante (credencial vale +24 h).
func Settle(ctx context.Context, tx pgx.Tx, p PaymentInfo, now time.Time, audit auditoria.Event,
	visitClosed func(context.Context, database.Querier, uuid.UUID, time.Time) error) error {
	version, err := bumpWith(ctx, tx, p.ID, `status = 'settled', closed_at = $2, `, now)
	if err != nil {
		return err
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action, audit.TargetType, audit.TargetID = &p.EstablishmentID, now, "T-O-10", "order", p.ID
	audit.FromState, audit.ToState = versionTag(p.Status, version-1), versionTag(StatusSettled, version)
	if err := auditoria.Record(ctx, tx, audit); err != nil {
		return err
	}
	if p.Identity.VisitorID != nil && visitClosed != nil {
		return visitClosed(ctx, tx, *p.Identity.VisitorID, now)
	}
	return nil
}

// RecordPaymentEvents grava order.updated e payment.updated (outbox) depois de uma mudança de
// pagamento. bump=true sobe a versão da comanda antes (a visão mudou: cobrança ou recebimento).
func RecordPaymentEvents(ctx context.Context, tx pgx.Tx, estID, orderID uuid.UUID, now time.Time, bump bool) error {
	if bump {
		if _, err := bumpVersion(ctx, tx, orderID, now); err != nil {
			return err
		}
	}
	return recordOrder(ctx, tx, estID, orderID, now, evOrder, evPayment)
}

// PaymentData é a parte da visão da comanda que vem do módulo pagamento.
type PaymentData struct {
	Charges        any
	Receipts       any
	ReceivedCents  int64 // tudo o que entrou (principal + excesso)
	ConferredCents int64
	PrincipalCents int64 // o que cobre o devido
	Verifying      bool  // há cobrança com resultado desconhecido
	Refunds        any
	RefundedCents  int64    // estornos confirmados
	OpenIssueKinds []string // pendências financeiras abertas da comanda
}

var paymentData func(ctx context.Context, q database.Querier, orderID uuid.UUID) (PaymentData, error)

// RegisterPaymentData liga a visão de pagamento (módulo pagamento) à visão da comanda. Chamado
// na montagem da API e do worker, antes de atender pedidos.
func RegisterPaymentData(f func(ctx context.Context, q database.Querier, orderID uuid.UUID) (PaymentData, error)) {
	paymentData = f
}

// Who é quem age sobre a comanda (para o módulo pagamento).
type Who struct {
	Customer bool
	IsStaff  bool
	Staff    acesso.Actor
	Key      string // escopo da Idempotency-Key
}

// Role é o papel da equipe ("owner" ou "professional").
func (w Who) Role() string {
	if w.Staff.IsOwner() {
		return acesso.RoleOwner
	}
	return acesso.RoleProfessional
}

// Audit devolve o ator para a auditoria (só pseudônimos).
func (w Who) Audit(ctx context.Context, p PaymentInfo) auditoria.Event {
	return who{customer: w.Customer, isStaff: w.IsStaff, staff: w.Staff}.audit(ctx, orderRow{Identity: p.Identity})
}

// CreatedBy devolve o tipo de ator ("visitor", "customer", "professional", "owner").
func (w Who) CreatedBy(p PaymentInfo) string {
	switch {
	case w.IsStaff:
		return w.Role()
	case p.Identity.AccountID != nil:
		return "customer"
	default:
		return "visitor"
	}
}

// AuthorizeOrder aplica as mesmas regras de acesso da comanda (dono da visita, profissional do
// atendimento, dono da loja; outros → 404; papel insuficiente → 403).
func (s *Service) AuthorizeOrder(r *http.Request, p PaymentInfo, customer, professional, owner bool) (Who, error) {
	w, err := s.authorize(r, orderRow{ID: p.ID, EstablishmentID: p.EstablishmentID, ProfessionalID: p.ProfessionalID,
		Identity: p.Identity}, allow{customer: customer, professional: professional, owner: owner})
	if err != nil {
		return Who{}, err
	}
	return Who{Customer: w.customer, IsStaff: w.isStaff, Staff: w.staff, Key: w.key}, nil
}

// OrderResult devolve a comanda atualizada como resposta de um comando de pagamento.
func OrderResult(ctx context.Context, q database.Querier, estID, orderID uuid.UUID) (hs.Data[OrderView], error) {
	v, err := LoadOrder(ctx, q, estID, orderID)
	return hs.Data[OrderView]{Data: v}, err
}

// OrderCounts são os contadores de comandas do painel do dono.
type OrderCounts struct {
	AwaitingPayment      int
	AwaitingConfirmation int
	PendingReview        int
}

// PendencyCounts conta as comandas da loja que pedem ação (painel do dono).
func PendencyCounts(ctx context.Context, q database.Querier, estID uuid.UUID) (OrderCounts, error) {
	var c OrderCounts
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'awaiting_payment'), count(*) FILTER (WHERE status = 'awaiting_confirmation'),
		       count(*) FILTER (WHERE status = 'pending_review')
		FROM service_order WHERE establishment_id = $1 AND status IN ('awaiting_payment', 'awaiting_confirmation', 'pending_review')`,
		estID).Scan(&c.AwaitingPayment, &c.AwaitingConfirmation, &c.PendingReview)
	return c, err
}

// OrderCredit é o que a fidelidade precisa saber de uma comanda para creditar pontos (T-LG-01).
type OrderCredit struct {
	EstablishmentID uuid.UUID
	AccountID       *uuid.UUID // nil = visitante (nunca ganha pontos)
	Eligible        bool       // snapshot de elegibilidade da entrada (R-FID-03)
	Interrupted     bool       // atendimento interrompido não gera pontos (R-FID-05)
	Status          string
	Points          int // soma dos pontos aceitos dos itens realizados e cobrados
}

// CreditInfo lê a comanda DESTA loja para o crédito ou a reversão de pontos.
func CreditInfo(ctx context.Context, q database.Querier, estID, orderID uuid.UUID) (OrderCredit, error) {
	var c OrderCredit
	var interrupted *time.Time
	err := q.QueryRow(ctx, `
		SELECT o.establishment_id, o.account_id, o.loyalty_eligible, o.interrupted_at, o.status,
		       coalesce((SELECT sum(points) FROM order_item WHERE order_id = o.id AND charged AND status = 'performed'), 0)
		FROM service_order o WHERE o.establishment_id = $1 AND o.id = $2`, estID, orderID).
		Scan(&c.EstablishmentID, &c.AccountID, &c.Eligible, &interrupted, &c.Status, &c.Points)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrOrderNotFound
	}
	c.Interrupted = interrupted != nil
	return c, err
}

var pointsCredited func(ctx context.Context, q database.Querier, orderIDs []uuid.UUID) (map[uuid.UUID]int, error)

// RegisterPointsCredited liga o histórico aos pontos efetivamente creditados (módulo fidelidade).
func RegisterPointsCredited(f func(ctx context.Context, q database.Querier, orderIDs []uuid.UUID) (map[uuid.UUID]int, error)) {
	pointsCredited = f
}
