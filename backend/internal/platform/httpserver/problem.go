package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Problem é o envelope de erro do contrato (RFC 9457 + extensões). Ver docs/contrato/convencoes.md §3.
type Problem struct {
	Type              string       `json:"type"`
	Title             string       `json:"title"`
	Status            int          `json:"status"`
	Detail            string       `json:"detail,omitempty"`
	Code              string       `json:"code"`
	RequestID         string       `json:"request_id"`
	Errors            []FieldError `json:"errors,omitempty"`
	Current           any          `json:"current,omitempty"`
	RetryAfterSeconds int          `json:"retry_after_seconds,omitempty"`
}

// FieldError aponta o campo inválido (JSON Pointer) e o motivo estável.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Códigos de FieldError (enum do contrato).
const (
	FieldRequired      = "required"
	FieldUnknown       = "unknown_field"
	FieldInvalidFormat = "invalid_format"
	FieldTooShort      = "too_short"
	FieldTooLong       = "too_long"
	FieldOutOfRange    = "out_of_range"
	FieldNotAllowed    = "not_allowed"
)

// Error é um erro destinado ao cliente. Title e Detail precisam ser seguros para o usuário
// (português, sem dados internos). Handlers devolvem *Error; qualquer outro erro vira 500.
type Error struct {
	Status     int
	Code       string
	Title      string
	Detail     string
	Fields     []FieldError
	RetryAfter time.Duration
	Current    any
}

func (e *Error) Error() string { return e.Code + ": " + e.Title }

// Erros comuns do contrato.
func ErrUnauthenticated() *Error {
	return &Error{Status: 401, Code: "unauthenticated", Title: "Entre para continuar",
		Detail: "Sua sessão não é válida ou expirou."}
}

func ErrForbidden(detail string) *Error {
	return &Error{Status: 403, Code: "forbidden", Title: "Sem permissão", Detail: detail}
}

func ErrNotFound() *Error {
	return &Error{Status: 404, Code: "not_found", Title: "Não encontrado",
		Detail: "O recurso não existe ou você não tem acesso a ele."}
}

func ErrValidation(fields ...FieldError) *Error {
	return &Error{Status: 400, Code: "validation_failed", Title: "Dados inválidos",
		Detail: "Confira os campos destacados.", Fields: fields}
}

func ErrRateLimited(retry time.Duration) *Error {
	return &Error{Status: 429, Code: "rate_limited", Title: "Muitas tentativas",
		Detail: "Aguarde um pouco antes de tentar de novo.", RetryAfter: retry}
}

// WriteProblem responde com application/problem+json. title e detail devem ser seguros para o usuário.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, title, detail string) {
	writeProblem(w, r, &Error{Status: status, Code: code, Title: title, Detail: detail})
}

func writeProblem(w http.ResponseWriter, r *http.Request, e *Error) {
	p := Problem{
		Type:      "/problems/" + e.Code,
		Title:     e.Title,
		Status:    e.Status,
		Detail:    e.Detail,
		Code:      e.Code,
		RequestID: RequestIDFrom(r.Context()),
		Errors:    e.Fields,
		Current:   e.Current,
	}
	if e.RetryAfter > 0 {
		secs := int(math.Ceil(e.RetryAfter.Seconds()))
		p.RetryAfterSeconds = max(secs, 1)
		w.Header().Set("Retry-After", strconv.Itoa(p.RetryAfterSeconds))
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// StatusClientClosedRequest (499) marca pedido abandonado pelo cliente (não é erro do servidor).
const StatusClientClosedRequest = 499

// WriteError escreve err como problem+json. *Error vai como está; qualquer outro erro é
// registrado no log (só o texto do erro, sem dados da requisição) e vira 500 genérico.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var e *Error
	if errors.As(err, &e) {
		writeProblem(w, r, e)
		return
	}
	// O cliente foi embora no meio (mudou de página, fechou a aba, perdeu a rede): a consulta foi
	// cancelada por isso. Não é falha do servidor — ninguém vai ler a resposta. Registra como
	// informação e responde 499 (convenção para "cliente fechou a conexão"), sem virar alarme.
	if r.Context().Err() != nil {
		if log != nil {
			log.Info("cliente desistiu da requisição", "request_id", RequestIDFrom(r.Context()), "route", r.Pattern)
		}
		w.WriteHeader(StatusClientClosedRequest)
		return
	}
	if log != nil {
		log.Error("erro interno ao tratar requisição", "request_id", RequestIDFrom(r.Context()),
			"route", r.Pattern, "error", err.Error())
	}
	writeProblem(w, r, &Error{Status: 500, Code: "internal_error", Title: "Erro inesperado",
		Detail: "Tente novamente. Se persistir, informe o código da requisição."})
}

// WriteJSON responde com application/json.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Data envolve um recurso no envelope de sucesso { "data": ... } do contrato.
type Data[T any] struct {
	Data T `json:"data"`
}

// NotFound é o tratamento padrão para rotas inexistentes.
func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, http.StatusNotFound, "not_found", "Não encontrado", "O endereço solicitado não existe.")
}

// HandlerFunc é um handler que devolve erro; use Handle para adaptá-lo a http.Handler.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle converte HandlerFunc em http.Handler, escrevendo o erro devolvido com WriteError.
func Handle(log *slog.Logger, h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RecordRoute(r)
		if err := h(w, r); err != nil {
			WriteError(w, r, log, err)
		}
	})
}
