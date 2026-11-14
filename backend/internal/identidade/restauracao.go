package identidade

import (
	"context"
	"time"

	"fila/internal/platform/database"
)

// RevokeAllAfterRestore encerra TODAS as sessões de conta e invalida os links de e-mail ainda não
// usados (verificação, redefinição de senha) depois de restaurar um backup. Motivo: o banco
// restaurado "esquece" saídas, trocas de senha e links já usados depois do backup — uma sessão
// encerrada ou um link consumido voltariam a valer. Todos entram de novo; quem precisar pede
// outro link. Repetível.
func RevokeAllAfterRestore(ctx context.Context, q database.Querier, now time.Time) (sessions, tokens int64, err error) {
	tag, err := q.Exec(ctx, `UPDATE account_session SET revoked_at = $1, revoke_reason = 'restore' WHERE revoked_at IS NULL`, now)
	if err != nil {
		return 0, 0, err
	}
	sessions = tag.RowsAffected()
	tag, err = q.Exec(ctx, `UPDATE account_token SET revoked_at = $1 WHERE consumed_at IS NULL AND revoked_at IS NULL`, now)
	if err != nil {
		return sessions, 0, err
	}
	return sessions, tag.RowsAffected(), notifyRevocation(ctx, q)
}
