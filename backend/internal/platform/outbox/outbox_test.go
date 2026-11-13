package outbox_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/clock"
	"fila/internal/platform/outbox"
	"fila/internal/platform/testdb"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func setup(t *testing.T) (*pgxpool.Pool, uuid.UUID, *outbox.Publisher, *clock.Fake) {
	t.Helper()
	pool := testdb.Migrated(t)
	var est uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO establishment (name, slug, timezone, created_at, updated_at)
		VALUES ('Loja', 'loja-eventos', 'America/Sao_Paulo', now(), now()) RETURNING id`).Scan(&est); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
	return pool, est, &outbox.Publisher{Pool: pool, Clock: clk, Log: quiet}, clk
}

func event(est uuid.UUID, n int) outbox.Event {
	return outbox.Event{EstablishmentID: est, Type: "queue.public.updated", ResourceType: "queue", ResourceID: uuid.New(),
		ResourceVersion: int64(n), Audience: outbox.Audience{Kind: outbox.Public}, Data: map[string]int{"n": n},
		OccurredAt: time.Now()}
}

// publishAll publica até a outbox esvaziar. Transações de outros testes no mesmo servidor
// PostgreSQL seguram o horizonte (xmin é do servidor inteiro), então uma volta vazia não basta.
func publishAll(t *testing.T, p *outbox.Publisher) int {
	t.Helper()
	total := 0
	deadline := time.Now().Add(15 * time.Second)
	for {
		n, err := p.PublishOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		total += n
		if n > 0 {
			continue
		}
		var left int
		_ = p.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox`).Scan(&left)
		if left == 0 {
			return total
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d eventos não publicados", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func positions(t *testing.T, pool *pgxpool.Pool) (list []int64, versions []int64) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT position, resource_version FROM event_log ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p, v int64
		_ = rows.Scan(&p, &v)
		list, versions = append(list, p), append(versions, v)
	}
	return
}

// Uma transação mais antiga ainda aberta segura a publicação das mais novas: quem já leu até a
// posição X nunca perde um evento que apareceria "atrás" (eventos-sse.md §5).
func TestPublisherWaitsForOlderTransactions(t *testing.T) {
	pool, est, pub, _ := setup(t)
	ctx := context.Background()
	older, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = older.Rollback(ctx) }()
	if err := outbox.Record(ctx, older, event(est, 1)); err != nil { // pega o xid mais antigo
		t.Fatal(err)
	}
	newer, _ := pool.Begin(ctx)
	if err := outbox.Record(ctx, newer, event(est, 2)); err != nil {
		t.Fatal(err)
	}
	if err := newer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 5 { // várias voltas: nenhuma pode publicar enquanto a mais antiga estiver aberta
		if n, err := pub.PublishOnce(ctx); err != nil || n != 0 {
			t.Fatalf("publicou %d eventos com uma transação mais antiga aberta (%v)", n, err)
		}
	}
	if err := older.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := publishAll(t, pub); n != 2 {
		t.Fatalf("publicados = %d", n)
	}
	pos, vers := positions(t, pool)
	if len(pos) != 2 || pos[0] != 1 || pos[1] != 2 || vers[0] != 1 || vers[1] != 2 {
		t.Fatalf("posições %v versões %v", pos, vers)
	}
	var left int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&left)
	if left != 0 {
		t.Fatalf("sobraram %d na outbox", left)
	}
}

// Uma transação desfeita não deixa evento (o evento só existe se a mudança existir).
func TestRolledBackChangeHasNoEvent(t *testing.T) {
	pool, est, pub, _ := setup(t)
	ctx := context.Background()
	tx, _ := pool.Begin(ctx)
	_ = outbox.Record(ctx, tx, event(est, 1))
	_ = tx.Rollback(ctx)
	if n := publishAll(t, pub); n != 0 {
		t.Fatalf("publicou evento de transação desfeita: %d", n)
	}
	var logged int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM event_log`).Scan(&logged)
	if logged != 0 {
		t.Fatal("evento de transação desfeita no event_log")
	}
}

// Dois publicadores ao mesmo tempo (dois workers): cada evento uma vez, posições densas 1..N.
func TestTwoPublishersNoDuplicatesNoGaps(t *testing.T) {
	pool, est, pub, clk := setup(t)
	ctx := context.Background()
	const total = 300
	var wg sync.WaitGroup
	for w := range 6 {
		wg.Go(func() {
			for i := range total / 6 {
				_ = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return outbox.Record(ctx, tx, event(est, w*1000+i)) })
			}
		})
	}
	other := &outbox.Publisher{Pool: pool, Clock: clk, Log: quiet, Batch: 7}
	pub.Batch = 11
	stop := make(chan struct{})
	var pubWG sync.WaitGroup
	for _, p := range []*outbox.Publisher{pub, other} {
		pubWG.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := p.PublishOnce(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond)
	close(stop)
	pubWG.Wait()
	publishAll(t, pub)
	pos, _ := positions(t, pool)
	if len(pos) != total {
		t.Fatalf("publicados %d de %d", len(pos), total)
	}
	for i, p := range pos {
		if p != int64(i+1) {
			t.Fatalf("posição %d na ordem %d (buraco ou repetição)", p, i+1)
		}
	}
	var distinct int
	_ = pool.QueryRow(ctx, `SELECT count(DISTINCT event_id) FROM event_log`).Scan(&distinct)
	if distinct != total {
		t.Fatalf("event_id repetido: %d distintos", distinct)
	}
}

// Publicação interrompida no meio (queda do worker, prazo estourado) não publica nada pela
// metade; a próxima tentativa publica tudo uma vez só.
func TestInterruptedPublishIsRetriedSafely(t *testing.T) {
	pool, est, pub, _ := setup(t)
	ctx := context.Background()
	for i := range 5 {
		_ = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return outbox.Record(ctx, tx, event(est, i)) })
	}
	holder, _ := pool.Begin(ctx) // outro "publicador" travado segurando o estado do fluxo
	if _, err := holder.Exec(ctx, `SELECT 1 FROM event_stream_state WHERE id = 1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := pub.PublishOnce(short); err == nil {
		t.Fatal("esperava falha com o estado bloqueado")
	}
	_ = holder.Rollback(ctx) // o travado "morre": o banco solta o bloqueio
	if n := publishAll(t, pub); n != 5 {
		t.Fatalf("depois da falha publicou %d", n)
	}
	if n := publishAll(t, pub); n != 0 {
		t.Fatalf("repetição publicou de novo: %d", n)
	}
}

// Monitoramento de atraso, retenção de 24 h, época e decisão de retomada pelo cursor.
func TestLagRetentionEpochAndResume(t *testing.T) {
	pool, est, pub, clk := setup(t)
	ctx := context.Background()
	_ = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return outbox.Record(ctx, tx, event(est, 1)) })
	lag, err := outbox.MeasureLag(ctx, pool, time.Now().Add(time.Minute))
	if err != nil || lag.Pending != 1 || lag.Oldest < 50*time.Second {
		t.Fatalf("atraso: %+v %v", lag, err)
	}
	publishAll(t, pub)
	head, _ := outbox.ReadHead(ctx, pool)
	at1 := outbox.Cursor{Epoch: head.Epoch, Position: 1}.Encode()
	at0 := outbox.Cursor{Epoch: head.Epoch, Position: 0}.Encode()
	for raw, want := range map[string]string{
		"":    "initial",
		"@@@": "cursor_invalid",
		at1:   "",
		at0:   "",
		outbox.Cursor{Epoch: head.Epoch, Position: 9}.Encode(): "cursor_invalid",
		outbox.Cursor{Epoch: uuid.New(), Position: 1}.Encode(): "epoch_changed",
	} {
		if got := head.Resume(raw); got != want {
			t.Errorf("Resume(%q) = %q, esperado %q", raw, got, want)
		}
	}
	// Mais um evento e o primeiro passa da retenção: o cursor 0 expira, o 1 continua valendo.
	_ = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return outbox.Record(ctx, tx, event(est, 2)) })
	publishAll(t, pub)
	_, _ = pool.Exec(ctx, `UPDATE event_log SET published_at = $1 WHERE position = 1`, clk.Now().Add(-25*time.Hour))
	if n, err := outbox.Prune(ctx, pool, clk.Now()); err != nil || n != 1 {
		t.Fatalf("retenção apagou %d (%v)", n, err)
	}
	head, _ = outbox.ReadHead(ctx, pool)
	if head.Resume(at0) != "cursor_expired" || head.Resume(at1) != "" {
		t.Fatalf("depois da retenção: %q %q", head.Resume(at0), head.Resume(at1))
	}
	if _, err := outbox.NewEpoch(ctx, pool); err != nil {
		t.Fatal(err)
	}
	head, _ = outbox.ReadHead(ctx, pool)
	if head.Resume(at1) != "epoch_changed" {
		t.Fatal("cursor de outra época deveria pedir snapshot")
	}
}

// Hub: entrega na ordem só para conexões da loja; cliente lento estoura o buffer e é marcado;
// aviso de revogação pede revalidação a todos.
func TestHubDeliversAndDetectsSlowClients(t *testing.T) {
	pool, est, pub, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := outbox.NewHub(pool, quiet)
	hub.PollEvery = 50 * time.Millisecond
	fast, err := hub.Subscribe(ctx, est, 16)
	if err != nil {
		t.Fatal(err)
	}
	slow, _ := hub.Subscribe(ctx, est, 1)
	otherShop, _ := hub.Subscribe(ctx, uuid.New(), 16)
	go hub.Run(ctx)
	for i := range 3 {
		_ = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return outbox.Record(ctx, tx, event(est, i)) })
	}
	publishAll(t, pub)
	for want := int64(1); want <= 3; want++ {
		select {
		case e := <-fast.C:
			if e.Position != want {
				t.Fatalf("fora de ordem: %d, esperado %d", e.Position, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("evento %d não chegou", want)
		}
	}
	select {
	case <-slow.Overflow:
	case <-time.After(5 * time.Second):
		t.Fatal("cliente lento não foi marcado")
	}
	if len(otherShop.C) != 0 {
		t.Fatal("evento entregue a conexão de outra loja")
	}
	<-hub.Listening()
	select { // a própria conexão da escuta já pede revalidação; descarta e testa o aviso
	case <-fast.Revalidate:
	default:
	}
	if _, err := pool.Exec(ctx, `SELECT pg_notify($1, '')`, outbox.ChannelRevocation); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fast.Revalidate:
	case <-time.After(5 * time.Second):
		t.Fatal("revogação não pediu revalidação")
	}
	hub.Unsubscribe(fast)
	hub.Unsubscribe(slow)
	hub.Unsubscribe(otherShop)
	if hub.Count() != 0 {
		t.Fatal("conexões não removidas")
	}
}

// Cursor ilegível ou grande demais nunca é aceito.
func TestCursorEncoding(t *testing.T) {
	c := outbox.Cursor{Epoch: uuid.New(), Position: 42}
	got, ok := outbox.DecodeCursor(c.Encode())
	if !ok || got != c || len(c.Encode()) > 128 {
		t.Fatalf("ida e volta: %+v %v", got, ok)
	}
	for _, bad := range []string{"", "x", "AAAA", string(make([]byte, 200))} {
		if _, ok := outbox.DecodeCursor(bad); ok {
			t.Errorf("aceitou %q", bad)
		}
	}
}
