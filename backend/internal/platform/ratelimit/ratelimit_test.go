package ratelimit_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"fila/internal/platform/clock"
	"fila/internal/platform/keys"
	"fila/internal/platform/ratelimit"
	"fila/internal/platform/testdb"
)

func TestFixedWindowPerSubject(t *testing.T) {
	pool := testdb.Migrated(t)
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	ring, _ := keys.New(bytes.Repeat([]byte{7}, 32))
	l := ratelimit.New(pool, c, ring)
	rule := ratelimit.Rule{Name: "teste", Limit: 3, Window: 15 * time.Minute}

	for i := 1; i <= 3; i++ {
		ok, _, err := l.Allow(ctx, rule, "ana@exemplo.com")
		if err != nil || !ok {
			t.Fatalf("tentativa %d deveria passar: %v", i, err)
		}
	}
	ok, retry, err := l.Allow(ctx, rule, "ana@exemplo.com")
	if err != nil || ok {
		t.Fatalf("4ª tentativa deveria ser bloqueada (ok=%v err=%v)", ok, err)
	}
	if retry <= 0 || retry > 15*time.Minute {
		t.Fatalf("Retry-After fora da janela: %v", retry)
	}
	if ex, _, _ := l.Exceeded(ctx, rule, "ana@exemplo.com"); !ex {
		t.Fatal("Exceeded deveria indicar bloqueio")
	}
	// Outro assunto não é afetado.
	if ok, _, _ := l.Allow(ctx, rule, "bia@exemplo.com"); !ok {
		t.Fatal("outro assunto não pode ser bloqueado")
	}
	// Nova janela libera.
	c.Advance(15 * time.Minute)
	if ex, _, _ := l.Exceeded(ctx, rule, "ana@exemplo.com"); ex {
		t.Fatal("nova janela deveria liberar")
	}

	// O assunto nunca é gravado em claro.
	var buckets []string
	rows, err := pool.Query(ctx, `SELECT bucket FROM rate_limit`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b string
		_ = rows.Scan(&b)
		buckets = append(buckets, b)
	}
	rows.Close()
	if strings.Contains(strings.Join(buckets, " "), "exemplo") {
		t.Fatalf("assunto gravado em claro: %v", buckets)
	}
}
