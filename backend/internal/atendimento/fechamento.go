package atendimento

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Resultados de item (CompleteServiceRequest, InterruptServiceRequest, CloseInterruptionRequest).
const (
	OutcomePerformed     = "performed"
	OutcomeNotPerformed  = "not_performed"
	OutcomeFully         = "fully_performed"
	OutcomePartially     = "partially_performed"
	OutcomeNotStarted    = "not_started"
	reviewOutcomeMessage = "use fully_performed, partially_performed ou not_started"
)

// ItemOutcome é o resultado de um item informado pela equipe.
type ItemOutcome struct {
	ItemID  *uuid.UUID `json:"item_id"`
	Outcome *string    `json:"outcome"`
	Reason  *string    `json:"reason,omitempty"`
}

// ValidateOutcomes confere a lista de resultados (formato): pelo menos um, sem item repetido,
// resultado conhecido; withReason exige motivo em "not_performed" (conclusão).
func ValidateOutcomes(v *validate.Collector, items *[]ItemOutcome, allowed []string, withReason bool) {
	if items == nil {
		v.Required("/items", true)
		return
	}
	if len(*items) == 0 {
		v.Add(&hs.FieldError{Field: "/items", Code: hs.FieldTooShort})
	}
	if len(*items) > maxItems {
		v.Add(&hs.FieldError{Field: "/items", Code: hs.FieldTooLong})
	}
	seen := map[uuid.UUID]bool{}
	for i, it := range *items {
		f := "/items/" + strconv.Itoa(i)
		v.Required(f+"/item_id", it.ItemID == nil)
		v.Required(f+"/outcome", it.Outcome == nil)
		if it.ItemID != nil {
			if seen[*it.ItemID] {
				v.Add(&hs.FieldError{Field: f + "/item_id", Code: "duplicate"})
			}
			seen[*it.ItemID] = true
		}
		if it.Outcome != nil && !slices.Contains(allowed, *it.Outcome) {
			v.Add(&hs.FieldError{Field: f + "/outcome", Code: hs.FieldNotAllowed})
		}
		switch {
		case withReason && it.Outcome != nil && *it.Outcome == OutcomeNotPerformed:
			if it.Reason == nil {
				v.Add(&hs.FieldError{Field: f + "/reason", Code: hs.FieldRequired, Message: "explique por que o serviço não foi feito"})
			} else {
				_, fe := validate.Text(f+"/reason", *it.Reason, 3, 500)
				v.Add(fe)
			}
		case it.Reason != nil:
			v.Add(&hs.FieldError{Field: f + "/reason", Code: hs.FieldNotAllowed, Message: "motivo só para serviço não realizado"})
		}
	}
}

// matchOutcomes confere que há exatamente um resultado para cada item não retirado.
func matchOutcomes(items []itemRow, outcomes []ItemOutcome) (map[uuid.UUID]ItemOutcome, error) {
	byItem := map[uuid.UUID]ItemOutcome{}
	live := map[uuid.UUID]bool{}
	for _, it := range items {
		live[it.ID] = true
	}
	for i, oc := range outcomes {
		if !live[*oc.ItemID] {
			return nil, hs.ErrValidation(hs.FieldError{Field: "/items/" + strconv.Itoa(i) + "/item_id", Code: hs.FieldNotAllowed,
				Message: "item não está nesta comanda"})
		}
		byItem[*oc.ItemID] = oc
	}
	for _, it := range items {
		if _, ok := byItem[it.ID]; !ok {
			return nil, hs.ErrValidation(hs.FieldError{Field: "/items", Code: hs.FieldRequired,
				Message: fmt.Sprintf("informe o resultado de %q", it.Name)})
		}
	}
	return byItem, nil
}

// Closing são os dados comuns de conclusão e interrupção (chamados pelo módulo fila, na mesma
// transação que já travou fila e participação).
type Closing struct {
	EstablishmentID uuid.UUID
	OrderID         uuid.UUID
	ExpectedVersion int64
	Items           []ItemOutcome
	Reason          string // interrupção: motivo informado pelo profissional
	Now             time.Time
}

// lockForClosing trava a comanda, confere versão e estado aberto.
func lockForClosing(ctx context.Context, tx pgx.Tx, in Closing) (orderRow, error) {
	o, err := getOrder(ctx, tx, in.EstablishmentID, in.OrderID, true)
	if err != nil {
		return o, err
	}
	if o.Version != in.ExpectedVersion {
		return o, orderConflictQ(ctx, tx, o, "version_conflict", "A comanda mudou",
			"A comanda foi alterada. Confira os serviços atualizados antes de concluir.")
	}
	if o.Status != StatusOpen {
		return o, orderConflictQ(ctx, tx, o, "invalid_transition", "Ação não permitida agora", "A comanda não está aberta.")
	}
	return o, nil
}

func orderConflictQ(ctx context.Context, q database.Querier, o orderRow, code, title, detail string) error {
	view, err := LoadOrder(ctx, q, o.EstablishmentID, o.ID)
	if err != nil {
		return err
	}
	return conflict(code, title, detail, view)
}

// Complete (T-O-04, junto com T-P-08): sem solicitação pendente; resultado de cada item; pelo
// menos um realizado. Só itens realizados são cobrados, pelo preço aceito; o total fica fechado
// (total_frozen_at) e o pagamento segue separado — a cadeira já pode atender o próximo.
func Complete(ctx context.Context, tx pgx.Tx, in Closing, audit auditoria.Event) error {
	o, err := lockForClosing(ctx, tx, in)
	if err != nil {
		return err
	}
	if pending, err := hasPending(ctx, tx, o.ID); err != nil {
		return err
	} else if pending {
		return orderConflictQ(ctx, tx, o, "change_request_pending", "Há uma solicitação pendente",
			"Aprove ou recuse a solicitação do cliente antes de concluir.")
	}
	items, err := liveItems(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	byItem, err := matchOutcomes(items, in.Items)
	if err != nil {
		return err
	}
	performed := 0
	for _, oc := range byItem {
		if *oc.Outcome == OutcomePerformed {
			performed++
		}
	}
	if performed == 0 {
		return orderConflictQ(ctx, tx, o, "performed_item_required", "Nenhum serviço realizado",
			"Para concluir, pelo menos um serviço precisa ter sido realizado. Se nada foi feito, use a interrupção.")
	}
	for id, oc := range byItem {
		if *oc.Outcome == OutcomePerformed {
			_, err = tx.Exec(ctx, `UPDATE order_item SET status = 'performed', charged = true, performed_at = coalesce(performed_at, $2)
				WHERE id = $1`, id, in.Now)
		} else {
			reason, _ := validate.Text("/reason", *oc.Reason, 3, 500)
			_, err = tx.Exec(ctx, `UPDATE order_item SET status = 'not_performed', charged = false, performed_at = NULL, outcome_reason = $2
				WHERE id = $1`, id, reason)
		}
		if err != nil {
			return err
		}
	}
	version, err := freeze(ctx, tx, o.ID, StatusAwaitingPayment, "", in.Now)
	if err != nil {
		return err
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action, audit.TargetType, audit.TargetID = &o.EstablishmentID, in.Now, "T-O-04", "order", o.ID
	audit.FromState, audit.ToState = versionTag(StatusOpen, version-1), versionTag(StatusAwaitingPayment, version)
	if err := auditoria.Record(ctx, tx, audit); err != nil {
		return err
	}
	return recordOrder(ctx, tx, o.EstablishmentID, o.ID, in.Now, evOrder, evPayment)
}

// freeze fecha o valor: total = soma dos itens cobrados, com instante (nova versão).
func freeze(ctx context.Context, q database.Querier, orderID uuid.UUID, status, closure string, now time.Time) (int64, error) {
	var version int64
	var closurePtr *string
	var closedAt *time.Time
	if status == StatusClosedNoCharge {
		closurePtr, closedAt = &closure, &now
	}
	err := q.QueryRow(ctx, `
		UPDATE service_order SET status = $2, closure_reason = $3, closed_at = $4, total_frozen_at = $5,
		       total_cents = (SELECT coalesce(sum(unit_price_cents), 0) FROM order_item WHERE order_id = $1 AND charged),
		       version = version + 1, updated_at = $5
		WHERE id = $1 RETURNING version`, orderID, status, closurePtr, closedAt, now).Scan(&version)
	return version, err
}

// Interrupt (T-O-06, junto com T-P-09): grava o que o profissional informou de cada item e deixa
// a comanda em revisão do dono. A solicitação pendente deixa de valer (T-CR-04). Sem pontos.
func Interrupt(ctx context.Context, tx pgx.Tx, in Closing, audit auditoria.Event) error {
	o, err := lockForClosing(ctx, tx, in)
	if err != nil {
		return err
	}
	items, err := liveItems(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	byItem, err := matchOutcomes(items, in.Items)
	if err != nil {
		return err
	}
	if err := voidPending(ctx, tx, o.EstablishmentID, o.ID, in.Now, audit); err != nil {
		return err
	}
	for id, oc := range byItem {
		if _, err := tx.Exec(ctx, `UPDATE order_item SET reported_outcome = $2 WHERE id = $1`, id, *oc.Outcome); err != nil {
			return err
		}
	}
	var version int64
	if err := tx.QueryRow(ctx, `
		UPDATE service_order SET status = 'pending_review', interrupted_at = $2, version = version + 1, updated_at = $2
		WHERE id = $1 RETURNING version`, o.ID, in.Now).Scan(&version); err != nil {
		return err
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action, audit.TargetType, audit.TargetID = &o.EstablishmentID, in.Now, "T-O-06", "order", o.ID
	audit.FromState, audit.ToState, audit.Reason = versionTag(StatusOpen, version-1), versionTag(StatusPendingReview, version), in.Reason
	if err := auditoria.Record(ctx, tx, audit); err != nil {
		return err
	}
	return recordOrder(ctx, tx, o.EstablishmentID, o.ID, in.Now, evOrder)
}

// closeVisit avisa o módulo identidade que a visita do visitante terminou (comanda encerrada).
func (s *Service) closeVisit(ctx context.Context, q database.Querier, o orderRow, now time.Time) error {
	if o.Identity.VisitorID == nil || s.VisitClosed == nil {
		return nil
	}
	return s.VisitClosed(ctx, q, *o.Identity.VisitorID, now)
}

// closeInterruptedOrder (T-O-07/T-O-08): o dono revisa a interrupção com nota e decide cada
// item. Só "fully_performed" é cobrado, pelo preço aceito; parcial e não iniciado valem zero.
// Soma zero encerra sem cobrança (interruption_zero_total), sem Pix nem recebimento fictício.
func (s *Service) closeInterruptedOrder(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ExpectedVersion *int64         `json:"expected_version"`
		ReviewNote      *string        `json:"review_note"`
		Items           *[]ItemOutcome `json:"items"`
	}
	var expected int64
	var note string
	return s.orderCommand(w, r, "closeInterruptedOrder", allow{owner: true}, &body,
		func(who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			v.Required("/review_note", body.ReviewNote == nil)
			if body.ReviewNote != nil {
				n, fe := validate.Text("/review_note", *body.ReviewNote, 3, 500)
				v.Add(fe)
				note = n
			}
			ValidateOutcomes(&v, body.Items, []string{OutcomeFully, OutcomePartially, OutcomeNotStarted}, false)
			return v.Err()
		},
		func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error) {
			ctx := r.Context()
			cur, err := s.lockOrder(ctx, tx, o, expected)
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Status != StatusPendingReview {
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "invalid_transition", "Ação não permitida agora",
					"Só uma comanda interrompida aguardando revisão pode ser fechada assim.")
			}
			items, err := liveItems(ctx, tx, cur.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			byItem, err := matchOutcomes(items, *body.Items)
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			for id, oc := range byItem {
				if *oc.Outcome == OutcomeFully {
					_, err = tx.Exec(ctx, `UPDATE order_item SET status = 'performed', charged = true, performed_at = coalesce(performed_at, $2) WHERE id = $1`, id, now)
				} else {
					_, err = tx.Exec(ctx, `UPDATE order_item SET status = 'not_performed', charged = false, performed_at = NULL WHERE id = $1`, id)
				}
				if err != nil {
					return idempotency.Result{}, err
				}
			}
			var charged int64
			if err := tx.QueryRow(ctx, `SELECT coalesce(sum(unit_price_cents), 0) FROM order_item WHERE order_id = $1 AND charged`, cur.ID).Scan(&charged); err != nil {
				return idempotency.Result{}, err
			}
			to, action, closure := StatusAwaitingPayment, "T-O-07", ""
			if charged == 0 {
				to, action, closure = StatusClosedNoCharge, "T-O-08", ClosedInterruptedZero
			}
			if _, err := tx.Exec(ctx, `UPDATE service_order SET review_note = $2 WHERE id = $1`, cur.ID, note); err != nil {
				return idempotency.Result{}, err
			}
			version, err := freeze(ctx, tx, cur.ID, to, closure, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &cur.EstablishmentID, now, action, "order", cur.ID
			a.FromState, a.ToState, a.Reason = versionTag(StatusPendingReview, version-1), versionTag(to, version), note
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if to == StatusClosedNoCharge {
				if err := s.closeVisit(ctx, tx, cur, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			if err := recordOrder(ctx, tx, cur.EstablishmentID, cur.ID, now, evOrder, evPayment); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusOK)
		})
}

// adjustClosedOrder (T-O-11): correção do dono, reautenticado, com motivo, só por item: retirar a
// cobrança de um item ou incluir um serviço feito e não registrado (preço atual do catálogo vira
// snapshot). Nunca valor livre nem desconto. Bloqueada com cobrança Pix ativa ou desconhecida.
//
// Recebimentos chegam nas etapas 08/09: por enquanto só existe comanda aguardando pagamento sem
// nada recebido, então a correção vale para "awaiting_payment". Corrigir comanda já recebida
// (excesso → pendência, falta → volta a aguardar) entra com os recebimentos.
func (s *Service) adjustClosedOrder(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ExpectedVersion        *int64       `json:"expected_version"`
		Reason                 *string      `json:"reason"`
		UnchargeItemIDs        *[]uuid.UUID `json:"uncharge_item_ids"`
		AddPerformedServiceIDs *[]uuid.UUID `json:"add_performed_service_ids"`
	}
	var expected int64
	var reason string
	return s.orderCommand(w, r, "adjustClosedOrder", allow{owner: true}, &body,
		func(who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			v.Required("/reason", body.Reason == nil)
			if body.Reason != nil {
				n, fe := validate.Text("/reason", *body.Reason, 3, 500)
				v.Add(fe)
				reason = n
			}
			validateServiceList(&v, "/uncharge_item_ids", body.UnchargeItemIDs, 0)
			validateServiceList(&v, "/add_performed_service_ids", body.AddPerformedServiceIDs, 0)
			if len(list(body.UnchargeItemIDs))+len(list(body.AddPerformedServiceIDs)) == 0 {
				v.Add(&hs.FieldError{Field: "/uncharge_item_ids", Code: hs.FieldRequired, Message: "retire ou inclua pelo menos um item"})
			}
			if err := v.Err(); err != nil {
				return err
			}
			return s.Auth.RequireReauth(r)
		},
		func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error) {
			ctx := r.Context()
			cur, err := s.lockOrder(ctx, tx, o, expected)
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Status != StatusAwaitingPayment {
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "invalid_transition", "Ação não permitida agora",
					"Só é possível corrigir uma comanda com valor fechado aguardando pagamento.")
			}
			if s.Charges != nil {
				state, err := s.Charges.ChargeState(ctx, tx, cur.ID)
				if err != nil {
					return idempotency.Result{}, err
				}
				switch state {
				case ChargeActive:
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "pix_charge_active", "Há uma cobrança Pix ativa",
						"Cancele a cobrança Pix (e espere a confirmação) antes de corrigir o valor.")
				case ChargeUnknown:
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "pix_charge_outcome_unknown", "Pagamento Pix em verificação",
						"O resultado de uma cobrança Pix ainda é desconhecido. Aguarde a verificação antes de corrigir.")
				}
			}
			items, err := liveItems(ctx, tx, cur.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			uncharge, addIDs := list(body.UnchargeItemIDs), list(body.AddPerformedServiceIDs)
			chargedItem, inOrder := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
			for _, it := range items {
				chargedItem[it.ID] = it.Charged
				inOrder[it.ServiceID] = true
			}
			for i, id := range uncharge {
				if !chargedItem[id] {
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/uncharge_item_ids/" + strconv.Itoa(i),
						Code: hs.FieldNotAllowed, Message: "item não é cobrado nesta comanda"})
				}
			}
			var snaps map[uuid.UUID]catalogo.Snapshot
			if len(addIDs) > 0 {
				if snaps, err = catalogo.ServicesForEntry(ctx, tx, cur.EstablishmentID, addIDs); err != nil {
					return idempotency.Result{}, err
				}
			}
			for i, id := range addIDs {
				if _, ok := snaps[id]; !ok {
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/add_performed_service_ids/" + strconv.Itoa(i),
						Code: hs.FieldNotAllowed, Message: "serviço não encontrado nesta loja"})
				}
				if inOrder[id] {
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "duplicate_service", "Serviço já está na comanda",
						"Cada serviço aparece uma vez por comanda.")
				}
			}
			now := s.Clock.Now()
			if len(uncharge) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE order_item SET charged = false WHERE order_id = $1 AND id = ANY($2)`, cur.ID, uncharge); err != nil {
					return idempotency.Result{}, err
				}
			}
			added := []uuid.UUID{}
			for _, id := range addIDs {
				itemID, err := insertItem(ctx, tx, cur.EstablishmentID, cur.ID, snaps[id], now)
				if err != nil {
					return idempotency.Result{}, err
				}
				if _, err := tx.Exec(ctx, `UPDATE order_item SET status = 'performed', charged = true, performed_at = $2 WHERE id = $1`, itemID, now); err != nil {
					return idempotency.Result{}, err
				}
				added = append(added, itemID)
			}
			if uncharge == nil {
				uncharge = []uuid.UUID{}
			}
			var newTotal int64
			if err := tx.QueryRow(ctx, `SELECT coalesce(sum(unit_price_cents), 0) FROM order_item WHERE order_id = $1 AND charged`, cur.ID).Scan(&newTotal); err != nil {
				return idempotency.Result{}, err
			}
			to, closure := StatusAwaitingPayment, ""
			if newTotal == 0 { // nada recebido e nada devido: encerra sem cobrança (R-CMD-14)
				to, closure = StatusClosedNoCharge, ClosedAdjustedZero
			}
			version, err := freeze(ctx, tx, cur.ID, to, closure, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO order_adjustment (establishment_id, order_id, reason, previous_total_cents, new_total_cents,
				                              uncharged_item_ids, added_item_ids, from_version, to_version, by_account_id, at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				cur.EstablishmentID, cur.ID, reason, cur.TotalCents, newTotal, uncharge, added, version-1, version, by.staff.AccountID, now); err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &cur.EstablishmentID, now, "T-O-11", "order", cur.ID
			a.FromState, a.ToState, a.Reason = versionTag(cur.Status, version-1), versionTag(to, version), reason
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if to == StatusClosedNoCharge {
				if err := s.closeVisit(ctx, tx, cur, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			if err := recordOrder(ctx, tx, cur.EstablishmentID, cur.ID, now, evOrder, evPayment); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusOK)
		})
}
