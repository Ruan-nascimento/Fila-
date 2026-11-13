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
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

type replaceRequest struct {
	ExpectedVersion *int64       `json:"expected_version"`
	ServiceIDs      *[]uuid.UUID `json:"service_ids"`
	Reason          *string      `json:"reason"`
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
	case len(*ids) > maxItems:
		v.Add(&hs.FieldError{Field: field, Code: hs.FieldTooLong})
	}
	for i, id := range *ids {
		if slices.Index(*ids, id) != i {
			v.Add(&hs.FieldError{Field: field + "/" + strconv.Itoa(i), Code: "duplicate"})
		}
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

// snapshotsToAdd lê do catálogo (FOR SHARE) os serviços a incluir e confere que existem nesta
// loja, estão ativos (R-CMD-03) e são feitos pelo profissional do atendimento.
func snapshotsToAdd(ctx context.Context, tx pgx.Tx, o orderRow, ids []uuid.UUID, field func(uuid.UUID) string) ([]catalogo.Snapshot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	snaps, err := catalogo.ServicesForEntry(ctx, tx, o.EstablishmentID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]catalogo.Snapshot, 0, len(ids))
	for _, id := range ids {
		sn, ok := snaps[id]
		switch {
		case !ok:
			return nil, hs.ErrValidation(hs.FieldError{Field: field(id), Code: hs.FieldNotAllowed, Message: "serviço não encontrado nesta loja"})
		case !sn.Active:
			return nil, conflict("service_inactive", "Serviço indisponível",
				fmt.Sprintf("O serviço %q não está disponível no momento.", sn.Name), nil)
		case !sn.Enabled(o.ProfessionalID):
			return nil, conflict("service_not_eligible", "Serviço não feito por este profissional",
				fmt.Sprintf("O profissional deste atendimento não faz o serviço %q.", sn.Name), nil)
		}
		out = append(out, sn)
	}
	return out, nil
}

// hasPending informa se a comanda tem solicitação pendente.
func hasPending(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM change_request WHERE order_id = $1 AND status = 'pending')`, orderID).Scan(&ok)
	return ok, err
}

// replaceOrderItems define o conjunto de serviços da comanda aberta.
//   - Cliente (T-O-02): só enquanto aguarda; depois da chamada, faz uma solicitação.
//   - Equipe (T-O-03): aguardando, chamado ou em atendimento, com motivo, sem solicitação pendente.
//
// Itens mantidos preservam o snapshot; retirados viram "removed" (nada é apagado); novos copiam o
// catálogo atual. Item realizado não sai. Total recalculado no servidor.
func (s *Service) replaceOrderItems(w http.ResponseWriter, r *http.Request) error {
	var body replaceRequest
	var reason string
	var expected int64
	return s.orderCommand(w, r, "replaceOrderItems", allow{customer: true, professional: true, owner: true}, &body,
		func(by who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			validateServiceList(&v, "/service_ids", body.ServiceIDs, 1)
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
			want := *body.ServiceIDs
			var remove []uuid.UUID
			have := map[uuid.UUID]bool{}
			for _, it := range items {
				have[it.ServiceID] = true
				if slices.Contains(want, it.ServiceID) {
					continue
				}
				if it.Status == ItemPerformed {
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "item_already_performed", "Serviço já realizado",
						fmt.Sprintf("%q já foi realizado e não pode ser retirado.", it.Name))
				}
				remove = append(remove, it.ID)
			}
			var add []uuid.UUID
			for _, id := range want {
				if !have[id] {
					add = append(add, id)
				}
			}
			if len(add) == 0 && len(remove) == 0 {
				return s.done(ctx, tx, cur, http.StatusOK) // mesmo conjunto: nada muda
			}
			snaps, err := snapshotsToAdd(ctx, tx, cur, add, func(id uuid.UUID) string {
				return "/service_ids/" + strconv.Itoa(slices.Index(want, id))
			})
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			if len(remove) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE order_item SET status = 'removed', removed_at = $2 WHERE id = ANY($1)`, remove, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			for _, sn := range snaps {
				if _, err := insertItem(ctx, tx, cur.EstablishmentID, cur.ID, sn, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			version, err := bumpOpenTotal(ctx, tx, cur.ID, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			action := "T-O-02"
			if by.isStaff {
				action = "T-O-03"
			}
			a := by.audit(ctx, cur)
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

// markItemPerformed (T-O-13): durante o atendimento, a equipe marca um item como integralmente
// realizado; a partir daí o cliente não pode retirá-lo (R-CMD-09).
func (s *Service) markItemPerformed(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	var expected int64
	itemID, err := pathID(r, "itemId")
	if err != nil {
		return err
	}
	return s.orderCommand(w, r, "markItemPerformed", allow{professional: true, owner: true}, &body,
		func(who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			return v.Err()
		},
		func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error) {
			ctx := r.Context()
			cur, visit, err := s.lockWithVisit(ctx, tx, o, expected)
			if err != nil {
				return idempotency.Result{}, err
			}
			var status, name string
			err = tx.QueryRow(ctx, `SELECT status, name FROM order_item WHERE order_id = $1 AND id = $2`, cur.ID, itemID).Scan(&status, &name)
			if errors.Is(err, pgx.ErrNoRows) {
				return idempotency.Result{}, hs.ErrNotFound()
			}
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Status != StatusOpen || visit.Status != visitInService || status != ItemAccepted {
				detail := "Só é possível marcar como realizado um serviço aceito, durante o atendimento."
				if status == ItemPerformed {
					detail = fmt.Sprintf("%q já está marcado como realizado.", name)
				}
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "invalid_transition", "Ação não permitida agora", detail)
			}
			now := s.Clock.Now()
			if _, err := tx.Exec(ctx, `UPDATE order_item SET status = 'performed', performed_at = $2 WHERE id = $1`, itemID, now); err != nil {
				return idempotency.Result{}, err
			}
			version, err := bumpVersion(ctx, tx, cur.ID, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &cur.EstablishmentID, now, "T-O-13", "order_item", itemID
			a.FromState, a.ToState = ItemAccepted+"@"+strconv.FormatInt(version-1, 10), ItemPerformed+"@"+strconv.FormatInt(version, 10)
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if err := recordOrder(ctx, tx, cur.EstablishmentID, cur.ID, now, evOrder); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusOK)
		})
}
