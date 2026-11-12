package mail_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
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
