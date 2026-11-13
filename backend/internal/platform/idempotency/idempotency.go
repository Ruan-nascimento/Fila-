// Package idempotency implementa o cabeçalho Idempotency-Key dos comandos (ADR-0009,
// convencoes.md §8).
//
// Como funciona:
//  1. O comando abre uma transação e "reserva" a chave (ator + operação + chave) nela.
//  2. Se outra requisição com a mesma chave está em andamento, o INSERT espera ela terminar
//     (bloqueio de linha do PostgreSQL); assim duas cópias simultâneas nunca aplicam o efeito duas vezes.
//  3. Se a chave já existe: mesma impressão digital → devolve a resposta guardada
//     (Idempotent-Replayed: true); impressão diferente → 422 idempotency_key_reused.
//  4. A resposta é gravada na MESMA transação do efeito: não existe efeito sem registro nem
//     registro sem efeito.
//
// Respostas de erro não são guardadas: a repetição reavalia o pedido (nada foi aplicado).
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
)

// Header é o nome do cabeçalho.
const Header = "Idempotency-Key"

// TTL é por quanto tempo uma resposta fica guardada.
const TTL = 24 * time.Hour

var keyFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// Runner executa comandos idempotentes.
type Runner struct {
	pool  *pgxpool.Pool
	clock clock.Clock
	log   *slog.Logger
}

// New cria o executor.
func New(pool *pgxpool.Pool, c clock.Clock, log *slog.Logger) *Runner {
	return &Runner{pool: pool, clock: c, log: log}
}

// Command descreve um comando idempotente.
type Command struct {
	ActorKey  string // ex.: "conta:<uuid>"
	Operation string // operationId do contrato
	Body      []byte // corpo cru recebido (entra na impressão digital)
}

// Result é a resposta de sucesso do comando.
type Result struct {
	Status int
	Body   any
	// Stored (opcional) é o que fica guardado para repetições no lugar de Body, quando a resposta
	// ao vivo traz algo que não pode ficar gravado em claro (ex.: protocolo completo da exclusão,
	// mostrado uma única vez ao titular). A repetição devolve Stored.
	Stored any
}

// Run valida a chave, executa fn numa transação e escreve a resposta (nova ou repetida).
func (rn *Runner) Run(w http.ResponseWriter, r *http.Request, cmd Command, fn func(tx pgx.Tx) (Result, error)) error {
	key := r.Header.Get(Header)
	if key == "" {
		return &httpserver.Error{Status: 400, Code: "idempotency_key_missing", Title: "Falta a chave de repetição",
			Detail: "Envie o cabeçalho Idempotency-Key gerado uma vez por ação."}
	}
	if !keyFormat.MatchString(key) {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/header/Idempotency-Key", Code: httpserver.FieldInvalidFormat,
			Message: "use de 16 a 128 caracteres entre letras, números, - e _"})
	}
	fp := fingerprint(r, cmd.Body)
	now := rn.clock.Now()

	var status int
	var body []byte
	replayed := false
	err := database.InTx(r.Context(), rn.pool, func(tx pgx.Tx) error {
		stored, err := claim(r.Context(), tx, cmd, key, fp, now)
		if err != nil {
			return err
		}
		if stored != nil {
			status, body, replayed = stored.status, stored.body, true
			return nil
		}
		res, err := fn(tx)
		if err != nil {
			return err
		}
		if body, err = json.Marshal(res.Body); err != nil {
			return fmt.Errorf("serializar resposta: %w", err)
		}
		status = res.Status
		keep := body
		if res.Stored != nil {
			if keep, err = json.Marshal(res.Stored); err != nil {
				return fmt.Errorf("serializar resposta guardada: %w", err)
			}
		}
		_, err = tx.Exec(r.Context(), `
			UPDATE idempotency_record SET response_status = $4, response_body = $5
			WHERE actor_key = $1 AND operation = $2 AND idem_key = $3`,
			cmd.ActorKey, cmd.Operation, key, status, keep)
		return err
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
	return nil
}

// fingerprint = SHA-256(método, caminho com parâmetros, corpo cru).
func fingerprint(r *http.Request, body []byte) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n", r.Method, r.URL.Path)
	h.Write(body)
	return h.Sum(nil)
}

type stored struct {
	status int
	body   []byte
}

var errReused = &httpserver.Error{Status: 422, Code: "idempotency_key_reused", Title: "Chave de repetição já usada",
	Detail: "Esta chave já foi usada para um pedido diferente. Gere uma nova chave para uma nova ação."}

// claim reserva a chave. Devolve a resposta guardada quando é repetição legítima.
func claim(ctx context.Context, tx pgx.Tx, cmd Command, key string, fp []byte, now time.Time) (*stored, error) {
	for range 3 {
		// Status 200 provisório: é substituído pela resposta real antes do commit.
		tag, err := tx.Exec(ctx, `
			INSERT INTO idempotency_record (actor_key, operation, idem_key, fingerprint, response_status,
			                                response_body, created_at, expires_at)
			VALUES ($1, $2, $3, $4, 200, ''::bytea, $5, $6)
			ON CONFLICT DO NOTHING`,
			cmd.ActorKey, cmd.Operation, key, fp, now, now.Add(TTL))
		if err != nil {
			return nil, fmt.Errorf("reservar chave de idempotência: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return nil, nil
		}
		var s stored
		var gotFP []byte
		var expires time.Time
		err = tx.QueryRow(ctx, `
			SELECT fingerprint, response_status, response_body, expires_at FROM idempotency_record
			WHERE actor_key = $1 AND operation = $2 AND idem_key = $3`,
			cmd.ActorKey, cmd.Operation, key).Scan(&gotFP, &s.status, &s.body, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // a outra transação foi desfeita entre o INSERT e o SELECT: tenta reservar de novo
		}
		if err != nil {
			return nil, fmt.Errorf("ler chave de idempotência: %w", err)
		}
		if !expires.After(now) {
			// Registro vencido (24 h): a chave pode ser usada de novo.
			if _, err := tx.Exec(ctx, `DELETE FROM idempotency_record WHERE actor_key = $1 AND operation = $2 AND idem_key = $3`,
				cmd.ActorKey, cmd.Operation, key); err != nil {
				return nil, err
			}
			continue
		}
		if string(gotFP) != string(fp) {
			return nil, errReused
		}
		return &s, nil
	}
	return nil, &httpserver.Error{Status: 409, Code: "idempotency_in_progress", Title: "Pedido em andamento",
		Detail: "Este pedido ainda está sendo processado. Tente novamente em instantes."}
}
