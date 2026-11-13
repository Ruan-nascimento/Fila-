package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/pressly/goose/v3/lock"

	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

// Enquanto outro processo segura a trava de migração (ex.: API e worker subindo juntos, ou uma
// migração demorada), a verificação de versão (readiness e partida do worker) não pode esperar.
func TestCheckCurrentDoesNotWaitForMigrationLock(t *testing.T) {
	pool := testdb.Pool(t, testdb.New(t))
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Banco vazio: atrasado, sem erro de "tabela não existe".
	if err := m.CheckCurrent(ctx); err == nil {
		t.Fatal("banco vazio deveria estar atrasado")
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lock.DefaultLockID) //nolint:errcheck

	quick, cancelQuick := context.WithTimeout(ctx, 2*time.Second)
	defer cancelQuick()
	start := time.Now()
	if err := m.CheckCurrent(quick); err != nil {
		t.Fatalf("verificação com a trava ocupada: %v (após %s)", err, time.Since(start))
	}
}
