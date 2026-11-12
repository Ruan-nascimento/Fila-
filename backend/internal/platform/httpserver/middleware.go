package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

type ctxKey int

const requestIDKey ctxKey = 1

// HeaderRequestID devolve ao cliente o identificador da requisição (para suporte e diagnóstico).
const HeaderRequestID = "X-Request-Id"

// RequestIDFrom lê o ID da requisição do contexto ("" se ausente).
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Chain aplica os middlewares da plataforma na ordem correta.
// O ID é sempre gerado pelo servidor: um valor enviado pelo cliente não é confiável nem reaproveitado.
func Chain(h http.Handler, log *slog.Logger) http.Handler {
	return requestID(accessLog(log, recoverer(log, securityHeaders(h))))
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler { // sinal do próprio net/http para abortar; repassa
				panic(rec)
			}
			// Registramos o tipo e a pilha, nunca o corpo da requisição.
			log.Error("pânico ao tratar requisição",
				"request_id", RequestIDFrom(r.Context()),
				"panic_type", typeName(rec),
				"stack", string(debug.Stack()))
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error",
				"Erro inesperado", "Tente novamente. Se persistir, informe o código da requisição.")
		}()
		next.ServeHTTP(w, r)
	})
}

func typeName(v any) string {
	switch v.(type) {
	case error:
		return "error"
	case string:
		return "string"
	default:
		return "outro"
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap permite que http.ResponseController alcance o writer original (necessário para SSE na etapa 06).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// accessLog registra método, rota-modelo, status e duração. Não registra query string, corpo,
// cabeçalhos nem cookies (podem conter dados pessoais ou segredos).
func accessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "(sem rota)"
		}
		level := slog.LevelInfo
		if r.URL.Path == "/healthz/live" || r.URL.Path == "/healthz/ready" {
			level = slog.LevelDebug // evita poluir o log com sondas de saúde
		}
		if rec.status >= 500 {
			level = slog.LevelError
		}
		log.Log(r.Context(), level, "requisição",
			"request_id", RequestIDFrom(r.Context()),
			"method", r.Method,
			"route", route,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds())
	})
}
