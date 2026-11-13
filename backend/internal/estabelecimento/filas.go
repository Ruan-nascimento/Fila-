package estabelecimento

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Estados da fila (QueueStatus).
const (
	QueueOpen   = "open"
	QueuePaused = "paused"
	QueueClosed = "closed"
)

type queueProfessional struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
}

type unavailableSignal struct {
	Active bool       `json:"active"`
	Note   *string    `json:"note"`
	Since  *time.Time `json:"since"`
}

// staffQueueView segue o esquema StaffQueue. As entradas vêm do módulo fila (QueueActivity).
type staffQueueView struct {
	QueueID      uuid.UUID         `json:"queue_id"`
	Professional queueProfessional `json:"professional"`
	Status       string            `json:"status"`
	Version      int64             `json:"version"`
	Unavailable  unavailableSignal `json:"unavailable_signal"`
	Entries      []StaffEntry      `json:"entries"`
}

const queueSelect = `
	SELECT q.id, p.id, p.display_name, p.status, q.status, q.version, q.unavailable, q.unavailable_note, q.unavailable_since
	FROM queue q JOIN professional p ON p.id = q.professional_id`

func scanQueue(row pgx.Row) (staffQueueView, error) {
	var v staffQueueView
	err := row.Scan(&v.QueueID, &v.Professional.ID, &v.Professional.DisplayName, &v.Professional.Status,
		&v.Status, &v.Version, &v.Unavailable.Active, &v.Unavailable.Note, &v.Unavailable.Since)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, httpserver.ErrNotFound()
	}
	v.Entries = []StaffEntry{}
	return v, err
}

func loadQueue(ctx context.Context, q database.Querier, estID, queueID uuid.UUID, lock bool) (staffQueueView, error) {
	sql := queueSelect + ` WHERE q.establishment_id = $1 AND q.id = $2`
	if lock {
		sql += ` FOR UPDATE OF q`
	}
	return scanQueue(q.QueryRow(ctx, sql, estID, queueID))
}

// listStaffQueues: dono vê todas as filas da loja; profissional só a própria (own_queue).
// cursor fica nulo até a etapa 06 (fluxo de eventos SSE); ver ADR-0018.
func (s *Service) listStaffQueues(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	ctx := r.Context()
	sql := queueSelect + ` WHERE q.establishment_id = $1`
	args := []any{estID}
	if !actor.IsOwner() {
		if actor.ProfessionalID == nil {
			return httpserver.ErrForbidden("Somente a equipe da loja pode ver as filas.")
		}
		sql += ` AND q.professional_id = $2`
		args = append(args, *actor.ProfessionalID)
	}
	rows, err := s.Pool.Query(ctx, sql+` ORDER BY p.queue_letter`, args...)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (staffQueueView, error) { return scanQueue(row) })
	if err != nil {
		return err
	}
	if list == nil {
		list = []staffQueueView{}
	}
	if err := s.fillEntries(ctx, s.Pool, list); err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data   []staffQueueView `json:"data"`
		Cursor *string          `json:"cursor"`
	}{Data: list})
	return nil
}

type versionRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

func (s *Service) openQueue(w http.ResponseWriter, r *http.Request) error {
	return s.changeQueue(w, r, "openQueue")
}

func (s *Service) pauseQueue(w http.ResponseWriter, r *http.Request) error {
	return s.changeQueue(w, r, "pauseQueue")
}

func (s *Service) closeQueue(w http.ResponseWriter, r *http.Request) error {
	return s.changeQueue(w, r, "closeQueue")
}

// changeQueue aplica T-Q-01…04. Só o dono (R-FILA-07); versão esperada; Idempotency-Key.
func (s *Service) changeQueue(w http.ResponseWriter, r *http.Request, op string) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	queueID, err := pathID(r, "queueId")
	if err != nil {
		return err
	}
	var req versionRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: op, Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadQueue(ctx, tx, estID, queueID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		var to, action string
		switch op {
		case "openQueue":
			if cur.Status == QueueOpen {
				return idempotency.Result{}, invalidTransition("A fila já está aberta.", cur)
			}
			if cur.Professional.Status != "active" {
				return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "professional_inactive", Title: "Profissional desativado",
					Detail: "Reative o profissional antes de abrir a fila.", Current: cur}
			}
			to, action = QueueOpen, "T-Q-01"
			if cur.Status == QueuePaused {
				action = "T-Q-03"
			}
		case "pauseQueue":
			if cur.Status != QueueOpen {
				return idempotency.Result{}, invalidTransition("Só é possível pausar uma fila aberta.", cur)
			}
			to, action = QueuePaused, "T-Q-02"
		case "closeQueue":
			if cur.Status == QueueClosed {
				return idempotency.Result{}, invalidTransition("A fila já está fechada.", cur)
			}
			pending, err := s.pendingInQueue(ctx, tx, queueID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if pending > 0 {
				return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "queue_has_pending_participants",
					Title:  "Ainda há clientes aguardando ou chamados",
					Detail: "Transfira ou cancele (com motivo) quem está aguardando ou chamado antes de fechar.", Current: cur}
			}
			to, action = QueueClosed, "T-Q-04"
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE queue SET status = $2, version = version + 1, updated_at = $3 WHERE id = $1`, queueID, to, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(actor.AccountID), Action: action, TargetType: "queue", TargetID: queueID,
			FromState: cur.Status, ToState: to, RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		next, err := loadQueue(ctx, tx, estID, queueID, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		views := []staffQueueView{next}
		if err := s.fillEntries(ctx, tx, views); err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[staffQueueView]{Data: views[0]}}, nil
	})
}

type signalRequest struct {
	Unavailable *bool   `json:"unavailable"`
	Note        *string `json:"note"`
}

// signalAvailability (T-Q-05): o profissional avisa o dono; o estado da fila NÃO muda.
// Só o profissional da própria fila (x-authz: professional/own_queue).
func (s *Service) signalAvailability(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	queueID, err := pathID(r, "queueId")
	if err != nil {
		return err
	}
	var req signalRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/unavailable", req.Unavailable == nil)
	var note string
	if req.Note != nil {
		n, fe := validate.Text("/note", *req.Note, 0, 200)
		v.Add(fe)
		note = n
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "signalAvailability", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadQueue(ctx, tx, estID, queueID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if actor.ProfessionalID == nil || *actor.ProfessionalID != cur.Professional.ID {
			return idempotency.Result{}, httpserver.ErrForbidden("Só o profissional desta fila pode sinalizar indisponibilidade.")
		}
		now := s.Clock.Now()
		var notePtr *string
		var since *time.Time
		if *req.Unavailable {
			if note != "" {
				notePtr = &note
			}
			since = &now
			if cur.Unavailable.Active && cur.Unavailable.Since != nil {
				since = cur.Unavailable.Since // repetir o sinal não reinicia o "desde"
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE queue SET unavailable = $2, unavailable_note = $3, unavailable_since = $4, updated_at = $5 WHERE id = $1`,
			queueID, *req.Unavailable, notePtr, since, now); err != nil {
			return idempotency.Result{}, err
		}
		to := "available"
		if *req.Unavailable {
			to = "unavailable"
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorProfessional,
			ActorID: auditoria.Ptr(actor.AccountID), Action: "T-Q-05", TargetType: "queue", TargetID: queueID,
			ToState: to, RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		next, err := loadQueue(ctx, tx, estID, queueID, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		views := []staffQueueView{next}
		if err := s.fillEntries(ctx, tx, views); err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[staffQueueView]{Data: views[0]}}, nil
	})
}

// ---------------------------------------------------------------------------------------------
// listPublicQueues: público; só dados públicos (R-PERF-01): profissional, estado, quantidade,
// senhas e estimativa. Nunca nick, serviços, valores, sinal de indisponibilidade ou contatos.

type publicQueueView struct {
	QueueID      uuid.UUID `json:"queue_id"`
	Professional struct {
		ID          uuid.UUID `json:"id"`
		DisplayName string    `json:"display_name"`
	} `json:"professional"`
	Status        string         `json:"status"`
	WaitingCount  int            `json:"waiting_count"`
	Tickets       []PublicTicket `json:"tickets"`
	EstimatedWait estimate       `json:"estimated_wait"`
	Version       int64          `json:"version"`
}

type estimate struct {
	Kind       string    `json:"kind"`
	Minutes    int       `json:"minutes"`
	ComputedAt time.Time `json:"computed_at"`
}

func (s *Service) listPublicQueues(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := ActiveByID(ctx, s.Pool, estID); errors.Is(err, ErrNotFound) {
		return httpserver.ErrNotFound()
	} else if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT q.id, p.id, p.display_name, q.status, q.version
		FROM queue q JOIN professional p ON p.id = q.professional_id
		WHERE q.establishment_id = $1 AND p.status = 'active'
		ORDER BY p.queue_letter`, estID)
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (publicQueueView, error) {
		var v publicQueueView
		err := row.Scan(&v.QueueID, &v.Professional.ID, &v.Professional.DisplayName, &v.Status, &v.Version)
		v.Tickets = []PublicTicket{}
		v.EstimatedWait = estimate{Kind: "estimate", Minutes: 0, ComputedAt: now}
		return v, err
	})
	if err != nil {
		return err
	}
	if s.Activity != nil && len(list) > 0 {
		ids := make([]uuid.UUID, len(list))
		for i := range list {
			ids[i] = list[i].QueueID
		}
		snaps, err := s.Activity.PublicSnapshots(ctx, s.Pool, ids, now)
		if err != nil {
			return err
		}
		for i := range list {
			list[i].apply(snaps)
		}
	}
	if list == nil {
		list = []publicQueueView{}
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data   []publicQueueView `json:"data"`
		Cursor *string           `json:"cursor"`
	}{Data: list})
	return nil
}

// fillEntries preenche as pessoas de cada fila (módulo fila).
func (s *Service) fillEntries(ctx context.Context, q database.Querier, views []staffQueueView) error {
	return fillEntries(ctx, q, s.Activity, views)
}

func fillEntries(ctx context.Context, q database.Querier, activity QueueActivity, views []staffQueueView) error {
	if activity == nil || len(views) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(views))
	for i := range views {
		ids[i] = views[i].QueueID
	}
	entries, err := activity.StaffEntries(ctx, q, ids)
	if err != nil {
		return err
	}
	for i := range views {
		if e, ok := entries[views[i].QueueID]; ok && e != nil {
			views[i].Entries = e
		}
	}
	return nil
}

func (v *publicQueueView) apply(snaps map[uuid.UUID]PublicSnapshot) {
	if sn, ok := snaps[v.QueueID]; ok {
		v.WaitingCount = sn.WaitingCount
		if sn.Tickets != nil {
			v.Tickets = sn.Tickets
		}
		v.EstimatedWait.Minutes = sn.EstimateMinutes
	}
}

// QueueViews são as visões de UMA fila usadas nos eventos (outbox): a da equipe (StaffQueue,
// com nick e serviços) e a pública (PublicQueue, só senhas e estimativa), montada separadamente
// a partir da projeção pública — nunca da visão da equipe com campos apagados.
type QueueViews struct {
	Staff          any
	Public         any
	Version        int64
	ProfessionalID uuid.UUID
}

// QueueEventViews monta as duas visões da fila queueID desta loja.
func QueueEventViews(ctx context.Context, q database.Querier, activity QueueActivity, estID, queueID uuid.UUID, now time.Time) (QueueViews, error) {
	staff, err := loadQueue(ctx, q, estID, queueID, false)
	if err != nil {
		return QueueViews{}, err
	}
	views := []staffQueueView{staff}
	if err := fillEntries(ctx, q, activity, views); err != nil {
		return QueueViews{}, err
	}
	pub := publicQueueView{QueueID: staff.QueueID, Status: staff.Status, Version: staff.Version, Tickets: []PublicTicket{},
		EstimatedWait: estimate{Kind: "estimate", ComputedAt: now}}
	pub.Professional.ID, pub.Professional.DisplayName = staff.Professional.ID, staff.Professional.DisplayName
	if activity != nil {
		snaps, err := activity.PublicSnapshots(ctx, q, []uuid.UUID{queueID}, now)
		if err != nil {
			return QueueViews{}, err
		}
		pub.apply(snaps)
	}
	return QueueViews{Staff: views[0], Public: pub, Version: staff.Version, ProfessionalID: staff.Professional.ID}, nil
}
