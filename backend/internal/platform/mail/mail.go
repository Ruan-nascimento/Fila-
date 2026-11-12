// Package mail entrega e-mails transacionais (verificação, redefinição de senha, convites)
// por uma caixa de saída no banco.
//
//   - Enqueue grava a mensagem na MESMA transação que gerou o token: se a transação falhar,
//     nenhum e-mail sai; se der certo, o e-mail sai mesmo que a API caia logo depois.
//   - O conteúdo (destinatário, assunto e corpo com link) fica cifrado no banco e é apagado
//     depois do envio.
//   - O worker entrega em segundo plano. Isso também deixa as respostas públicas com o mesmo
//     tempo, exista ou não o e-mail (R-CONTA-02).
//   - Em desenvolvimento a entrega grava arquivos numa pasta local (FileSender). Ainda não há
//     provedor de e-mail real (limitação externa registrada em docs/regras.md).
package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	"fila/internal/platform/keys"
)

// Message é um e-mail de texto simples.
type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Outbox grava mensagens cifradas na caixa de saída.
type Outbox struct {
	ring *keys.Ring
}

// NewOutbox cria a caixa de saída.
func NewOutbox(ring *keys.Ring) *Outbox { return &Outbox{ring: ring} }

// Enqueue grava a mensagem dentro da transação do chamador.
// kind identifica o tipo (ex.: "verificacao_email") só para diagnóstico.
func (o *Outbox) Enqueue(ctx context.Context, q database.Querier, kind string, m Message, now time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	sealed, err := o.ring.Seal(keys.PurposeMail, plain, id[:])
	if err != nil {
		return fmt.Errorf("cifrar e-mail: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO mail_outbox (id, kind, payload, created_at, next_attempt_at)
		VALUES ($1, $2, $3, $4, $4)`, id, kind, sealed, now)
	if err != nil {
		return fmt.Errorf("gravar e-mail na caixa de saída: %w", err)
	}
	return nil
}

// Sender entrega uma mensagem.
type Sender interface {
	Send(ctx context.Context, id uuid.UUID, m Message) error
}

// FileSender grava cada mensagem num arquivo .txt (somente desenvolvimento e testes).
// Os arquivos contêm links com tokens: a pasta fica fora do Git (.gitignore).
type FileSender struct {
	Dir string
}

// Send grava <instante>-<id>.txt com permissão só do usuário.
func (f FileSender) Send(_ context.Context, id uuid.UUID, m Message) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	name := time.Now().UTC().Format("20060102T150405Z") + "-" + id.String() + ".txt"
	content := "Para: " + m.To + "\nAssunto: " + m.Subject + "\n\n" + m.Body + "\n"
	return os.WriteFile(filepath.Join(f.Dir, name), []byte(content), 0o600)
}

// Dispatcher entrega as mensagens pendentes.
type Dispatcher struct {
	Pool        *pgxpool.Pool
	Ring        *keys.Ring
	Sender      Sender
	Clock       clock.Clock
	Log         *slog.Logger
	MaxAttempts int
	BatchSize   int
}

// RunOnce entrega até BatchSize mensagens vencidas e devolve quantas foram enviadas.
// Várias instâncias podem rodar juntas: cada linha é travada (FOR UPDATE SKIP LOCKED).
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	batch := max(d.BatchSize, 1)
	maxAttempts := max(d.MaxAttempts, 1)
	sent := 0
	err := database.InTx(ctx, d.Pool, func(tx pgx.Tx) error {
		now := d.Clock.Now()
		rows, err := tx.Query(ctx, `
			SELECT id, payload, attempts FROM mail_outbox
			WHERE sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= $1
			ORDER BY next_attempt_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED`, now, batch)
		if err != nil {
			return err
		}
		type item struct {
			id       uuid.UUID
			payload  []byte
			attempts int
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.payload, &it.attempts); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, it := range items {
			plain, err := d.Ring.Open(keys.PurposeMail, it.payload, it.id[:])
			var m Message
			if err == nil {
				err = json.Unmarshal(plain, &m)
			}
			if err != nil {
				// Chave trocada ou conteúdo inválido: não há como entregar; desiste sem conteúdo.
				d.Log.Error("e-mail ilegível; descartado", "mail_id", it.id.String())
				if _, err := tx.Exec(ctx, `UPDATE mail_outbox SET failed_at = $2, payload = NULL, last_error = 'conteúdo ilegível' WHERE id = $1`,
					it.id, now); err != nil {
					return err
				}
				continue
			}
			if err := d.Sender.Send(ctx, it.id, m); err != nil {
				attempts := it.attempts + 1
				// O texto do erro pode conter o destinatário: guardamos só uma categoria fixa.
				const msg = "falha de envio"
				if attempts >= maxAttempts {
					d.Log.Error("e-mail não entregue; desistindo", "mail_id", it.id.String(), "attempts", attempts)
					_, err = tx.Exec(ctx, `UPDATE mail_outbox SET attempts = $2, failed_at = $3, payload = NULL, last_error = $4 WHERE id = $1`,
						it.id, attempts, now, msg)
				} else {
					d.Log.Warn("falha ao entregar e-mail; nova tentativa programada", "mail_id", it.id.String(), "attempts", attempts)
					_, err = tx.Exec(ctx, `UPDATE mail_outbox SET attempts = $2, next_attempt_at = $3, last_error = $4 WHERE id = $1`,
						it.id, attempts, now.Add(backoff(attempts)), msg)
				}
				if err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE mail_outbox SET sent_at = $2, payload = NULL, attempts = attempts + 1, last_error = NULL WHERE id = $1`,
				it.id, now); err != nil {
				return err
			}
			sent++
		}
		return nil
	})
	return sent, err
}

// Run entrega periodicamente até ctx terminar.
func (d *Dispatcher) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.Log.Error("entrega de e-mails falhou", "error", err.Error())
		} else if n > 0 {
			d.Log.Info("e-mails entregues", "quantidade", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// backoff: 30 s, 1 min, 2 min… até 1 h.
func backoff(attempts int) time.Duration {
	d := 30 * time.Second << min(attempts-1, 7)
	return min(d, time.Hour)
}
