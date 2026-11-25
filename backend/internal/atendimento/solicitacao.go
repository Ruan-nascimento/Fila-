package atendimento

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

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

type changeRequestBody struct {
	ExpectedVersion *int64             `json:"expected_version"`
	AddServiceIDs   *[]uuid.UUID       `json:"add_service_ids"`
	AddItems        *[]LineRequest     `json:"add_items"`
	RemoveItemIDs   *[]uuid.UUID       `json:"remove_item_ids"`
	SetQuantities   *[]quantityRequest `json:"set_quantities"`
}

// quantityRequest pede uma nova quantidade (absoluta) para uma linha existente.
type quantityRequest struct {
	ItemID   *uuid.UUID `json:"item_id"`
	Quantity *int64     `json:"quantity"`
}

type quantityWant struct {
	ItemID   uuid.UUID
	Quantity int
	field    string
}

func list(p *[]uuid.UUID) []uuid.UUID {
	if p == nil {
		return nil
	}
	return *p
}

func parseQuantities(v *validate.Collector, p *[]quantityRequest) []quantityWant {
	if p == nil {
		return nil
	}
	if len(*p) > maxServiceLines+maxProductLines {
		v.Add(&hs.FieldError{Field: "/set_quantities", Code: hs.FieldTooLong})
	}
	seen := map[uuid.UUID]bool{}
	var out []quantityWant
	for i, q := range *p {
		f := "/set_quantities/" + strconv.Itoa(i)
		v.Required(f+"/item_id", q.ItemID == nil)
		v.Required(f+"/quantity", q.Quantity == nil)
		if q.ItemID == nil || q.Quantity == nil {
			continue
		}
		n := v.IntRange(f+"/quantity", q.Quantity, 1, maxLineQuantity)
		if seen[*q.ItemID] {
			v.Add(&hs.FieldError{Field: f + "/item_id", Code: "duplicate"})
		}
		seen[*q.ItemID] = true
		out = append(out, quantityWant{ItemID: *q.ItemID, Quantity: int(n), field: f})
	}
	return out
}

// changeSet é o pedido de mudança validado no formato.
type changeSet struct {
	add    []LineWant
	remove []uuid.UUID
	qty    []quantityWant
}

// desired monta o conjunto final (quantidade por item do catálogo) a partir das linhas atuais e
// da solicitação, conferindo o que não pode mudar: linha inexistente, linha com unidade realizada
// retirada, quantidade abaixo do já realizado e item já presente incluído de novo.
func (c changeSet) desired(items []itemRow) ([]LineWant, error) {
	byID := map[uuid.UUID]itemRow{}
	have := map[lineKey]bool{}
	for _, it := range items {
		byID[it.ID] = it
		have[it.key()] = true
	}
	removed := map[uuid.UUID]bool{}
	for i, id := range c.remove {
		it, ok := byID[id]
		if !ok {
			return nil, hs.ErrValidation(hs.FieldError{Field: "/remove_item_ids/" + strconv.Itoa(i), Code: hs.FieldNotAllowed,
				Message: "item não está nesta comanda"})
		}
		if it.PerformedQty > 0 {
			return nil, itemPerformedConflict(it, 0)
		}
		removed[id] = true
	}
	newQty := map[uuid.UUID]quantityWant{}
	for _, q := range c.qty {
		it, ok := byID[q.ItemID]
		if !ok || removed[q.ItemID] {
			return nil, hs.ErrValidation(hs.FieldError{Field: q.field + "/item_id", Code: hs.FieldNotAllowed,
				Message: "item não está nesta comanda"})
		}
		if q.Quantity < it.PerformedQty {
			return nil, itemPerformedConflict(it, q.Quantity)
		}
		newQty[q.ItemID] = q
	}
	var out []LineWant
	services, products := 0, 0
	for _, it := range items {
		if removed[it.ID] {
			continue
		}
		w := LineWant{Key: it.key(), Quantity: it.Quantity}
		if q, ok := newQty[it.ID]; ok {
			w.Quantity, w.field = q.Quantity, q.field
		}
		out = append(out, w)
	}
	for _, a := range c.add {
		if have[a.Key] {
			return nil, conflict("duplicate_service", "Item já está na comanda",
				"Cada serviço ou produto aparece uma vez por comanda: peça outra quantidade da mesma linha.", nil)
		}
		out = append(out, a)
	}
	for _, w := range out {
		if w.Key.Kind == catalogo.KindService {
			services++
		} else {
			products++
		}
	}
	if services < 1 {
		return nil, conflict("order_requires_item", "A comanda precisa de um serviço",
			"Para tirar todos os serviços, fale com o profissional.", nil)
	}
	if services > maxServiceLines || products > maxProductLines {
		return nil, hs.ErrValidation(hs.FieldError{Field: "/add_items", Code: hs.FieldTooLong,
			Message: "no máximo 10 serviços e 10 produtos diferentes por comanda"})
	}
	return out, nil
}

// createChangeRequest (T-CR-01): depois da chamada, o cliente pede inclusão, retirada ou outra
// quantidade. O total aceito NÃO muda até a decisão da equipe (R-CMD-07). Uma pendente por
// comanda (índice único). Inclusões guardam o preço visto pelo cliente.
func (s *Service) createChangeRequest(w http.ResponseWriter, r *http.Request) error {
	var body changeRequestBody
	var expected int64
	var cs changeSet
	return s.orderCommand(w, r, "createChangeRequest", allow{customer: true}, &body,
		func(who) error {
			var v validate.Collector
			expected = v.Version("/expected_version", body.ExpectedVersion)
			switch {
			case body.AddServiceIDs != nil && body.AddItems != nil:
				v.Add(&hs.FieldError{Field: "/add_items", Code: hs.FieldNotAllowed, Message: "envie add_items OU add_service_ids"})
			case body.AddItems != nil:
				cs.add = ParseLines(&v, "/add_items", body.AddItems, 0)
			default:
				validateServiceList(&v, "/add_service_ids", body.AddServiceIDs, 0)
				cs.add = ServiceLines(list(body.AddServiceIDs), "/add_service_ids")
			}
			validateServiceList(&v, "/remove_item_ids", body.RemoveItemIDs, 0)
			cs.remove = list(body.RemoveItemIDs)
			cs.qty = parseQuantities(&v, body.SetQuantities)
			if len(cs.add)+len(cs.remove)+len(cs.qty) == 0 {
				v.Add(&hs.FieldError{Field: "/add_items", Code: hs.FieldRequired, Message: "inclua, retire ou mude a quantidade de pelo menos um item"})
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
			wants, err := cs.desired(items)
			if err != nil {
				return idempotency.Result{}, s.withOrder(ctx, tx, cur, err)
			}
			st, err := estabelecimento.LoadSettings(ctx, tx, cur.EstablishmentID)
			if err != nil {
				return idempotency.Result{}, err
			}
			adds, err := growthSnapshots(ctx, tx, cur, items, wants, st)
			if err != nil {
				return idempotency.Result{}, s.withOrder(ctx, tx, cur, err)
			}
			now := s.Clock.Now()
			removeIDs := cs.remove
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
			for _, a := range cs.add {
				if err := insertAddition(ctx, tx, cur.EstablishmentID, crID, adds[a.Key], a.Quantity); err != nil {
					return idempotency.Result{}, err
				}
			}
			for _, q := range cs.qty {
				if _, err := tx.Exec(ctx, `INSERT INTO change_request_item_quantity (change_request_id, item_id, quantity) VALUES ($1, $2, $3)`,
					crID, q.ItemID, q.Quantity); err != nil {
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

// growthSnapshots confere, no catálogo atual, toda linha nova ou que cresce (ativa, feita pelo
// profissional, produtos ligados, limite da loja) e devolve os snapshots das linhas novas.
func growthSnapshots(ctx context.Context, tx pgx.Tx, o orderRow, items []itemRow, wants []LineWant,
	st estabelecimento.Settings) (map[lineKey]catalogo.Snapshot, error) {
	current := map[lineKey]int{}
	for _, it := range items {
		current[it.key()] = it.Quantity
	}
	var keys []lineKey
	for _, w := range wants {
		if w.Quantity > current[w.Key] {
			keys = append(keys, w.Key)
		}
	}
	snaps, err := snapshotsFor(ctx, tx, o.EstablishmentID, keys)
	if err != nil {
		return nil, err
	}
	for _, w := range wants {
		if w.Quantity <= current[w.Key] {
			continue
		}
		sn, ok := snaps[w.Key]
		if err := checkGrowth(sn, ok, w, current[w.Key], o.ProfessionalID, "", st); err != nil {
			return nil, err
		}
	}
	return snaps, nil
}

func insertAddition(ctx context.Context, q database.Querier, estID, crID uuid.UUID, sn catalogo.Snapshot, quantity int) error {
	var serviceID, productID *uuid.UUID
	kind, duration, points := catalogo.KindService, sn.DurationMinutes, sn.Points
	if sn.Kind == catalogo.KindProduct {
		kind, productID, duration, points = catalogo.KindProduct, &sn.ID, 0, 0
	} else {
		serviceID = &sn.ID
	}
	_, err := q.Exec(ctx, `
		INSERT INTO change_request_addition (change_request_id, establishment_id, kind, service_id, product_id, name, unit_price_cents,
		                                     duration_minutes, points, quantity)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		crID, estID, kind, serviceID, productID, sn.Name, sn.PriceCents, duration, points, quantity)
	return err
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
// que o cliente viu nas inclusões e o preço já registrado nas linhas que mudam de quantidade.
// Tudo é reconferido (unidades realizadas, item ainda ativo e feito pelo profissional, limite).
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
			adds, err := crAdditions(ctx, tx, cr.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			qty, err := crQuantities(ctx, tx, cr.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			cs := changeSet{remove: cr.RemoveItemIDs, qty: qty}
			addSnap := map[lineKey]Line{}
			for _, a := range adds {
				k := lineKey{a.Snap.Kind, a.Snap.ID}
				cs.add = append(cs.add, LineWant{Key: k, Quantity: a.Quantity, field: "/add"})
				addSnap[k] = a
			}
			wants, err := cs.desired(items)
			if err != nil {
				return idempotency.Result{}, s.withOrder(ctx, tx, cur, withRejectHint(err))
			}
			st, err := estabelecimento.LoadSettings(ctx, tx, cur.EstablishmentID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if _, err := growthSnapshots(ctx, tx, cur, items, wants, st); err != nil {
				return idempotency.Result{}, s.withOrder(ctx, tx, cur, withRejectHint(err))
			}
			now := s.Clock.Now()
			var evs []qtyEvent
			byKey := map[lineKey]itemRow{}
			for _, it := range items {
				byKey[it.key()] = it
			}
			for _, id := range cr.RemoveItemIDs {
				for _, it := range items {
					if it.ID == id {
						if _, err := tx.Exec(ctx, `UPDATE order_item SET status = 'removed', removed_at = $2 WHERE id = $1`, id, now); err != nil {
							return idempotency.Result{}, err
						}
						evs = append(evs, qtyEvent{id, "quantity", it.Quantity, 0})
					}
				}
			}
			for _, q := range qty {
				for _, it := range items {
					if it.ID == q.ItemID && it.Quantity != q.Quantity {
						if err := setQuantity(ctx, tx, it, q.Quantity, now); err != nil {
							return idempotency.Result{}, err
						}
						evs = append(evs, qtyEvent{it.ID, "quantity", it.Quantity, q.Quantity})
					}
				}
			}
			for _, a := range cs.add { // preço aceito = o da solicitação (visto pelo cliente)
				l := addSnap[a.Key]
				if _, err := insertItem(ctx, tx, cur.EstablishmentID, cur.ID, l.Snap, l.Quantity, now); err != nil {
					if database.IsUniqueViolation(err, "order_item_service_uq") || database.IsUniqueViolation(err, "order_item_product_uq") {
						return idempotency.Result{}, s.orderConflict(ctx, tx, cur, "duplicate_service", "Item já está na comanda",
							"Cada serviço ou produto aparece uma vez por comanda.")
					}
					return idempotency.Result{}, err
				}
			}
			version, err := bumpOpenTotal(ctx, tx, cur.ID, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			a := by.audit(ctx, cur)
			if err := recordQuantities(ctx, tx, cur.EstablishmentID, cur.ID, evs, "change_request", a, version, now); err != nil {
				return idempotency.Result{}, err
			}
			if visit.Status == visitCalled || visit.Status == visitInService {
				if err := s.Visits.DurationChanged(ctx, tx, cur.EstablishmentID, visit.ParticipationID); err != nil {
					return idempotency.Result{}, s.withOrder(ctx, tx, cur, withRejectHint(err))
				}
			}
			if err := s.decide(r, tx, cur, by, cr.ID, CRApproved, "", now); err != nil {
				return idempotency.Result{}, err
			}
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

// withRejectHint orienta a equipe a recusar quando a solicitação não pode mais ser aplicada.
func withRejectHint(err error) error {
	var he *hs.Error
	if errors.As(err, &he) && he.Status == http.StatusConflict {
		he.Detail += " Recuse a solicitação."
	}
	return err
}

// crAdditions lê as inclusões da solicitação (snapshot visto pelo cliente, com quantidade).
func crAdditions(ctx context.Context, q database.Querier, crID uuid.UUID) ([]Line, error) {
	rows, err := q.Query(ctx, `
		SELECT kind, coalesce(service_id, product_id), name, unit_price_cents, duration_minutes, points, quantity
		FROM change_request_addition WHERE change_request_id = $1 ORDER BY kind DESC, name, coalesce(service_id, product_id)`, crID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Line, error) {
		var l Line
		err := row.Scan(&l.Snap.Kind, &l.Snap.ID, &l.Snap.Name, &l.Snap.PriceCents, &l.Snap.DurationMinutes, &l.Snap.Points, &l.Quantity)
		l.Snap.Active = true
		return l, err
	})
}

// crQuantities lê as novas quantidades pedidas para linhas existentes.
func crQuantities(ctx context.Context, q database.Querier, crID uuid.UUID) ([]quantityWant, error) {
	rows, err := q.Query(ctx, `SELECT item_id, quantity FROM change_request_item_quantity WHERE change_request_id = $1 ORDER BY item_id`, crID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (quantityWant, error) {
		var w quantityWant
		err := row.Scan(&w.ItemID, &w.Quantity)
		w.field = "/set_quantities"
		return w, err
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
