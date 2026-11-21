package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// O workflow Publicar usa estes cabeçalhos para saber que a instância NOVA entrou no ar:
// revisão do commit e início do processo (com o mesmo commit, só o início muda).
func TestLiveRevisionAndStartHeaders(t *testing.T) {
	h := NewHealth(slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	h.Revision = "abcdef123456"
	h.StartedAt = time.Unix(1795000000, 0)
	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz/live", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("X-Fila-Revision"); got != "abcdef123456" {
		t.Fatalf("revisão: %q", got)
	}
	if got := w.Header().Get("X-Fila-Started"); got != "1795000000" {
		t.Fatalf("início: %q", got)
	}

	vazio := NewHealth(slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	mux2 := http.NewServeMux()
	vazio.Register(mux2)
	w2 := httptest.NewRecorder()
	mux2.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/healthz/live", nil))
	if w2.Header().Get("X-Fila-Revision") != "" || w2.Header().Get("X-Fila-Started") != "" {
		t.Fatal("sem configuração, os cabeçalhos não devem aparecer")
	}
}
