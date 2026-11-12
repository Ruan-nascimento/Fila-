package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type pedido struct {
	Nome  string `json:"nome"`
	Idade int    `json:"idade"`
}

func decodeStatus(t *testing.T, ct, body string) (int, Problem) {
	t.Helper()
	h := Handle(slog.New(slog.NewTextHandler(io.Discard, nil)), func(w http.ResponseWriter, r *http.Request) error {
		var p pedido
		if _, err := DecodeJSON(r, &p); err != nil {
			return err
		}
		WriteJSON(w, 200, p)
		return nil
	})
	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var p Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return rec.Code, p
}

func TestDecodeJSON(t *testing.T) {
	if c, _ := decodeStatus(t, "application/json; charset=utf-8", `{"nome":"a","idade":3}`); c != 200 {
		t.Fatalf("válido = %d", c)
	}
	c, p := decodeStatus(t, "application/json", `{"nome":"a","role":"owner"}`)
	if c != 400 || p.Code != "validation_failed" || len(p.Errors) != 1 || p.Errors[0].Field != "/role" || p.Errors[0].Code != "unknown_field" {
		t.Fatalf("campo desconhecido: %d %+v", c, p)
	}
	c, p = decodeStatus(t, "application/json", `{"idade":"três"}`)
	if c != 400 || len(p.Errors) != 1 || p.Errors[0].Field != "/idade" {
		t.Fatalf("tipo errado: %d %+v", c, p)
	}
	if c, _ := decodeStatus(t, "text/plain", `{"nome":"a"}`); c != 415 {
		t.Fatalf("content-type errado = %d", c)
	}
	if c, _ := decodeStatus(t, "application/json", `{"nome":"a"} {"nome":"b"}`); c != 400 {
		t.Fatalf("dois objetos = %d", c)
	}
	if c, _ := decodeStatus(t, "application/json", `{"nome":`); c != 400 {
		t.Fatalf("JSON quebrado = %d", c)
	}
	big := `{"nome":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	if c, p := decodeStatus(t, "application/json", big); c != 413 || p.Code != "payload_too_large" {
		t.Fatalf("corpo grande = %d %s", c, p.Code)
	}
	if c, _ := decodeStatus(t, "", ""); c != 200 {
		t.Fatalf("corpo vazio vira objeto vazio (validação decide) = %d", c)
	}
}

func TestRequireOrigin(t *testing.T) {
	h := RequireOrigin([]string{"https://fila.exemplo"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	cases := []struct {
		method, path, origin string
		want                 int
	}{
		{"POST", "/v1/auth/login", "https://fila.exemplo", 204},
		{"POST", "/v1/auth/login", "https://FILA.exemplo", 204},
		{"POST", "/v1/auth/login", "https://outro.exemplo", 403},
		{"POST", "/v1/auth/login", "", 403},
		{"PATCH", "/v1/me", "null", 403},
		{"GET", "/v1/session", "", 204},
		{"POST", "/v1/webhooks/abacatepay", "", 204},
		{"POST", "/healthz/live", "", 204},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s origin=%q: %d, esperado %d", c.method, c.path, c.origin, rec.Code, c.want)
		}
		if rec.Code == 403 && !strings.Contains(rec.Body.String(), "origin_not_allowed") {
			t.Errorf("403 sem código origin_not_allowed: %s", rec.Body.String())
		}
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	if got := ClientIP(r, false); got != "10.0.0.1" {
		t.Fatalf("sem confiar no proxy: %s", got)
	}
	if got := ClientIP(r, true); got != "2.2.2.2" {
		t.Fatalf("com proxy: deveria usar o último endereço, veio %s", got)
	}
}

func TestRateLimitedHasRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest("GET", "/", nil), nil, ErrRateLimited(1500_000_000))
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "2" || !strings.Contains(rec.Body.String(), `"retry_after_seconds":2`) {
		t.Fatalf("429 incorreto: %d %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}
