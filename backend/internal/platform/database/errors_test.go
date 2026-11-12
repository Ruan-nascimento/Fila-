package database_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"fila/internal/platform/config"
	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

func TestDescribeNeverEchoesDriverText(t *testing.T) {
	segredo := "usuario-que-na-verdade-era-a-senha"
	casos := []error{
		&pgconn.PgError{Code: "28P01", Message: `password authentication failed for user "` + segredo + `"`},
		&pgconn.PgError{Code: "3D000", Message: `database "` + segredo + `" does not exist`},
		&pgconn.PgError{Code: "XX000", Message: segredo},
		fmt.Errorf("failed to connect to `user=%s database=fila`: %w", segredo, context.DeadlineExceeded),
		fmt.Errorf("qualquer coisa com %s", segredo),
	}
	for _, err := range casos {
		got := database.Describe(err)
		if got == "" || strings.Contains(got, segredo) {
			t.Errorf("Describe(%T) = %q; não pode ser vazio nem conter o texto original", err, got)
		}
	}
	if database.Describe(nil) != "" {
		t.Error("Describe(nil) deve ser vazio")
	}
}

// Com banco real: senha errada gera mensagem sem usuário nem senha.
func TestPingWrongPasswordDoesNotLeakUser(t *testing.T) {
	admin := testdb.URL(t)
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	const user = "usuario-inexistente-xyz"
	const pass = "senha-errada-abc"
	u.User = url.UserPassword(user, pass)
	pool, err := database.Open(config.DatabaseConfig{URL: config.Secret(u.String()), MaxConns: 1, ConnectTimeout: 5 * time.Second}, "fila-test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = database.Ping(ctx, pool)
	if err == nil {
		t.Fatal("esperava falha de autenticação")
	}
	if strings.Contains(err.Error(), user) || strings.Contains(err.Error(), pass) {
		t.Fatalf("mensagem vazou credenciais: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "28P01") && !strings.Contains(err.Error(), "28000") {
		t.Fatalf("esperava indicar falha de autenticação, veio %q", err.Error())
	}
}
