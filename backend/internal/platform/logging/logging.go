// Package logging cria o logger estruturado (log/slog) da aplicação.
//
// Proteções:
//   - Atributos cujo nome indica segredo (senha, token, cookie, dsn…) são substituídos por "[OCULTO]",
//     mesmo que alguém os registre por engano.
//   - Valores do tipo config.Secret já se ocultam sozinhos (LogValue).
//
// Isso é uma rede de segurança, não uma licença: o código não deve registrar dados pessoais
// (nick, e-mail) nem segredos.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

const redacted = "[OCULTO]"

// sensitiveFragments: se o nome do atributo contiver algum destes trechos, o valor é ocultado.
var sensitiveFragments = []string{
	"password", "passwd", "senha", "secret", "segredo", "token", "cookie", "authorization",
	"dsn", "database_url", "api_key", "apikey", "recovery", "credential",
}

// New cria um logger JSON (padrão) ou texto, no nível indicado.
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: redact}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

func redact(_ []string, a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	return a
}

// IsSensitiveKey informa se o nome de um campo indica conteúdo sensível.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, f := range sensitiveFragments {
		if strings.Contains(k, f) {
			return true
		}
	}
	return false
}
