// Package validate normaliza e valida textos livres e e-mails (convencoes.md §11).
//
// Textos (nome, nick, motivo) são normalizados em NFC, sem espaços nas pontas, sem caracteres
// de controle nem de formatação invisível (que permitiriam esconder ou inverter texto).
// O tamanho é contado em caracteres Unicode, como o maxLength do JSON Schema.
package validate

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"fila/internal/platform/httpserver"
)

// zwj (U+200D) é formatação invisível, mas necessária em emojis compostos (👨\u200d👩\u200d👧).
const zwj = '\u200d'

// Text normaliza s e confere o tamanho [minLen, maxLen]. field é o JSON Pointer do campo.
func Text(field, s string, minLen, maxLen int) (string, *httpserver.FieldError) {
	if !utf8.ValidString(s) {
		return "", &httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat}
	}
	s = strings.TrimSpace(norm.NFC.String(s))
	for _, r := range s {
		if unicode.IsControl(r) || (unicode.Is(unicode.Cf, r) && r != zwj) {
			return "", &httpserver.FieldError{Field: field, Code: httpserver.FieldNotAllowed,
				Message: "contém caracteres de controle ou invisíveis"}
		}
	}
	n := utf8.RuneCountInString(s)
	switch {
	case n == 0 && minLen > 0:
		return "", &httpserver.FieldError{Field: field, Code: httpserver.FieldRequired}
	case n < minLen:
		return "", &httpserver.FieldError{Field: field, Code: httpserver.FieldTooShort}
	case n > maxLen:
		return "", &httpserver.FieldError{Field: field, Code: httpserver.FieldTooLong}
	}
	return s, nil
}

// MaxEmailLen é o limite do contrato para e-mail.
const MaxEmailLen = 254

// Email valida o formato e devolve o endereço (como digitado, sem espaços nas pontas) e a forma
// normalizada usada para unicidade (minúsculas, NFC). Não aceita nome de exibição ("Ana <a@b>").
func Email(field, s string) (addr, normalized string, fe *httpserver.FieldError) {
	s = strings.TrimSpace(norm.NFC.String(s))
	if s == "" {
		return "", "", &httpserver.FieldError{Field: field, Code: httpserver.FieldRequired}
	}
	if utf8.RuneCountInString(s) > MaxEmailLen {
		return "", "", &httpserver.FieldError{Field: field, Code: httpserver.FieldTooLong}
	}
	parsed, err := mail.ParseAddress(s)
	if err != nil || parsed.Address != s || parsed.Name != "" || !strings.Contains(s[strings.LastIndexByte(s, '@')+1:], ".") {
		return "", "", &httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat}
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", "", &httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat}
		}
	}
	return s, strings.ToLower(s), nil
}

// Version confere expected_version (obrigatório, inteiro ≥ 1) e devolve o valor.
func (c *Collector) Version(field string, v *int64) int64 {
	switch {
	case v == nil:
		c.Required(field, true)
		return 0
	case *v < 1:
		c.errs = append(c.errs, httpserver.FieldError{Field: field, Code: httpserver.FieldOutOfRange})
		return 0
	}
	return *v
}

// IntRange confere um inteiro obrigatório dentro de [minV, maxV].
func (c *Collector) IntRange(field string, v *int64, minV, maxV int64) int64 {
	switch {
	case v == nil:
		c.Required(field, true)
		return 0
	case *v < minV || *v > maxV:
		c.errs = append(c.errs, httpserver.FieldError{Field: field, Code: httpserver.FieldOutOfRange})
		return 0
	}
	return *v
}

// Collector junta erros de campo para responder todos de uma vez.
type Collector struct {
	errs []httpserver.FieldError
}

// Add guarda o erro se não for nil.
func (c *Collector) Add(fe *httpserver.FieldError) {
	if fe != nil {
		c.errs = append(c.errs, *fe)
	}
}

// Required registra campo obrigatório ausente quando missing for verdadeiro.
func (c *Collector) Required(field string, missing bool) {
	if missing {
		c.errs = append(c.errs, httpserver.FieldError{Field: field, Code: httpserver.FieldRequired})
	}
}

// Err devolve 400 validation_failed com todos os campos, ou nil.
func (c *Collector) Err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return httpserver.ErrValidation(c.errs...)
}
