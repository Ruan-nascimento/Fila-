package atendimento

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

type replaceRequest struct {
	ExpectedVersion *int64         `json:"expected_version"`
	ServiceIDs      *[]uuid.UUID   `json:"service_ids"`
	Items           *[]LineRequest `json:"items"`
	Reason          *string        `json:"reason"`
}

// validateServiceList confere 1–10 serviços distintos (R-CMD-04/06).
func validateServiceList(v *validate.Collector, field string, ids *[]uuid.UUID, minItems int) {
	if ids == nil {
		if minItems > 0 {
			v.Required(field, true)
		}
		return
	}
	switch {
	case len(*ids) < minItems:
		v.Add(&hs.FieldError{Field: field, Code: hs.FieldTooShort, Message: "escolha pelo menos um serviço"})
	case len(*ids) > maxServiceLines:
		v.Add(&hs.FieldError{Field: field, Code: hs.FieldTooLong})
	}
	for i, id := range *ids {
		if slices.Index(*ids, id) != i {
			v.Add(&hs.FieldError{Field: field + "/" + strconv.Itoa(i), Code: "duplicate"})
		}
	}
}

// RequestedLines lê o pedido em um dos dois formatos: `items` (com quantidade, serviços e
// produtos) ou `service_ids` (formato anterior: quantidade 1 cada). Os dois juntos são recusados.
func RequestedLines(v *validate.Collector, serviceIDs *[]uuid.UUID, items *[]LineRequest, minServices int) []LineWant {
	switch {
	case serviceIDs != nil && items != nil:
		v.Add(&hs.FieldError{Field: "/items", Code: hs.FieldNotAllowed, Message: "envie items OU service_ids"})
		return nil
	case items != nil:
		return ParseLines(v, "/items", items, minServices)
	case serviceIDs == nil:
		v.Required("/items", minServices > 0)
		return nil
	default:
		validateServiceList(v, "/service_ids", serviceIDs, minServices)
		return ServiceLines(*serviceIDs, "/service_ids")
	}
}

// reasonFor valida o motivo; obrigatório quando quem age é a equipe (T-O-03, T-CR-03).
func reasonFor(v *validate.Collector, by who, reason *string, field string) string {
	if reason == nil {
		v.Required(field, by.isStaff)
		return ""
	}
	s, fe := validate.Text(field, *reason, 3, 500)
	v.Add(fe)
	return s
}

// hasPending informa se a comanda tem solicitação pendente.
func hasPending(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM change_request WHERE order_id = $1 AND status = 'pending')`, orderID).Scan(&ok)
	return ok, err
}

// withOrder anexa a comanda atual (current) a um conflito que ainda não a tem.
func (s *Service) withOrder(ctx context.Context, q database.Querier, o orderRow, err error) error {
	var he *hs.Error
	if errors.As(err, &he) && he.Status == http.StatusConflict && he.Current == nil {
		if view, verr := LoadOrder(ctx, q, o.EstablishmentID, o.ID); verr == nil {
			he.Current = view
		}
	}
	return err
}

// replaceOrderItems define os itens da comanda aberta, com quantidade absoluta por item.
//   - Cliente (T-O-02): só enquanto aguarda; depois da chamada, faz uma solicitação.
//   - Equipe (T-O-03): aguardando, chamado ou em atendimento, com motivo, sem solicitação pendente.
//
// Linhas mantidas preservam o snapshot (também ao aumentar a quantidade); retiradas viram
// "removed" (nada é apagado); novas copiam o catálogo atual. Unidade realizada não sai. Total e
// estimativa recalculados no servidor; histórico das quantidades gravado.
func (s *Service) replaceOrderItems(w http.ResponseWriter, r *http.Request) error {
	var body replaceRequest
	var reason string
	var expected int64
	var wants []LineWant
	return s.orderCommand(w, r, "replaceOrderItems", allow{customer: true, professional: true, owner: true}, &body,
		func(by who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			wants = RequestedLines(&v, body.ServiceIDs, body.Items, 1)
			reason = reasonFor(&v, by, body.Reason, "/reason")
			return v.Err()
		},
		func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error) {
			ctx := r.Context()
			cur, visit, err := s.lockWithVisit(ctx, tx, o, expected)
			if err != nil {
				return idempotency.Result{}, err
			}
			editable := cur.Status == StatusOpen && (visit.Status == visitWaiting ||
				(by.isStaff && (visit.Status == visitCalled || visit.Status == visitInService)))
			if !editable {
				detail := "Esta comanda não pode mais ser alterada."
				if by.customer && cur.Status == StatusOpen && visit.Status != visitWaiting {
					detail = "Depois da chamada, peça a alteração ao profissional (solicitação)."
				}
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "order_not_editable", "Comanda não editável", detail)
			}
			if pending, err := hasPending(ctx, tx, cur.ID); err != nil {
				return idempotency.Result{}, err
			} else if pending {
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "change_request_pending", "Há uma solicitação pendente",
					"Aprove ou recuse a solicitação do cliente antes de mudar os serviços.")
			}
			items, err := liveItems(ctx, tx, cur.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			st, err := estabelecimento.LoadSettings(ctx, tx, cur.EstablishmentID)
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			evs, changed, err := planItems(ctx, tx, cur, items, wants, "", st, now)
			if err != nil {
				return idempotency.Result{}, s.withOrder(ctx, tx, cur, err)
			}
			if !changed {
				return s.done(ctx, tx, cur, http.StatusOK) // mesmo conjunto: nada muda
			}
			version, err := bumpOpenTotal(ctx, tx, cur.ID, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			action, source := "T-O-02", "customer"
			if by.isStaff {
				action, source = "T-O-03", "staff"
			}
			a := by.audit(ctx, cur)
			if err := recordQuantities(ctx, tx, cur.EstablishmentID, cur.ID, evs, source, a, version, now); err != nil {
				return idempotency.Result{}, err
			}
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &cur.EstablishmentID, now, action, "order", cur.ID
			a.FromState, a.ToState, a.Reason = versionTag(StatusOpen, version-1), versionTag(StatusOpen, version), reason
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if err := recordOrder(ctx, tx, cur.EstablishmentID, cur.ID, now, evOrder); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.Visits.QueueChanged(ctx, tx, cur.EstablishmentID, visit.QueueID); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusOK)
		})
}

type versionOnly struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

// markItemPerformed (T-O-13): durante o atendimento, a equipe marca TODAS as unidades da linha
// como integralmente realizadas (serviço) ou entregues (produto); daí em diante o cliente não
// pode retirá-la (R-CMD-09). Para registrar parte ("2 de 3"), use setItemPerformedQuantity.
func (s *Service) markItemPerformed(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	return s.performedCommand(w, r, "markItemPerformed", "T-O-13", &body, func(v *validate.Collector) (int64, *int) {
		return v.Version("/expected_version", body.ExpectedVersion), nil
	})
}

type performedRequest struct {
	ExpectedVersion   *int64 `json:"expected_version"`
	PerformedQuantity *int64 `json:"performed_quantity"`
}

// setItemPerformedQuantity (T-O-14): registra quantas unidades da linha já foram integralmente
// realizadas (ou entregues, para produto), em valor ABSOLUTO com versão esperada — repetir não
// soma. Só cresce: corrigir para menos é decisão do dono na revisão (interrupção) ou na correção.
func (s *Service) setItemPerformedQuantity(w http.ResponseWriter, r *http.Request) error {
	var body performedRequest
	return s.performedCommand(w, r, "setItemPerformedQuantity", "T-O-14", &body, func(v *validate.Collector) (int64, *int) {
		expected := v.Version("/expected_version", body.ExpectedVersion)
		v.Required("/performed_quantity", body.PerformedQuantity == nil)
		if body.PerformedQuantity == nil {
			return expected, nil
		}
		n := int(v.IntRange("/performed_quantity", body.PerformedQuantity, 1, maxLineQuantity))
		return expected, &n
	})
}

// performedCommand é a parte comum de T-O-13/T-O-14. parse devolve a versão esperada e a
// quantidade pedida (nil = todas as unidades da linha).
func (s *Service) performedCommand(w http.ResponseWriter, r *http.Request, op, action string, body any,
	parse func(v *validate.Collector) (int64, *int)) error {
	itemID, err := pathID(r, "itemId")
	if err != nil {
		return err
	}
	var expected int64
	var want *int
	return s.orderCommand(w, r, op, allow{professional: true, owner: true}, body,
		func(who) error {
			var v validate.Collector
			expected, want = parse(&v)
			return v.Err()
		},
		func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error) {
			ctx := r.Context()
			cur, visit, err := s.lockWithVisit(ctx, tx, o, expected)
			if err != nil {
				return idempotency.Result{}, err
			}
			var it itemRow
			err = tx.QueryRow(ctx, `SELECT id, kind, name, status, quantity, performed_quantity FROM order_item
				WHERE order_id = $1 AND id = $2 AND status <> 'removed'`, cur.ID, itemID).
				Scan(&it.ID, &it.Kind, &it.Name, &it.Status, &it.Quantity, &it.PerformedQty)
			if errors.Is(err, pgx.ErrNoRows) {
				return idempotency.Result{}, hs.ErrNotFound()
			}
			if err != nil {
				return idempotency.Result{}, err
			}
			target := it.Quantity
			if want != nil {
				target = *want
			}
			unit := "realizado"
			if it.Kind == catalogo.KindProduct {
				unit = "entregue"
			}
			switch {
			case cur.Status != StatusOpen || visit.Status != visitInService:
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "invalid_transition", "Ação não permitida agora",
					"Só é possível registrar o que foi "+unit+" durante o atendimento.")
			case it.PerformedQty == it.Quantity:
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "invalid_transition", "Ação não permitida agora",
					fmt.Sprintf("%q já está todo marcado como %s.", it.Name, unit))
			case target > it.Quantity:
				return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/performed_quantity", Code: hs.FieldOutOfRange,
					Message: fmt.Sprintf("o pedido tem %d unidade(s) de %q", it.Quantity, it.Name)})
			case target < it.PerformedQty:
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "item_already_performed", "Unidades já registradas",
					fmt.Sprintf("%q já tem %d unidade(s) registradas como %s; o dono corrige na revisão, se preciso.",
						it.Name, it.PerformedQty, unit))
			case target == it.PerformedQty:
				return s.done(ctx, tx, cur, http.StatusOK) // nada muda
			}
			now := s.Clock.Now()
			if _, err := tx.Exec(ctx, `
				UPDATE order_item SET performed_quantity = $2,
				       status = CASE WHEN $2 = quantity THEN 'performed' ELSE status END,
				       performed_at = CASE WHEN $2 = quantity THEN $3 ELSE performed_at END
				WHERE id = $1`, itemID, target, now); err != nil {
				return idempotency.Result{}, err
			}
			version, err := bumpVersion(ctx, tx, cur.ID, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			if err := recordQuantities(ctx, tx, cur.EstablishmentID, cur.ID,
				[]qtyEvent{{itemID, "performed_quantity", it.PerformedQty, target}}, "staff", a, version, now); err != nil {
				return idempotency.Result{}, err
			}
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &cur.EstablishmentID, now, action, "order_item", itemID
			a.FromState = strconv.Itoa(it.PerformedQty) + "/" + strconv.Itoa(it.Quantity) + "@" + strconv.FormatInt(version-1, 10)
			a.ToState = strconv.Itoa(target) + "/" + strconv.Itoa(it.Quantity) + "@" + strconv.FormatInt(version, 10)
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if err := recordOrder(ctx, tx, cur.EstablishmentID, cur.ID, now, evOrder); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusOK)
		})
}
