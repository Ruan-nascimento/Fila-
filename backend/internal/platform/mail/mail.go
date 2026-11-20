// Package mail entrega e-mails transacionais (verificação, redefinição de senha, convites)
// por uma caixa de saída no banco.
//
//   - Enqueue grava a mensagem na MESMA transação que gerou o token: se a transação falhar,
//     nenhum e-mail sai; se der certo, o e-mail sai mesmo que a API caia logo depois.
//   - O conteúdo (destinatário, assunto e corpo com link) fica cifrado no banco e é apagado
//     depois do envio.
//   - O worker entrega em segundo plano. Isso também deixa as respostas públicas com o mesmo
//     tempo, exista ou não o e-mail (R-CONTA-02).
//   - Em desenvolvimento a entrega grava arquivos numa pasta local (FileSender). Em homologação e
//     produção o provedor é o Resend (ResendSender, FILA_MAIL_MODE=resend).
//   - "Enviado" (sent_at) significa ACEITO pelo provedor, não entregue na caixa da pessoa.
package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
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

// Permanent marca um erro de envio que não adianta repetir (ex.: endereço recusado pelo
// provedor como inválido). O dispatcher desiste da mensagem na hora.
type Permanent struct{ Err error }

func (p *Permanent) Error() string { return p.Err.Error() }
func (p *Permanent) Unwrap() error { return p.Err }

// ReceiptSender é um Sender que também devolve o identificador do provedor (ex.: Resend).
type ReceiptSender interface {
	SendReceipt(ctx context.Context, id uuid.UUID, m Message) (providerID string, err error)
}

// ErrorCategory é a categoria fixa (sem dados pessoais) gravada em last_error e no log.
// Senders podem implementar Category() string no erro para refinar a categoria.
func ErrorCategory(err error) string {
	var c interface{ Category() string }
	if errors.As(err, &c) {
		return c.Category()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "tempo esgotado"
	}
	return "falha de envio"
}

// DefaultMaxAge é a validade de cada tipo de e-mail: depois dela o link/código do e-mail já
// expirou e reenviar só confundiria a pessoa. Mensagem vencida é descartada sem envio.
func DefaultMaxAge(kind string) time.Duration {
	switch kind {
	case "codigo_reautenticacao":
		return 10 * time.Minute
	case "redefinicao_senha":
		return 30 * time.Minute
	default: // verificação de e-mail e convites: 24 h; avisos sem link: 24 h
		return 24 * time.Hour
	}
}

// Dispatcher entrega as mensagens pendentes.
//
// Fluxo por mensagem (nenhuma transação fica aberta durante a chamada externa):
//  1. Reserva curta: UPDATE ... SET lease_until = agora + Lease, attempts + 1 (SKIP LOCKED),
//     autocommit. Dois workers nunca reservam a mesma mensagem ao mesmo tempo.
//  2. Envio fora da transação, com prazo menor que a reserva.
//  3. Confirmação individual condicionada à reserva ainda ser a nossa (lease_until igual).
//
// Se o processo cair entre 2 e 3, a reserva vence e outra tentativa reenvia com a MESMA chave
// de idempotência (o id da mensagem). O provedor deduplica dentro da janela dele (Resend: 24 h),
// então a pessoa recebe uma única vez nesse caso. Fora da janela não há garantia: por isso cada
// tipo tem validade curta (MaxAge) e há limite de tentativas.
type Dispatcher struct {
	Pool        *pgxpool.Pool
	Ring        *keys.Ring
	Sender      Sender
	Clock       clock.Clock
	Log         *slog.Logger
	MaxAttempts int
	BatchSize   int
	// Lease: duração da reserva (padrão 2 min). Precisa ser maior que SendTimeout.
	Lease time.Duration
	// SendTimeout: prazo de cada envio (padrão 30 s).
	SendTimeout time.Duration
	// MaxAge devolve a validade de um tipo de e-mail; nil = sem validade (testes).
	MaxAge func(kind string) time.Duration
}

type claimed struct {
	id        uuid.UUID
	kind      string
	payload   []byte
	attempts  int
	createdAt time.Time
	lease     time.Time
}

// claim reserva até batch mensagens vencidas numa transação curta (autocommit).
func (d *Dispatcher) claim(ctx context.Context, now time.Time, batch int, lease time.Duration) ([]claimed, error) {
	until := now.Add(lease)
	rows, err := d.Pool.Query(ctx, `
		UPDATE mail_outbox m SET lease_until = $3, attempts = m.attempts + 1
		FROM (SELECT id FROM mail_outbox
		      WHERE sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= $1
		        AND (lease_until IS NULL OR lease_until <= $1)
		      ORDER BY next_attempt_at, id
		      LIMIT $2
		      FOR UPDATE SKIP LOCKED) c
		WHERE m.id = c.id
		RETURNING m.id, m.kind, m.payload, m.attempts, m.created_at, m.lease_until`, now, batch, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.id, &c.kind, &c.payload, &c.attempts, &c.createdAt, &c.lease); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// finish grava o resultado só se a reserva ainda for desta tentativa (fencing).
func (d *Dispatcher) finish(ctx context.Context, c claimed, sql string, args ...any) error {
	_, err := d.Pool.Exec(ctx, sql, append([]any{c.id, c.lease}, args...)...)
	return err
}

// RunOnce entrega até BatchSize mensagens vencidas e devolve quantas foram aceitas pelo provedor.
// Várias instâncias podem rodar juntas.
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	batch := max(d.BatchSize, 1)
	maxAttempts := max(d.MaxAttempts, 1)
	lease := d.Lease
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	sendTimeout := d.SendTimeout
	if sendTimeout <= 0 {
		sendTimeout = 30 * time.Second
	}
	sendTimeout = min(sendTimeout, lease/2)

	items, err := d.claim(ctx, d.Clock.Now(), batch, lease)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, it := range items {
		if ctx.Err() != nil {
			// Desligando: a reserva vence sozinha e outra tentativa retoma.
			return sent, nil
		}
		now := d.Clock.Now()
		if d.MaxAge != nil && now.Sub(it.createdAt) > d.MaxAge(it.kind) {
			d.Log.Warn("e-mail vencido antes do envio; descartado", "mail_id", it.id.String(), "tipo", it.kind)
			if err := d.finish(ctx, it, `UPDATE mail_outbox SET failed_at = $3, payload = NULL, lease_until = NULL,
				last_error = 'vencido antes do envio' WHERE id = $1 AND lease_until = $2`, now); err != nil {
				return sent, err
			}
			continue
		}
		plain, err := d.Ring.Open(keys.PurposeMail, it.payload, it.id[:])
		var m Message
		if err == nil {
			err = json.Unmarshal(plain, &m)
		}
		if err != nil {
			// Chave trocada ou conteúdo inválido: não há como entregar; desiste sem conteúdo.
			d.Log.Error("e-mail ilegível; descartado", "mail_id", it.id.String())
			if err := d.finish(ctx, it, `UPDATE mail_outbox SET failed_at = $3, payload = NULL, lease_until = NULL,
				last_error = 'conteúdo ilegível' WHERE id = $1 AND lease_until = $2`, now); err != nil {
				return sent, err
			}
			continue
		}

		sctx, cancel := context.WithTimeout(ctx, sendTimeout)
		var providerID string
		if rs, ok := d.Sender.(ReceiptSender); ok {
			providerID, err = rs.SendReceipt(sctx, it.id, m)
		} else {
			err = d.Sender.Send(sctx, it.id, m)
		}
		cancel()
		now = d.Clock.Now()
		if err != nil {
			// O texto do erro pode conter o destinatário: guardamos só uma categoria fixa.
			msg := ErrorCategory(err)
			var perm *Permanent
			if errors.As(err, &perm) || it.attempts >= maxAttempts {
				d.Log.Error("e-mail não entregue; desistindo", "mail_id", it.id.String(), "tipo", it.kind,
					"attempts", it.attempts, "motivo", msg)
				err = d.finish(ctx, it, `UPDATE mail_outbox SET failed_at = $3, payload = NULL, lease_until = NULL,
					last_error = $4 WHERE id = $1 AND lease_until = $2`, now, msg)
			} else {
				d.Log.Warn("falha ao entregar e-mail; nova tentativa programada", "mail_id", it.id.String(), "tipo", it.kind,
					"attempts", it.attempts, "motivo", msg)
				err = d.finish(ctx, it, `UPDATE mail_outbox SET next_attempt_at = $3, lease_until = NULL,
					last_error = $4 WHERE id = $1 AND lease_until = $2`, now.Add(backoff(it.attempts)), msg)
			}
			if err != nil {
				return sent, err
			}
			continue
		}
		var pid *string
		if providerID != "" && len(providerID) <= 100 {
			pid = &providerID
		}
		// Confirmação com contexto próprio: o envio já foi aceito; perder a confirmação por causa
		// do desligamento só causaria um reenvio (deduplicado pela chave de idempotência).
		cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err = d.finish(cctx, it, `UPDATE mail_outbox SET sent_at = $3, payload = NULL, lease_until = NULL,
			last_error = NULL, provider_message_id = $4 WHERE id = $1 AND lease_until = $2`, now, pid)
		ccancel()
		if err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// Run entrega periodicamente até ctx terminar.
func (d *Dispatcher) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.Log.Error("entrega de e-mails falhou", "error", err.Error())
		} else if n > 0 {
			d.Log.Info("e-mails aceitos pelo provedor", "quantidade", n)
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
