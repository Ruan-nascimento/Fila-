package pagamento

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/platform/database"
)

func brl(c int64) atendimento.Money { return atendimento.Money{AmountCents: c, Currency: "BRL"} }

// chargeView segue PixCharge. O QR é desenhado no navegador a partir do br_code.
type chargeView struct {
	ID                uuid.UUID         `json:"id"`
	OrderID           uuid.UUID         `json:"order_id"`
	Status            string            `json:"status"`
	Amount            atendimento.Money `json:"amount"`
	BRCode            *string           `json:"br_code"`
	ExpiresAt         *time.Time        `json:"expires_at"`
	CreatedAt         time.Time         `json:"created_at"`
	ResolvedAt        *time.Time        `json:"resolved_at"`
	CancelRequestedAt *time.Time        `json:"cancel_requested_at"`
}

// chargeRow é a cobrança lida do banco.
type chargeRow struct {
	chargeView
	EstablishmentID uuid.UUID
	ProviderID      *string
	TestMode        bool
	Attempts        int
	amount          int64
}

const chargeSelect = `
	SELECT id, order_id, status, amount_cents, br_code, expires_at, created_at, resolved_at, cancel_requested_at,
	       establishment_id, provider_charge_id, test_mode, check_attempts
	FROM pix_charge`

func scanCharge(r pgx.Row) (chargeRow, error) {
	var c chargeRow
	err := r.Scan(&c.ID, &c.OrderID, &c.Status, &c.amount, &c.BRCode, &c.ExpiresAt, &c.CreatedAt, &c.ResolvedAt,
		&c.CancelRequestedAt, &c.EstablishmentID, &c.ProviderID, &c.TestMode, &c.Attempts)
	c.Amount = brl(c.amount)
	return c, err
}

// activeCharge devolve a tentativa ativa da comanda (ou nil). lock trava a linha.
func activeCharge(ctx context.Context, q database.Querier, orderID uuid.UUID, lock bool) (*chargeRow, error) {
	sql := chargeSelect + ` WHERE order_id = $1 AND status = ANY($2)`
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := scanCharge(q.QueryRow(ctx, sql, orderID, activeStatuses))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// receiptView segue Receipt. Recebimento e conferência são registros distintos.
type receiptView struct {
	ID              uuid.UUID          `json:"id"`
	Method          string             `json:"method"`
	Amount          atendimento.Money  `json:"amount"`
	Status          string             `json:"status"`
	Classification  string             `json:"classification"`
	PixChargeID     *uuid.UUID         `json:"pix_charge_id"`
	RegisteredAt    time.Time          `json:"registered_at"`
	RegisteredBy    string             `json:"registered_by"`
	ConferredAt     *time.Time         `json:"conferred_at"`
	ConferredByRole *string            `json:"conferred_by_role"`
	CashTendered    *atendimento.Money `json:"cash_tendered"`
	ChangeGiven     *atendimento.Money `json:"change_given"`
	CardReference   *string            `json:"card_reference"`
	Refunded        atendimento.Money  `json:"refunded"`
	amount          int64
}

func receipts(ctx context.Context, q database.Querier, orderID uuid.UUID) ([]receiptView, error) {
	rows, err := q.Query(ctx, `
		SELECT r.id, r.method, r.amount_cents, r.classification, r.pix_charge_id, r.registered_at, r.registered_by,
		       c.conferred_at, c.conferred_by_role, r.cash_tendered_cents, r.change_cents, r.card_reference
		FROM receipt r LEFT JOIN receipt_conferral c ON c.receipt_id = r.id
		WHERE r.order_id = $1 ORDER BY r.registered_at, r.id`, orderID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (receiptView, error) {
		var v receiptView
		var tendered, change *int64
		err := row.Scan(&v.ID, &v.Method, &v.amount, &v.Classification, &v.PixChargeID, &v.RegisteredAt, &v.RegisteredBy,
			&v.ConferredAt, &v.ConferredByRole, &tendered, &change, &v.CardReference)
		v.Amount, v.Refunded = brl(v.amount), brl(0)
		v.Status = "registered"
		if v.ConferredAt != nil {
			v.Status = "conferred"
		}
		if tendered != nil {
			t, c := brl(*tendered), brl(*change)
			v.CashTendered, v.ChangeGiven = &t, &c
		}
		return v, err
	})
}

// PaymentData monta a parte de pagamento da visão da comanda (registrada em atendimento).
func PaymentData(ctx context.Context, q database.Querier, orderID uuid.UUID) (atendimento.PaymentData, error) {
	var d atendimento.PaymentData
	rows, err := q.Query(ctx, chargeSelect+` WHERE order_id = $1 ORDER BY created_at DESC, id DESC`, orderID)
	if err != nil {
		return d, err
	}
	charges, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (chargeRow, error) { return scanCharge(r) })
	if err != nil {
		return d, err
	}
	views := make([]chargeView, 0, len(charges))
	for _, c := range charges {
		views = append(views, c.chargeView)
		if c.Status == ChargeUnknown || c.Status == ChargeCreating {
			d.Verifying = true
		}
	}
	recs, err := receipts(ctx, q, orderID)
	if err != nil {
		return d, err
	}
	for _, r := range recs {
		d.ReceivedCents += r.amount
		if r.ConferredAt != nil {
			d.ConferredCents += r.amount
		}
		if r.Classification == "principal" {
			d.PrincipalCents += r.amount
		}
	}
	if recs == nil {
		recs = []receiptView{}
	}
	d.Charges, d.Receipts = views, recs
	return d, nil
}
