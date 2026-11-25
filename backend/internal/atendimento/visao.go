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

// ItemView segue OrderItem. O valor é explicável item a item: com a comanda aberta, subtotal =
// preço unitário aceito × quantidade; com o valor fechado, × unidades cobradas. Produto não tem
// duração nem pontos; performed_quantity de produto são unidades ENTREGUES.
type ItemView struct {
	ID                uuid.UUID  `json:"id"`
	Kind              string     `json:"kind"`
	ServiceID         *uuid.UUID `json:"service_id"`
	ProductID         *uuid.UUID `json:"product_id"`
	Name              string     `json:"name"`
	UnitPrice         Money      `json:"unit_price"`
	Quantity          int        `json:"quantity"`
	PerformedQuantity int        `json:"performed_quantity"`
	ChargedQuantity   int        `json:"charged_quantity"`
	ReportedQuantity  *int       `json:"reported_quantity"`
	Subtotal          Money      `json:"subtotal"`
	DurationMinutes   int        `json:"duration_minutes"`
	Points            int        `json:"points"`
	Status            string     `json:"status"`
	Charged           bool       `json:"charged"`
	AddedAt           time.Time  `json:"added_at"`
	PerformedAt       *time.Time `json:"performed_at"`
	ReportedOutcome   *string    `json:"reported_outcome"`
	OutcomeReason     *string    `json:"outcome_reason"`
}

// orderTotals resume as unidades e a duração da comanda (participação ≠ unidades ≠ pessoas).
type orderTotals struct {
	ServiceUnits    int `json:"service_units"`
	ProductUnits    int `json:"product_units"`
	DurationMinutes int `json:"duration_minutes"`
}

type amounts struct {
	// Credit: sinal pago na reserva e abatido desta comanda (não é dinheiro novo); Due já o desconta.
	Credit      Money `json:"credit"`
	Due         Money `json:"due"`
	Received    Money `json:"received"`
	Conferred   Money `json:"conferred"`
	Refunded    Money `json:"refunded"`
	Outstanding Money `json:"outstanding"`
}

// PaymentView segue OrderPayment. Cobranças e recebimentos vêm do módulo pagamento (etapa 08);
// estornos chegam na etapa 09. O valor só é devido depois de fechado (conclusão ou revisão da
// interrupção). "Em aberto" = devido − recebimentos principais (excesso não abate o devido).
type PaymentView struct {
	MethodChoice *string `json:"method_choice"`
	Amounts      amounts `json:"amounts"`
	Verifying    bool    `json:"verifying"`
	Charges      any     `json:"charges"`
	Receipts     any     `json:"receipts"`
	Refunds      any     `json:"refunds"`
	// OpenIssueKinds: pendências financeiras abertas desta comanda (a equipe vê na comanda).
	OpenIssueKinds []string `json:"open_issue_kinds"`
}

// AdditionView é um item pedido numa solicitação, com o preço visto pelo cliente e a quantidade.
type AdditionView struct {
	Kind      string     `json:"kind"`
	ServiceID *uuid.UUID `json:"service_id"`
	ProductID *uuid.UUID `json:"product_id"`
	Name      string     `json:"name"`
	UnitPrice Money      `json:"unit_price"`
	Quantity  int        `json:"quantity"`
}

// QuantityView é uma nova quantidade pedida para uma linha existente.
type QuantityView struct {
	ItemID   uuid.UUID `json:"item_id"`
	Quantity int       `json:"quantity"`
}

// ChangeRequestView segue ChangeRequest.
type ChangeRequestView struct {
	ID             uuid.UUID      `json:"id"`
	Status         string         `json:"status"`
	Add            []AdditionView `json:"add"`
	RemoveItemIDs  []uuid.UUID    `json:"remove_item_ids"`
	SetQuantities  []QuantityView `json:"set_quantities"`
	CreatedAt      time.Time      `json:"created_at"`
	DecidedAt      *time.Time     `json:"decided_at"`
	DecidedByRole  *string        `json:"decided_by_role"`
	DecisionReason *string        `json:"decision_reason"`
	version        int64
	orderID        uuid.UUID
}

// AdjustmentView segue OrderAdjustment: correção do dono depois de fechado o valor (T-O-11).
type AdjustmentView struct {
	ID               uuid.UUID        `json:"id"`
	Reason           string           `json:"reason"`
	PreviousTotal    Money            `json:"previous_total"`
	NewTotal         Money            `json:"new_total"`
	UnchargedItemIDs []uuid.UUID      `json:"uncharged_item_ids"`
	AddedItemIDs     []uuid.UUID      `json:"added_item_ids"`
	QuantityChanges  []quantityChange `json:"quantity_changes"`
	At               time.Time        `json:"at"`
}

// OrderView segue Order.
type OrderView struct {
	ID                   uuid.UUID           `json:"id"`
	EstablishmentID      uuid.UUID           `json:"establishment_id"`
	ParticipationID      uuid.UUID           `json:"participation_id"`
	Status               string              `json:"status"`
	Version              int64               `json:"version"`
	Items                []ItemView          `json:"items"`
	Totals               orderTotals         `json:"totals"`
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
	var method *string
	var credit int64
	err := q.QueryRow(ctx, `
		SELECT id, establishment_id, participation_id, status, version, total_cents, total_frozen_at,
		       closure_reason, interrupted_at, review_note, loyalty_eligible, created_at, closed_at, payment_method, credit_cents
		FROM service_order WHERE establishment_id = $1 AND id = $2`, estID, orderID).
		Scan(&v.ID, &v.EstablishmentID, &v.ParticipationID, &v.Status, &v.Version, &total, &v.TotalFrozenAt,
			&v.ClosureReason, &v.InterruptedAt, &v.ReviewNote, &eligible, &v.CreatedAt, &v.ClosedAt, &method, &credit)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	if err != nil {
		return v, err
	}
	v.Total = brl(total)
	due := int64(0)
	if payable(v.Status) {
		due = max(total-credit, 0)
	}
	zero := brl(0)
	v.Payment = PaymentView{MethodChoice: method, Amounts: amounts{Credit: brl(credit), Due: brl(due), Received: zero, Conferred: zero, Refunded: zero,
		Outstanding: brl(due)}, Charges: []any{}, Receipts: []any{}, Refunds: []any{}, OpenIssueKinds: []string{}}
	if paymentData != nil {
		pd, err := paymentData(ctx, q, orderID)
		if err != nil {
			return v, err
		}
		v.Payment.Charges, v.Payment.Receipts, v.Payment.Verifying = pd.Charges, pd.Receipts, pd.Verifying
		v.Payment.Refunds, v.Payment.OpenIssueKinds = pd.Refunds, pd.OpenIssueKinds
		v.Payment.Amounts.Received, v.Payment.Amounts.Conferred = brl(pd.ReceivedCents), brl(pd.ConferredCents)
		v.Payment.Amounts.Refunded = brl(pd.RefundedCents)
		v.Payment.Amounts.Outstanding = brl(max(due-pd.PrincipalCents, 0))
	}

	rows, err := q.Query(ctx, `
		SELECT id, kind, service_id, product_id, name, unit_price_cents, quantity, performed_quantity, charged_quantity,
		       reported_quantity, duration_minutes, points, status, charged, added_at, performed_at, reported_outcome, outcome_reason
		FROM order_item WHERE order_id = $1 ORDER BY added_at, name, id`, orderID)
	if err != nil {
		return v, err
	}
	frozen := v.TotalFrozenAt != nil
	v.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemView, error) {
		var it ItemView
		var price int64
		err := r.Scan(&it.ID, &it.Kind, &it.ServiceID, &it.ProductID, &it.Name, &price, &it.Quantity, &it.PerformedQuantity,
			&it.ChargedQuantity, &it.ReportedQuantity, &it.DurationMinutes, &it.Points, &it.Status, &it.Charged, &it.AddedAt,
			&it.PerformedAt, &it.ReportedOutcome, &it.OutcomeReason)
		it.UnitPrice = brl(price)
		switch {
		case it.Status == ItemRemoved:
			it.Subtotal = brl(0)
		case frozen:
			it.Subtotal = brl(price * int64(it.ChargedQuantity))
		default:
			it.Subtotal = brl(price * int64(it.Quantity))
		}
		return it, err
	})
	if err != nil {
		return v, err
	}
	if v.Items == nil {
		v.Items = []ItemView{}
	}
	for _, it := range v.Items {
		if it.Status == ItemRemoved {
			continue
		}
		n := it.Quantity
		if frozen {
			n = it.ChargedQuantity
		}
		if it.Kind == "product" {
			v.Totals.ProductUnits += n
		} else {
			v.Totals.ServiceUnits += n
			v.Totals.DurationMinutes += n * it.DurationMinutes
		}
	}
	// Pontos previstos (R-FID-03/05): só conta elegível; interrupção nunca gera pontos. Pontos
	// por unidade de serviço (pedidas enquanto aberta; cobradas depois); produto não pontua.
	if eligible {
		pts := 0
		if v.InterruptedAt == nil {
			for _, it := range v.Items {
				switch {
				case it.Kind == "product" || it.Status == ItemRemoved:
				case !frozen:
					pts += it.Points * it.Quantity
				case it.Charged:
					pts += it.Points * it.ChargedQuantity
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
		SELECT id, reason, previous_total_cents, new_total_cents, uncharged_item_ids, added_item_ids, quantity_changes, at
		FROM order_adjustment WHERE order_id = $1 ORDER BY at, id`, orderID)
	if err != nil {
		return v, err
	}
	v.Adjustments, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdjustmentView, error) {
		var a AdjustmentView
		var prev, next int64
		err := r.Scan(&a.ID, &a.Reason, &prev, &next, &a.UnchargedItemIDs, &a.AddedItemIDs, &a.QuantityChanges, &a.At)
		a.PreviousTotal, a.NewTotal = brl(prev), brl(next)
		if a.QuantityChanges == nil {
			a.QuantityChanges = []quantityChange{}
		}
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
		c.Add, c.SetQuantities = []AdditionView{}, []QuantityView{}
		return c, err
	})
	if err != nil || len(list) == 0 {
		return []ChangeRequestView{}, err
	}
	idx := map[uuid.UUID]int{}
	for i := range list {
		idx[list[i].ID] = i
	}
	rows, err = q.Query(ctx, `
		SELECT a.change_request_id, a.kind, a.service_id, a.product_id, a.name, a.unit_price_cents, a.quantity
		FROM change_request_addition a JOIN change_request c ON c.id = a.change_request_id
		WHERE c.order_id = $1 ORDER BY a.kind DESC, a.name, coalesce(a.service_id, a.product_id)`, orderID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var crID uuid.UUID
		var a AdditionView
		var price int64
		if err := rows.Scan(&crID, &a.Kind, &a.ServiceID, &a.ProductID, &a.Name, &price, &a.Quantity); err != nil {
			rows.Close()
			return nil, err
		}
		a.UnitPrice = brl(price)
		list[idx[crID]].Add = append(list[idx[crID]].Add, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `
		SELECT q.change_request_id, q.item_id, q.quantity
		FROM change_request_item_quantity q JOIN change_request c ON c.id = q.change_request_id
		WHERE c.order_id = $1 ORDER BY q.item_id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var crID uuid.UUID
		var qv QuantityView
		if err := rows.Scan(&crID, &qv.ItemID, &qv.Quantity); err != nil {
			return nil, err
		}
		list[idx[crID]].SetQuantities = append(list[idx[crID]].SetQuantities, qv)
	}
	return list, rows.Err()
}
