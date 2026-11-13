package database_test

import (
	"context"
	"testing"
	"time"

	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

// Um banco da etapa 02 com loja e profissionais (tabela mínima, sem letra nem fila) é
// atualizado para a etapa 03 sem perder dados: cada profissional ganha letra e fila fechada.
func TestUpgradeFromStep02KeepsProfessionals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := testdb.Pool(t, testdb.New(t))
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var est string
	if err := pool.QueryRow(ctx, `INSERT INTO establishment (name, slug, timezone, created_at, updated_at)
		VALUES ('Loja', 'loja-antiga', 'America/Sao_Paulo', $1, $1) RETURNING id`, now).Scan(&est); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Beto", "Caio"} {
		if _, err := pool.Exec(ctx, `INSERT INTO professional (establishment_id, display_name, created_at, updated_at)
			VALUES ($1, $2, $3, $3)`, est, name, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("atualizar para a versão atual: %v", err)
	}
	rows, err := pool.Query(ctx, `
		SELECT p.display_name, p.queue_letter, q.status FROM professional p JOIN queue q ON q.professional_id = p.id
		ORDER BY p.queue_letter`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, letter, status string
		_ = rows.Scan(&name, &letter, &status)
		got = append(got, name+":"+letter+":"+status)
	}
	if len(got) != 2 || got[0] != "Beto:A:closed" || got[1] != "Caio:B:closed" {
		t.Fatalf("profissionais após a atualização: %v", got)
	}
}

// Um banco da etapa 04 com comandas (uma aberta, uma encerrada sem cobrança) é atualizado para
// a etapa 05 sem perder dados: a comanda ganha o profissional do atendimento e a encerrada passa
// a ter valor fechado em zero. Reproduz o erro "pending trigger events" da FK adiada.
func TestUpgradeFromStep04KeepsOrders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := testdb.Pool(t, testdb.New(t))
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var est, prof, queue, svc, acct string
	_ = pool.QueryRow(ctx, `INSERT INTO establishment (name, slug, timezone, created_at, updated_at)
		VALUES ('Loja', 'loja-04', 'America/Sao_Paulo', $1, $1) RETURNING id`, now).Scan(&est)
	_ = pool.QueryRow(ctx, `INSERT INTO professional (establishment_id, display_name, queue_letter, created_at, updated_at)
		VALUES ($1, 'Beto', 'A', $2, $2) RETURNING id`, est, now).Scan(&prof)
	_ = pool.QueryRow(ctx, `INSERT INTO queue (establishment_id, professional_id, status, created_at, updated_at)
		VALUES ($1, $2, 'open', $3, $3) RETURNING id`, est, prof, now).Scan(&queue)
	_ = pool.QueryRow(ctx, `INSERT INTO service (establishment_id, name, price_cents, duration_minutes, points, created_at, updated_at)
		VALUES ($1, 'Corte', 4500, 30, 0, $2, $2) RETURNING id`, est, now).Scan(&svc)
	_ = pool.QueryRow(ctx, `INSERT INTO account (name, email, email_normalized, password_hash, status, email_verified_at, password_changed_at, created_at, updated_at)
		VALUES ('Maria', 'maria@exemplo.com', 'maria@exemplo.com', '$argon2id$teste', 'active', $1, $1, $1, $1) RETURNING id`, now).Scan(&acct)
	orders := map[string]string{}
	for i, status := range []string{"open", "closed_no_charge"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }() // nunca deixa a conexão presa se o teste falhar
		var order, part string
		_ = tx.QueryRow(ctx, `SELECT uuidv7(), uuidv7()`).Scan(&order, &part)
		pstatus, closure, closed := "waiting", any(nil), any(nil)
		if status == "closed_no_charge" {
			pstatus, closure, closed = "cancelled", "cancelled_by_customer", now
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO service_order (id, establishment_id, participation_id, account_id, status, closure_reason, total_cents, created_at, updated_at, closed_at)
				VALUES ($1, $2, $3, $4, $5, $6, 4500, $7, $7, $8::timestamptz)`, []any{order, est, part, acct, status, closure, now, closed}},
			{`INSERT INTO order_item (establishment_id, order_id, service_id, name, unit_price_cents, duration_minutes, points, added_at)
				VALUES ($1, $2, $3, 'Corte', 4500, 30, 0, $4)`, []any{est, order, svc, now}},
			{`INSERT INTO participation (id, establishment_id, queue_id, order_id, account_id, nick, loyalty_eligible, status, position,
				ticket_day, ticket_number, ticket_code, finished_at, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, 'Maria', false, $6, $7::bigint, current_date, $7::int, $8, $9::timestamptz, $10, $10)`,
				[]any{part, est, queue, order, acct, pstatus, i + 1, "A00" + string(rune('1'+i)), closed, now}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				t.Fatalf("dados da etapa 04: %v", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		orders[status] = order
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("atualizar para a versão atual: %v", err)
	}
	var gotProf string
	var total int64
	var frozen *time.Time
	_ = pool.QueryRow(ctx, `SELECT professional_id::text FROM service_order WHERE id = $1`, orders["open"]).Scan(&gotProf)
	_ = pool.QueryRow(ctx, `SELECT total_cents, total_frozen_at FROM service_order WHERE id = $1`, orders["closed_no_charge"]).Scan(&total, &frozen)
	if gotProf != prof || total != 0 || frozen == nil {
		t.Fatalf("após a atualização: profissional=%s total encerrada=%d fechado=%v", gotProf, total, frozen)
	}
	// O gatilho novo aceita mudanças coerentes nas comandas antigas.
	exec(`UPDATE service_order SET version = version + 1 WHERE id = $1`, orders["open"])
	_ = acct
}
