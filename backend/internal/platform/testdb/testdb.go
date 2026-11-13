// Package testdb cria bancos PostgreSQL temporários e vazios para testes de integração.
//
// Configuração:
//   - FILA_TEST_DATABASE_URL: URL de um usuário com permissão CREATEDB (nunca o banco de produção).
//   - FILA_REQUIRE_DB_TESTS=1: torna obrigatório (o CI define). Sem ele, os testes são pulados
//     com aviso explícito — teste pulado nunca conta como aprovado.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/config"
	"fila/internal/platform/database"
)

// URL devolve a URL administrativa de teste, pulando ou falhando conforme a configuração.
func URL(t testing.TB) string {
	t.Helper()
	u := os.Getenv(config.VarTestDatabaseURL)
	if u == "" {
		if os.Getenv(config.VarRequireDBTests) == "1" {
			t.Fatalf("%s não definida, mas %s=1 exige os testes com banco", config.VarTestDatabaseURL, config.VarRequireDBTests)
		}
		t.Skipf("PULADO: defina %s para rodar testes com PostgreSQL real", config.VarTestDatabaseURL)
	}
	return u
}

// New cria um banco vazio exclusivo do teste e devolve a URL dele. O banco é apagado no fim.
func New(t testing.TB) string {
	t.Helper()
	admin := URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("conectar ao PostgreSQL de teste: %v", err)
	}
	defer conn.Close(ctx)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "fila_t_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" ENCODING 'UTF8' TEMPLATE template0"); err != nil {
		t.Fatalf("criar banco temporário: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Errorf("limpeza: conectar: %v", err)
			return
		}
		defer c.Close(ctx)
		// Um processo do próprio PostgreSQL (ex.: autovacuum, que roda como superusuário) pode
		// estar no banco temporário; um usuário de teste sem superusuário não consegue encerrá-lo
		// (SQLSTATE 42501). Esses processos terminam sozinhos em instantes: tentamos de novo.
		var err2 error
		for attempt := range 10 {
			_, err2 = c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			var pgErr *pgconn.PgError
			if err2 == nil || !errors.As(err2, &pgErr) || pgErr.Code != "42501" {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
		}
		if err2 != nil {
			t.Errorf("limpeza: apagar banco %s: %v", name, err2)
		}
	})

	parsed, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("%s inválida", config.VarTestDatabaseURL)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

// Migrated cria um banco temporário, aplica todas as migrações e devolve o pool.
func Migrated(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := Pool(t, New(t))
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("migrar banco de teste: %v", err)
	}
	return pool
}

// SecretKeyVar devolve "FILA_SECRET_KEY=<chave aleatória>" para testes. A chave é gerada a cada
// chamada: nenhuma chave fica escrita no código.
func SecretKeyVar() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return config.VarSecretKey + "=" + base64.RawURLEncoding.EncodeToString(b)
}

// Pool abre um pool para a URL informada e o fecha no fim do teste.
func Pool(t testing.TB, dbURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := database.Open(config.DatabaseConfig{
		URL: config.Secret(dbURL), MaxConns: 5, ConnectTimeout: 5 * time.Second,
	}, "fila-test")
	if err != nil {
		t.Fatalf("abrir pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
