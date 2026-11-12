package database_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

func newMigrator(t *testing.T) *database.Migrator {
	t.Helper()
	pool := testdb.Pool(t, testdb.New(t))
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestEmptyDatabaseMigratesAndSecondRunIsNoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	m := newMigrator(t)

	if err := m.CheckCurrent(ctx); !errors.Is(err, database.ErrSchemaBehind) {
		t.Fatalf("banco vazio deveria estar atrasado, veio %v", err)
	}
	applied, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("migrar banco vazio: %v", err)
	}
	if len(applied) == 0 || applied[len(applied)-1] != m.LatestVersion() {
		t.Fatalf("esperava aplicar até a versão %d, aplicou %v", m.LatestVersion(), applied)
	}
	if err := m.CheckCurrent(ctx); err != nil {
		t.Fatalf("depois de migrar deveria estar em dia: %v", err)
	}
	again, err := m.Up(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("segunda execução deveria não fazer nada: %v %v", again, err)
	}
	st, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if !s.Applied {
			t.Fatalf("migração %d (%s) não aplicada", s.Version, s.Name)
		}
	}
}

func TestConcurrentMigrationsAreSerialized(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dbURL := testdb.New(t)

	// Dois "processos" independentes (pools separados) migrando o mesmo banco ao mesmo tempo.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	applied := make([][]int64, 2)
	for i := range 2 {
		m, err := database.NewMigrator(testdb.Pool(t, dbURL))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close() })
		wg.Go(func() { applied[i], errs[i] = m.Up(ctx) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("instância %d falhou: %v", i, err)
		}
	}
	if total := len(applied[0]) + len(applied[1]); total != 1 {
		t.Fatalf("cada migração deve ser aplicada exatamente uma vez; aplicadas: %v", applied)
	}
}

func TestUnreachableDatabaseFailsClearly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Porta 1 em localhost: conexão recusada imediatamente.
	pool := testdb.Pool(t, "postgres://ninguem:senha-que-nao-pode-vazar@127.0.0.1:1/fila?sslmode=disable&connect_timeout=2")
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	err = m.CheckCurrent(ctx)
	if err == nil {
		t.Fatal("esperava erro com banco inacessível")
	}
	if strings.Contains(err.Error(), "senha-que-nao-pode-vazar") {
		t.Fatalf("erro expôs a senha: %v", err)
	}
}
