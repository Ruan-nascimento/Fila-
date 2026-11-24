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

// Um banco já em uso (versão 00023, publicada) é atualizado para as melhorias 1.0.0 sem perder
// nada (ADR-0033 a ADR-0035): itens antigos viram quantidade 1 com o realizado e o cobrado
// preservados; o total continua explicável; chamadas existentes mantêm os 10 minutos; cartão
// antigo fica "não especificado" e sem custo fabricado; filas ficam com a entrada aberta e sem
// limite, como antes.
func TestUpgradeTo100KeepsOrdersQuantitiesAndReceipts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testdb.Pool(t, testdb.New(t))
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.UpTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var est, prof, queue, corte, barba, acct string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO establishment (name, slug, timezone, created_at, updated_at)
		VALUES ('Loja', 'loja-23', 'America/Sao_Paulo', $1, $1) RETURNING id`, now).Scan(&est))
	must(pool.QueryRow(ctx, `INSERT INTO professional (establishment_id, display_name, queue_letter, created_at, updated_at)
		VALUES ($1, 'Beto', 'A', $2, $2) RETURNING id`, est, now).Scan(&prof))
	must(pool.QueryRow(ctx, `INSERT INTO queue (establishment_id, professional_id, status, created_at, updated_at)
		VALUES ($1, $2, 'open', $3, $3) RETURNING id`, est, prof, now).Scan(&queue))
	must(pool.QueryRow(ctx, `INSERT INTO service (establishment_id, name, price_cents, duration_minutes, points, created_at, updated_at)
		VALUES ($1, 'Corte', 4500, 30, 10, $2, $2) RETURNING id`, est, now).Scan(&corte))
	must(pool.QueryRow(ctx, `INSERT INTO service (establishment_id, name, price_cents, duration_minutes, points, created_at, updated_at)
		VALUES ($1, 'Barba', 3000, 20, 5, $2, $2) RETURNING id`, est, now).Scan(&barba))
	must(pool.QueryRow(ctx, `INSERT INTO account (name, email, email_normalized, password_hash, status, email_verified_at, password_changed_at, created_at, updated_at)
		VALUES ('Maria', 'maria@exemplo.com', 'maria@exemplo.com', '$argon2id$teste', 'active', $1, $1, $1, $1) RETURNING id`, now).Scan(&acct))

	// Comanda paga no cartão (antes da modalidade): Corte e Barba realizados e cobrados.
	var paid, paidPart, called, calledPart string
	_ = pool.QueryRow(ctx, `SELECT uuidv7(), uuidv7(), uuidv7(), uuidv7()`).Scan(&paid, &paidPart, &called, &calledPart)
	tx, err := pool.Begin(ctx)
	must(err)
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO service_order (id, establishment_id, participation_id, professional_id, account_id, status, total_cents,
			total_frozen_at, payment_method, created_at, updated_at, closed_at)
			VALUES ($1, $2, $3, $4, $5, 'settled', 7500, $6, 'card', $6, $6, $6)`, []any{paid, est, paidPart, prof, acct, now}},
		{`INSERT INTO order_item (establishment_id, order_id, service_id, name, unit_price_cents, duration_minutes, points, status, charged, added_at, performed_at)
			VALUES ($1, $2, $3, 'Corte', 4500, 30, 10, 'performed', true, $4, $4), ($1, $2, $5, 'Barba', 3000, 20, 5, 'performed', true, $4, $4)`,
			[]any{est, paid, corte, now, barba}},
		{`INSERT INTO participation (id, establishment_id, queue_id, order_id, account_id, nick, loyalty_eligible, status, position,
			ticket_day, ticket_number, ticket_code, called_at, tolerance_ends_at, started_at, finished_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 'Maria', false, 'completed', 1, current_date, 1, 'A001', $6, $6::timestamptz + interval '10 minutes', $6, $6, $6, $6)`,
			[]any{paidPart, est, queue, paid, acct, now}},
		{`INSERT INTO receipt (establishment_id, order_id, method, amount_cents, classification, evidence, registered_by, registered_by_account_id, registered_at)
			VALUES ($1, $2, 'card', 7500, 'principal', 'in_person', 'owner', $3, $4)`, []any{est, paid, acct, now}},
		{`INSERT INTO receipt_conferral (receipt_id, establishment_id, conferred_at, conferred_by_role, conferred_by_account_id)
			SELECT id, $1, $2, 'owner', $3 FROM receipt WHERE order_id = $4`, []any{est, now, acct, paid}},
		// Visitante (aqui, conta) chamado há pouco com a tolerância fixa de 10 minutos.
		{`INSERT INTO service_order (id, establishment_id, participation_id, professional_id, account_id, status, total_cents, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 'open', 4500, $6, $6)`, []any{called, est, calledPart, prof, acct, now}},
		{`INSERT INTO order_item (establishment_id, order_id, service_id, name, unit_price_cents, duration_minutes, points, added_at)
			VALUES ($1, $2, $3, 'Corte', 4500, 30, 10, $4)`, []any{est, called, corte, now}},
		{`INSERT INTO participation (id, establishment_id, queue_id, order_id, account_id, nick, loyalty_eligible, status, position,
			ticket_day, ticket_number, ticket_code, called_at, tolerance_ends_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 'Maria', false, 'called', 2, current_date, 2, 'A002', $6, $6::timestamptz + interval '10 minutes', $6, $6)`,
			[]any{calledPart, est, queue, called, acct, now}},
	} {
		if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("dados da versão publicada: %v", err)
		}
	}
	must(tx.Commit(ctx))

	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("atualizar para 1.0.0: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT o.id::text, i.name, i.kind, i.quantity, i.performed_quantity, i.charged_quantity, o.total_cents
		FROM order_item i JOIN service_order o ON o.id = i.order_id ORDER BY o.id, i.name`)
	must(err)
	got := map[string][]int{}
	for rows.Next() {
		var oid, name, kind string
		var q, p, c int
		var total int64
		must(rows.Scan(&oid, &name, &kind, &q, &p, &c, &total))
		if kind != "service" {
			t.Fatalf("item antigo virou %s", kind)
		}
		got[oid+"/"+name] = []int{q, p, c, int(total)}
	}
	rows.Close()
	for key, want := range map[string][]int{paid + "/Corte": {1, 1, 1, 7500}, paid + "/Barba": {1, 1, 1, 7500}, called + "/Corte": {1, 0, 0, 4500}} {
		if g := got[key]; len(g) != 4 || g[0] != want[0] || g[1] != want[1] || g[2] != want[2] || g[3] != want[3] {
			t.Fatalf("%s depois da atualização: %v, esperado %v", key, g, want)
		}
	}
	// O gatilho novo (preço × quantidade) aceita as comandas antigas como estão.
	if _, err := pool.Exec(ctx, `UPDATE service_order SET version = version + 1 WHERE id = ANY($1::uuid[])`, []string{paid, called}); err != nil {
		t.Fatalf("total das comandas antigas pela regra nova: %v", err)
	}
	var tol *int
	var ends, calledAt time.Time
	must(pool.QueryRow(ctx, `SELECT tolerance_minutes, tolerance_ends_at, called_at FROM participation WHERE id = $1`, calledPart).Scan(&tol, &ends, &calledAt))
	if tol == nil || *tol != 10 || ends.Sub(calledAt) != 10*time.Minute {
		t.Fatalf("chamada existente mudou de prazo: %v %v", tol, ends.Sub(calledAt))
	}
	var cardType *string
	var fees int
	must(pool.QueryRow(ctx, `SELECT card_type, (SELECT count(*) FROM receipt_fee) FROM receipt WHERE order_id = $1`, paid).Scan(&cardType, &fees))
	if cardType != nil || fees != 0 {
		t.Fatalf("cartão antigo: modalidade %v, custos fabricados %d", cardType, fees)
	}
	var admission string
	var capacity *int
	must(pool.QueryRow(ctx, `SELECT admission, capacity FROM queue WHERE id = $1`, queue).Scan(&admission, &capacity))
	if admission != "open" || capacity != nil {
		t.Fatalf("fila depois da atualização: %s %v", admission, capacity)
	}
	var settings int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM establishment_settings`).Scan(&settings))
	if settings != 0 {
		t.Fatalf("configurações gravadas sem ninguém salvar: %d", settings)
	}
}
