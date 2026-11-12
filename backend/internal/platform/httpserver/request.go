package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"slices"
	"strings"
)

// MaxBodyBytes é o limite de corpo das rotas da API (convencoes.md §1).
const MaxBodyBytes = 64 << 10

// ReadBody lê o corpo respeitando o limite e exige Content-Type JSON quando há corpo.
// Devolve nil (sem erro) para corpo vazio.
func ReadBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, &Error{Status: 413, Code: "payload_too_large", Title: "Conteúdo grande demais",
				Detail: "O pedido excede o tamanho máximo permitido."}
		}
		return nil, &Error{Status: 400, Code: "validation_failed", Title: "Dados inválidos",
			Detail: "Não foi possível ler o pedido."}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	if !isJSON(r.Header.Get("Content-Type")) {
		return nil, &Error{Status: 415, Code: "unsupported_media_type", Title: "Formato não suportado",
			Detail: "Envie os dados como application/json."}
	}
	return body, nil
}

func isJSON(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/json"
}

// DecodeJSON lê o corpo e o decodifica em dst, recusando campos desconhecidos
// (additionalProperties: false no contrato). Corpo ausente é tratado como objeto vazio,
// para que os campos obrigatórios apareçam como "required" na validação.
// Devolve também os bytes lidos (usados na impressão digital da idempotência).
func DecodeJSON(r *http.Request, dst any) ([]byte, error) {
	body, err := ReadBody(r)
	if err != nil {
		return nil, err
	}
	if body == nil {
		body = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return nil, decodeError(err)
	}
	if dec.More() {
		return nil, ErrValidation(FieldError{Field: "", Code: FieldInvalidFormat, Message: "conteúdo extra após o JSON"})
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrValidation(FieldError{Field: "", Code: FieldInvalidFormat, Message: "conteúdo extra após o JSON"})
	}
	return body, nil
}

func decodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return ErrValidation(FieldError{Field: pointer(typeErr.Field), Code: FieldInvalidFormat})
	}
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "json: unknown field "); ok {
		return ErrValidation(FieldError{Field: "/" + strings.Trim(name, `"`), Code: FieldUnknown})
	}
	return ErrValidation(FieldError{Field: "", Code: FieldInvalidFormat, Message: "JSON inválido"})
}

// pointer converte "a.b.0" (formato do encoding/json) em JSON Pointer "/a/b/0".
func pointer(field string) string {
	if field == "" {
		return ""
	}
	return "/" + strings.ReplaceAll(field, ".", "/")
}

// ClientIP devolve o IP do cliente. Por padrão usa a conexão TCP; com trustForwardedFor,
// usa o último endereço de X-Forwarded-For (o acrescentado pelo nosso proxy reverso).
// O resultado só serve para limitar tentativas: nunca é prova de identidade.
func ClientIP(r *http.Request, trustForwardedFor bool) string {
	if trustForwardedFor {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			parts := strings.Split(xff[len(xff)-1], ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsUnsafeMethod informa se o método altera estado (exige validação de origem e CSRF).
func IsUnsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// RequireOrigin recusa métodos não seguros sob /v1/ cujo cabeçalho Origin não esteja na lista
// (convencoes.md §10). Webhooks de provedor (/v1/webhooks/) não vêm de navegador e ficam fora.
func RequireOrigin(allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsUnsafeMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/v1/") &&
			!strings.HasPrefix(r.URL.Path, "/v1/webhooks/") {
			origin := strings.ToLower(r.Header.Get("Origin"))
			if origin == "" || !slices.Contains(allowed, origin) {
				writeProblem(w, r, &Error{Status: 403, Code: "origin_not_allowed", Title: "Origem não permitida",
					Detail: "Este pedido não veio de uma página autorizada."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
