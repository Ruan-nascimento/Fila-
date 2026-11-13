package atendimento

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// Money segue o esquema Money: centavos inteiros, sempre BRL.
type Money struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

func brl(c int64) Money { return Money{AmountCents: c, Currency: "BRL"} }

// ItemView segue OrderItem.
type ItemView struct {
	ID              uuid.UUID  `json:"id"`
	ServiceID       uuid.UUID  `json:"service_id"`
	Name            string     `json:"name"`
	UnitPrice       Money      `json:"unit_price"`
	DurationMinutes int        `json:"duration_minutes"`
	Points          int        `json:"points"`
	Status          string     `json:"status"`
	Charged         bool       `json:"charged"`
	AddedAt         time.Time  `json:"added_at"`
	PerformedAt     *time.Time `json:"performed_at"`
}

type amounts struct {
	Due         Money `json:"due"`
	Received    Money `json:"received"`
	Conferred   Money `json:"conferred"`
	Refunded    Money `json:"refunded"`
	Outstanding Money `json:"outstanding"`
}

// PaymentView segue OrderPayment. Pagamentos chegam nas etapas 08/09: até lá não há método,
// cobrança, recebimento nem estorno, e nada é devido enquanto a comanda está aberta (o valor só
// fica devido depois da conclusão, etapa 05).
type PaymentView struct {
	MethodChoice *string  `json:"method_choice"`
	Amounts      amounts  `json:"amounts"`
	Verifying    bool     `json:"verifying"`
	Charges      []string `json:"charges"`
	Receipts     []string `json:"receipts"`
	Refunds      []string `json:"refunds"`
}

// OrderView segue Order.
type OrderView struct {
	ID                   uuid.UUID   `json:"id"`
	EstablishmentID      uuid.UUID   `json:"establishment_id"`
	ParticipationID      uuid.UUID   `json:"participation_id"`
	Status               string      `json:"status"`
	Version              int64       `json:"version"`
	Items                []ItemView  `json:"items"`
	Total                Money       `json:"total"`
	TotalFrozenAt        *time.Time  `json:"total_frozen_at"`
	PendingChangeRequest *struct{}   `json:"pending_change_request"`
	ClosureReason        *string     `json:"closure_reason"`
	PointsExpected       *int        `json:"points_expected"`
	Payment              PaymentView `json:"payment"`
	CreatedAt            time.Time   `json:"created_at"`
	ClosedAt             *time.Time  `json:"closed_at"`
}

// LoadOrder monta a visão da comanda DESTA loja.
func LoadOrder(ctx context.Context, q database.Querier, estID, orderID uuid.UUID) (OrderView, error) {
	var v OrderView
	var total int64
	err := q.QueryRow(ctx, `
		SELECT id, establishment_id, participation_id, status, version, total_cents, total_frozen_at,
		       closure_reason, created_at, closed_at
		FROM service_order WHERE establishment_id = $1 AND id = $2`, estID, orderID).
		Scan(&v.ID, &v.EstablishmentID, &v.ParticipationID, &v.Status, &v.Version, &total, &v.TotalFrozenAt,
			&v.ClosureReason, &v.CreatedAt, &v.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	if err != nil {
		return v, err
	}
	v.Total = brl(total)
	zero := brl(0)
	v.Payment = PaymentView{Amounts: amounts{Due: zero, Received: zero, Conferred: zero, Refunded: zero, Outstanding: zero},
		Charges: []string{}, Receipts: []string{}, Refunds: []string{}}
	rows, err := q.Query(ctx, `
		SELECT id, service_id, name, unit_price_cents, duration_minutes, points, status, charged, added_at, performed_at
		FROM order_item WHERE order_id = $1 ORDER BY added_at, name, id`, orderID)
	if err != nil {
		return v, err
	}
	v.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemView, error) {
		var it ItemView
		var price int64
		err := r.Scan(&it.ID, &it.ServiceID, &it.Name, &price, &it.DurationMinutes, &it.Points, &it.Status, &it.Charged, &it.AddedAt, &it.PerformedAt)
		it.UnitPrice = brl(price)
		return it, err
	})
	if v.Items == nil {
		v.Items = []ItemView{}
	}
	return v, err
}
