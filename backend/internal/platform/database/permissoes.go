package database

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// Menor privilégio no banco (etapa 12, docs/operacao/implantacao.md):
//
//   - o papel MIGRADOR é dono do banco e das tabelas; só o `migrate` usa;
//   - o papel da APLICAÇÃO (api, worker, fila-admin) só lê e escreve dados: não cria, altera nem
//     apaga tabelas, não esvazia tabela (TRUNCATE) e não mexe nas versões das migrações;
//   - tabelas que são só de inserção (auditoria, extrato de pontos, movimentos de estoque,
//     conferências, acertos e transferências) ficam sem UPDATE e DELETE para a aplicação: mesmo
//     com um erro no código, o histórico não pode ser reescrito.
//
// ApplyGrants é repetível e roda depois de cada `migrate up` (tabela nova recebe as permissões).

// AppendOnlyTables nunca recebem UPDATE nem DELETE da aplicação.
var AppendOnlyTables = []string{"audit_event", "loyalty_ledger", "reward_stock_movement", "receipt_conferral",
	"order_adjustment", "participation_transfer", "change_request_addition"}

var roleName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ApplyGrants dá ao papel da aplicação as permissões mínimas no banco atual. Deve ser chamado
// pelo dono das tabelas (o papel migrador).
func ApplyGrants(ctx context.Context, q Querier, appRole string) error {
	if !roleName.MatchString(appRole) {
		return fmt.Errorf("nome de papel inválido: use letras minúsculas, números e _")
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, appRole).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("o papel %s não existe; crie-o antes (deploy/postgres/papeis.sql)", appRole)
	}
	var db string
	if err := q.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		return err
	}
	r := pgx.Identifier{appRole}.Sanitize()
	stmts := []string{
		// Ninguém além do dono cria objetos no esquema.
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		`GRANT CONNECT ON DATABASE ` + pgx.Identifier{db}.Sanitize() + ` TO ` + r,
		`GRANT USAGE ON SCHEMA public TO ` + r,
		`REVOKE ALL ON ALL TABLES IN SCHEMA public FROM ` + r,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ` + r,
		`REVOKE INSERT, UPDATE, DELETE ON goose_db_version FROM ` + r,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ` + r,
	}
	for _, t := range AppendOnlyTables {
		stmts = append(stmts, `REVOKE UPDATE, DELETE ON `+pgx.Identifier{t}.Sanitize()+` FROM `+r)
	}
	for _, s := range stmts {
		if _, err := q.Exec(ctx, s); err != nil {
			return fmt.Errorf("permissões (%s): %w", s, err)
		}
	}
	return nil
}

// PrivilegeReport descreve o que o papel da aplicação PODE fazer além do esperado (vazio = ok).
func PrivilegeReport(ctx context.Context, q Querier, appRole string) ([]string, error) {
	var out []string
	rows, err := q.Query(ctx, `
		SELECT c.relname, p.priv
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN unnest(ARRAY['TRUNCATE', 'REFERENCES', 'TRIGGER']) AS p(priv)
		WHERE n.nspname = 'public' AND c.relkind = 'r' AND has_table_privilege($1, c.oid, p.priv)
		UNION ALL
		SELECT c.relname, p.priv
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN unnest(ARRAY['UPDATE', 'DELETE']) AS p(priv)
		WHERE n.nspname = 'public' AND c.relname = ANY($2) AND has_table_privilege($1, c.oid, p.priv)
		ORDER BY 1, 2`, appRole, append(AppendOnlyTables, "goose_db_version"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, p string
		if err := rows.Scan(&t, &p); err != nil {
			return nil, err
		}
		out = append(out, t+": "+p)
	}
	var create, owner bool
	if err := q.QueryRow(ctx, `SELECT has_schema_privilege($1, 'public', 'CREATE'),
		EXISTS (SELECT 1 FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner JOIN pg_namespace n ON n.oid = c.relnamespace
		        WHERE n.nspname = 'public' AND r.rolname = $1)`, appRole).Scan(&create, &owner); err != nil {
		return nil, err
	}
	if create {
		out = append(out, "esquema public: CREATE")
	}
	if owner {
		out = append(out, "é dono de tabelas (deveria ser o papel migrador)")
	}
	var super bool
	if err := q.QueryRow(ctx, `SELECT rolsuper OR rolcreaterole OR rolcreatedb OR rolbypassrls FROM pg_roles WHERE rolname = $1`,
		appRole).Scan(&super); err != nil {
		return nil, err
	}
	if super {
		out = append(out, "papel com atributos administrativos (SUPERUSER/CREATEROLE/CREATEDB/BYPASSRLS)")
	}
	return out, rows.Err()
}
