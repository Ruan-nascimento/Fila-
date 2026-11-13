package atendimento

import (
	"errors"
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

type changeRequestBody struct {
	ExpectedVersion *int64       `json:"expected_version"`
	AddServiceIDs   *[]uuid.UUID `json:"add_service_ids"`
	RemoveItemIDs   *[]uuid.UUID `json:"remove_item_ids"`
}

func list(p *[]uuid.UUID) []uuid.UUID {
	if p == nil {
		return nil
	}
	return *p
}

// createChangeRequest (T-CR-01): depois da chamada, o cliente pede inclusão/remoção. O total
// aceito NÃO muda até a decisão da equipe (R-CMD-07). Uma pendente por comanda (índice único).
func (s *Service) createChangeRequest(w http.ResponseWriter, r *http.Request) error {
	var body changeRequestBody
	var expected int64
	return s.orderCommand(w, r, "createChangeRequest", allow{customer: true}, &body,
		func(who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			validateServiceList(&v, "/add_service_ids", body.AddServiceIDs, 0)
			validateServiceList(&v, "/remove_item_ids", body.RemoveItemIDs, 0)
			if len(list(body.AddServiceIDs))+len(list(body.RemoveItemIDs)) == 0 {
				v.Add(&hs.FieldError{Field: "/add_service_ids", Code: hs.FieldRequired, Message: "inclua ou retire pelo menos um serviço"})
			}
			return v.Err()
		},
		func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error) {
			ctx := r.Context()
			cur, visit, err := s.lockWithVisit(ctx, tx, o, expected)
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Status != StatusOpen || (visit.Status != visitCalled && visit.Status != visitInService) {
				detail := "Só é possível pedir alteração depois de ser chamado e antes de terminar o atendimento."
				if cur.Status == StatusOpen && visit.Status == visitWaiting {
					detail = "Enquanto aguarda, altere os serviços diretamente."
				}
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "invalid_transition", "Ação não permitida agora", detail)
			}
			if pending, err := hasPending(ctx, tx, cur.ID); err != nil {
				return idempotency.Result{}, err
			} else if pending {
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "change_request_pending", "Já existe uma solicitação pendente",
					"Aguarde a resposta do profissional antes de pedir outra alteração.")
			}
			items, err := liveItems(ctx, tx, cur.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			removeIDs, addIDs := list(body.RemoveItemIDs), list(body.AddServiceIDs)
			byID := map[uuid.UUID]itemRow{}
			have := map[uuid.UUID]bool{}
			for _, it := range items {
				byID[it.ID] = it
				have[it.ServiceID] = true
			}
			for i, id := range removeIDs {
				it, ok := byID[id]
				if !ok {
					return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/remove_item_ids/" + strconv.Itoa(i),
						Code: hs.FieldNotAllowed, Message: "item não está nesta comanda"})
				}
				if it.Status == ItemPerformed {
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "item_already_performed", "Serviço já realizado",
						fmt.Sprintf("%q já foi realizado e não pode ser retirado.", it.Name))
				}
			}
			for _, id := range addIDs {
				if have[id] {
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "duplicate_service", "Serviço já está na comanda",
						"Cada serviço aparece uma vez por comanda.")
				}
			}
			if len(items)-len(removeIDs)+len(addIDs) < 1 {
				return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "order_requires_item", "A comanda precisa de um serviço",
					"Para tirar todos os serviços, fale com o profissional.")
			}
			if len(items)-len(removeIDs)+len(addIDs) > maxItems {
				return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/add_service_ids", Code: hs.FieldTooLong,
					Message: "no máximo 10 serviços por comanda"})
			}
			snaps, err := snapshotsToAdd(ctx, tx, cur, addIDs, func(id uuid.UUID) string {
				return "/add_service_ids/" + strconv.Itoa(slices.Index(addIDs, id))
			})
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			if removeIDs == nil {
				removeIDs = []uuid.UUID{}
			}
			var crID uuid.UUID
			err = tx.QueryRow(ctx, `
				INSERT INTO change_request (establishment_id, order_id, remove_item_ids, created_at)
				VALUES ($1, $2, $3, $4) RETURNING id`, cur.EstablishmentID, cur.ID, removeIDs, now).Scan(&crID)
			if database.IsUniqueViolation(err, "change_request_one_pending_uq") {
				return idempotency.Result{}, conflict("change_request_pending", "Já existe uma solicitação pendente",
					"Aguarde a resposta do profissional antes de pedir outra alteração.", nil)
			}
			if err != nil {
				return idempotency.Result{}, err
			}
			for _, sn := range snaps {
				if _, err := tx.Exec(ctx, `
					INSERT INTO change_request_addition (change_request_id, establishment_id, service_id, name, unit_price_cents, duration_minutes, points)
					VALUES ($1, $2, $3, $4, $5, $6, $7)`, crID, cur.EstablishmentID, sn.ID, sn.Name, sn.PriceCents, sn.DurationMinutes, sn.Points); err != nil {
					return idempotency.Result{}, err
				}
			}
			// A comanda muda de versão (passa a mostrar a solicitação), mas o total não.
			if _, err := bumpVersion(ctx, tx, cur.ID, now); err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID, a.ToState = &cur.EstablishmentID, now, "T-CR-01", "change_request", crID, CRPending
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.afterDecision(r, tx, cur, visit, crID); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusCreated)
		})
}

// afterDecision grava os eventos de solicitação, comanda e fila depois de criar/decidir.
func (s *Service) afterDecision(r *http.Request, tx pgx.Tx, o orderRow, visit VisitState, crID uuid.UUID) error {
	ctx, now := r.Context(), s.Clock.Now()
	if err := recordChangeRequest(ctx, tx, o.EstablishmentID, o.ID, crID, now); err != nil {
		return err
	}
	if err := recordOrder(ctx, tx, o.EstablishmentID, o.ID, now, evOrder); err != nil {
		return err
	}
	return s.Visits.QueueChanged(ctx, tx, o.EstablishmentID, visit.QueueID)
}

type pendingCR struct {
	ID            uuid.UUID
	RemoveItemIDs []uuid.UUID
}

// lockPending trava a solicitação pendente crID da comanda (404 se não for desta comanda).
func (s *Service) lockPending(r *http.Request, tx pgx.Tx, o orderRow) (pendingCR, error) {
	crID, err := pathID(r, "changeRequestId")
	if err != nil {
		return pendingCR{}, err
	}
	var cr pendingCR
	var status string
	err = tx.QueryRow(r.Context(), `SELECT id, status, remove_item_ids FROM change_request WHERE order_id = $1 AND id = $2 FOR UPDATE`,
		o.ID, crID).Scan(&cr.ID, &status, &cr.RemoveItemIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return cr, hs.ErrNotFound()
	}
	if err != nil {
		return cr, err
	}
	if status != CRPending {
		return cr, s.orderConflict(r.Context(), tx, o, "invalid_transition", "Solicitação já resolvida",
			"Esta solicitação já foi aprovada, recusada ou anulada.")
	}
	return cr, nil
}

// approveChangeRequest (T-CR-02): aplica a solicitação como nova versão da comanda, com o preço
// que o cliente viu. Inclusões são reconferidas (serviço ainda ativo e feito pelo profissional).
func (s *Service) approveChangeRequest(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	var expected int64
	return s.orderCommand(w, r, "approveChangeRequest", allow{professional: true, owner: true}, &body,
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
			cr, err := s.lockPending(r, tx, cur)
			if err != nil {
				return idempotency.Result{}, err
			}
			items, err := liveItems(ctx, tx, cur.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			for _, it := range items {
				if slices.Contains(cr.RemoveItemIDs, it.ID) && it.Status == ItemPerformed {
					return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "item_already_performed", "Serviço já realizado",
						fmt.Sprintf("%q já foi realizado e não pode ser retirado. Recuse a solicitação.", it.Name))
				}
			}
			rows, err := tx.Query(ctx, `
				SELECT service_id, name, unit_price_cents, duration_minutes, points
				FROM change_request_addition WHERE change_request_id = $1 ORDER BY name, service_id`, cr.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			adds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (catalogo.Snapshot, error) {
				var sn catalogo.Snapshot
				err := row.Scan(&sn.ID, &sn.Name, &sn.PriceCents, &sn.DurationMinutes, &sn.Points)
				return sn, err
			})
			if err != nil {
				return idempotency.Result{}, err
			}
			ids := make([]uuid.UUID, len(adds))
			for i := range adds {
				ids[i] = adds[i].ID
			}
			if _, err := snapshotsToAdd(ctx, tx, cur, ids, func(uuid.UUID) string { return "/add" }); err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			if len(cr.RemoveItemIDs) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE order_item SET status = 'removed', removed_at = $3
					WHERE order_id = $1 AND id = ANY($2) AND status = 'accepted'`, cur.ID, cr.RemoveItemIDs, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			for _, sn := range adds { // preço aceito = o da solicitação (visto pelo cliente)
				if _, err := insertItem(ctx, tx, cur.EstablishmentID, cur.ID, sn, now); err != nil {
					if database.IsUniqueViolation(err, "order_item_service_uq") {
						return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "duplicate_service", "Serviço já está na comanda",
							"Cada serviço aparece uma vez por comanda.")
					}
					return idempotency.Result{}, err
				}
			}
			version, err := bumpOpenTotal(ctx, tx, cur.ID, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			if err := s.decide(r, tx, cur, by, cr.ID, CRApproved, "", now); err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &cur.EstablishmentID, now, "T-CR-02", "order", cur.ID
			a.FromState, a.ToState = versionTag(StatusOpen, version-1), versionTag(StatusOpen, version)
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.afterDecision(r, tx, cur, visit, cr.ID); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusOK)
		})
}

// rejectChangeRequest (T-CR-03): a comanda fica como estava; o cliente é avisado (evento).
func (s *Service) rejectChangeRequest(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ExpectedVersion *int64  `json:"expected_version"`
		Reason          *string `json:"reason"`
	}
	var expected int64
	var reason string
	return s.orderCommand(w, r, "rejectChangeRequest", allow{professional: true, owner: true}, &body,
		func(by who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			reason = reasonFor(&v, by, body.Reason, "/reason")
			return v.Err()
		},
		func(tx pgx.Tx, o orderRow, by who) (idempotency.Result, error) {
			ctx := r.Context()
			cur, visit, err := s.lockWithVisit(ctx, tx, o, expected)
			if err != nil {
				return idempotency.Result{}, err
			}
			cr, err := s.lockPending(r, tx, cur)
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			if _, err := bumpVersion(ctx, tx, cur.ID, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.decide(r, tx, cur, by, cr.ID, CRRejected, reason, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.afterDecision(r, tx, cur, visit, cr.ID); err != nil {
				return idempotency.Result{}, err
			}
			return s.done(ctx, tx, cur, http.StatusOK)
		})
}

// decide grava a decisão da equipe na solicitação, com autor, papel e instante (R-CMD-08).
func (s *Service) decide(r *http.Request, tx pgx.Tx, o orderRow, by who, crID uuid.UUID, to, reason string, now time.Time) error {
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE change_request SET status = $2, decided_at = $3, decided_by_role = $4, decided_by_account_id = $5,
		       decision_reason = $6, version = version + 1
		WHERE id = $1`, crID, to, now, by.role(), by.staff.AccountID, reasonPtr); err != nil {
		return err
	}
	a := by.audit(r.Context(), o)
	a.EstablishmentID, a.OccurredAt, a.TargetType, a.TargetID = &o.EstablishmentID, now, "change_request", crID
	a.Action, a.FromState, a.ToState, a.Reason = map[string]string{CRApproved: "T-CR-02", CRRejected: "T-CR-03"}[to], CRPending, to, reason
	return auditoria.Record(r.Context(), tx, a)
}
