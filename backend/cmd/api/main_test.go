package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

func environ(dbURL string) []string {
	return []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + dbURL, "FILA_HTTP_ADDR=127.0.0.1:0", "FILA_LOG_FORMAT=json", testdb.SecretKeyVar()}
}

func TestInvalidConfigFailsClearly(t *testing.T) {
	t.Chdir(t.TempDir()) // garante que nenhum .env real seja lido
	var out, errb bytes.Buffer
	code := run(context.Background(),
		[]string{"FILA_ENV=test", "FILA_DATABASE_URL=mysql://u:senha-xyz@h/db", "FILA_HTTP_ADDR=sem-porta"},
		&out, &errb, nil)
	if code != 2 {
		t.Fatalf("configuração inválida deveria sair com 2, saiu com %d", code)
	}
	msg := errb.String()
	for _, want := range []string{"configuração inválida", "FILA_DATABASE_URL", "FILA_HTTP_ADDR"} {
		if !strings.Contains(msg, want) {
			t.Errorf("faltou %q na mensagem:\n%s", want, msg)
		}
	}
	if strings.Contains(msg+out.String(), "senha-xyz") {
		t.Fatal("a senha apareceu na saída")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func get(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Fluxo real: banco vazio → API sobe (ready 503) → migra → ready 200 → SIGTERM → sai com 0.
func TestAPIAgainstRealDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	dbURL := testdb.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logs bytes.Buffer
	listening := make(chan string, 1)
	done := make(chan int, 1)
	metricsAddr := freeAddr(t)
	go func() {
		done <- run(ctx, append(environ(dbURL), "FILA_METRICS_ADDR="+metricsAddr), &logs, &logs, listening)
	}()

	var base string
	select {
	case addr := <-listening:
		base = "http://" + addr
	case code := <-done:
		t.Fatalf("API saiu cedo com %d: %s", code, logs.String())
	case <-time.After(30 * time.Second):
		t.Fatal("API não subiu a tempo")
	}

	if c := get(t, base+"/healthz/live"); c != 200 {
		t.Fatalf("live = %d", c)
	}
	if c := get(t, base+"/healthz/ready"); c != 503 {
		t.Fatalf("ready com banco sem migração deveria ser 503, veio %d", c)
	}

	m, err := database.NewMigrator(testdb.Pool(t, dbURL))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := get(t, base+"/healthz/ready"); c != 200 {
		t.Fatalf("ready após migrar deveria ser 200, veio %d", c)
	}
	// Métricas: só no endereço interno separado, nunca na porta pública da API.
	if c := get(t, base+"/metrics"); c != 404 {
		t.Fatalf("/metrics não pode existir na porta da API: %d", c)
	}
	res, err := http.Get("http://" + metricsAddr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	for _, want := range []string{`fila_http_requests_total{route="GET /healthz/ready",method="GET",status="200"} 1`,
		"fila_sse_connections 0", "fila_db_pool_max_connections", "fila_http_request_duration_seconds_bucket"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("métricas sem %q: %s", want, body)
		}
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("encerramento gracioso deveria sair com 0, saiu com %d: %s", code, logs.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("API não encerrou a tempo")
	}
	if strings.Contains(logs.String(), dbURL) {
		t.Fatal("a URL do banco apareceu nos logs")
	}
}
