package database_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"fila/internal/platform/config"
	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

// Menor privilégio: com as permissões aplicadas, o papel da aplicação lê e escreve dados, mas
// não cria nem altera tabelas, não esvazia tabela e não reescreve o histórico (auditoria, extrato).
func TestAppRoleHasLeastPrivilege(t *testing.T) {
	role, pw := os.Getenv(config.VarTestAppRole), os.Getenv(config.VarTestAppPassword)
	if role == "" {
		if os.Getenv(config.VarRequireDBTests) == "1" && os.Getenv("CI") != "" {
			t.Fatal("o CI deve definir FILA_TEST_APP_ROLE para provar o menor privilégio")
		}
		t.Skip("PULADO: defina FILA_TEST_APP_ROLE/FILA_TEST_APP_PASSWORD (papel de aplicação do PostgreSQL de teste)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ownerURL := testdb.New(t)
	owner := testdb.Pool(t, ownerURL)
	m, err := database.NewMigrator(owner)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyGrants(ctx, owner, "nome inválido;"); err == nil {
		t.Fatal("nome de papel inválido deveria ser recusado")
	}
	if err := database.ApplyGrants(ctx, owner, "papel_que_nao_existe"); err == nil {
		t.Fatal("papel inexistente deveria ser recusado")
	}
	if err := database.ApplyGrants(ctx, owner, role); err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyGrants(ctx, owner, role); err != nil { // repetível
		t.Fatal(err)
	}
	if extra, err := database.PrivilegeReport(ctx, owner, role); err != nil || len(extra) != 0 {
		t.Fatalf("papel da aplicação com privilégio sobrando: %v %v", extra, err)
	}
	u, _ := url.Parse(ownerURL)
	u.User = url.UserPassword(role, pw)
	app := testdb.Pool(t, u.String())

	allowed := []string{
		`INSERT INTO audit_event (occurred_at, actor_kind, action, target_type, target_id) VALUES (now(), 'system', 'T-OP-03', 'operation_state', gen_random_uuid())`,
		`SELECT count(*) FROM audit_event`,
		`UPDATE operation_state SET updated_at = now()`,
		`SELECT max(version_id) FROM goose_db_version`,
	}
	for _, sql := range allowed {
		if _, err := app.Exec(ctx, sql); err != nil {
			t.Errorf("deveria poder: %s: %v", sql, err)
		}
	}
	denied := []string{
		`CREATE TABLE invasora (id int)`,
		`ALTER TABLE account ADD COLUMN x int`,
		`DROP TABLE rate_limit`,
		`TRUNCATE rate_limit`,
		`UPDATE audit_event SET reason = 'apagando rastro'`,
		`DELETE FROM audit_event`,
		`DELETE FROM loyalty_ledger`,
		`UPDATE reward_stock_movement SET delta = 0`,
		`DELETE FROM goose_db_version`,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (999, true)`,
	}
	for _, sql := range denied {
		_, err := app.Exec(ctx, sql)
		var pgErr *pgconn.PgError
		if err == nil {
			t.Errorf("NÃO deveria poder: %s", sql)
		} else if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: esperava recusa por permissão (42501), veio %v", sql, err)
		}
	}
}
