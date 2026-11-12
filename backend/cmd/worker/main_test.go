package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

func TestWorkerRefusesUnmigratedDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	var logs bytes.Buffer
	code := run(context.Background(), []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + testdb.New(t)}, &logs, &logs, nil)
	if code != 1 || !strings.Contains(logs.String(), "sem migrações") {
		t.Fatalf("esperava recusa com código 1, veio %d: %s", code, logs.String())
	}
}

func TestWorkerStartsAndStopsGracefully(t *testing.T) {
	t.Chdir(t.TempDir())
	dbURL := testdb.New(t)
	m, err := database.NewMigrator(testdb.Pool(t, dbURL))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	started := make(chan struct{})
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + dbURL}, &logs, &logs, started)
	}()
	select {
	case <-started:
	case code := <-done:
		t.Fatalf("worker saiu cedo (%d): %s", code, logs.String())
	case <-time.After(30 * time.Second):
		t.Fatal("worker não ficou pronto")
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("encerramento deveria sair com 0, saiu com %d", code)
	}
}
