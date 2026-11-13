package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/clock"
	"fila/internal/platform/database"
)

// Canais de NOTIFY (só avisos "há novidade"; nenhum dado vai no aviso).
const (
	ChannelOutbox     = "fila_outbox"    // comando gravou na outbox
	ChannelEvents     = "fila_eventos"   // publicador gravou no event_log
	ChannelRevocation = "fila_revogacao" // alguma sessão/credencial foi revogada
	Retention         = 24 * time.Hour   // eventos-sse.md §8
	lagWarning        = 10 * time.Second // atraso que vira aviso no log
	maxBackoff        = 30 * time.Second // espera máxima entre tentativas com erro
	publishTimeout    = 10 * time.Second // "lease": uma publicação travada é abortada pelo banco
)

// Publisher move eventos da outbox para o event_log (worker). Pode haver mais de um rodando: o
// bloqueio da linha event_stream_state serializa as publicações, então a posição cresce na
// ordem de commit e nunca se repete.
type Publisher struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	Batch int
}

// PublishOnce publica um lote. Só entram linhas de transações JÁ TERMINADAS:
// xact_id < xmin do snapshot atual. Uma transação mais antiga ainda aberta não pode mais
// aparecer "atrás" de algo já publicado (eventos-sse.md §5). Copiar, apagar da outbox e
// avançar a posição acontecem na mesma transação: se cair no meio, nada é publicado e a próxima
// tentativa refaz o lote inteiro (repetição segura).
func (p *Publisher) PublishOnce(ctx context.Context) (int, error) {
	batch := p.Batch
	if batch <= 0 {
		batch = 500
	}
	n := 0
	err := database.InTx(ctx, p.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL statement_timeout = %d`, publishTimeout.Milliseconds())); err != nil {
			return err
		}
		var last int64
		// Primeiro o bloqueio: um segundo publicador espera aqui e, depois, enxerga o que o
		// primeiro apagou (cada comando em READ COMMITTED lê um snapshot novo).
		if err := tx.QueryRow(ctx, `SELECT last_position FROM event_stream_state WHERE id = 1 FOR UPDATE`).Scan(&last); err != nil {
			return fmt.Errorf("ler estado do fluxo: %w", err)
		}
		rows, err := tx.Query(ctx, `
			SELECT id, event_id, establishment_id, audience, professional_id, account_id, visitor_id, type,
			       resource_type, resource_id, resource_version, payload, occurred_at
			FROM outbox
			WHERE xact_id < pg_snapshot_xmin(pg_current_snapshot())
			ORDER BY xact_id, id
			LIMIT $1`, batch)
		if err != nil {
			return err
		}
		type row struct {
			id int64
			e  Published
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			err := r.Scan(&x.id, &x.e.EventID, &x.e.EstablishmentID, &x.e.Audience, &x.e.ProfessionalID, &x.e.AccountID,
				&x.e.VisitorID, &x.e.Type, &x.e.ResourceType, &x.e.ResourceID, &x.e.ResourceVersion, &x.e.Payload, &x.e.OccurredAt)
			return x, err
		})
		if err != nil || len(list) == 0 {
			return err
		}
		now := p.Clock.Now()
		ids := make([]int64, 0, len(list))
		for _, x := range list {
			ids = append(ids, x.id)
			// event_id único: se a mesma linha já tivesse sido publicada, não gasta posição.
			tag, err := tx.Exec(ctx, `
				INSERT INTO event_log (position, event_id, establishment_id, audience, professional_id, account_id, visitor_id,
				                       type, resource_type, resource_id, resource_version, payload, occurred_at, published_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
				ON CONFLICT (event_id) DO NOTHING`,
				last+1, x.e.EventID, x.e.EstablishmentID, x.e.Audience, x.e.ProfessionalID, x.e.AccountID, x.e.VisitorID,
				x.e.Type, x.e.ResourceType, x.e.ResourceID, x.e.ResourceVersion, x.e.Payload, x.e.OccurredAt, now)
			if err != nil {
				return fmt.Errorf("gravar no event_log: %w", err)
			}
			if tag.RowsAffected() == 1 {
				last++
				n++
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE id = ANY($1)`, ids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE event_stream_state SET last_position = $1, updated_at = $2 WHERE id = 1`, last, now); err != nil {
			return err
		}
		// O aviso só é entregue depois do COMMIT: a API nunca lê algo que ainda não existe.
		_, err = tx.Exec(ctx, `SELECT pg_notify($1, '')`, ChannelEvents)
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Run publica continuamente: acorda com o aviso dos comandos (LISTEN fila_outbox) e também a
// cada `every` (rede de segurança para avisos perdidos e para linhas que esperavam uma
// transação antiga terminar). Erros são registrados e tentados de novo com espera crescente.
func (p *Publisher) Run(ctx context.Context, every time.Duration) {
	wake := make(chan struct{}, 1)
	go listen(ctx, p.Pool, p.Log, []string{ChannelOutbox}, func(string) {
		select {
		case wake <- struct{}{}:
		default:
		}
	}, nil)
	backoff := time.Second
	lastLagCheck := time.Time{}
	for {
		n, err := p.PublishOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			p.Log.Error("publicação de eventos falhou; tentando de novo", "error", database.Describe(err), "espera", backoff.String())
			if !sleep(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second
		if n > 0 {
			continue // pode haver mais no próximo lote
		}
		if time.Since(lastLagCheck) >= time.Minute {
			lastLagCheck = time.Now()
			p.reportLag(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-time.After(every):
		}
	}
}

func (p *Publisher) reportLag(ctx context.Context) {
	lag, err := MeasureLag(ctx, p.Pool, p.Clock.Now())
	if err != nil {
		return
	}
	if lag.Oldest >= lagWarning {
		p.Log.Warn("outbox de eventos atrasada (transação longa aberta ou publicador com erro?)",
			"pendentes", lag.Pending, "mais_antigo_s", int(lag.Oldest.Seconds()))
	}
}

// Lag é o atraso da publicação: quantos eventos esperam e há quanto tempo o mais antigo espera.
type Lag struct {
	Pending int
	Oldest  time.Duration
}

// MeasureLag mede o atraso da outbox (monitoramento).
func MeasureLag(ctx context.Context, q database.Querier, now time.Time) (Lag, error) {
	var l Lag
	var oldest *time.Time
	if err := q.QueryRow(ctx, `SELECT count(*), min(occurred_at) FROM outbox`).Scan(&l.Pending, &oldest); err != nil {
		return l, err
	}
	if oldest != nil && now.After(*oldest) {
		l.Oldest = now.Sub(*oldest)
	}
	return l, nil
}

// Prune apaga do event_log o que passou da retenção (24 h). Corta por posição para nunca abrir
// buraco no meio da sequência; um cursor anterior ao corte recebe snapshot (cursor_expired).
func Prune(ctx context.Context, q database.Querier, now time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `
		DELETE FROM event_log WHERE position <= (SELECT max(position) FROM event_log WHERE published_at < $1)`,
		now.Add(-Retention))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// NewEpoch troca a época do fluxo (usar depois de restaurar backup): todo cursor antigo passa a
// receber snapshot (epoch_changed).
func NewEpoch(ctx context.Context, q database.Querier) (uuid.UUID, error) {
	var e uuid.UUID
	err := q.QueryRow(ctx, `UPDATE event_stream_state SET epoch = gen_random_uuid(), updated_at = now() WHERE id = 1 RETURNING epoch`).Scan(&e)
	return e, err
}

// listen mantém uma conexão dedicada com LISTEN nos canais e chama on(canal) a cada aviso.
// Reconecta com espera crescente se a conexão cair; connected (opcional) é chamado a cada
// (re)conexão, para o chamador reler o que pode ter perdido enquanto estava desconectado.
func listen(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, channels []string, on func(string), connected func()) {
	backoff := 500 * time.Millisecond
	for ctx.Err() == nil {
		err := func() error {
			acquired, err := pool.Acquire(ctx)
			if err != nil {
				return err
			}
			// A conexão sai do pool (LISTEN é estado da sessão) e é fechada ao terminar.
			conn := acquired.Hijack()
			defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
			for _, ch := range channels {
				if _, err := conn.Exec(ctx, `LISTEN `+pgx.Identifier{ch}.Sanitize()); err != nil {
					return err
				}
			}
			backoff = 500 * time.Millisecond
			if connected != nil {
				connected()
			}
			for {
				n, err := conn.WaitForNotification(ctx)
				if err != nil {
					return err
				}
				on(n.Channel)
			}
		}()
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("escuta de avisos do banco caiu; reconectando", "error", database.Describe(err), "espera", backoff.String())
		}
		if !sleep(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
