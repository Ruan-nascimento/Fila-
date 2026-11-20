package mail_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/clock"
	"fila/internal/platform/keys"
	"fila/internal/platform/mail"
	"fila/internal/platform/testdb"
)

type flaky struct {
	fail int
	got  []mail.Message
}

func (f *flaky) Send(_ context.Context, _ uuid.UUID, m mail.Message) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("smtp: 550 ana@exemplo.com recusado")
	}
	f.got = append(f.got, m)
	return nil
}

func TestOutboxEncryptsDeliversAndRetries(t *testing.T) {
	pool := testdb.Migrated(t)
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	ring, _ := keys.New(bytes.Repeat([]byte{3}, 32))
	out := mail.NewOutbox(ring)

	msg := mail.Message{To: "ana@exemplo.com", Subject: "Teste", Body: "link com token-secreto-123"}
	if err := out.Enqueue(ctx, pool, "teste", msg, c.Now()); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM mail_outbox`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("token-secreto-123")) || bytes.Contains(raw, []byte("ana@exemplo.com")) {
		t.Fatal("conteúdo do e-mail gravado em claro")
	}

	sender := &flaky{fail: 1}
	d := &mail.Dispatcher{Pool: pool, Ring: ring, Sender: sender, Clock: c,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxAttempts: 3, BatchSize: 10}

	if n, err := d.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("1ª rodada (falha) n=%d err=%v", n, err)
	}
	var lastErr string
	_ = pool.QueryRow(ctx, `SELECT last_error FROM mail_outbox`).Scan(&lastErr)
	if strings.Contains(lastErr, "@") {
		t.Fatalf("erro gravado com destinatário: %q", lastErr)
	}
	if n, _ := d.RunOnce(ctx); n != 0 {
		t.Fatal("não deveria tentar antes do intervalo de espera")
	}
	c.Advance(time.Minute)
	if n, err := d.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("2ª tentativa n=%d err=%v", n, err)
	}
	if len(sender.got) != 1 || sender.got[0] != msg {
		t.Fatalf("mensagem entregue incorreta: %+v", sender.got)
	}
	var cleared bool
	_ = pool.QueryRow(ctx, `SELECT payload IS NULL AND sent_at IS NOT NULL FROM mail_outbox`).Scan(&cleared)
	if !cleared {
		t.Fatal("conteúdo deve ser apagado depois do envio")
	}
	if n, _ := d.RunOnce(ctx); n != 0 {
		t.Fatal("mensagem enviada não pode sair de novo")
	}
}

func TestOutboxGivesUpAfterMaxAttempts(t *testing.T) {
	pool := testdb.Migrated(t)
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	ring, _ := keys.New(bytes.Repeat([]byte{3}, 32))
	_ = mail.NewOutbox(ring).Enqueue(ctx, pool, "teste", mail.Message{To: "a@b.co", Subject: "s", Body: "b"}, c.Now())
	d := &mail.Dispatcher{Pool: pool, Ring: ring, Sender: &flaky{fail: 99}, Clock: c,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxAttempts: 2, BatchSize: 10}
	for range 3 {
		_, _ = d.RunOnce(ctx)
		c.Advance(time.Hour)
	}
	var failed bool
	_ = pool.QueryRow(ctx, `SELECT failed_at IS NOT NULL AND payload IS NULL AND attempts = 2 FROM mail_outbox`).Scan(&failed)
	if !failed {
		t.Fatal("deveria desistir após 2 tentativas e apagar o conteúdo")
	}
}

func TestFileSenderWritesPrivateFile(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()
	if err := (mail.FileSender{Dir: dir}).Send(context.Background(), id, mail.Message{To: "a@b.co", Subject: "Oi", Body: "Corpo"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.Contains(entries[0].Name(), id.String()) {
		t.Fatalf("arquivo esperado não encontrado: %v", entries)
	}
	b, _ := os.ReadFile(dir + "/" + entries[0].Name())
	if !strings.Contains(string(b), "Assunto: Oi") || !strings.Contains(string(b), "Corpo") {
		t.Fatalf("conteúdo inesperado: %s", b)
	}
}

// recorder conta envios por mensagem (chave de idempotência = id) e pode travar o envio.
type recorder struct {
	mu    sync.Mutex
	calls map[uuid.UUID]int
	block chan struct{} // se não nil, o 1º envio fica preso até fechar (processo "caído")
	once  sync.Once
	stuck chan struct{}
	err   error
}

func newRecorder() *recorder {
	return &recorder{calls: map[uuid.UUID]int{}, stuck: make(chan struct{})}
}

func (r *recorder) Send(_ context.Context, id uuid.UUID, _ mail.Message) error {
	r.mu.Lock()
	r.calls[id]++
	first := r.block != nil
	r.mu.Unlock()
	if first {
		r.once.Do(func() {
			close(r.stuck)
			<-r.block
		})
	}
	return r.err
}

func (r *recorder) total() (msgs, calls int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.calls {
		msgs++
		calls += n
	}
	return
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Dois workers ao mesmo tempo nunca enviam a mesma mensagem (reserva com SKIP LOCKED + lease).
func TestDispatcherConcurrentWorkersSendEachOnce(t *testing.T) {
	pool := testdb.Migrated(t)
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	ring, _ := keys.New(bytes.Repeat([]byte{3}, 32))
	out := mail.NewOutbox(ring)
	for range 30 {
		_ = out.Enqueue(ctx, pool, "teste", mail.Message{To: "a@b.co", Subject: "s", Body: "b"}, c.Now())
	}
	rec := newRecorder()
	var wg sync.WaitGroup
	for range 4 {
		d := &mail.Dispatcher{Pool: pool, Ring: ring, Sender: rec, Clock: c, Log: quietLog(), MaxAttempts: 3, BatchSize: 5}
		wg.Go(func() {
			for {
				if n, err := d.RunOnce(ctx); err != nil || n == 0 {
					return
				}
			}
		})
	}
	wg.Wait()
	msgs, calls := rec.total()
	if msgs != 30 || calls != 30 {
		t.Fatalf("esperava 30 mensagens enviadas uma vez cada; mensagens=%d envios=%d", msgs, calls)
	}
}

// Worker cai depois do aceite e antes da confirmação: a reserva vence, outro worker reenvia com a
// MESMA chave de idempotência e a confirmação atrasada do primeiro não sobrescreve nada.
func TestDispatcherCrashAfterAcceptRetriesWithSameKey(t *testing.T) {
	pool := testdb.Migrated(t)
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	ring, _ := keys.New(bytes.Repeat([]byte{3}, 32))
	_ = mail.NewOutbox(ring).Enqueue(ctx, pool, "teste", mail.Message{To: "a@b.co", Subject: "s", Body: "b"}, c.Now())

	stuckRec := newRecorder()
	stuckRec.block = make(chan struct{})
	a := &mail.Dispatcher{Pool: pool, Ring: ring, Sender: stuckRec, Clock: c, Log: quietLog(), MaxAttempts: 3, BatchSize: 1,
		Lease: time.Minute, SendTimeout: time.Hour}
	done := make(chan struct{})
	go func() { _, _ = a.RunOnce(ctx); close(done) }()
	<-stuckRec.stuck // A reservou e "enviou"; ainda não confirmou

	rec := newRecorder()
	b := &mail.Dispatcher{Pool: pool, Ring: ring, Sender: rec, Clock: c, Log: quietLog(), MaxAttempts: 3, BatchSize: 1,
		Lease: time.Minute}
	if n, _ := b.RunOnce(ctx); n != 0 {
		t.Fatal("reserva ainda válida não pode ser tomada por outro worker")
	}
	c.Advance(2 * time.Minute) // reserva de A venceu (A "caiu")
	if n, err := b.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("B deveria reenviar: n=%d err=%v", n, err)
	}
	var idA, idB uuid.UUID
	for k := range stuckRec.calls {
		idA = k
	}
	for k := range rec.calls {
		idB = k
	}
	if idA != idB {
		t.Fatal("o reenvio deve usar a mesma identificação (chave de idempotência)")
	}
	close(stuckRec.block) // A "volta" e tenta confirmar com a reserva antiga
	<-done
	var attempts int
	var sent bool
	_ = pool.QueryRow(ctx, `SELECT attempts, sent_at IS NOT NULL AND payload IS NULL AND lease_until IS NULL FROM mail_outbox`).Scan(&attempts, &sent)
	if !sent || attempts != 2 {
		t.Fatalf("estado final incorreto: enviado=%v tentativas=%d", sent, attempts)
	}
}

func TestDispatcherDropsExpiredAndPermanentFailures(t *testing.T) {
	pool := testdb.Migrated(t)
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	ring, _ := keys.New(bytes.Repeat([]byte{3}, 32))
	out := mail.NewOutbox(ring)
	_ = out.Enqueue(ctx, pool, "redefinicao_senha", mail.Message{To: "a@b.co", Subject: "s", Body: "b"}, c.Now())
	c.Advance(31 * time.Minute) // link de redefinição (30 min) já venceu
	rec := newRecorder()
	d := &mail.Dispatcher{Pool: pool, Ring: ring, Sender: rec, Clock: c, Log: quietLog(), MaxAttempts: 8, BatchSize: 10,
		MaxAge: mail.DefaultMaxAge}
	if n, err := d.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if m, _ := rec.total(); m != 0 {
		t.Fatal("e-mail com link vencido não pode ser enviado")
	}
	var lastErr string
	_ = pool.QueryRow(ctx, `SELECT last_error FROM mail_outbox WHERE failed_at IS NOT NULL AND payload IS NULL`).Scan(&lastErr)
	if lastErr != "vencido antes do envio" {
		t.Fatalf("last_error=%q", lastErr)
	}

	_ = out.Enqueue(ctx, pool, "verificacao_email", mail.Message{To: "x@b.co", Subject: "s", Body: "b"}, c.Now())
	rec.err = &mail.Permanent{Err: errors.New("recusado")}
	_, _ = d.RunOnce(ctx)
	var failed int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM mail_outbox WHERE failed_at IS NOT NULL AND attempts = 1 AND kind = 'verificacao_email'`).Scan(&failed)
	if failed != 1 {
		t.Fatal("erro permanente deve desistir na primeira tentativa")
	}
}
