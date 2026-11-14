package httpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Cliente que vai embora no meio (troca de página, aba fechada, rede caiu) cancela a consulta:
// isso não é erro do servidor — não pode virar 500 nem log de erro (alarme falso em produção).
func TestClientGoneIsNotServerError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	mux := http.NewServeMux()
	Mount(mux, log, []Route{{Operation: "x", Method: "GET", Path: "/v1/lento", Handler: func(_ http.ResponseWriter, r *http.Request) error {
		<-r.Context().Done()
		return errors.New("ler sessão: " + r.Context().Err().Error())
	}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	Chain(mux, log).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/lento", nil).WithContext(ctx))
	if rec.Code != StatusClientClosedRequest {
		t.Fatalf("status %d, esperado 499", rec.Code)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), "cliente desistiu") {
		t.Fatalf("log: %s", logs.String())
	}
	// Erro de verdade (cliente ainda conectado) continua 500 e log de erro.
	logs.Reset()
	mux2 := http.NewServeMux()
	Mount(mux2, log, []Route{{Operation: "y", Method: "GET", Path: "/v1/quebrado", Handler: func(http.ResponseWriter, *http.Request) error {
		return errors.New("falha real")
	}}})
	rec = httptest.NewRecorder()
	Chain(mux2, log).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/quebrado", nil))
	if rec.Code != 500 || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("erro real: %d %s", rec.Code, logs.String())
	}
}
