package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"fila/internal/platform/config"
)

func TestGenerateKeyIsValidForConfig(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"gerar-chave"}, nil, &out, &errb); code != 0 {
		t.Fatalf("saída %d: %s", code, errb.String())
	}
	key := strings.TrimSpace(out.String())
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		t.Fatalf("chave inválida: %v (%d bytes)", err, len(raw))
	}
	cfg, err := config.Load([]string{"FILA_ENV=development", "FILA_DATABASE_URL=postgres://u:p@h/db", "FILA_SECRET_KEY=" + key})
	if err != nil || cfg.RequireSecretKey() != nil {
		t.Fatalf("configuração deveria aceitar a chave gerada: %v", err)
	}
	var out2 bytes.Buffer
	run(context.Background(), []string{"gerar-chave"}, nil, &out2, &errb)
	if out2.String() == out.String() {
		t.Fatal("duas chaves geradas não podem ser iguais")
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"inexistente"}, {"gerar-chave", "extra"}} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), args, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "uso:") {
			t.Errorf("%v: esperava código 2 e uso, veio %d %q", args, code, errb.String())
		}
	}
}
