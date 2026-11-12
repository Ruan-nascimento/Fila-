package validate

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	cases := []struct {
		in   string
		want string
		code string
	}{
		{"  Ana  ", "Ana", ""},
		{"Jose\u0301", "José", ""}, // NFD → NFC
		{"<img src=x onerror=alert(1)>", "<img src=x onerror=alert(1)>", ""}, // texto, não HTML: aceito e exibido literalmente
		{"👨\u200d👩\u200d👧", "👨\u200d👩\u200d👧", ""},
		{"", "", "required"},
		{"   ", "", "required"},
		{"a\x00b", "", "not_allowed"},
		{"a\nb", "", "not_allowed"},
		{"abc\u202edef", "", "not_allowed"}, // inversão de direção do texto
		{strings.Repeat("á", 31), "", "too_long"},
	}
	for _, c := range cases {
		got, fe := Text("/nick", c.in, 1, 30)
		code := ""
		if fe != nil {
			code = fe.Code
		}
		if code != c.code || (c.code == "" && got != c.want) {
			t.Errorf("Text(%q) = %q, %q; esperado %q, %q", c.in, got, code, c.want, c.code)
		}
	}
	if _, fe := Text("/nick", strings.Repeat("á", 30), 1, 30); fe != nil {
		t.Error("30 caracteres acentuados devem caber (conta caracteres, não bytes)")
	}
}

func TestEmail(t *testing.T) {
	ok := map[string]string{
		"Ana@Exemplo.com":     "ana@exemplo.com",
		" ana@exemplo.com.br": "ana@exemplo.com.br",
	}
	for in, norm := range ok {
		_, n, fe := Email("/email", in)
		if fe != nil || n != norm {
			t.Errorf("Email(%q) = %q, %v", in, n, fe)
		}
	}
	for _, in := range []string{"", "ana", "ana@", "Ana <ana@exemplo.com>", "ana@exemplo", "a b@exemplo.com", "ana@exem\nplo.com"} {
		if _, _, fe := Email("/email", in); fe == nil {
			t.Errorf("Email(%q) deveria falhar", in)
		}
	}
}
