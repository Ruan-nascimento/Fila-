// Package jsonscrub tira o nome de uma pessoa excluída de documentos JSON guardados por pouco
// tempo (eventos de tempo real, respostas guardadas para repetição) sem tocar em mais nada.
//
// Regra (revisão da etapa 13, R-13-01): a troca é por IDENTIDADE, nunca por texto. Só muda:
//   - o campo "nick" de um objeto cujo "id" ou "participation_id" é uma visita da pessoa;
//   - o campo "holder_display_name" de um objeto cujo "id" é uma reserva da pessoa.
//
// Assim, outra pessoa com o mesmo nome e valores iguais ao nome (ex.: um estado "waiting")
// continuam intactos.
package jsonscrub

import (
	"bytes"
	"encoding/json"
)

// Targets são as visitas e reservas da pessoa excluída e o texto que entra no lugar do nome.
type Targets struct {
	Participations map[string]bool
	Reservations   map[string]bool
	Replacement    string
}

// Empty informa se não há nada a procurar.
func (t Targets) Empty() bool { return len(t.Participations) == 0 && len(t.Reservations) == 0 }

// IDs devolve todos os ids procurados (para filtrar candidatos no banco antes de ler o JSON).
func (t Targets) IDs() []string {
	out := make([]string, 0, len(t.Participations)+len(t.Reservations))
	for id := range t.Participations {
		out = append(out, id)
	}
	for id := range t.Reservations {
		out = append(out, id)
	}
	return out
}

// Rewrite devolve o documento com os nomes trocados e se algo mudou. Documento que não é JSON
// válido volta igual (nada a fazer).
func (t Targets) Rewrite(doc []byte) ([]byte, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber() // números voltam exatamente como estavam
	var v any
	if err := dec.Decode(&v); err != nil {
		return doc, false, nil
	}
	if !t.walk(v) {
		return doc, false, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return doc, false, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true, nil
}

func (t Targets) walk(v any) bool {
	changed := false
	switch x := v.(type) {
	case map[string]any:
		id, _ := x["id"].(string)
		pid, _ := x["participation_id"].(string)
		if _, ok := x["nick"].(string); ok && (t.Participations[id] || t.Participations[pid]) && x["nick"] != t.Replacement {
			x["nick"] = t.Replacement
			changed = true
		}
		if _, ok := x["holder_display_name"].(string); ok && t.Reservations[id] && x["holder_display_name"] != t.Replacement {
			x["holder_display_name"] = t.Replacement
			changed = true
		}
		for _, child := range x {
			if t.walk(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range x {
			if t.walk(child) {
				changed = true
			}
		}
	}
	return changed
}
