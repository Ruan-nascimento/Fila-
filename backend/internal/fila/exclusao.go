package fila

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
)

// DeletedNick substitui o nick nas participações depois da anonimização (o registro operacional
// continua, sem o dado pessoal).
const DeletedNick = "Cliente"

// CancelForDeletion (T-P-10): cancela as participações AGUARDANDO ou CHAMADAS da conta em todas as
// lojas, com a comanda encerrada sem cobrança (cancelled_by_account_deletion). Atendimento já
// iniciado continua com a equipe. Repetível: o que já foi cancelado não muda. Segue a ordem de
// travas fila → participação → comanda.
func (s *Service) CancelForDeletion(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, now time.Time) (int, error) {
	rows, err := tx.Query(ctx, `SELECT establishment_id, id FROM participation WHERE account_id = $1 AND status IN ('waiting', 'called')`, accountID)
	if err != nil {
		return 0, err
	}
	type ref struct{ est, id uuid.UUID }
	refs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ref, error) {
		var x ref
		err := r.Scan(&x.est, &x.id)
		return x, err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range refs {
		p, _, err := s.lockForCommand(ctx, tx, x.est, x.id)
		if err != nil {
			var he *hs.Error
			if errors.As(err, &he) && he.Status == 404 {
				continue
			}
			return n, err
		}
		if p.Status != Waiting && p.Status != Called {
			continue // começou o atendimento no meio do caminho: fica com a equipe
		}
		if _, err := tx.Exec(ctx, `UPDATE participation SET status = 'cancelled', finished_at = $2, version = version + 1, updated_at = $2 WHERE id = $1`,
			p.ID, now); err != nil {
			return n, err
		}
		audit := auditoria.Event{ActorKind: auditoria.ActorSystem, RequestID: hs.RequestIDFrom(ctx)}
		if err := atendimento.CloseNoCharge(ctx, tx, p.EstablishmentID, p.OrderID, atendimento.ClosedByAccountDeletion, now, audit); err != nil &&
			!errors.Is(err, atendimento.ErrNotOpen) {
			return n, err
		}
		audit.EstablishmentID, audit.OccurredAt, audit.Action = &p.EstablishmentID, now, "T-P-10"
		audit.TargetType, audit.TargetID, audit.FromState, audit.ToState = "participation", p.ID, p.Status, Cancelled
		if err := auditoria.Record(ctx, tx, audit); err != nil {
			return n, err
		}
		cur, err := load(ctx, tx, p.EstablishmentID, p.ID, false)
		if err != nil {
			return n, err
		}
		if err := s.recordParticipation(ctx, tx, cur); err != nil {
			return n, err
		}
		if err := s.QueueChanged(ctx, tx, p.EstablishmentID, p.QueueID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// InServiceEstablishments devolve as lojas onde a conta está em atendimento (bloqueio
// service_in_progress: quem resolve é a equipe).
func InServiceEstablishments(ctx context.Context, q database.Querier, accountID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT establishment_id FROM participation WHERE account_id = $1 AND status = 'in_service'`, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// BusyProfessionalQueues devolve as lojas em que a fila de algum destes profissionais ainda tem
// gente aguardando, chamada ou em atendimento (bloqueio professional_queue_has_participants).
func BusyProfessionalQueues(ctx context.Context, q database.Querier, professionalIDs []uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, pid := range professionalIDs {
		queueID, err := estabelecimento.QueueOfProfessional(ctx, q, pid)
		if errors.Is(err, estabelecimento.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var est *uuid.UUID
		err = q.QueryRow(ctx, `SELECT establishment_id FROM participation WHERE queue_id = $1 AND status = ANY($2) LIMIT 1`,
			queueID, activeStatuses).Scan(&est)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *est)
	}
	return out, nil
}

// AnonymizeParticipations troca o nick (cópia do nome da conta) nas participações da conta. O
// registro operacional continua com o pseudônimo (account_id). Repetível.
func AnonymizeParticipations(ctx context.Context, q database.Querier, accountID uuid.UUID) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE participation SET nick = $2 WHERE account_id = $1 AND nick <> $2`, accountID, DeletedNick)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// VisitorParticipation é uma visita de visitante já terminada (candidata a perder o nick).
type VisitorParticipation struct {
	ID      uuid.UUID
	OrderID uuid.UUID
}

// FinishedVisitorParticipations devolve visitas de visitante terminadas antes de `before` que
// ainda guardam o nick.
func FinishedVisitorParticipations(ctx context.Context, q database.Querier, before time.Time, limit int) ([]VisitorParticipation, error) {
	rows, err := q.Query(ctx, `
		SELECT id, order_id FROM participation
		WHERE visitor_id IS NOT NULL AND finished_at < $1 AND nick <> $2
		ORDER BY finished_at LIMIT $3`, before, VisitorNick, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (VisitorParticipation, error) {
		var v VisitorParticipation
		err := r.Scan(&v.ID, &v.OrderID)
		return v, err
	})
}

// VisitorNick substitui o nick do visitante nas participações antigas.
const VisitorNick = "Visitante"

// ScrubNicks troca o nick das participações indicadas por "Visitante".
func ScrubNicks(ctx context.Context, q database.Querier, ids []uuid.UUID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := q.Exec(ctx, `UPDATE participation SET nick = $2 WHERE id = ANY($1) AND nick <> $2`, ids, VisitorNick)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Statuses devolve o estado de cada participação (histórico do cliente).
func Statuses(ctx context.Context, q database.Querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, status FROM participation WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// ActiveInEstablishment conta pessoas aguardando, chamadas ou em atendimento na loja
// (encerramento da loja pelo operador).
func ActiveInEstablishment(ctx context.Context, q database.Querier, estID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM participation WHERE establishment_id = $1 AND status = ANY($2)`, estID, activeStatuses).Scan(&n)
	return n, err
}

// NicksOf devolve os nicks usados pela conta nas visitas (antes de anonimizar), para apagar o
// texto também dos eventos e respostas guardados da equipe.
func NicksOf(ctx context.Context, q database.Querier, accountID uuid.UUID) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT nick FROM participation WHERE account_id = $1 AND nick <> $2`, accountID, DeletedNick)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
