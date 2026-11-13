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

// ItemView segue OrderItem. O valor cobrado é explicável item a item: entra no total só o item
// com charged = true, pelo unit_price aceito na inclusão.
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
	ReportedOutcome *string    `json:"reported_outcome"`
	OutcomeReason   *string    `json:"outcome_reason"`
}

type amounts struct {
	Due         Money `json:"due"`
	Received    Money `json:"received"`
	Conferred   Money `json:"conferred"`
	Refunded    Money `json:"refunded"`
	Outstanding Money `json:"outstanding"`
}

// PaymentView segue OrderPayment. Cobranças e recebimentos chegam nas etapas 08/09: até lá as
// listas ficam vazias e nada foi recebido. O valor só é devido depois de fechado (conclusão ou
// revisão da interrupção).
type PaymentView struct {
	MethodChoice *string  `json:"method_choice"`
	Amounts      amounts  `json:"amounts"`
	Verifying    bool     `json:"verifying"`
	Charges      []string `json:"charges"`
	Receipts     []string `json:"receipts"`
	Refunds      []string `json:"refunds"`
}

// AdditionView é um serviço pedido numa solicitação, com o preço visto pelo cliente.
type AdditionView struct {
	ServiceID uuid.UUID `json:"service_id"`
	Name      string    `json:"name"`
	UnitPrice Money     `json:"unit_price"`
}

// ChangeRequestView segue ChangeRequest.
type ChangeRequestView struct {
	ID             uuid.UUID      `json:"id"`
	Status         string         `json:"status"`
	Add            []AdditionView `json:"add"`
	RemoveItemIDs  []uuid.UUID    `json:"remove_item_ids"`
	CreatedAt      time.Time      `json:"created_at"`
	DecidedAt      *time.Time     `json:"decided_at"`
	DecidedByRole  *string        `json:"decided_by_role"`
	DecisionReason *string        `json:"decision_reason"`
	version        int64
	orderID        uuid.UUID
}

// AdjustmentView segue OrderAdjustment: correção do dono depois de fechado o valor (T-O-11).
type AdjustmentView struct {
	ID               uuid.UUID   `json:"id"`
	Reason           string      `json:"reason"`
	PreviousTotal    Money       `json:"previous_total"`
	NewTotal         Money       `json:"new_total"`
	UnchargedItemIDs []uuid.UUID `json:"uncharged_item_ids"`
	AddedItemIDs     []uuid.UUID `json:"added_item_ids"`
	At               time.Time   `json:"at"`
}

// OrderView segue Order.
type OrderView struct {
	ID                   uuid.UUID           `json:"id"`
	EstablishmentID      uuid.UUID           `json:"establishment_id"`
	ParticipationID      uuid.UUID           `json:"participation_id"`
	Status               string              `json:"status"`
	Version              int64               `json:"version"`
	Items                []ItemView          `json:"items"`
	Total                Money               `json:"total"`
	TotalFrozenAt        *time.Time          `json:"total_frozen_at"`
	PendingChangeRequest *ChangeRequestView  `json:"pending_change_request"`
	ChangeRequests       []ChangeRequestView `json:"change_requests"`
	ClosureReason        *string             `json:"closure_reason"`
	InterruptedAt        *time.Time          `json:"interrupted_at"`
	ReviewNote           *string             `json:"review_note"`
	Adjustments          []AdjustmentView    `json:"adjustments"`
	PointsExpected       *int                `json:"points_expected"`
	Payment              PaymentView         `json:"payment"`
	CreatedAt            time.Time           `json:"created_at"`
	ClosedAt             *time.Time          `json:"closed_at"`
}

// payable: estados em que o valor fechado é devido.
func payable(status string) bool {
	return status == StatusAwaitingPayment || status == "awaiting_confirmation" || status == "settled"
}

// LoadOrder monta a visão da comanda DESTA loja (de outra loja → pgx.ErrNoRows).
func LoadOrder(ctx context.Context, q database.Querier, estID, orderID uuid.UUID) (OrderView, error) {
	var v OrderView
	var total int64
	var eligible bool
	err := q.QueryRow(ctx, `
		SELECT id, establishment_id, participation_id, status, version, total_cents, total_frozen_at,
		       closure_reason, interrupted_at, review_note, loyalty_eligible, created_at, closed_at
		FROM service_order WHERE establishment_id = $1 AND id = $2`, estID, orderID).
		Scan(&v.ID, &v.EstablishmentID, &v.ParticipationID, &v.Status, &v.Version, &total, &v.TotalFrozenAt,
			&v.ClosureReason, &v.InterruptedAt, &v.ReviewNote, &eligible, &v.CreatedAt, &v.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	if err != nil {
		return v, err
	}
	v.Total = brl(total)
	due := int64(0)
	if payable(v.Status) {
		due = total
	}
	zero := brl(0)
	v.Payment = PaymentView{Amounts: amounts{Due: brl(due), Received: zero, Conferred: zero, Refunded: zero, Outstanding: brl(due)},
		Charges: []string{}, Receipts: []string{}, Refunds: []string{}}

	rows, err := q.Query(ctx, `
		SELECT id, service_id, name, unit_price_cents, duration_minutes, points, status, charged, added_at, performed_at,
		       reported_outcome, outcome_reason
		FROM order_item WHERE order_id = $1 ORDER BY added_at, name, id`, orderID)
	if err != nil {
		return v, err
	}
	v.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemView, error) {
		var it ItemView
		var price int64
		err := r.Scan(&it.ID, &it.ServiceID, &it.Name, &price, &it.DurationMinutes, &it.Points, &it.Status, &it.Charged,
			&it.AddedAt, &it.PerformedAt, &it.ReportedOutcome, &it.OutcomeReason)
		it.UnitPrice = brl(price)
		return it, err
	})
	if err != nil {
		return v, err
	}
	if v.Items == nil {
		v.Items = []ItemView{}
	}
	// Pontos previstos (R-FID-03/05): só conta elegível; interrupção nunca gera pontos.
	if eligible {
		pts := 0
		if v.InterruptedAt == nil {
			for _, it := range v.Items {
				if (v.TotalFrozenAt == nil && it.Status != ItemRemoved) || it.Charged {
					pts += it.Points
				}
			}
		}
		v.PointsExpected = &pts
	}

	v.ChangeRequests, err = changeRequests(ctx, q, orderID)
	if err != nil {
		return v, err
	}
	for i := range v.ChangeRequests {
		if v.ChangeRequests[i].Status == CRPending {
			cr := v.ChangeRequests[i]
			v.PendingChangeRequest = &cr
		}
	}
	rows, err = q.Query(ctx, `
		SELECT id, reason, previous_total_cents, new_total_cents, uncharged_item_ids, added_item_ids, at
		FROM order_adjustment WHERE order_id = $1 ORDER BY at, id`, orderID)
	if err != nil {
		return v, err
	}
	v.Adjustments, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdjustmentView, error) {
		var a AdjustmentView
		var prev, next int64
		err := r.Scan(&a.ID, &a.Reason, &prev, &next, &a.UnchargedItemIDs, &a.AddedItemIDs, &a.At)
		a.PreviousTotal, a.NewTotal = brl(prev), brl(next)
		return a, err
	})
	if v.Adjustments == nil {
		v.Adjustments = []AdjustmentView{}
	}
	return v, err
}

// changeRequests lê as solicitações da comanda, em ordem de criação.
func changeRequests(ctx context.Context, q database.Querier, orderID uuid.UUID) ([]ChangeRequestView, error) {
	rows, err := q.Query(ctx, `
		SELECT id, order_id, status, remove_item_ids, version, created_at, decided_at, decided_by_role, decision_reason
		FROM change_request WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChangeRequestView, error) {
		var c ChangeRequestView
		err := r.Scan(&c.ID, &c.orderID, &c.Status, &c.RemoveItemIDs, &c.version, &c.CreatedAt, &c.DecidedAt, &c.DecidedByRole, &c.DecisionReason)
		c.Add = []AdditionView{}
		return c, err
	})
	if err != nil || len(list) == 0 {
		return []ChangeRequestView{}, err
	}
	rows, err = q.Query(ctx, `
		SELECT a.change_request_id, a.service_id, a.name, a.unit_price_cents
		FROM change_request_addition a JOIN change_request c ON c.id = a.change_request_id
		WHERE c.order_id = $1 ORDER BY a.name, a.service_id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[uuid.UUID]int{}
	for i := range list {
		idx[list[i].ID] = i
	}
	for rows.Next() {
		var crID uuid.UUID
		var a AdditionView
		var price int64
		if err := rows.Scan(&crID, &a.ServiceID, &a.Name, &price); err != nil {
			return nil, err
		}
		a.UnitPrice = brl(price)
		list[idx[crID]].Add = append(list[idx[crID]].Add, a)
	}
	return list, rows.Err()
}
