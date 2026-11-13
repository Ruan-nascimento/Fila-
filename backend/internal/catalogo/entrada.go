package catalogo

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// Snapshot é a cópia do serviço no momento em que entra numa comanda (R-CMD-05).
type Snapshot struct {
	ID              uuid.UUID
	Name            string
	PriceCents      int64
	DurationMinutes int
	Points          int
	Active          bool
	ProfessionalIDs []uuid.UUID
}

// Enabled informa se o profissional está habilitado para o serviço.
func (s Snapshot) Enabled(professionalID uuid.UUID) bool {
	return slices.Contains(s.ProfessionalIDs, professionalID)
}

// ServicesForEntry lê os serviços DESTA loja com bloqueio compartilhado (FOR SHARE): uma
// alteração de preço ou desativação simultânea espera a entrada terminar (ou vice-versa), então
// o snapshot gravado é sempre uma versão real do catálogo. Serviços de outra loja não voltam.
func ServicesForEntry(ctx context.Context, q database.Querier, estID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Snapshot, error) {
	rows, err := q.Query(ctx, `
		SELECT s.id, s.name, s.price_cents, s.duration_minutes, s.points, s.status = 'active',
		       coalesce((SELECT array_agg(sp.professional_id) FROM service_professional sp WHERE sp.service_id = s.id), '{}')
		FROM service s WHERE s.establishment_id = $1 AND s.id = ANY($2)
		ORDER BY s.id
		FOR SHARE OF s`, estID, ids)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Snapshot, error) {
		var s Snapshot
		err := r.Scan(&s.ID, &s.Name, &s.PriceCents, &s.DurationMinutes, &s.Points, &s.Active, &s.ProfessionalIDs)
		return s, err
	})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]Snapshot, len(list))
	for _, s := range list {
		out[s.ID] = s
	}
	return out, nil
}

// EnabledServiceIDs devolve os serviços ativos habilitados para o profissional (transferência).
func EnabledServiceIDs(ctx context.Context, q database.Querier, estID, professionalID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := q.Query(ctx, `
		SELECT sp.service_id FROM service_professional sp JOIN service s ON s.id = sp.service_id
		WHERE sp.establishment_id = $1 AND sp.professional_id = $2`, estID, professionalID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
