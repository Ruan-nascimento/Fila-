package pagamento

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
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
)

// Estados da cobrança (PixChargeStatus).
const (
	ChargeCreating  = "creating"
	ChargePending   = "pending"
	ChargeUnknown   = "unknown"
	ChargePaid      = "paid"
	ChargeExpired   = "expired"
	ChargeCancelled = "cancelled"
	ChargeFailed    = "failed"
)

var activeStatuses = []string{ChargeCreating, ChargePending, ChargeUnknown}

// Deps são as dependências do módulo.
type Deps struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Log    *slog.Logger
	Orders *atendimento.Service
	Auth   acesso.Authorizer
	// Provider é nil quando o Pix está desligado (FILA_PIX_PROVIDER=disabled): só dinheiro e cartão.
	Provider  Provider
	ExpiresIn time.Duration
	// Timeout de cada chamada ao provedor (cobrança "criando" há mais que isso vira desconhecida).
	Timeout     time.Duration
	VisitClosed func(ctx context.Context, q database.Querier, visitorID uuid.UUID, at time.Time) error
	// WebhookSecret do webhook cadastrado na AbacatePay; vazio = webhook desligado (ADR-0024).
	WebhookSecret config.Secret
	// Pendencies grava pendencies.updated (contadores do painel do dono) na transação. nil = sem painel.
	Pendencies func(ctx context.Context, tx pgx.Tx, estID uuid.UUID, now time.Time) error
}

// Service implementa pagamentos (cobrança Pix, recebimentos e conferência).
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço e liga a visão de pagamento à visão da comanda.
func New(d Deps) *Service {
	if d.ExpiresIn == 0 {
		d.ExpiresIn = 15 * time.Minute
	}
	if d.Timeout == 0 {
		d.Timeout = 10 * time.Second
	}
	atendimento.RegisterPaymentData(PaymentData)
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	const one = "/v1/establishments/{establishmentId}/orders/{orderId}"
	visitOwner := []hs.Grant{{Actor: "visitor", Scope: "visit_owner"}, {Actor: "customer", Scope: "visit_owner"}}
	staff := []hs.Grant{{Actor: "professional", Scope: "own_attendance"}, hs.GrantOwner}
	owner := []hs.Grant{hs.GrantOwner}
	all := append(append([]hs.Grant{}, visitOwner...), staff...)
	return []hs.Route{
		{Operation: "choosePaymentMethod", Method: http.MethodPost, Path: one + "/payment-method", Grants: all, Handler: s.choosePaymentMethod},
		{Operation: "createPixCharge", Method: http.MethodPost, Path: one + "/pix-charges", Grants: all, Handler: s.createPixCharge},
		{Operation: "getPixCharge", Method: http.MethodGet, Path: one + "/pix-charges/{chargeId}", Grants: all, Handler: s.getPixCharge},
		{Operation: "cancelPixCharge", Method: http.MethodPost, Path: one + "/pix-charges/{chargeId}/cancel", Grants: staff, Handler: s.cancelPixCharge},
		{Operation: "registerInPersonReceipt", Method: http.MethodPost, Path: one + "/receipts", Grants: staff, Handler: s.registerInPersonReceipt},
		{Operation: "conferReceipt", Method: http.MethodPost, Path: one + "/receipts/{receiptId}/confer", Grants: staff, Handler: s.conferReceipt},
		{Operation: "requestRefund", Method: http.MethodPost, Path: one + "/receipts/{receiptId}/refunds", Grants: owner, Handler: s.requestRefund},
		{Operation: "confirmRefundManually", Method: http.MethodPost, Path: one + "/refunds/{refundId}/confirm", Grants: owner,
			Handler: s.manualRefund(RefundConfirmed)},
		{Operation: "markRefundFailed", Method: http.MethodPost, Path: one + "/refunds/{refundId}/mark-failed", Grants: owner,
			Handler: s.manualRefund(RefundFailed)},
		{Operation: "listFinancialIssues", Method: http.MethodGet, Path: "/v1/establishments/{establishmentId}/financial-issues",
			Grants: owner, Handler: s.listFinancialIssues},
		{Operation: "resolveFinancialIssue", Method: http.MethodPost,
			Path: "/v1/establishments/{establishmentId}/financial-issues/{issueId}/resolve", Grants: owner, Handler: s.resolveFinancialIssue},
		{Operation: "receiveAbacatePayWebhook", Method: http.MethodPost, Path: "/v1/webhooks/abacatepay",
			Grants: []hs.Grant{{Actor: "provider", Scope: "provider_signature"}}, Handler: s.receiveAbacatePayWebhook},
	}
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

// order lê a comanda do caminho e autoriza o pedido sobre ela.
func (s *Service) order(r *http.Request, customer, professional, owner bool) (atendimento.PaymentInfo, atendimento.Who, error) {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return atendimento.PaymentInfo{}, atendimento.Who{}, err
	}
	orderID, err := pathID(r, "orderId")
	if err != nil {
		return atendimento.PaymentInfo{}, atendimento.Who{}, err
	}
	p, err := atendimento.GetPaymentInfo(r.Context(), s.Pool, estID, orderID)
	if errors.Is(err, atendimento.ErrOrderNotFound) {
		return p, atendimento.Who{}, hs.ErrNotFound()
	}
	if err != nil {
		return p, atendimento.Who{}, err
	}
	who, err := s.Orders.AuthorizeOrder(r, p, customer, professional, owner)
	return p, who, err
}

// orderConflict devolve 409 com a comanda atual.
func orderConflict(ctx context.Context, q database.Querier, p atendimento.PaymentInfo, code, title, detail string) error {
	cur, err := atendimento.OrderResult(ctx, q, p.EstablishmentID, p.ID)
	if err != nil {
		return err
	}
	return conflict(code, title, detail, cur.Data)
}

// payableCheck confere a comanda travada: versão e "aguardando pagamento" (R-PAG-02, R-PAG-14).
func payableCheck(ctx context.Context, tx pgx.Tx, p atendimento.PaymentInfo, expected int64) error {
	if p.Version != expected {
		return orderConflict(ctx, tx, p, "version_conflict", "A comanda mudou", "Confira os dados atualizados e tente de novo.")
	}
	if p.Status != atendimento.StatusAwaitingPayment || p.DueCents() == 0 {
		return orderConflict(ctx, tx, p, "order_not_payable", "Nada a pagar agora",
			"Esta comanda não está aguardando pagamento (ou o valor é zero).")
	}
	return nil
}

// ChargeState implementa atendimento.ChargeGuard: correção de valor só sem Pix ativo ou desconhecido.
func (s *Service) ChargeState(ctx context.Context, q database.Querier, orderID uuid.UUID) (string, error) {
	var status string
	err := q.QueryRow(ctx, `SELECT status FROM pix_charge WHERE order_id = $1 AND status = ANY($2)`, orderID, activeStatuses).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return atendimento.ChargeNone, nil
	case err != nil:
		return "", err
	case status == ChargeUnknown || status == ChargeCreating:
		return atendimento.ChargeUnknown, nil
	}
	return atendimento.ChargeActive, nil
}

var _ atendimento.ChargeGuard = (*Service)(nil)
