package idempotency

import (
	"context"

	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
	"fila/internal/platform/jsonscrub"
)

// Exclusão de conta (etapa 13): as respostas guardadas para repetição (24 h) podem conter dados
// pessoais. As da própria pessoa são apagadas (ela não tem mais sessão para repetir nada); nas
// respostas de OUTRAS pessoas (ex.: a equipe chamou e atendeu a pessoa), o texto pessoal vira o
// substituto, para a repetição continuar funcionando sem o dado.

// ForgetActor apaga as respostas guardadas de um ator (ex.: "conta:<uuid>").
func ForgetActor(ctx context.Context, q database.Querier, actorKey string) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM idempotency_record WHERE actor_key = $1`, actorKey)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ScrubIdentity troca, nas respostas guardadas de OUTRAS pessoas, o nick e o nome de reserva da
// pessoa excluída pelo substituto. A troca é por identidade (id da visita ou da reserva), nunca
// por texto: valores iguais ao nome em outras partes da resposta ficam como estavam.
func ScrubIdentity(ctx context.Context, q database.Querier, t jsonscrub.Targets) (int64, error) {
	if t.Empty() {
		return 0, nil
	}
	rows, err := q.Query(ctx, `SELECT actor_key, operation, idem_key, response_body FROM idempotency_record
		WHERE EXISTS (SELECT 1 FROM unnest($1::text[]) AS i WHERE position(convert_to(i, 'UTF8') in response_body) > 0)
		ORDER BY actor_key, operation, idem_key FOR UPDATE`, t.IDs())
	if err != nil {
		return 0, err
	}
	type row struct {
		actor, op, key string
		body           []byte
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.actor, &x.op, &x.key, &x.body)
		return x, err
	})
	if err != nil {
		return 0, err
	}
	var total int64
	for _, x := range found {
		out, changed, err := t.Rewrite(x.body)
		if err != nil {
			return total, err
		}
		if !changed {
			continue
		}
		if _, err := q.Exec(ctx, `UPDATE idempotency_record SET response_body = $4
			WHERE actor_key = $1 AND operation = $2 AND idem_key = $3`, x.actor, x.op, x.key, out); err != nil {
			return total, err
		}
		total++
	}
	return total, nil
}
