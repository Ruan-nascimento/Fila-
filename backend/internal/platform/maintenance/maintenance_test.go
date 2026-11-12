package maintenance_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"fila/internal/platform/clock"
	"fila/internal/platform/maintenance"
	"fila/internal/platform/testdb"
)

func TestCleanerRemovesOnlyExpired(t *testing.T) {
	pool := testdb.Migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO rate_limit VALUES ('velho', $1, 1, $2), ('atual', $2, 1, $3)`, now.Add(-time.Hour), now, now.Add(time.Hour))
	exec(`INSERT INTO idempotency_record VALUES ('a','op','chave-0000000000001',$1,200,'',$2,$3),
	                                            ('a','op','chave-0000000000002',$1,200,'',$3,$4)`,
		make([]byte, 32), now.Add(-48*time.Hour), now.Add(-24*time.Hour), now.Add(24*time.Hour))
	exec(`INSERT INTO mail_outbox (kind, payload, created_at, next_attempt_at) VALUES ('pendente_velho', '\x01', $1, $1), ('pendente_novo', '\x01', $2, $2)`,
		now.Add(-49*time.Hour), now.Add(-time.Hour))
	exec(`INSERT INTO mail_outbox (kind, created_at, next_attempt_at, sent_at) VALUES ('enviado_velho', $1, $1, $1)`, now.Add(-8*24*time.Hour))

	c := &maintenance.Cleaner{Pool: pool, Clock: clock.NewFake(now), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := c.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	count := func(sql string) int {
		var n int
		if err := pool.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(`SELECT count(*) FROM rate_limit WHERE bucket = 'atual'`) != 1 || count(`SELECT count(*) FROM rate_limit`) != 1 {
		t.Fatal("rate_limit: só o vencido deveria sair")
	}
	if count(`SELECT count(*) FROM idempotency_record`) != 1 {
		t.Fatal("idempotency_record: só o vencido deveria sair")
	}
	if count(`SELECT count(*) FROM mail_outbox WHERE kind = 'enviado_velho'`) != 0 {
		t.Fatal("e-mail enviado há mais de 7 dias deveria sair")
	}
	if count(`SELECT count(*) FROM mail_outbox WHERE kind = 'pendente_velho' AND failed_at IS NOT NULL AND payload IS NULL`) != 1 {
		t.Fatal("e-mail pendente há mais de 48 h deveria ser abandonado sem conteúdo")
	}
	if count(`SELECT count(*) FROM mail_outbox WHERE kind = 'pendente_novo' AND failed_at IS NULL AND payload IS NOT NULL`) != 1 {
		t.Fatal("e-mail pendente recente deve continuar")
	}
}
