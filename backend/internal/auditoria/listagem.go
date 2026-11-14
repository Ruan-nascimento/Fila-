package auditoria

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	hs "fila/internal/platform/httpserver"
)

// Service atende a consulta do registro de auditoria da loja (só o dono).
type Service struct {
	Pool *pgxpool.Pool
	Auth acesso.Authorizer
}

// Routes devolve a operação do contrato.
func (s *Service) Routes() []hs.Route {
	return []hs.Route{{Operation: "listAuditEvents", Method: http.MethodGet, Path: "/v1/establishments/{establishmentId}/audit-events",
		Grants: []hs.Grant{hs.GrantOwner}, Handler: s.listAuditEvents}}
}

type eventView struct {
	ID         uuid.UUID `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Actor      struct {
		Kind string     `json:"kind"`
		ID   *uuid.UUID `json:"id"`
	} `json:"actor"`
	Action string `json:"action"`
	Target struct {
		Type string    `json:"type"`
		ID   uuid.UUID `json:"id"`
	} `json:"target"`
	FromState *string `json:"from_state"`
	ToState   *string `json:"to_state"`
	Reason    *string `json:"reason"`
	RequestID *string `json:"request_id"`
}

// listAuditEvents: eventos DESTA loja, mais recentes primeiro. Atores e alvos só por pseudônimo
// (ids internos); nunca e-mail, nome ou nick (o registro não guarda esses dados).
func (s *Service) listAuditEvents(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return hs.ErrNotFound()
	}
	if _, err := s.Auth.Owner(r, estID); err != nil {
		return err
	}
	q := r.URL.Query()
	var fe []hs.FieldError
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			fe = append(fe, hs.FieldError{Field: "limit", Code: hs.FieldOutOfRange})
		}
		limit = n
	}
	var afterAt time.Time
	var afterID uuid.UUID
	cursor := q.Get("cursor")
	if cursor != "" {
		ok := false
		if raw, err := base64.RawURLEncoding.DecodeString(cursor); err == nil && len(cursor) <= 512 {
			if at, id, cut := strings.Cut(string(raw), "|"); cut {
				t, e1 := time.Parse(time.RFC3339Nano, at)
				u, e2 := uuid.Parse(id)
				afterAt, afterID, ok = t, u, e1 == nil && e2 == nil
			}
		}
		if !ok {
			fe = append(fe, hs.FieldError{Field: "cursor", Code: hs.FieldInvalidFormat})
		}
	}
	if len(fe) > 0 {
		return hs.ErrValidation(fe...)
	}
	sql := `SELECT id, occurred_at, actor_kind, actor_id, action, target_type, target_id, from_state, to_state, reason, request_id
		FROM audit_event WHERE establishment_id = $1`
	args := []any{estID}
	if cursor != "" {
		sql += ` AND (occurred_at, id) < ($2, $3)`
		args = append(args, afterAt, afterID)
	}
	args = append(args, limit+1)
	sql += ` ORDER BY occurred_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (eventView, error) {
		var v eventView
		err := row.Scan(&v.ID, &v.OccurredAt, &v.Actor.Kind, &v.Actor.ID, &v.Action, &v.Target.Type, &v.Target.ID,
			&v.FromState, &v.ToState, &v.Reason, &v.RequestID)
		return v, err
	})
	if err != nil {
		return err
	}
	page := struct {
		NextCursor *string `json:"next_cursor"`
		HasMore    bool    `json:"has_more"`
	}{}
	if len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		c := base64.RawURLEncoding.EncodeToString([]byte(last.OccurredAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID.String()))
		page.NextCursor, page.HasMore = &c, true
	}
	if list == nil {
		list = []eventView{}
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []eventView `json:"data"`
		Page any         `json:"page"`
	}{list, page})
	return nil
}
