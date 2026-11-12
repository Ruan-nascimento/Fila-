package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"fila/internal/platform/testdb"
)

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"down"}, {"up", "extra"}} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), args, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "uso: migrate") {
			t.Errorf("args %v: código %d, saída %q", args, code, errb.String())
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"up"}, []string{"FILA_ENV=development"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "FILA_DATABASE_URL") {
		t.Fatalf("código %d, saída %q", code, errb.String())
	}
}

func TestUnreachableDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"up"}, []string{"FILA_ENV=development",
		"FILA_DATABASE_URL=postgres://u:p-oculta@127.0.0.1:1/fila?sslmode=disable", "FILA_DB_CONNECT_TIMEOUT=2s"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "banco inacessível") {
		t.Fatalf("código %d, saída %q", code, errb.String())
	}
	if strings.Contains(errb.String()+out.String(), "p-oculta") {
		t.Fatal("senha apareceu na saída")
	}
}

func TestUpOnEmptyDatabaseThenNoopAndStatus(t *testing.T) {
	t.Chdir(t.TempDir())
	env := []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + testdb.New(t)}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"up"}, env, &out, &errb); code != 0 {
		t.Fatalf("up falhou (%d): %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "aplicada: 00001") {
		t.Fatalf("saída inesperada: %q", out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"up"}, env, &out, &errb); code != 0 || !strings.Contains(out.String(), "nada a aplicar") {
		t.Fatalf("segunda execução: %d %q", code, out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"status"}, env, &out, &errb); code != 0 || strings.Contains(out.String(), "PENDENTE") {
		t.Fatalf("status: %d %q", code, out.String())
	}
}
