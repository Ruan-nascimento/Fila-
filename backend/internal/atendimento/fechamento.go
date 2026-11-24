package atendimento

import (
	"context"
	"encoding/json"
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
	OutcomePerformed    = "performed"
	OutcomeNotPerformed = "not_performed"
	OutcomeFully        = "fully_performed"
	OutcomePartially    = "partially_performed"
	OutcomeNotStarted   = "not_started"

	maxOutcomes = maxServiceLines + maxProductLines
)

// ItemOutcome é o resultado de um item informado pela equipe. Quantity (opcional) são as
// unidades INTEGRALMENTE feitas (serviço) ou entregues (produto); ausente = todas, para
// "performed"/"fully_performed", e nenhuma, para os outros resultados. Uma unidade começada e
// não terminada não conta: não há cobrança proporcional por minutos (R-CMD-13).
type ItemOutcome struct {
	ItemID   *uuid.UUID `json:"item_id"`
	Outcome  *string    `json:"outcome"`
	Quantity *int64     `json:"quantity,omitempty"`
	Reason   *string    `json:"reason,omitempty"`
}

// ValidateOutcomes confere a lista de resultados (formato): pelo menos um, sem item repetido,
// resultado conhecido, quantidade de 0 a 50; withReason permite motivo (conclusão: obrigatório
// em "not_performed" e quando nem todas as unidades foram feitas — conferido com os itens).
func ValidateOutcomes(v *validate.Collector, items *[]ItemOutcome, allowed []string, withReason bool) {
	if items == nil {
		v.Required("/items", true)
		return
	}
	if len(*items) == 0 {
		v.Add(&hs.FieldError{Field: "/items", Code: hs.FieldTooShort})
	}
	if len(*items) > maxOutcomes {
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
		if it.Quantity != nil {
			v.IntRange(f+"/quantity", it.Quantity, 0, maxLineQuantity)
		}
		switch {
		case it.Reason != nil && !withReason:
			v.Add(&hs.FieldError{Field: f + "/reason", Code: hs.FieldNotAllowed, Message: "motivo só na conclusão"})
		case it.Reason != nil:
			_, fe := validate.Text(f+"/reason", *it.Reason, 3, 500)
			v.Add(fe)
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

func outcomeField(outcomes []ItemOutcome, id uuid.UUID, sub string) string {
	for i, oc := range outcomes {
		if *oc.ItemID == id {
			return "/items/" + strconv.Itoa(i) + sub
		}
	}
	return "/items"
}

// units resolve as unidades informadas: ausente = all (todas) ou none (nenhuma) conforme o resultado.
func units(oc ItemOutcome, all, none int) int {
	if oc.Quantity != nil {
		return int(*oc.Quantity)
	}
	if *oc.Outcome == OutcomePerformed || *oc.Outcome == OutcomeFully {
		return all
	}
	return none
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

func unitWord(it itemRow) string {
	if it.Kind == catalogo.KindProduct {
		return "entregue(s)"
	}
	return "realizada(s)"
}

// Complete (T-O-04, junto com T-P-08): sem solicitação pendente; resultado de cada item; pelo
// menos uma unidade de serviço realizada. Cobra-se só o que foi feito (unidades inteiras), pelo
// preço aceito; o total fica fechado e o pagamento segue separado — a cadeira já pode atender o
// próximo. Unidade já registrada como feita não pode ser declarada não feita; quantidade menor
// que a pedida exige motivo.
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
	serviceUnits := 0
	done := map[uuid.UUID]int{}
	for _, it := range items {
		oc := byItem[it.ID]
		n := units(oc, it.Quantity, 0)
		switch {
		case *oc.Outcome == OutcomeNotPerformed && it.PerformedQty > 0:
			return orderConflictQ(ctx, tx, o, "item_already_performed", "Unidades já realizadas",
				fmt.Sprintf("%q já tem %d unidade(s) %s: informe como realizado, com a quantidade.", it.Name, it.PerformedQty, unitWord(it)))
		case *oc.Outcome == OutcomeNotPerformed && n != 0:
			return hs.ErrValidation(hs.FieldError{Field: outcomeField(in.Items, it.ID, "/quantity"), Code: hs.FieldNotAllowed,
				Message: "item não realizado não tem unidades"})
		case *oc.Outcome == OutcomeNotPerformed && oc.Reason == nil:
			return hs.ErrValidation(hs.FieldError{Field: outcomeField(in.Items, it.ID, "/reason"), Code: hs.FieldRequired,
				Message: "explique por que o item não foi feito"})
		case *oc.Outcome == OutcomePerformed && (n < 1 || n > it.Quantity):
			return hs.ErrValidation(hs.FieldError{Field: outcomeField(in.Items, it.ID, "/quantity"), Code: hs.FieldOutOfRange,
				Message: fmt.Sprintf("informe de 1 a %d unidade(s) de %q", it.Quantity, it.Name)})
		case *oc.Outcome == OutcomePerformed && n < it.PerformedQty:
			return orderConflictQ(ctx, tx, o, "item_already_performed", "Unidades já realizadas",
				fmt.Sprintf("%q já tem %d unidade(s) %s; a conclusão não pode registrar menos.", it.Name, it.PerformedQty, unitWord(it)))
		case *oc.Outcome == OutcomePerformed && n < it.Quantity && oc.Reason == nil:
			return hs.ErrValidation(hs.FieldError{Field: outcomeField(in.Items, it.ID, "/reason"), Code: hs.FieldRequired,
				Message: fmt.Sprintf("explique por que só %d de %d unidade(s) de %q foram feitas", n, it.Quantity, it.Name)})
		}
		done[it.ID] = n
		if it.Kind == catalogo.KindService {
			serviceUnits += n
		}
	}
	if serviceUnits == 0 {
		return orderConflictQ(ctx, tx, o, "performed_item_required", "Nenhum serviço realizado",
			"Para concluir, pelo menos um serviço precisa ter sido realizado. Se nada foi feito, use a interrupção.")
	}
	var evs []qtyEvent
	for _, it := range items {
		oc, n := byItem[it.ID], done[it.ID]
		var reason *string
		if oc.Reason != nil {
			r, _ := validate.Text("/reason", *oc.Reason, 3, 500)
			reason = &r
		}
		if err := settleItem(ctx, tx, it, n, reason, in.Now); err != nil {
			return err
		}
		evs = append(evs, qtyEvent{it.ID, "performed_quantity", it.PerformedQty, n}, qtyEvent{it.ID, "charged_quantity", it.ChargedQty, n})
	}
	version, err := freeze(ctx, tx, o.ID, StatusAwaitingPayment, "", in.Now)
	if err != nil {
		return err
	}
	if err := recordQuantities(ctx, tx, o.EstablishmentID, o.ID, evs, "completion", audit, version, in.Now); err != nil {
		return err
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action, audit.TargetType, audit.TargetID = &o.EstablishmentID, in.Now, "T-O-04", "order", o.ID
	audit.FromState, audit.ToState = versionTag(StatusOpen, version-1), versionTag(StatusAwaitingPayment, version)
	if err := auditoria.Record(ctx, tx, audit); err != nil {
		return err
	}
	return recordOrder(ctx, tx, o.EstablishmentID, o.ID, in.Now, evOrder, evPayment)
}

// settleItem fecha uma linha: n unidades feitas e cobradas; zero = não realizada.
func settleItem(ctx context.Context, q database.Querier, it itemRow, n int, reason *string, now time.Time) error {
	var err error
	if n > 0 {
		_, err = q.Exec(ctx, `UPDATE order_item SET status = 'performed', performed_quantity = $2, charged_quantity = $2, charged = true,
			performed_at = coalesce(performed_at, $3), outcome_reason = $4 WHERE id = $1`, it.ID, n, now, reason)
	} else {
		_, err = q.Exec(ctx, `UPDATE order_item SET status = 'not_performed', performed_quantity = 0, charged_quantity = 0, charged = false,
			performed_at = NULL, outcome_reason = coalesce($2, outcome_reason) WHERE id = $1`, it.ID, reason)
	}
	return err
}

// freeze fecha o valor: total = Σ preço × unidades cobradas, com instante (nova versão).
func freeze(ctx context.Context, q database.Querier, orderID uuid.UUID, status, closure string, now time.Time) (int64, error) {
	var version int64
	var closurePtr *string
	var closedAt *time.Time
	if status == StatusClosedNoCharge {
		closurePtr, closedAt = &closure, &now
	}
	err := q.QueryRow(ctx, `
		UPDATE service_order SET status = $2, closure_reason = $3, closed_at = $4, total_frozen_at = $5,
		       total_cents = (SELECT coalesce(sum(unit_price_cents * charged_quantity), 0) FROM order_item WHERE order_id = $1 AND charged),
		       version = version + 1, updated_at = $5
		WHERE id = $1 RETURNING version`, orderID, status, closurePtr, closedAt, now).Scan(&version)
	return version, err
}

// Interrupt (T-O-06, junto com T-P-09): grava o que o profissional informou de cada item
// (resultado e unidades inteiras feitas) e deixa a comanda em revisão do dono. A solicitação
// pendente deixa de valer (T-CR-04). Sem pontos.
//
//	fully_performed     = todas as unidades feitas;
//	partially_performed = `quantity` unidades feitas e a seguinte começada sem terminar;
//	not_started         = `quantity` unidades feitas e as restantes nem começaram.
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
	reported := map[uuid.UUID]int{}
	for _, it := range items {
		oc := byItem[it.ID]
		n := units(oc, it.Quantity, it.PerformedQty)
		switch {
		case *oc.Outcome == OutcomeFully && n != it.Quantity:
			return hs.ErrValidation(hs.FieldError{Field: outcomeField(in.Items, it.ID, "/quantity"), Code: hs.FieldOutOfRange,
				Message: fmt.Sprintf("feito por inteiro = as %d unidade(s) de %q", it.Quantity, it.Name)})
		case *oc.Outcome != OutcomeFully && n >= it.Quantity:
			return hs.ErrValidation(hs.FieldError{Field: outcomeField(in.Items, it.ID, "/quantity"), Code: hs.FieldOutOfRange,
				Message: fmt.Sprintf("%q tem %d unidade(s): informe quantas foram feitas por inteiro (menos que o total)", it.Name, it.Quantity)})
		case n < it.PerformedQty:
			return orderConflictQ(ctx, tx, o, "item_already_performed", "Unidades já realizadas",
				fmt.Sprintf("%q já tem %d unidade(s) %s; informe pelo menos isso.", it.Name, it.PerformedQty, unitWord(it)))
		}
		reported[it.ID] = n
	}
	if err := voidPending(ctx, tx, o.EstablishmentID, o.ID, in.Now, audit); err != nil {
		return err
	}
	var evs []qtyEvent
	for _, it := range items {
		n := reported[it.ID]
		if _, err := tx.Exec(ctx, `
			UPDATE order_item SET reported_outcome = $2, reported_quantity = $3, performed_quantity = $3,
			       status = CASE WHEN $3 = quantity THEN 'performed' ELSE status END,
			       performed_at = CASE WHEN $3 = quantity THEN coalesce(performed_at, $4) ELSE performed_at END
			WHERE id = $1`, it.ID, *byItem[it.ID].Outcome, n, in.Now); err != nil {
			return err
		}
		evs = append(evs, qtyEvent{it.ID, "performed_quantity", it.PerformedQty, n})
	}
	var version int64
	if err := tx.QueryRow(ctx, `
		UPDATE service_order SET status = 'pending_review', interrupted_at = $2, version = version + 1, updated_at = $2
		WHERE id = $1 RETURNING version`, o.ID, in.Now).Scan(&version); err != nil {
		return err
	}
	if err := recordQuantities(ctx, tx, o.EstablishmentID, o.ID, evs, "interruption", audit, version, in.Now); err != nil {
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

// closeInterruptedOrder (T-O-07/T-O-08): o dono revisa a interrupção com nota e decide, item a
// item, quantas unidades foram INTEGRALMENTE feitas (serviços) ou entregues (produtos). Só essas
// são cobradas, pelo preço aceito; a unidade parcial e as não iniciadas valem zero. Sem
// `quantity`: "fully_performed" = todas, os outros = nenhuma (regra anterior, quantidade 1).
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
			var evs []qtyEvent
			var charged int64
			for _, it := range items {
				oc := byItem[it.ID]
				n := units(oc, it.Quantity, 0)
				switch {
				case *oc.Outcome == OutcomeFully && n != it.Quantity:
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: outcomeField(*body.Items, it.ID, "/quantity"),
						Code: hs.FieldOutOfRange, Message: fmt.Sprintf("feito por inteiro = as %d unidade(s) de %q", it.Quantity, it.Name)})
				case *oc.Outcome != OutcomeFully && n >= it.Quantity:
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: outcomeField(*body.Items, it.ID, "/quantity"),
						Code: hs.FieldOutOfRange, Message: fmt.Sprintf("%q tem %d unidade(s): informe quantas foram feitas por inteiro", it.Name, it.Quantity)})
				}
				if err := settleItem(ctx, tx, it, n, nil, now); err != nil {
					return idempotency.Result{}, err
				}
				charged += it.PriceCents * int64(n)
				evs = append(evs, qtyEvent{it.ID, "performed_quantity", it.PerformedQty, n}, qtyEvent{it.ID, "charged_quantity", it.ChargedQty, n})
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
			if err := recordQuantities(ctx, tx, cur.EstablishmentID, cur.ID, evs, "review", a, version, now); err != nil {
				return idempotency.Result{}, err
			}
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

// quantityChange é uma mudança de unidades cobradas registrada na correção (order_adjustment).
type quantityChange struct {
	ItemID uuid.UUID `json:"item_id"`
	From   int       `json:"from"`
	To     int       `json:"to"`
}

// adjustClosedOrder (T-O-11): correção do dono, reautenticado, com motivo, só por item: retirar a
// cobrança de uma linha, mudar as unidades cobradas de uma linha ou incluir um item feito e não
// registrado (preço atual do catálogo vira snapshot). Nunca valor livre nem desconto. Bloqueada
// com cobrança Pix ativa ou desconhecida: uma cobrança antiga nunca vale por um total novo.
func (s *Service) adjustClosedOrder(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ExpectedVersion        *int64             `json:"expected_version"`
		Reason                 *string            `json:"reason"`
		UnchargeItemIDs        *[]uuid.UUID       `json:"uncharge_item_ids"`
		AddPerformedServiceIDs *[]uuid.UUID       `json:"add_performed_service_ids"`
		SetChargedQuantities   *[]quantityRequest `json:"set_charged_quantities"`
		AddPerformedItems      *[]LineRequest     `json:"add_performed_items"`
	}
	var expected int64
	var reason string
	var setQty []quantityWant
	var addLines []LineWant
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
			switch {
			case body.AddPerformedServiceIDs != nil && body.AddPerformedItems != nil:
				v.Add(&hs.FieldError{Field: "/add_performed_items", Code: hs.FieldNotAllowed,
					Message: "envie add_performed_items OU add_performed_service_ids"})
			case body.AddPerformedItems != nil:
				addLines = ParseLines(&v, "/add_performed_items", body.AddPerformedItems, 0)
			default:
				validateServiceList(&v, "/add_performed_service_ids", body.AddPerformedServiceIDs, 0)
				addLines = ServiceLines(list(body.AddPerformedServiceIDs), "/add_performed_service_ids")
			}
			if body.SetChargedQuantities != nil {
				setQty = parseChargedQuantities(&v, body.SetChargedQuantities)
			}
			if len(list(body.UnchargeItemIDs))+len(addLines)+len(setQty) == 0 {
				v.Add(&hs.FieldError{Field: "/uncharge_item_ids", Code: hs.FieldRequired, Message: "retire, ajuste ou inclua pelo menos um item"})
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
			uncharge := list(body.UnchargeItemIDs)
			byID, inOrder := map[uuid.UUID]itemRow{}, map[lineKey]bool{}
			for _, it := range items {
				byID[it.ID] = it
				inOrder[it.key()] = true
			}
			for i, id := range uncharge {
				if !byID[id].Charged {
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/uncharge_item_ids/" + strconv.Itoa(i),
						Code: hs.FieldNotAllowed, Message: "item não é cobrado nesta comanda"})
				}
			}
			for _, q := range setQty {
				if _, ok := byID[q.ItemID]; !ok || slices.Contains(uncharge, q.ItemID) {
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: q.field + "/item_id", Code: hs.FieldNotAllowed,
						Message: "item não está nesta comanda"})
				}
			}
			var snaps map[lineKey]catalogo.Snapshot
			if len(addLines) > 0 {
				keys := make([]lineKey, len(addLines))
				for i, l := range addLines {
					keys[i] = l.Key
				}
				if snaps, err = snapshotsFor(ctx, tx, cur.EstablishmentID, keys); err != nil {
					return idempotency.Result{}, err
				}
			}
			for _, l := range addLines {
				if _, ok := snaps[l.Key]; !ok {
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: l.field, Code: hs.FieldNotAllowed,
						Message: "item não encontrado nesta loja"})
				}
				if inOrder[l.Key] {
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "duplicate_service", "Item já está na comanda",
						"Cada item aparece uma vez por comanda: ajuste as unidades cobradas da linha existente.")
				}
			}
			now := s.Clock.Now()
			var evs []qtyEvent
			changes := []quantityChange{}
			for _, id := range uncharge {
				it := byID[id]
				if _, err := tx.Exec(ctx, `UPDATE order_item SET charged = false, charged_quantity = 0 WHERE id = $1`, id); err != nil {
					return idempotency.Result{}, err
				}
				evs = append(evs, qtyEvent{id, "charged_quantity", it.ChargedQty, 0})
			}
			for _, q := range setQty {
				it := byID[q.ItemID]
				if q.Quantity == it.ChargedQty {
					continue
				}
				if _, err := tx.Exec(ctx, `
					UPDATE order_item SET quantity = greatest(quantity, $2), performed_quantity = greatest(performed_quantity, $2),
					       charged_quantity = $2, charged = $2 > 0,
					       status = CASE WHEN greatest(performed_quantity, $2) > 0 THEN 'performed' ELSE status END,
					       performed_at = CASE WHEN greatest(performed_quantity, $2) > 0 THEN coalesce(performed_at, $3) ELSE performed_at END
					WHERE id = $1`, it.ID, q.Quantity, now); err != nil {
					return idempotency.Result{}, err
				}
				if q.Quantity > it.Quantity {
					evs = append(evs, qtyEvent{it.ID, "quantity", it.Quantity, q.Quantity})
				}
				if q.Quantity > it.PerformedQty {
					evs = append(evs, qtyEvent{it.ID, "performed_quantity", it.PerformedQty, q.Quantity})
				}
				evs = append(evs, qtyEvent{it.ID, "charged_quantity", it.ChargedQty, q.Quantity})
				changes = append(changes, quantityChange{it.ID, it.ChargedQty, q.Quantity})
			}
			added := []uuid.UUID{}
			for _, l := range addLines {
				itemID, err := insertItem(ctx, tx, cur.EstablishmentID, cur.ID, snaps[l.Key], l.Quantity, now)
				if err != nil {
					return idempotency.Result{}, err
				}
				if _, err := tx.Exec(ctx, `UPDATE order_item SET status = 'performed', performed_quantity = quantity, charged_quantity = quantity,
					charged = true, performed_at = $2 WHERE id = $1`, itemID, now); err != nil {
					return idempotency.Result{}, err
				}
				added = append(added, itemID)
			}
			if uncharge == nil {
				uncharge = []uuid.UUID{}
			}
			var newTotal int64
			if err := tx.QueryRow(ctx, `SELECT coalesce(sum(unit_price_cents * charged_quantity), 0) FROM order_item WHERE order_id = $1 AND charged`,
				cur.ID).Scan(&newTotal); err != nil {
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
			changesJSON, _ := json.Marshal(changes)
			if _, err := tx.Exec(ctx, `
				INSERT INTO order_adjustment (establishment_id, order_id, reason, previous_total_cents, new_total_cents,
				                              uncharged_item_ids, added_item_ids, quantity_changes, from_version, to_version, by_account_id, at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
				cur.EstablishmentID, cur.ID, reason, cur.TotalCents, newTotal, uncharge, added, changesJSON, version-1, version,
				by.staff.AccountID, now); err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			if err := recordQuantities(ctx, tx, cur.EstablishmentID, cur.ID, evs, "adjustment", a, version, now); err != nil {
				return idempotency.Result{}, err
			}
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

// parseChargedQuantities valida as novas unidades cobradas (0 a 50; 0 = não cobrar a linha).
func parseChargedQuantities(v *validate.Collector, p *[]quantityRequest) []quantityWant {
	if len(*p) > maxOutcomes {
		v.Add(&hs.FieldError{Field: "/set_charged_quantities", Code: hs.FieldTooLong})
	}
	seen := map[uuid.UUID]bool{}
	var out []quantityWant
	for i, q := range *p {
		f := "/set_charged_quantities/" + strconv.Itoa(i)
		v.Required(f+"/item_id", q.ItemID == nil)
		v.Required(f+"/quantity", q.Quantity == nil)
		if q.ItemID == nil || q.Quantity == nil {
			continue
		}
		n := v.IntRange(f+"/quantity", q.Quantity, 0, maxLineQuantity)
		if seen[*q.ItemID] {
			v.Add(&hs.FieldError{Field: f + "/item_id", Code: "duplicate"})
		}
		seen[*q.ItemID] = true
		out = append(out, quantityWant{ItemID: *q.ItemID, Quantity: int(n), field: f})
	}
	return out
}
