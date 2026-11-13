package estabelecimento

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"
	_ "time/tzdata" // fusos IANA embutidos (o Windows não traz a base)

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// SlugPattern é o formato do endereço público da loja (contrato: Slug).
var SlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,48}[a-z0-9])$`)

// ValidTimezone confere um fuso IANA (ex.: America/Sao_Paulo).
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" || len(tz) > 64 {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

type establishmentView struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Slug     string    `json:"slug"`
	Timezone string    `json:"timezone"`
	Version  int64     `json:"version"`
}

func loadEstablishment(ctx context.Context, q database.Querier, id uuid.UUID, lock bool) (establishmentView, error) {
	sql := `SELECT id, name, slug, timezone, version FROM establishment WHERE id = $1 AND status = 'active'`
	if lock {
		sql += ` FOR UPDATE`
	}
	var v establishmentView
	err := q.QueryRow(ctx, sql, id).Scan(&v.ID, &v.Name, &v.Slug, &v.Timezone, &v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, httpserver.ErrNotFound()
	}
	return v, err
}

// getEstablishment: equipe da loja (dono ou profissional).
func (s *Service) getEstablishment(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Member(r, estID); err != nil {
		return err
	}
	v, err := loadEstablishment(r.Context(), s.Pool, estID, false)
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[establishmentView]{Data: v})
	return nil
}

type updateEstablishmentRequest struct {
	ExpectedVersion *int64  `json:"expected_version"`
	Name            *string `json:"name"`
	Slug            *string `json:"slug"`
	Timezone        *string `json:"timezone"`
}

// updateEstablishment (T-ES-01): dono; nome, slug e fuso; versão esperada.
func (s *Service) updateEstablishment(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	var req updateEstablishmentRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	var name string
	if req.Name != nil {
		n, fe := validate.Text("/name", *req.Name, 1, 80)
		v.Add(fe)
		name = n
	}
	if req.Slug != nil && !SlugPattern.MatchString(*req.Slug) {
		v.Add(&httpserver.FieldError{Field: "/slug", Code: httpserver.FieldInvalidFormat,
			Message: "use de 3 a 50 letras minúsculas, números e hífens"})
	}
	if req.Timezone != nil && !ValidTimezone(*req.Timezone) {
		v.Add(&httpserver.FieldError{Field: "/timezone", Code: httpserver.FieldInvalidFormat, Message: "fuso IANA, ex.: America/Sao_Paulo"})
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "updateEstablishment", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadEstablishment(ctx, tx, estID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		next := cur
		if req.Name != nil {
			next.Name = name
		}
		if req.Slug != nil {
			next.Slug = *req.Slug
		}
		if req.Timezone != nil {
			next.Timezone = *req.Timezone
		}
		now := s.Clock.Now()
		err = tx.QueryRow(ctx, `
			UPDATE establishment SET name = $2, slug = $3, timezone = $4, version = version + 1, updated_at = $5
			WHERE id = $1 RETURNING version`, estID, next.Name, next.Slug, next.Timezone, now).Scan(&next.Version)
		if database.IsUniqueViolation(err, "establishment_slug_key") {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "slug_taken", Title: "Endereço já usado",
				Detail: "Outra loja já usa esse endereço. Escolha outro.", Current: cur}
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorOwner, ActorID: auditoria.Ptr(actor.AccountID), Action: "T-ES-01",
			TargetType: "establishment", TargetID: estID, RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[establishmentView]{Data: next}}, nil
	})
}
