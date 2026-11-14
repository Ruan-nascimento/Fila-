package idempotency

import (
	"context"
	"encoding/json"
	"strings"

	"fila/internal/platform/database"
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

// ScrubStrings troca cada texto pessoal (como valor JSON inteiro, na forma em que o servidor o
// gravou) pelo substituto nas respostas guardadas.
func ScrubStrings(ctx context.Context, q database.Querier, values []string, replacement string) (int64, error) {
	rep, _ := json.Marshal(replacement)
	var total int64
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		enc, _ := json.Marshal(v) // mesma codificação das respostas (encoding/json)
		tag, err := q.Exec(ctx, `
			UPDATE idempotency_record
			SET response_body = convert_to(replace(convert_from(response_body, 'UTF8'), $1, $2), 'UTF8')
			WHERE position(convert_to($1, 'UTF8') in response_body) > 0`, string(enc), string(rep))
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}
