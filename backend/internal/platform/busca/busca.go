// Package busca normaliza texto para pesquisa sem diferenciar maiúsculas nem acentos, igual no
// Go e no SQL (translate + lower), e protege os curingas do LIKE. Sem extensão do PostgreSQL nem
// mecanismo externo de busca: os volumes por loja são pequenos (ADR-0036).
package busca

import (
	"fmt"
	"strings"
)

const (
	foldFrom = "áàâãäéèêëíìîïóòôõöúùûüçñÁÀÂÃÄÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑ"
	foldTo   = "aaaaaeeeeiiiiooooouuuucnaaaaaeeeeiiiiooooouuuucn"
	// MaxTerm limita o termo de busca (caracteres depois de normalizar).
	MaxTerm = 60
)

var foldMap = func() map[rune]rune {
	from, to := []rune(foldFrom), []rune(foldTo)
	m := make(map[rune]rune, len(from))
	for i, r := range from {
		m[r] = to[i]
	}
	return m
}()

// FoldSQL devolve a expressão SQL normalizada (mesma regra de Fold).
func FoldSQL(expr string) string {
	return fmt.Sprintf("translate(lower(%s), '%s', '%s')", expr, foldFrom, foldTo)
}

// Fold normaliza: minúsculas, sem acento, espaços repetidos e das pontas removidos.
func Fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.Join(strings.Fields(s), " ")) {
		if t, ok := foldMap[r]; ok {
			r = t
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LikeEscape protege os curingas do LIKE (% e _) digitados na busca.
func LikeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Contains devolve o padrão LIKE "contém" já normalizado e protegido.
func Contains(term string) string { return "%" + LikeEscape(Fold(term)) + "%" }
