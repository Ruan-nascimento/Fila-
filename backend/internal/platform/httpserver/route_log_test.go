package httpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type ctxMarker struct{}

// Um middleware que cria uma cópia da requisição (como o de sessão) não pode fazer o log de
// acesso perder a rota-modelo.
func TestAccessLogKeepsRouteBehindCopyingMiddleware(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	mux := http.NewServeMux()
	Mount(mux, log, []Route{{Operation: "x", Method: "GET", Path: "/v1/lojas/{id}", Handler: func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(204)
		return nil
	}}})
	copying := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxMarker{}, 1)))
	})
	Chain(copying, log).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/lojas/123?segredo=x", nil))
	out := logs.String()
	if !strings.Contains(out, `"route":"GET /v1/lojas/{id}"`) {
		t.Fatalf("rota ausente no log de acesso: %s", out)
	}
	if strings.Contains(out, "segredo") || strings.Contains(out, "123") {
		t.Fatalf("log com query ou valor do caminho: %s", out)
	}
}
