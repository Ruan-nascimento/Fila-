package atendimento

import (
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	hs "fila/internal/platform/httpserver"
)

// getOrder: dono da visita, profissional do atendimento ou dono da loja.
func (s *Service) getOrder(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	orderID, err := pathID(r, "orderId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	o, err := getOrder(ctx, s.Pool, estID, orderID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return hs.ErrNotFound()
	}
	if err != nil {
		return err
	}
	if _, err := s.authorize(r, o, allow{customer: true, professional: true, owner: true}); err != nil {
		return err
	}
	view, err := LoadOrder(ctx, s.Pool, estID, orderID)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[OrderView]{Data: view})
	return nil
}

var orderStatuses = []string{StatusOpen, StatusPendingReview, StatusAwaitingPayment, "awaiting_confirmation", "settled", StatusClosedNoCharge}

type pageInfo struct {
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// cursor de listagem: posição (created_at, id) da última comanda entregue, em base64url. Não dá
// acesso a nada — o filtro de loja e de profissional é sempre aplicado pelo servidor.
func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	t, err1 := time.Parse(time.RFC3339Nano, at)
	u, err2 := uuid.Parse(id)
	return t, u, err1 == nil && err2 == nil
}

// listOrders: dono vê as comandas da loja; profissional, as dos seus atendimentos
// (own_attendance). Mais recentes primeiro, com filtro de estado e paginação por cursor.
func (s *Service) listOrders(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	var fe []hs.FieldError
	var statuses []string
	if raw := q.Get("status"); raw != "" {
		for _, st := range strings.Split(raw, ",") {
			if !slices.Contains(orderStatuses, st) {
				fe = append(fe, hs.FieldError{Field: "status", Code: hs.FieldNotAllowed})
				break
			}
			statuses = append(statuses, st)
		}
	}
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			fe = append(fe, hs.FieldError{Field: "limit", Code: hs.FieldOutOfRange})
		}
		limit = n
	}
	var afterAt time.Time
	var afterID uuid.UUID
	hasCursor := false
	if raw := q.Get("cursor"); raw != "" {
		var ok bool
		afterAt, afterID, ok = decodeCursor(raw)
		if !ok || len(raw) > 512 {
			fe = append(fe, hs.FieldError{Field: "cursor", Code: hs.FieldInvalidFormat})
		}
		hasCursor = true
	}
	if len(fe) > 0 {
		return hs.ErrValidation(fe...)
	}
	sql := `SELECT id, created_at FROM service_order WHERE establishment_id = $1`
	args := []any{estID}
	if !actor.IsOwner() {
		if actor.ProfessionalID == nil {
			return hs.ErrForbidden("Somente a equipe da loja pode ver as comandas.")
		}
		args = append(args, *actor.ProfessionalID)
		sql += ` AND professional_id = $` + strconv.Itoa(len(args))
	}
	if len(statuses) > 0 {
		args = append(args, statuses)
		sql += ` AND status = ANY($` + strconv.Itoa(len(args)) + `)`
	}
	if hasCursor {
		args = append(args, afterAt, afterID)
		sql += ` AND (created_at, id) < ($` + strconv.Itoa(len(args)-1) + `, $` + strconv.Itoa(len(args)) + `)`
	}
	args = append(args, limit+1)
	sql += ` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))
	ctx := r.Context()
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	type pos struct {
		id uuid.UUID
		at time.Time
	}
	found, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pos, error) {
		var p pos
		err := row.Scan(&p.id, &p.at)
		return p, err
	})
	if err != nil {
		return err
	}
	page := pageInfo{}
	if len(found) > limit {
		found = found[:limit]
		last := found[len(found)-1]
		c := encodeCursor(last.at, last.id)
		page = pageInfo{NextCursor: &c, HasMore: true}
	}
	data := make([]OrderView, 0, len(found))
	for _, p := range found {
		v, err := LoadOrder(ctx, s.Pool, estID, p.id)
		if err != nil {
			return err
		}
		data = append(data, v)
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []OrderView `json:"data"`
		Page pageInfo    `json:"page"`
	}{Data: data, Page: page})
	return nil
}
