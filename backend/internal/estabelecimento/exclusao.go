package estabelecimento

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// SoleOwnerOf devolve as lojas ATIVAS em que a conta é a única dona ativa (R-EXC-06). Trava os
// vínculos de dono dessas lojas (FOR UPDATE, em ordem): se dois donos pedirem exclusão ao mesmo
// tempo, o segundo espera o primeiro e então se vê como único — a loja nunca fica sem dono.
func SoleOwnerOf(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.establishment_id FROM staff_membership m JOIN establishment e ON e.id = m.establishment_id
		WHERE m.account_id = $1 AND m.role = 'owner' AND m.status = 'active' AND e.status = 'active'
		ORDER BY m.establishment_id`, accountID)
	if err != nil {
		return nil, err
	}
	ests, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	var sole []uuid.UUID
	for _, est := range ests {
		rows, err := tx.Query(ctx, `
			SELECT account_id FROM staff_membership
			WHERE establishment_id = $1 AND role = 'owner' AND status = 'active'
			ORDER BY id FOR UPDATE`, est)
		if err != nil {
			return nil, err
		}
		owners, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, err
		}
		others := 0
		for _, o := range owners {
			if o != accountID {
				others++
			}
		}
		if others == 0 {
			sole = append(sole, est)
		}
	}
	return sole, nil
}

// EndedMembership é um vínculo encerrado pela exclusão da conta.
type EndedMembership struct {
	EstablishmentID uuid.UUID
	ProfessionalID  *uuid.UUID
}

// EndMembershipsForDeletion encerra todos os vínculos ativos da conta (dono e profissional) com
// motivo account_deletion. O profissional (e a fila dele) continua existindo sob o dono, que
// redistribui a operação (R-EXC-07).
func EndMembershipsForDeletion(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, now time.Time) ([]EndedMembership, error) {
	rows, err := tx.Query(ctx, `
		UPDATE staff_membership SET status = 'inactive', ended_reason = 'account_deletion', updated_at = $2
		WHERE account_id = $1 AND status = 'active'
		RETURNING establishment_id, professional_id`, accountID, now)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (EndedMembership, error) {
		var m EndedMembership
		err := r.Scan(&m.EstablishmentID, &m.ProfessionalID)
		return m, err
	})
}

// ProfessionalNames devolve o nome de exibição dos profissionais (histórico do cliente).
func ProfessionalNames(ctx context.Context, q database.Querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, display_name FROM professional WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// EstablishmentExists informa se a loja existe (ativa ou encerrada).
func EstablishmentExists(ctx context.Context, q database.Querier, estID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM establishment WHERE id = $1)`, estID).Scan(&ok)
	return ok, err
}

// CloseEstablishment encerra a loja (T-OP-02, operador). Só sem pessoas na fila e sem comanda
// pendente — quem confere isso é o chamador (pending > 0 → recusa). Fecha também as filas.
func CloseEstablishment(ctx context.Context, tx pgx.Tx, estID uuid.UUID, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE establishment SET status = 'closed', version = version + 1, updated_at = $2
		WHERE id = $1 AND status = 'active'`, estID, now)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE queue SET status = 'closed', version = version + 1, updated_at = $2
		WHERE establishment_id = $1 AND status <> 'closed'`, estID, now)
	return true, err
}

// BySlug devolve o id da loja pelo endereço (operador).
func BySlug(ctx context.Context, q database.Querier, slug string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var status string
	err := q.QueryRow(ctx, `SELECT id, status FROM establishment WHERE slug = $1`, slug).Scan(&id, &status)
	if err == pgx.ErrNoRows {
		return uuid.Nil, "", ErrNotFound
	}
	return id, status, err
}

// LockAllQueues trava todas as filas da loja (em ordem): nenhuma entrada ou chamada acontece
// enquanto o operador confere e encerra a loja.
func LockAllQueues(ctx context.Context, tx pgx.Tx, estID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT id FROM queue WHERE establishment_id = $1 ORDER BY id FOR UPDATE`, estID)
	return err
}
