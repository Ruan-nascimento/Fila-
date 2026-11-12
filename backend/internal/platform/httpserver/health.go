package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// ReadinessCheck verifica uma dependência (banco acessível, migrações em dia…).
type ReadinessCheck func(ctx context.Context) error

// Check é uma verificação com nome (o nome aparece só no log). Rodam na ordem dada.
type Check struct {
	Name string
	Fn   ReadinessCheck
}

// Health implementa /healthz/live e /healthz/ready (operações getLiveness e getReadiness do contrato).
//
//   - live: o processo responde. Não consulta dependências (evita reinícios em cascata se o banco cair).
//   - ready: pode receber tráfego. Falha se alguma verificação falhar ou se o servidor está encerrando.
type Health struct {
	log      *slog.Logger
	checks   []Check
	timeout  time.Duration
	draining atomic.Bool
}

// NewHealth cria o verificador. timeout limita cada rodada de verificações.
func NewHealth(log *slog.Logger, timeout time.Duration, checks ...Check) *Health {
	return &Health{log: log, checks: checks, timeout: timeout}
}

// StartDraining faz o readiness falhar, para o balanceador parar de mandar tráfego antes do desligamento.
func (h *Health) StartDraining() { h.draining.Store(true) }

// Register adiciona as rotas de saúde ao mux.
func (h *Health) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz/live", h.live)
	mux.HandleFunc("GET /healthz/ready", h.ready)
}

type healthBody struct {
	Status string `json:"status"`
}

func (h *Health) live(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, healthBody{Status: "ok"})
}

func (h *Health) ready(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		WriteProblem(w, r, http.StatusServiceUnavailable, "service_unavailable",
			"Serviço encerrando", "O servidor está sendo desligado.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	for _, c := range h.checks {
		if err := c.Fn(ctx); err != nil {
			// O motivo detalhado vai só para o log; o cliente recebe mensagem genérica.
			h.log.Warn("readiness falhou", "check", c.Name, "request_id", RequestIDFrom(r.Context()), "error", err.Error())
			WriteProblem(w, r, http.StatusServiceUnavailable, "service_unavailable",
				"Serviço indisponível", "Uma dependência do servidor não está pronta.")
			return
		}
	}
	WriteJSON(w, http.StatusOK, healthBody{Status: "ok"})
}
