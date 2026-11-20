package estabelecimento

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // fusos IANA embutidos (o Windows não traz a base)

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/midia"
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
	// Identidade e informações públicas (ADR-0028).
	Palette           string          `json:"palette"`
	PublicDescription *string         `json:"public_description"`
	PublicAddress     *string         `json:"public_address"`
	PublicPhone       *string         `json:"public_phone"`
	Logo              *midia.ImageRef `json:"logo"`
	// PublicURL: link canônico da loja (origem pública configurada + slug), usado no QR Code.
	PublicURL string `json:"public_url"`

	logoID *uuid.UUID
}

func (s *Service) loadEstablishment(ctx context.Context, q database.Querier, id uuid.UUID, lock bool) (establishmentView, error) {
	return loadEstablishmentWith(ctx, q, id, lock, s.PublicBaseURL)
}

func loadEstablishmentWith(ctx context.Context, q database.Querier, id uuid.UUID, lock bool, base string) (establishmentView, error) {
	sql := `SELECT id, name, slug, timezone, version, palette, public_description, public_address, public_phone, logo_asset_id
		FROM establishment WHERE id = $1 AND status = 'active'`
	if lock {
		sql += ` FOR UPDATE`
	}
	var v establishmentView
	err := q.QueryRow(ctx, sql, id).Scan(&v.ID, &v.Name, &v.Slug, &v.Timezone, &v.Version, &v.Palette,
		&v.PublicDescription, &v.PublicAddress, &v.PublicPhone, &v.logoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, httpserver.ErrNotFound()
	}
	if err != nil {
		return v, err
	}
	logo, err := midia.AttachedOne(ctx, q, v.logoID, "public")
	if err != nil {
		return v, err
	}
	if logo != nil {
		v.Logo = logo.PublicRef()
	}
	if base != "" {
		v.PublicURL = base + "/loja/" + v.Slug
	}
	return v, nil
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
	v, err := s.loadEstablishment(r.Context(), s.Pool, estID, false)
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[establishmentView]{Data: v})
	return nil
}

type updateEstablishmentRequest struct {
	ExpectedVersion   *int64  `json:"expected_version"`
	Name              *string `json:"name"`
	Slug              *string `json:"slug"`
	Timezone          *string `json:"timezone"`
	Palette           *string `json:"palette"`
	PublicDescription *string `json:"public_description"`
	PublicAddress     *string `json:"public_address"`
	PublicPhone       *string `json:"public_phone"`
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
	if req.Palette != nil && !ValidPalette(*req.Palette) {
		v.Add(&httpserver.FieldError{Field: "/palette", Code: httpserver.FieldNotAllowed, Message: "escolha uma das 16 paletas do catálogo"})
	}
	desc, fe := optionalText("/public_description", req.PublicDescription, 280)
	v.Add(fe)
	addr, fe := optionalText("/public_address", req.PublicAddress, 200)
	v.Add(fe)
	var phone *string
	if req.PublicPhone != nil {
		var fe *httpserver.FieldError
		phone, fe = NormalizePhone("/public_phone", *req.PublicPhone)
		v.Add(fe)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "updateEstablishment", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := s.loadEstablishment(ctx, tx, estID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		next := cur
		var changed []string
		if req.Name != nil && name != cur.Name {
			next.Name = name
			changed = append(changed, "name")
		}
		if req.Slug != nil && *req.Slug != cur.Slug {
			next.Slug = *req.Slug
			changed = append(changed, "slug")
		}
		if req.Timezone != nil && *req.Timezone != cur.Timezone {
			next.Timezone = *req.Timezone
			changed = append(changed, "timezone")
		}
		if req.Palette != nil && *req.Palette != cur.Palette {
			next.Palette = *req.Palette
			changed = append(changed, "palette")
		}
		if req.PublicDescription != nil && !sameText(desc, cur.PublicDescription) {
			next.PublicDescription = desc
			changed = append(changed, "public_description")
		}
		if req.PublicAddress != nil && !sameText(addr, cur.PublicAddress) {
			next.PublicAddress = addr
			changed = append(changed, "public_address")
		}
		if req.PublicPhone != nil && !sameText(phone, cur.PublicPhone) {
			next.PublicPhone = phone
			changed = append(changed, "public_phone")
		}
		now := s.Clock.Now()
		err = tx.QueryRow(ctx, `
			UPDATE establishment SET name = $2, slug = $3, timezone = $4, palette = $5, public_description = $6,
			       public_address = $7, public_phone = $8, version = version + 1, updated_at = $9
			WHERE id = $1 RETURNING version`, estID, next.Name, next.Slug, next.Timezone, next.Palette,
			next.PublicDescription, next.PublicAddress, next.PublicPhone, now).Scan(&next.Version)
		if database.IsUniqueViolation(err, "establishment_slug_key") {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "slug_taken", Title: "Endereço já usado",
				Detail: "Outra loja já usa esse endereço. Escolha outro.", Current: cur}
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		if next.Slug != cur.Slug && s.PublicBaseURL != "" {
			next.PublicURL = s.PublicBaseURL + "/loja/" + next.Slug
		}
		// Auditoria com os NOMES dos campos alterados (nunca os valores: telefone e endereço são públicos,
		// mas a auditoria não guarda conteúdo).
		reason := ""
		if len(changed) > 0 {
			reason = "campos: " + strings.Join(changed, ", ")
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorOwner, ActorID: auditoria.Ptr(actor.AccountID), Action: "T-ES-01",
			TargetType: "establishment", TargetID: estID, Reason: reason, RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[establishmentView]{Data: next}}, nil
	})
}
