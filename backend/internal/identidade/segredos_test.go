package identidade

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestTokenIs256BitsAndHashed(t *testing.T) {
	tok, h := newToken()
	if len(tok) != 43 || len(h) != 32 {
		t.Fatalf("token %d chars, hash %d bytes", len(tok), len(h))
	}
	tok2, _ := newToken()
	if tok == tok2 {
		t.Fatal("tokens repetidos")
	}
	if string(hashToken(tok)) != string(h) {
		t.Fatal("hash inconsistente")
	}
}

func TestRecoveryCodeFormatAndNormalization(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`)
	seen := map[string]bool{}
	for range 200 {
		c := newRecoveryCode()
		if !re.MatchString(c) {
			t.Fatalf("formato inválido: %q", c)
		}
		if seen[c] {
			t.Fatal("código repetido")
		}
		seen[c] = true
		n, ok := normalizeRecoveryCode(strings.ToLower(strings.ReplaceAll(c, "-", " ")))
		if !ok || n != strings.ReplaceAll(c, "-", "") {
			t.Fatalf("normalização de %q falhou: %q", c, n)
		}
	}
	if n, ok := normalizeRecoveryCode("oooo-iiii-llll-0000"); !ok || n != "0000111111110000" {
		t.Fatalf("trocas O→0 e I/L→1: %q %v", n, ok)
	}
	for _, bad := range []string{"", "ABCD-EFGH-JKMN", "ABCD-EFGH-JKMN-PQRS-TVWX", "ABCD-EFGH-JKMN-PQRU"} {
		if _, ok := normalizeRecoveryCode(bad); ok {
			t.Errorf("%q deveria ser recusado", bad)
		}
	}
}

func TestPasswordHashing(t *testing.T) {
	h := NewHasher(DefaultPasswordParams, 2)
	ctx := context.Background()
	start := time.Now()
	enc, err := h.Hash(ctx, "senha muito boa")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tempo de um hash Argon2id (m=19 MiB, t=2, p=1) nesta máquina: %s", time.Since(start))
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(enc, "senha") {
		t.Fatalf("formato inesperado: %s", enc)
	}
	if ok, rehash, err := h.Verify(ctx, "senha muito boa", enc); !ok || rehash || err != nil {
		t.Fatalf("verificação correta falhou: %v %v %v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify(ctx, "senha muito boA", enc); ok {
		t.Fatal("senha errada aceita")
	}
	// NFKC: "\ufb01" (ligadura) e "fi" são a mesma senha digitada.
	enc2, _ := h.Hash(ctx, "con\ufb01ança-total")
	if ok, _, _ := h.Verify(ctx, "confiança-total", enc2); !ok {
		t.Fatal("normalização NFKC não aplicada")
	}
	weaker := NewHasher(PasswordParams{MemoryKiB: 8 * 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 1)
	old, _ := weaker.Hash(ctx, "senha muito boa")
	if ok, rehash, _ := h.Verify(ctx, "senha muito boa", old); !ok || !rehash {
		t.Fatal("hash com parâmetros antigos deve validar e pedir recálculo")
	}
	if _, _, err := h.Verify(ctx, "x", "$2y$10$bcrypt"); err == nil {
		t.Fatal("formato desconhecido deve dar erro")
	}
}

func TestValidateNewPassword(t *testing.T) {
	cases := map[string]string{"curta": "too_short", "": "required", strings.Repeat("a", 129): "too_long",
		"          ": "not_allowed", "dez-chars!": "", strings.Repeat("ç", 10): ""}
	for pw, want := range cases {
		got := ""
		if fe := ValidateNewPassword("/password", pw); fe != nil {
			got = fe.Code
		}
		if got != want {
			t.Errorf("ValidateNewPassword(%q) = %q, esperado %q", pw, got, want)
		}
	}
}
