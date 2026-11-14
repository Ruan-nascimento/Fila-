package metrics

import (
	"strings"
	"testing"
)

func TestTextFormat(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("fila_http_requests_total", "Requisições.", "route", "code")
	c.Inc("GET /v1/x", "200")
	c.Add(2, "GET /v1/x", "200")
	c.Inc(`rota "estranha"`+"\n", "500")
	g := r.NewGauge("fila_sse_connections", "Conexões.")
	g.Set(3)
	g.Add(-1)
	r.NewGaugeFunc("fila_lag_seconds", "Atraso.", func() float64 { return 1.5 })
	h := r.NewHistogram("fila_http_request_duration_seconds", "Latência.", []float64{0.1, 1}, "route")
	h.Observe(0.05, "GET /v1/x")
	h.Observe(0.5, "GET /v1/x")
	h.Observe(5, "GET /v1/x")

	var b strings.Builder
	r.Write(&b)
	out := b.String()
	for _, want := range []string{
		"# TYPE fila_http_requests_total counter",
		`fila_http_requests_total{route="GET /v1/x",code="200"} 3`,
		`fila_http_requests_total{route="rota \"estranha\"\n",code="500"} 1`,
		"fila_sse_connections 2",
		"fila_lag_seconds 1.5",
		`fila_http_request_duration_seconds_bucket{route="GET /v1/x",le="0.1"} 1`,
		`fila_http_request_duration_seconds_bucket{route="GET /v1/x",le="1"} 2`,
		`fila_http_request_duration_seconds_bucket{route="GET /v1/x",le="+Inf"} 3`,
		`fila_http_request_duration_seconds_sum{route="GET /v1/x"} 5.55`,
		`fila_http_request_duration_seconds_count{route="GET /v1/x"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q em:\n%s", want, out)
		}
	}
	if c.Value("GET /v1/x", "200") != 3 {
		t.Fatal("Value")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("nome repetido deveria entrar em pânico")
		}
	}()
	r.NewGauge("fila_sse_connections", "de novo")
}
