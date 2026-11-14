// Package operacao é o módulo de operação (etapa 12, ADR-0026): a trava do worker
// (manutenção e restauração de backup), o vínculo do estado com o banco em uso e o sinal de vida
// do worker. É dono da tabela operation_state.
//
// Por que a trava existe: depois de restaurar um backup, o banco "volta no tempo", mas o mundo lá
// fora não volta (Pix pagos, estornos feitos, e-mails enviados, exclusões prometidas). Se o worker
// voltasse a rodar direto, ele agiria sobre o passado: por exemplo, venceria reservas, estornaria
// de novo, reenviaria e-mails. Por isso o worker só trabalha com a trava solta, e a trava só é
// solta depois da conciliação (fila-admin restauracao).
//
// Detecção automática: o estado guarda o oid do banco em que foi gravado. Uma restauração lógica
// (pg_restore num banco novo) cria um banco com OUTRO oid, então o worker percebe sozinho que o
// banco foi restaurado e trava, mesmo se o operador esquecer o passo manual. Uma restauração
// física (cópia de arquivos do PostgreSQL) mantém o oid: nesse caso o procedimento manda travar
// explicitamente (docs/operacao/backup-restauracao.md).
package operacao

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/auditoria"
	"fila/internal/platform/database"
)

// audit registra a ação de operação (T-OP-03 manutenção, T-OP-04 restauração). O alvo é o estado
// de operação (linha única, id nulo). Motivo digitado pelo operador; nunca dado pessoal.
func audit(ctx context.Context, q database.Querier, actor, action, from, to, reason string, now time.Time) error {
	return auditoria.Record(ctx, q, auditoria.Event{OccurredAt: now, ActorKind: actor, Action: action,
		TargetType: "operation_state", TargetID: uuid.Nil, FromState: from, ToState: to, Reason: reason})
}

func stateName(s State) string {
	if r := reason(s); r != "" {
		return "held:" + r
	}
	return "running"
}

// Motivos de trava.
const (
	HoldMaintenance     = "maintenance"      // manutenção planejada (operador)
	HoldRestore         = "restore"          // restauração declarada pelo operador
	HoldRestoreDetected = "restore_detected" // o worker percebeu banco restaurado/copiado
)

// State é a linha de operation_state.
type State struct {
	BoundOID      *uint32
	CurrentOID    uint32
	HoldReason    *string
	HoldNote      *string
	HoldSince     *time.Time
	ReconciledAt  *time.Time
	WorkerSeenAt  *time.Time
	UpdatedAt     time.Time
	BoundMismatch bool // o estado foi gravado em outro banco (restauração/cópia)
}

// Held informa se o worker deve ficar parado.
func (s State) Held() bool { return s.HoldReason != nil || s.BoundMismatch }

// RestoreHold informa se a trava é de restauração (exige conciliação antes de soltar).
func (s State) RestoreHold() bool {
	return s.BoundMismatch || (s.HoldReason != nil && (*s.HoldReason == HoldRestore || *s.HoldReason == HoldRestoreDetected))
}

// Read lê o estado (sem alterar nada).
func Read(ctx context.Context, q database.Querier) (State, error) {
	var s State
	var bound *uint32
	err := q.QueryRow(ctx, `
		SELECT bound_database_oid, (SELECT oid FROM pg_database WHERE datname = current_database()),
		       hold_reason, hold_note, hold_since, reconciled_at, worker_seen_at, updated_at
		FROM operation_state WHERE singleton`).
		Scan(&bound, &s.CurrentOID, &s.HoldReason, &s.HoldNote, &s.HoldSince, &s.ReconciledAt, &s.WorkerSeenAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, errors.New("operation_state sem a linha única (migração 00016 incompleta?)")
	}
	if err != nil {
		return s, err
	}
	s.BoundOID = bound
	s.BoundMismatch = bound != nil && *bound != s.CurrentOID
	return s, nil
}

// Gate é chamado pelo worker: vincula o estado ao banco na primeira vez, transforma uma
// divergência de banco em trava gravada (restore_detected) e devolve o estado atual. Também
// registra o sinal de vida do worker quando ele está liberado.
func Gate(ctx context.Context, pool *pgxpool.Pool, now time.Time) (State, error) {
	var out State
	err := database.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM operation_state WHERE singleton FOR UPDATE`); err != nil {
			return err
		}
		s, err := Read(ctx, tx)
		if err != nil {
			return err
		}
		switch {
		case s.BoundOID == nil:
			if _, err := tx.Exec(ctx, `UPDATE operation_state SET bound_database_oid = $1, updated_at = $2 WHERE singleton`,
				s.CurrentOID, now); err != nil {
				return err
			}
			oid := s.CurrentOID
			s.BoundOID = &oid
		case s.BoundMismatch && (s.HoldReason == nil || *s.HoldReason == HoldMaintenance):
			// Banco restaurado ou copiado: trava e exige conciliação antes de qualquer tarefa.
			reason, note := HoldRestoreDetected, "banco diferente do vinculado (restauração ou cópia detectada)"
			if _, err := tx.Exec(ctx, `
				UPDATE operation_state SET hold_reason = $1, hold_note = $2, hold_since = $3, reconciled_at = NULL, updated_at = $3
				WHERE singleton`, reason, note, now); err != nil {
				return err
			}
			s.HoldReason, s.HoldNote, s.HoldSince, s.ReconciledAt = &reason, &note, &now, nil
			if err := audit(ctx, tx, auditoria.ActorSystem, "T-OP-04", "running", "held:"+reason, note, now); err != nil {
				return err
			}
		}
		if !s.Held() {
			if _, err := tx.Exec(ctx, `UPDATE operation_state SET worker_seen_at = $1 WHERE singleton`, now); err != nil {
				return err
			}
			s.WorkerSeenAt = &now
		}
		out = s
		return nil
	})
	return out, err
}

// ErrNotHeld: não há trava para soltar / conciliação sem trava de restauração.
var ErrNotHeld = errors.New("o worker não está travado")

// ErrNotReconciled: trava de restauração só sai depois da conciliação.
var ErrNotReconciled = errors.New("trava de restauração: rode a conciliação (fila-admin restauracao reconciliar) antes de liberar")

// Hold trava o worker (manutenção ou restauração declarada). Repetível: com trava já ativa só
// atualiza a nota (uma trava de restauração nunca vira manutenção).
func Hold(ctx context.Context, pool *pgxpool.Pool, reason, note string, now time.Time) (State, error) {
	if reason != HoldMaintenance && reason != HoldRestore {
		return State{}, fmt.Errorf("motivo de trava inválido: %s", reason)
	}
	var out State
	err := database.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM operation_state WHERE singleton FOR UPDATE`); err != nil {
			return err
		}
		s, err := Read(ctx, tx)
		if err != nil {
			return err
		}
		since, reconciled := now, (*time.Time)(nil)
		switch {
		case reason == HoldRestore:
			// Restauração (de novo ou pela primeira vez): toda conciliação anterior deixa de valer.
		case s.RestoreHold():
			// Pedir manutenção não "rebaixa" uma trava de restauração.
			reason = HoldRestoreDetected
			if s.HoldReason != nil {
				reason = *s.HoldReason
			}
			since, reconciled = now, s.ReconciledAt
			if s.HoldSince != nil {
				since = *s.HoldSince
			}
		case s.HoldSince != nil:
			since = *s.HoldSince // manutenção repetida: mantém o início
		}
		if _, err := tx.Exec(ctx, `
			UPDATE operation_state SET hold_reason = $1, hold_note = $2, hold_since = $3, reconciled_at = $4, updated_at = $5
			WHERE singleton`, reason, note, since, reconciled, now); err != nil {
			return err
		}
		action := "T-OP-03"
		if reason != HoldMaintenance {
			action = "T-OP-04"
		}
		if err := audit(ctx, tx, auditoria.ActorOperator, action, stateName(s), "held:"+reason, note, now); err != nil {
			return err
		}
		out, err = Read(ctx, tx)
		return err
	})
	return out, err
}

// MarkReconciled registra que a conciliação pós-restauração terminou sem erro (summary: resumo
// só com contagens, vai para a auditoria).
func MarkReconciled(ctx context.Context, q database.Querier, summary string, now time.Time) error {
	tag, err := q.Exec(ctx, `UPDATE operation_state SET reconciled_at = $1, updated_at = $1
		WHERE singleton AND hold_reason IN ('restore', 'restore_detected')`, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotHeld
	}
	return audit(ctx, q, auditoria.ActorOperator, "T-OP-04", "held:restore", "reconciled", summary, now)
}

// Release solta a trava. Trava de restauração exige conciliação concluída depois da trava. O
// estado passa a ser vinculado ao banco atual.
func Release(ctx context.Context, pool *pgxpool.Pool, now time.Time) (State, error) {
	var out State
	err := database.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM operation_state WHERE singleton FOR UPDATE`); err != nil {
			return err
		}
		s, err := Read(ctx, tx)
		if err != nil {
			return err
		}
		if !s.Held() {
			return ErrNotHeld
		}
		if s.RestoreHold() && (s.ReconciledAt == nil || s.HoldSince == nil || s.ReconciledAt.Before(*s.HoldSince)) {
			return ErrNotReconciled
		}
		if _, err := tx.Exec(ctx, `
			UPDATE operation_state SET hold_reason = NULL, hold_note = NULL, hold_since = NULL, bound_database_oid = $1, updated_at = $2
			WHERE singleton`, s.CurrentOID, now); err != nil {
			return err
		}
		action := "T-OP-03"
		if s.RestoreHold() {
			action = "T-OP-04"
		}
		if err := audit(ctx, tx, auditoria.ActorOperator, action, stateName(s), "running", "", now); err != nil {
			return err
		}
		out, err = Read(ctx, tx)
		return err
	})
	return out, err
}
