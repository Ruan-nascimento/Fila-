package busca

import "testing"

func TestFold(t *testing.T) {
	for in, want := range map[string]string{"  Corte   Degradê ": "corte degrade", "AÇÃO": "acao", "Pezinho": "pezinho", "": ""} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, esperado %q", in, got, want)
		}
	}
	if got := Contains("50%_off"); got != `%50\%\_off%` {
		t.Errorf("curingas: %q", got)
	}
}
