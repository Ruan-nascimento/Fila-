package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fila/internal/platform/metrics"
)

func TestIPLimiterRefillsAndSweeps(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	l := NewIPLimiter(60) // 1 por segundo, rajada de 30
	l.now = func() time.Time { return now }
	for i := range 30 {
		if ok, _ := l.Allow("1.1.1.1"); !ok {
			t.Fatalf("rajada recusada na %d", i)
		}
	}
	ok, wait := l.Allow("1.1.1.1")
	if ok || wait < time.Second {
		t.Fatalf("31ª deveria esperar: %v %v", ok, wait)
	}
	if ok, _ := l.Allow("2.2.2.2"); !ok {
		t.Fatal("outro IP não é afetado")
	}
	now = now.Add(2 * time.Second)
	if ok, _ := l.Allow("1.1.1.1"); !ok {
		t.Fatal("depois de 2 s há ficha de novo")
	}
	now = now.Add(5 * time.Minute)
	l.Allow("3.3.3.3")
	if l.Size() != 1 {
		t.Fatalf("IPs parados deveriam sair da memória: %d", l.Size())
	}
}

func TestChainWithLimitsAndObserves(t *testing.T) {
	reg := metrics.NewRegistry()
	m := NewHTTPMetrics(reg)
	lim := NewIPLimiter(30) // rajada 15
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/x/{id}", func(w http.ResponseWriter, r *http.Request) { RecordRoute(r); w.WriteHeader(204) })
	mux.HandleFunc("POST /v1/webhooks/p", func(w http.ResponseWriter, r *http.Request) { RecordRoute(r); w.WriteHeader(200) })
	mux.HandleFunc("GET /healthz/live", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := ChainWith(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Limiter: lim, TrustForwardedFor: true,
		Observe: m.Observe, Limited: m.Limited})
	do := func(method, path, ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Forwarded-For", ip)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	var last *httptest.ResponseRecorder
	for range 16 {
		last = do("GET", "/v1/x/123", "9.9.9.9")
	}
	if last.Code != 429 || last.Header().Get("Retry-After") == "" || !strings.Contains(last.Body.String(), "rate_limited") {
		t.Fatalf("16ª requisição: %d %s", last.Code, last.Body)
	}
	// Webhook do provedor e sondas de saúde não entram no limite geral.
	if r := do("POST", "/v1/webhooks/p", "9.9.9.9"); r.Code != 200 {
		t.Fatalf("webhook limitado: %d", r.Code)
	}
	if r := do("GET", "/healthz/live", "9.9.9.9"); r.Code != 200 {
		t.Fatalf("saúde limitada: %d", r.Code)
	}
	var b strings.Builder
	reg.Write(&b)
	out := b.String()
	for _, want := range []string{
		`fila_http_requests_total{route="GET /v1/x/{id}",method="GET",status="204"} 15`,
		`fila_http_requests_total{route="(sem rota)",method="GET",status="429"} 1`,
		"fila_http_rate_limited_total 1",
		`fila_http_request_duration_seconds_count{route="GET /v1/x/{id}"} 15`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "123") || strings.Contains(out, "9.9.9.9") {
		t.Fatal("métrica não pode ter id real nem IP")
	}
}
