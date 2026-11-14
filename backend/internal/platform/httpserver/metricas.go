package httpserver

import (
	"strconv"
	"strings"
	"time"

	"fila/internal/platform/metrics"
)

// HTTPMetrics são as métricas de requisição da API: contagem por rota-modelo, método e status,
// latência por rota (sem a rota SSE, que fica aberta por minutos de propósito) e recusas do
// limite geral. Rótulos nunca têm id, IP ou caminho real: só a rota-modelo do contrato.
type HTTPMetrics struct {
	requests *metrics.Counter
	duration *metrics.Histogram
	limited  *metrics.Counter
}

// NewHTTPMetrics registra as métricas HTTP.
func NewHTTPMetrics(reg *metrics.Registry) *HTTPMetrics {
	return &HTTPMetrics{
		requests: reg.NewCounter("fila_http_requests_total", "Requisições HTTP por rota-modelo, método e status.", "route", "method", "status"),
		duration: reg.NewHistogram("fila_http_request_duration_seconds", "Duração das requisições HTTP (sem o fluxo SSE).", metrics.DefaultBuckets, "route"),
		limited:  reg.NewCounter("fila_http_rate_limited_total", "Requisições recusadas pelo limite geral por IP."),
	}
}

// Observe registra uma requisição (para Options.Observe).
func (m *HTTPMetrics) Observe(route, method string, status int, d time.Duration) {
	if status == 0 {
		status = 200
	}
	m.requests.Inc(route, method, strconv.Itoa(status))
	if !strings.Contains(route, "/v1/stream") {
		m.duration.Observe(d.Seconds(), route)
	}
}

// Limited conta uma recusa do limite geral (para Options.Limited).
func (m *HTTPMetrics) Limited() { m.limited.Inc() }
