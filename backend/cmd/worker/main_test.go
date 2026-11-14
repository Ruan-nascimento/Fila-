package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fila/internal/operacao"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fila/internal/platform/database"
	"fila/internal/platform/keys"
	"fila/internal/platform/mail"
	"fila/internal/platform/testdb"
)

func TestWorkerRefusesUnmigratedDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	var logs bytes.Buffer
	code := run(context.Background(), []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + testdb.New(t), testdb.SecretKeyVar()}, &logs, &logs, nil)
	if code != 1 || !strings.Contains(logs.String(), "sem migrações") {
		t.Fatalf("esperava recusa com código 1, veio %d: %s", code, logs.String())
	}
}

func TestWorkerStartsAndStopsGracefully(t *testing.T) {
	t.Chdir(t.TempDir())
	dbURL := testdb.New(t)
	m, err := database.NewMigrator(testdb.Pool(t, dbURL))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	started := make(chan struct{})
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + dbURL, testdb.SecretKeyVar(), "FILA_MAIL_DIR=" + t.TempDir()}, &logs, &logs, started)
	}()
	select {
	case <-started:
	case code := <-done:
		t.Fatalf("worker saiu cedo (%d): %s", code, logs.String())
	case <-time.After(30 * time.Second):
		t.Fatal("worker não ficou pronto")
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("encerramento deveria sair com 0, saiu com %d", code)
	}
}

func TestWorkerRequiresSecretKey(t *testing.T) {
	t.Chdir(t.TempDir())
	var logs bytes.Buffer
	code := run(context.Background(), []string{"FILA_ENV=test", "FILA_DATABASE_URL=postgres://u:p@127.0.0.1:1/db"}, &logs, &logs, nil)
	if code != 2 || !strings.Contains(logs.String(), "FILA_SECRET_KEY") {
		t.Fatalf("sem chave deveria sair com 2 citando FILA_SECRET_KEY; veio %d: %s", code, logs.String())
	}
}

// O worker entrega e-mails da caixa de saída gravando arquivos (modo de desenvolvimento).
func TestWorkerDeliversQueuedMail(t *testing.T) {
	t.Chdir(t.TempDir())
	pool := testdb.Migrated(t)
	dbURL := pool.Config().ConnString()
	keyVar := testdb.SecretKeyVar()
	_, encoded, _ := strings.Cut(keyVar, "=")
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	ring, _ := keys.New(raw)
	msg := mail.Message{To: "ana@exemplo.com", Subject: "Teste", Body: "Olá"}
	if err := mail.NewOutbox(ring).Enqueue(context.Background(), pool, "teste", msg, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + dbURL, keyVar, "FILA_MAIL_DIR=" + dir}, &logs, &logs, nil)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		entries, _ := os.ReadDir(dir)
		if len(entries) == 1 {
			b, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			if !strings.Contains(string(b), "Assunto: Teste") {
				t.Fatalf("conteúdo inesperado: %s", b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("e-mail não entregue: %s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("saída %d", code)
	}
	if strings.Contains(logs.String(), "ana@exemplo.com") {
		t.Fatal("destinatário apareceu no log")
	}
}

// Worker travado (manutenção) não roda NENHUMA tarefa, mas continua expondo os indicadores (é
// assim que o alerta aparece). Ao soltar a trava, as tarefas voltam e o trabalho parado é feito.
func TestWorkerHeldDoesNothingUntilReleased(t *testing.T) {
	t.Chdir(t.TempDir())
	pool := testdb.Migrated(t)
	dbURL := pool.Config().ConnString()
	keyVar := testdb.SecretKeyVar()
	_, encoded, _ := strings.Cut(keyVar, "=")
	raw, _ := base64.RawURLEncoding.DecodeString(encoded)
	ring, _ := keys.New(raw)
	if _, err := operacao.Hold(context.Background(), pool, operacao.HoldMaintenance, "teste de trava", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := mail.NewOutbox(ring).Enqueue(context.Background(), pool, "teste", mail.Message{To: "x@exemplo.com", Subject: "Oi", Body: "b"},
		time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	metricsAddr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs syncBuf
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + dbURL, keyVar, "FILA_MAIL_DIR=" + dir,
			"FILA_METRICS_ADDR=" + metricsAddr}, &logs, &logs, nil)
	}()
	scrape := func() string {
		res, err := http.Get("http://" + metricsAddr + "/metrics")
		if err != nil {
			return ""
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(scrape(), "\nfila_worker_hold 1\n") {
		if time.Now().After(deadline) {
			t.Fatalf("métrica da trava não apareceu: %s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out := scrape(); !strings.Contains(out, "fila_mail_pending 1") || !strings.Contains(out, "fila_alerts_active") {
		t.Fatalf("indicadores: %s", out)
	}
	time.Sleep(time.Second)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("worker travado entregou e-mail")
	}
	if _, err := operacao.Release(context.Background(), pool, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(25 * time.Second)
	for {
		if entries, _ := os.ReadDir(dir); len(entries) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("e-mail não entregue depois de soltar a trava: %s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("saída %d", code)
	}
	if !strings.Contains(logs.String(), "worker TRAVADO") || !strings.Contains(logs.String(), "tarefas do worker ligadas") {
		t.Fatalf("log deveria explicar a trava e a volta: %s", logs.String())
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
