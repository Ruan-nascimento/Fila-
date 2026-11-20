package estabelecimento

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// Papéis de equipe (StaffRole no contrato).
const (
	RoleOwner        = "owner"
	RoleProfessional = "professional"
)

// Erros do módulo.
var (
	ErrNotFound      = errors.New("loja ou profissional não encontrado")
	ErrSlugTaken     = errors.New("endereço (slug) já usado por outra loja")
	ErrAlreadyMember = errors.New("a conta já tem esse acesso nesta loja")
)

// Membership é um vínculo de equipe ativo numa loja ativa.
type Membership struct {
	EstablishmentID   uuid.UUID
	EstablishmentName string
	Role              string
	ProfessionalID    *uuid.UUID
}

// Public são os dados públicos de uma loja ativa.
type Public struct {
	ID       uuid.UUID
	Slug     string
	Name     string
	Timezone string
	// Identidade pública (ADR-0028): paleta do catálogo e informações publicadas pelo dono.
	Palette     string
	Description *string
	Address     *string
	Phone       *string
	LogoAssetID *uuid.UUID
}

const publicColumns = `id, slug, name, timezone, palette, public_description, public_address, public_phone, logo_asset_id`

// ActiveMemberships lista os vínculos ativos da conta em lojas ativas.
func ActiveMemberships(ctx context.Context, q database.Querier, accountID uuid.UUID) ([]Membership, error) {
	rows, err := q.Query(ctx, `
		SELECT m.establishment_id, e.name, m.role, m.professional_id
		FROM staff_membership m
		JOIN establishment e ON e.id = m.establishment_id
		LEFT JOIN professional p ON p.id = m.professional_id
		WHERE m.account_id = $1 AND m.status = 'active' AND e.status = 'active'
		  AND (m.professional_id IS NULL OR p.status = 'active')
		ORDER BY e.name, m.role`, accountID)
	if err != nil {
		return nil, fmt.Errorf("listar vínculos: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Membership, error) {
		var m Membership
		err := r.Scan(&m.EstablishmentID, &m.EstablishmentName, &m.Role, &m.ProfessionalID)
		return m, err
	})
}

// MembershipsIn filtra os vínculos ativos da conta numa loja.
func MembershipsIn(ctx context.Context, q database.Querier, accountID, establishmentID uuid.UUID) ([]Membership, error) {
	all, err := ActiveMemberships(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	var out []Membership
	for _, m := range all {
		if m.EstablishmentID == establishmentID {
			out = append(out, m)
		}
	}
	return out, nil
}

// PublicBySlug devolve a loja ativa pelo endereço público (link/QR). Endereços antigos (a loja
// trocou de nome) também valem: a loja volta com o endereço ATUAL em Slug, e quem chamou pode
// levar o navegador até ele. Assim QR Codes já impressos continuam funcionando.
func PublicBySlug(ctx context.Context, q database.Querier, slug string) (Public, error) {
	return scanPublic(q.QueryRow(ctx, `SELECT `+publicColumns+` FROM establishment
		WHERE status = 'active' AND (slug = $1 OR id = (SELECT establishment_id FROM establishment_slug_alias WHERE slug = $1))
		ORDER BY slug = $1 DESC LIMIT 1`, slug))
}

// slugUsedByOther diz se o endereço é (ou já foi) de OUTRA loja. Endereços antigos continuam
// reservados para a loja que os usou, para que um QR impresso nunca abra a loja errada.
func slugUsedByOther(ctx context.Context, q database.Querier, slug string, self uuid.UUID) (bool, error) {
	var used bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM establishment WHERE slug = $1 AND id <> $2)
		OR EXISTS (SELECT 1 FROM establishment_slug_alias WHERE slug = $1 AND establishment_id <> $2)`, slug, self).Scan(&used)
	return used, err
}

// ActiveByID devolve a loja ativa pelo identificador.
func ActiveByID(ctx context.Context, q database.Querier, id uuid.UUID) (Public, error) {
	return scanPublic(q.QueryRow(ctx, `SELECT `+publicColumns+` FROM establishment WHERE id = $1 AND status = 'active'`, id))
}

func scanPublic(row pgx.Row) (Public, error) {
	var p Public
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Timezone, &p.Palette, &p.Description, &p.Address, &p.Phone, &p.LogoAssetID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Public{}, ErrNotFound
		}
		return Public{}, fmt.Errorf("ler loja: %w", err)
	}
	return p, nil
}

// Create cria uma loja ativa (processo controlado do operador, ADR-0013).
func Create(ctx context.Context, q database.Querier, name, slug, timezone string, now time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	if used, err := slugUsedByOther(ctx, q, slug, uuid.Nil); err != nil {
		return uuid.Nil, fmt.Errorf("conferir endereço: %w", err)
	} else if used {
		return uuid.Nil, ErrSlugTaken
	}
	err := q.QueryRow(ctx, `
		INSERT INTO establishment (name, slug, timezone, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4) RETURNING id`, name, slug, timezone, now).Scan(&id)
	if database.IsUniqueViolation(err, "establishment_slug_key") {
		return uuid.Nil, ErrSlugTaken
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("criar loja: %w", err)
	}
	return id, nil
}

// ProfessionalAccess é a situação de um profissional para fins de convite.
type ProfessionalAccess struct {
	Active    bool // profissional ativo
	HasAccess bool // já tem conta com vínculo ativo
}

// LockProfessional trava o profissional da loja (FOR UPDATE) e informa a situação de acesso.
// Profissional de outra loja ou inexistente → ErrNotFound.
func LockProfessional(ctx context.Context, q database.Querier, establishmentID, professionalID uuid.UUID) (ProfessionalAccess, error) {
	var status string
	var pa ProfessionalAccess
	err := q.QueryRow(ctx, `
		SELECT p.status, EXISTS (SELECT 1 FROM staff_membership m WHERE m.professional_id = p.id AND m.status = 'active')
		FROM professional p WHERE p.establishment_id = $1 AND p.id = $2 FOR UPDATE OF p`,
		establishmentID, professionalID).Scan(&status, &pa.HasAccess)
	if errors.Is(err, pgx.ErrNoRows) {
		return pa, ErrNotFound
	}
	if err != nil {
		return pa, fmt.Errorf("ler profissional: %w", err)
	}
	pa.Active = status == "active"
	return pa, nil
}

// AddMembership cria um vínculo ativo. Devolve ErrAlreadyMember se a conta já tem o papel na
// loja ou se o profissional já tem conta com acesso (restrições únicas do banco).
func AddMembership(ctx context.Context, q database.Querier, establishmentID, accountID uuid.UUID, role string,
	professionalID *uuid.UUID, now time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO staff_membership (establishment_id, account_id, role, professional_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5) RETURNING id`,
		establishmentID, accountID, role, professionalID, now).Scan(&id)
	if database.IsUniqueViolation(err, "") {
		return uuid.Nil, ErrAlreadyMember
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("criar vínculo: %w", err)
	}
	return id, nil
}
