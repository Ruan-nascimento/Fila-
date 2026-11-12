package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"fila/internal/platform/config"
	"fila/internal/platform/testdb"
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

func TestBootstrapCreatesShopAndOwnerInvite(t *testing.T) {
	t.Chdir(t.TempDir())
	pool := testdb.Migrated(t)
	env := []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + pool.Config().ConnString(), testdb.SecretKeyVar()}
	args := []string{"bootstrap", "-nome", "Barbearia Teste", "-slug", "barbearia-teste", "-email", "dona@exemplo.com", "-motivo", "piloto"}
	var out, errb bytes.Buffer
	if code := run(context.Background(), args, env, &out, &errb); code != 0 {
		t.Fatalf("saída %d: %s %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "loja criada:") || strings.Contains(out.String()+errb.String(), "token") {
		t.Fatalf("saída inesperada: %s", out.String())
	}
	var shops, invites, mails, audits int
	ctx := context.Background()
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM establishment WHERE slug = 'barbearia-teste'`).Scan(&shops)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM invite WHERE role = 'owner' AND status = 'pending' AND created_by_account_id IS NULL`).Scan(&invites)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM mail_outbox WHERE kind = 'convite_owner'`).Scan(&mails)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE action = 'T-OP-01' AND actor_kind = 'operator' AND reason = 'piloto'`).Scan(&audits)
	if shops != 1 || invites != 1 || mails != 1 || audits != 1 {
		t.Fatalf("loja=%d convite=%d email=%d auditoria=%d", shops, invites, mails, audits)
	}
	if strings.Contains(out.String()+errb.String(), "dona@exemplo.com") {
		t.Fatal("e-mail não deveria aparecer na saída/log")
	}

	// Slug repetido e dados inválidos falham com código 2 sem criar nada.
	out.Reset()
	errb.Reset()
	if code := run(context.Background(), args, env, &out, &errb); code != 2 || !strings.Contains(errb.String(), "slug") {
		t.Fatalf("slug repetido: %d %s", code, errb.String())
	}
	bad := []string{"bootstrap", "-nome", "X", "-slug", "Com Espaço", "-email", "invalido", "-fuso", "Marte/Base"}
	errb.Reset()
	if code := run(context.Background(), bad, env, &out, &errb); code != 2 || !strings.Contains(errb.String(), "/slug") || !strings.Contains(errb.String(), "/email") || !strings.Contains(errb.String(), "/fuso") {
		t.Fatalf("dados inválidos: %d %s", code, errb.String())
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM establishment`).Scan(&shops)
	if shops != 1 {
		t.Fatalf("nada deveria ser criado nas falhas; lojas=%d", shops)
	}
	errb.Reset()
	if code := run(context.Background(), []string{"bootstrap", "-nome", "X"}, env, &out, &errb); code != 2 {
		t.Fatalf("faltando opções: %d", code)
	}
}
