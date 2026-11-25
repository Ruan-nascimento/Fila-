package agenda

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
)

// Tarefas do worker (T-AP-07 e T-AP-08). Cada rodada lê candidatos SEM trava e processa cada um
// numa transação própria, na ordem fila → participação → reserva; tudo é reconferido sob trava,
// então rodar duas vezes (ou em dois workers, ou depois de reiniciar) não muda o resultado. O prazo
// é decidido pelo relógio do servidor no momento do processamento: um worker atrasado não estende
// prazo de ninguém, e uma reserva com presença confirmada nunca vira falta.

const batch = 50

// Run executa as tarefas a cada intervalo até ctx terminar.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("tarefas da agenda falharam", "error", err.Error())
		} else if n > 0 {
			s.Log.Info("agenda: reservas atualizadas", "quantidade", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick faz uma rodada: faltas de chamadas vencidas e "atraso da loja" de horários não chamados.
func (s *Service) Tick(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	done := 0
	// 1. Falta: chamada válida, prazo vencido (no instante exato do prazo já venceu), sem presença.
	rows, err := s.Pool.Query(ctx, `SELECT establishment_id, participation_id FROM appointment
		WHERE status = 'called' AND deadline_at <= $1 AND arrived_at IS NULL ORDER BY deadline_at LIMIT $2`, now, batch)
	if err != nil {
		return 0, err
	}
	type cand struct{ est, part uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cand, error) {
		var c cand
		err := r.Scan(&c.est, &c.part)
		return c, err
	})
	if err != nil {
		return 0, err
	}
	for _, c := range list {
		var changed bool
		err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
			var err error
			changed, err = s.Visits.EndScheduledNoShow(ctx, tx, c.est, c.part)
			return err
		})
		if err != nil {
			return done, err
		}
		if changed {
			done++
		}
	}
	// 2. Atraso da loja: horário confirmado, tolerância passou e a loja não chamou. Não é falta do
	// cliente: a reserva fica sinalizada para a equipe resolver (chamar, remarcar ou cancelar).
	rows, err = s.Pool.Query(ctx, `SELECT id, establishment_id, professional_id FROM appointment
		WHERE status = 'confirmed' AND store_late_at IS NULL
		  AND starts_at + make_interval(mins => tolerance_minutes) <= $1 ORDER BY starts_at LIMIT $2`, now, batch)
	if err != nil {
		return done, err
	}
	type late struct{ id, est, prof uuid.UUID }
	lates, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (late, error) {
		var l late
		err := r.Scan(&l.id, &l.est, &l.prof)
		return l, err
	})
	if err != nil {
		return done, err
	}
	for _, l := range lates {
		err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
			queueID, err := estabelecimento.QueueOfProfessional(ctx, tx, l.prof)
			if err != nil {
				return err
			}
			if _, err := estabelecimento.LockQueues(ctx, tx, l.est, queueID); err != nil {
				return err
			}
			a, err := loadAppointment(ctx, tx, l.est, l.id, true)
			if err != nil {
				return err
			}
			at := s.Clock.Now()
			if a.Status != StatusConfirmed || a.StoreLateAt != nil || at.Before(a.StartsAt.Add(time.Duration(a.Tolerance)*time.Minute)) {
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE appointment SET store_late_at = $2, version = version + 1, updated_at = $2 WHERE id = $1`, a.ID, at); err != nil {
				return err
			}
			done++
			return s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-08", a.Status, actorInfo{kind: "system"}, "loja não chamou no horário", at)
		})
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// FutureAppointmentsOf conta as reservas confirmadas ou em curso de um profissional que ainda não
// terminaram (desativar o profissional exige remanejá-las antes).
func FutureAppointmentsOf(ctx context.Context, q database.Querier, professionalID uuid.UUID, now time.Time) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM appointment WHERE professional_id = $1
		AND status IN ('confirmed', 'called', 'in_service') AND ends_at > $2`, professionalID, now).Scan(&n)
	return n, err
}

// FutureAppointments conta as reservas confirmadas que ainda vão acontecer (mostrado ao dono antes
// de mudar a forma de atendimento).
func FutureAppointments(ctx context.Context, q database.Querier, estID uuid.UUID, now time.Time) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM appointment WHERE establishment_id = $1 AND status = 'confirmed' AND ends_at > $2`,
		estID, now).Scan(&n)
	return n, err
}
