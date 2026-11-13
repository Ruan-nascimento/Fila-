package estabelecimento

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

var letterPattern = regexp.MustCompile(`^[A-Z]$`)

type professionalView struct {
	ID            uuid.UUID   `json:"id"`
	DisplayName   string      `json:"display_name"`
	QueueID       uuid.UUID   `json:"queue_id"`
	QueueLetter   string      `json:"queue_letter"`
	Status        string      `json:"status"`
	HasAccess     bool        `json:"has_access"`
	PendingInvite *InviteInfo `json:"pending_invite"`
	Version       int64       `json:"version"`
}

const professionalSelect = `
	SELECT p.id, p.display_name, q.id, p.queue_letter, p.status,
	       EXISTS (SELECT 1 FROM staff_membership m WHERE m.professional_id = p.id AND m.status = 'active'),
	       p.version
	FROM professional p JOIN queue q ON q.professional_id = p.id`

func scanProfessional(row pgx.Row) (professionalView, error) {
	var v professionalView
	err := row.Scan(&v.ID, &v.DisplayName, &v.QueueID, &v.QueueLetter, &v.Status, &v.HasAccess, &v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, httpserver.ErrNotFound()
	}
	return v, err
}

// loadProfessional lê (e opcionalmente trava) o profissional DESTA loja; de outra loja → 404.
func loadProfessional(ctx context.Context, q database.Querier, estID, profID uuid.UUID, lock bool) (professionalView, error) {
	sql := professionalSelect + ` WHERE p.establishment_id = $1 AND p.id = $2`
	if lock {
		sql += ` FOR UPDATE OF p`
	}
	return scanProfessional(q.QueryRow(ctx, sql, estID, profID))
}

func (s *Service) withInvite(ctx context.Context, q database.Querier, estID uuid.UUID, vs []professionalView) error {
	if s.PendingInvites == nil {
		return nil
	}
	invites, err := s.PendingInvites(ctx, q, estID)
	if err != nil {
		return err
	}
	for i := range vs {
		if inv, ok := invites[vs[i].ID]; ok {
			inv := inv
			vs[i].PendingInvite = &inv
		}
	}
	return nil
}

// listProfessionals: dono; inclui inativos.
func (s *Service) listProfessionals(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Owner(r, estID); err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.Pool.Query(ctx, professionalSelect+` WHERE p.establishment_id = $1 ORDER BY p.queue_letter`, estID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (professionalView, error) { return scanProfessional(row) })
	if err != nil {
		return err
	}
	if err := s.withInvite(ctx, s.Pool, estID, list); err != nil {
		return err
	}
	if list == nil {
		list = []professionalView{}
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[[]professionalView]{Data: list})
	return nil
}

func (s *Service) professionalResult(ctx context.Context, q database.Querier, estID, profID uuid.UUID, status int) (idempotency.Result, error) {
	v, err := loadProfessional(ctx, q, estID, profID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	list := []professionalView{v}
	if err := s.withInvite(ctx, q, estID, list); err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: status, Body: httpserver.Data[professionalView]{Data: list[0]}}, nil
}

type createProfessionalRequest struct {
	DisplayName *string `json:"display_name"`
	QueueLetter *string `json:"queue_letter"`
}

// createProfessional (T-PR-01): cria o profissional e a fila dele, fechada.
func (s *Service) createProfessional(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	var req createProfessionalRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/display_name", req.DisplayName == nil)
	v.Required("/queue_letter", req.QueueLetter == nil)
	var name string
	if req.DisplayName != nil {
		n, fe := validate.Text("/display_name", *req.DisplayName, 1, 40)
		v.Add(fe)
		name = n
	}
	if req.QueueLetter != nil && !letterPattern.MatchString(*req.QueueLetter) {
		v.Add(&httpserver.FieldError{Field: "/queue_letter", Code: httpserver.FieldInvalidFormat, Message: "uma letra maiúscula de A a Z"})
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "createProfessional", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.Clock.Now()
		var profID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO professional (establishment_id, display_name, queue_letter, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $4) RETURNING id`, estID, name, *req.QueueLetter, now).Scan(&profID)
		if database.IsUniqueViolation(err, "professional_letter_uq") {
			return idempotency.Result{}, httpserver.ErrValidation(httpserver.FieldError{Field: "/queue_letter",
				Code: "duplicate", Message: "outro profissional desta loja já usa essa letra"})
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		var queueID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO queue (establishment_id, professional_id, created_at, updated_at)
			VALUES ($1, $2, $3, $3) RETURNING id`, estID, profID, now).Scan(&queueID); err != nil {
			return idempotency.Result{}, err
		}
		rid := httpserver.RequestIDFrom(ctx)
		for _, e := range []auditoria.Event{
			{TargetType: "professional", TargetID: profID, ToState: "active"},
			{TargetType: "queue", TargetID: queueID, ToState: "closed"},
		} {
			e.EstablishmentID, e.OccurredAt, e.ActorKind, e.ActorID, e.Action, e.RequestID =
				&estID, now, auditoria.ActorOwner, auditoria.Ptr(actor.AccountID), "T-PR-01", rid
			if err := auditoria.Record(ctx, tx, e); err != nil {
				return idempotency.Result{}, err
			}
		}
		return s.professionalResult(ctx, tx, estID, profID, http.StatusCreated)
	})
}

type updateProfessionalRequest struct {
	ExpectedVersion *int64  `json:"expected_version"`
	DisplayName     *string `json:"display_name"`
}

// updateProfessional (T-PR-04): nome exibido.
func (s *Service) updateProfessional(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	profID, err := pathID(r, "professionalId")
	if err != nil {
		return err
	}
	var req updateProfessionalRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	v.Required("/display_name", req.DisplayName == nil)
	var name string
	if req.DisplayName != nil {
		n, fe := validate.Text("/display_name", *req.DisplayName, 1, 40)
		v.Add(fe)
		name = n
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "updateProfessional", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadProfessional(ctx, tx, estID, profID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE professional SET display_name = $2, version = version + 1, updated_at = $3 WHERE id = $1`,
			profID, name, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(actor.AccountID), Action: "T-PR-04", TargetType: "professional", TargetID: profID,
			RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		return s.professionalResult(ctx, tx, estID, profID, http.StatusOK)
	})
}

type versionReasonRequest struct {
	ExpectedVersion *int64  `json:"expected_version"`
	Reason          *string `json:"reason"`
}

// deactivateProfessional (T-PR-02): dono reautenticado, com motivo. Recusa se houver
// participação ativa na fila. Não apaga nada: comandas antigas continuam com o dono.
func (s *Service) deactivateProfessional(w http.ResponseWriter, r *http.Request) error {
	return s.changeProfessionalStatus(w, r, false)
}

// reactivateProfessional (T-PR-03): dono reautenticado; a fila continua fechada.
func (s *Service) reactivateProfessional(w http.ResponseWriter, r *http.Request) error {
	return s.changeProfessionalStatus(w, r, true)
}

func (s *Service) changeProfessionalStatus(w http.ResponseWriter, r *http.Request, activate bool) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	if err := s.Auth.RequireReauth(r); err != nil {
		return err
	}
	profID, err := pathID(r, "professionalId")
	if err != nil {
		return err
	}
	var req versionReasonRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	var reason string
	if !activate {
		// O ator é sempre equipe aqui, então o motivo é obrigatório (VersionedReasonRequest).
		v.Required("/reason", req.Reason == nil)
	}
	if req.Reason != nil {
		if activate {
			v.Add(&httpserver.FieldError{Field: "/reason", Code: httpserver.FieldUnknown})
		} else {
			n, fe := validate.Text("/reason", *req.Reason, 3, 500)
			v.Add(fe)
			reason = n
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	op := "deactivateProfessional"
	if activate {
		op = "reactivateProfessional"
	}
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: op, Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadProfessional(ctx, tx, estID, profID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		now := s.Clock.Now()
		rid := httpserver.RequestIDFrom(ctx)
		if activate {
			if cur.Status == "active" {
				return idempotency.Result{}, invalidTransition("O profissional já está ativo.", cur)
			}
			if _, err := tx.Exec(ctx, `UPDATE professional SET status = 'active', version = version + 1, updated_at = $2 WHERE id = $1`, profID, now); err != nil {
				return idempotency.Result{}, err
			}
			// O vínculo desligado pela desativação volta (se a conta não tiver outro igual ativo).
			if _, err := tx.Exec(ctx, `
				UPDATE staff_membership m SET status = 'active', ended_reason = NULL, updated_at = $2
				WHERE m.id = (SELECT id FROM staff_membership
				              WHERE professional_id = $1 AND status = 'inactive' AND ended_reason = 'professional_deactivated'
				              ORDER BY updated_at DESC LIMIT 1)
				  AND NOT EXISTS (SELECT 1 FROM staff_membership o WHERE o.establishment_id = m.establishment_id
				                  AND o.account_id = m.account_id AND o.role = m.role AND o.status = 'active')`, profID, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
				ActorID: auditoria.Ptr(actor.AccountID), Action: "T-PR-03", TargetType: "professional", TargetID: profID,
				FromState: "inactive", ToState: "active", RequestID: rid}); err != nil {
				return idempotency.Result{}, err
			}
			return s.professionalResult(ctx, tx, estID, profID, http.StatusOK)
		}

		if cur.Status != "active" {
			return idempotency.Result{}, invalidTransition("O profissional já está desativado.", cur)
		}
		active, err := s.activeForProfessional(ctx, tx, profID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if active > 0 {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "professional_has_active_participations",
				Title:  "Há clientes na fila deste profissional",
				Detail: "Transfira, conclua ou cancele os atendimentos da fila antes de desativar.", Current: cur}
		}
		if _, err := tx.Exec(ctx, `UPDATE professional SET status = 'inactive', version = version + 1, updated_at = $2 WHERE id = $1`, profID, now); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE queue SET status = 'closed', version = version + 1, unavailable = false, unavailable_note = NULL,
			       unavailable_since = NULL, updated_at = $2
			WHERE professional_id = $1 AND status <> 'closed'`, profID, now); err != nil {
			return idempotency.Result{}, err
		}
		rows, err := tx.Query(ctx, `
			UPDATE staff_membership SET status = 'inactive', ended_reason = 'professional_deactivated', updated_at = $2
			WHERE professional_id = $1 AND status = 'active' RETURNING account_id`, profID, now)
		if err != nil {
			return idempotency.Result{}, err
		}
		accounts, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return idempotency.Result{}, err
		}
		if s.AccessRevoked != nil {
			if err := s.AccessRevoked(ctx, tx, accounts, profID, now); err != nil {
				return idempotency.Result{}, err
			}
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(actor.AccountID), Action: "T-PR-02", TargetType: "professional", TargetID: profID,
			FromState: "active", ToState: "inactive", Reason: reason, RequestID: rid}); err != nil {
			return idempotency.Result{}, err
		}
		return s.professionalResult(ctx, tx, estID, profID, http.StatusOK)
	})
}

// ActiveProfessionalIDs devolve os profissionais ativos da loja (usado pelo catálogo público).
func ActiveProfessionalIDs(ctx context.Context, q database.Querier, estID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := q.Query(ctx, `SELECT id FROM professional WHERE establishment_id = $1 AND status = 'active'`, estID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// ProfessionalsInEstablishment devolve quais dos ids pertencem à loja (qualquer situação).
func ProfessionalsInEstablishment(ctx context.Context, q database.Querier, estID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := q.Query(ctx, `SELECT id FROM professional WHERE establishment_id = $1 AND id = ANY($2)`, estID, ids)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(found))
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}
