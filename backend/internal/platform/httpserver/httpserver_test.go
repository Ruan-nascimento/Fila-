package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fila/internal/platform/config"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func handlerWith(t *testing.T, logs *bytes.Buffer, checks ...Check) (http.Handler, *Health) {
	t.Helper()
	log := testLogger(logs)
	mux := http.NewServeMux()
	h := NewHealth(log, time.Second, checks...)
	h.Register(mux)
	mux.HandleFunc("GET /panico", func(http.ResponseWriter, *http.Request) { panic("detalhe-interno-secreto") })
	mux.HandleFunc("/", NotFound)
	return Chain(mux, log), h
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("corpo não é JSON: %q", rec.Body.String())
	}
	return m
}

func TestLiveIgnoresDependencies(t *testing.T) {
	var logs bytes.Buffer
	h, _ := handlerWith(t, &logs, Check{"database", func(context.Context) error { return errors.New("banco fora") }})
	rec := do(h, http.MethodGet, "/healthz/live")
	if rec.Code != 200 || decode(t, rec)["status"] != "ok" {
		t.Fatalf("live deveria responder 200 mesmo com banco fora: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get(HeaderRequestID) == "" {
		t.Fatal("faltou X-Request-Id")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("faltou cabeçalho de segurança")
	}
}

func TestReadyReflectsChecksWithoutLeakingDetails(t *testing.T) {
	var logs bytes.Buffer
	failing := true
	h, _ := handlerWith(t, &logs, Check{"database", func(context.Context) error {
		if failing {
			return errors.New("dial tcp 10.0.0.5:5432: detalhe interno")
		}
		return nil
	}})
	rec := do(h, http.MethodGet, "/healthz/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("esperava 503, veio %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type inesperado: %s", ct)
	}
	body := decode(t, rec)
	if body["code"] != "service_unavailable" || body["request_id"] != rec.Header().Get(HeaderRequestID) {
		t.Fatalf("problem inesperado: %v", body)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatal("detalhe interno vazou para o cliente")
	}
	if !strings.Contains(logs.String(), "10.0.0.5") {
		t.Fatal("o motivo deveria estar no log para diagnóstico")
	}
	failing = false
	if rec := do(h, http.MethodGet, "/healthz/ready"); rec.Code != 200 {
		t.Fatalf("com dependências ok deveria ser 200, veio %d", rec.Code)
	}
}

func TestReadyFailsWhileDraining(t *testing.T) {
	var logs bytes.Buffer
	h, health := handlerWith(t, &logs)
	health.StartDraining()
	if rec := do(h, http.MethodGet, "/healthz/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("durante encerramento deveria ser 503, veio %d", rec.Code)
	}
}

func TestPanicBecomesProblemWithoutDetails(t *testing.T) {
	var logs bytes.Buffer
	h, _ := handlerWith(t, &logs)
	rec := do(h, http.MethodGet, "/panico")
	if rec.Code != 500 || decode(t, rec)["code"] != "internal_error" {
		t.Fatalf("esperava 500 internal_error: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "detalhe-interno-secreto") {
		t.Fatal("mensagem do pânico vazou para o cliente")
	}
}

func TestUnknownRouteAndAccessLogWithoutQuery(t *testing.T) {
	var logs bytes.Buffer
	h, _ := handlerWith(t, &logs)
	rec := do(h, http.MethodPost, "/v1/nao-existe?token=abc123")
	if rec.Code != 404 || decode(t, rec)["code"] != "not_found" {
		t.Fatalf("esperava 404 not_found: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(logs.String(), "abc123") {
		t.Fatalf("query string não pode ir para o log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"status":404`) {
		t.Fatalf("log de acesso deveria registrar o status: %s", logs.String())
	}
}

func TestServerTimeoutsComeFromConfig(t *testing.T) {
	cfg := config.HTTPConfig{Addr: "127.0.0.1:0", ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second,
		WriteTimeout: 4 * time.Second, IdleTimeout: 5 * time.Second}
	srv := New(cfg, http.NotFoundHandler(), slog.New(slog.DiscardHandler))
	if srv.ReadHeaderTimeout != 2*time.Second || srv.ReadTimeout != 3*time.Second ||
		srv.WriteTimeout != 4*time.Second || srv.IdleTimeout != 5*time.Second {
		t.Fatalf("timeouts não aplicados: %+v", srv)
	}
}

// Servidor real: uma requisição lenta em andamento termina mesmo após o pedido de desligamento,
// o readiness passa a falhar antes, e novas conexões são recusadas depois.
func TestGracefulShutdownWaitsForInFlightRequest(t *testing.T) {
	var logs bytes.Buffer
	log := testLogger(&logs)
	started := make(chan struct{})
	mux := http.NewServeMux()
	health := NewHealth(log, time.Second)
	health.Register(mux)
	mux.HandleFunc("GET /lento", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "terminou")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.HTTPConfig{ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: time.Second}
	srv := New(cfg, Chain(mux, log), log)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	drained := make(chan struct{})
	go func() {
		done <- Run(ctx, srv, ln, 5*time.Second, func() { health.StartDraining(); close(drained) })
	}()

	base := "http://" + ln.Addr().String()
	result := make(chan string, 1)
	go func() {
		resp, err := http.Get(base + "/lento")
		if err != nil {
			result <- "erro: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		result <- string(b)
	}()
	<-started
	cancel() // equivalente a receber SIGTERM
	<-drained

	if got := <-result; got != "terminou" {
		t.Fatalf("requisição em andamento deveria terminar, veio %q", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run devolveu erro: %v", err)
	}
	if _, err := http.Get(base + "/healthz/live"); err == nil {
		t.Fatal("depois do encerramento o servidor não deveria aceitar conexões")
	}
}

func TestShutdownTimeoutIsReported(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /preso", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(config.HTTPConfig{ReadHeaderTimeout: time.Second, WriteTimeout: 10 * time.Second}, mux, log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, srv, ln, 200*time.Millisecond, nil) }()
	go func() { _, _ = http.Get("http://" + ln.Addr().String() + "/preso") }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "encerramento excedeu") {
			t.Fatalf("esperava erro de prazo de encerramento, veio %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run não respeitou o prazo de encerramento")
	}
}
