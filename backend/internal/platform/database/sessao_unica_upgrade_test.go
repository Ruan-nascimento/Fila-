package database_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

// Migração 00017 (ADR-0027): sessões anteriores à vaga única. Para cada conta dona, só a sessão de
// equipe válida usada mais recentemente continua (e ocupa a vaga); as outras de equipe da conta
// são encerradas com owner_session_migration. Cliente e profissional sem vínculo de dono não mudam.
func TestUpgradeKeepsOneOwnerSessionPerAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := testdb.Pool(t, testdb.New(t))
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.UpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var est, dono, prof string
	if err := pool.QueryRow(ctx, `INSERT INTO establishment (name, slug, timezone, created_at, updated_at)
		VALUES ('Loja', 'loja-antiga', 'America/Sao_Paulo', $1, $1) RETURNING id`, now).Scan(&est); err != nil {
		t.Fatal(err)
	}
	account := func(email string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO account (name, email, email_normalized, password_hash, status, email_verified_at,
			password_changed_at, created_at, updated_at) VALUES ('X', $1, $1, '$argon2id$x', 'active', $2, $2, $2, $2) RETURNING id`,
			email, now).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	dono, prof = account("dono@x.com"), account("prof@x.com")
	exec(`INSERT INTO staff_membership (establishment_id, account_id, role, created_at, updated_at) VALUES ($1, $2, 'owner', $3, $3)`, est, dono, now)
	var profID string
	if err := pool.QueryRow(ctx, `INSERT INTO professional (establishment_id, display_name, queue_letter, created_at, updated_at)
		VALUES ($1, 'P', 'A', $2, $2) RETURNING id`, est, now).Scan(&profID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO staff_membership (establishment_id, account_id, role, professional_id, created_at, updated_at)
		VALUES ($1, $2, 'professional', $3, $4, $4)`, est, prof, profID, now)
	session := func(acc, kind string, lastUsed time.Time) string {
		h := make([]byte, 32)
		_, _ = rand.Read(h)
		var idle any
		if kind == "customer" {
			idle = 604800
		}
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO account_session (account_id, token_hash, kind, created_at, last_used_at,
			absolute_expires_at, idle_timeout_seconds) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			acc, h, kind, now.Add(-time.Hour), lastUsed, now.Add(10*time.Hour), idle).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	antiga := session(dono, "staff", now.Add(-30*time.Minute))
	recente := session(dono, "staff", now.Add(-time.Minute))
	cliente := session(dono, "customer", now)
	profA := session(prof, "staff", now.Add(-time.Minute))
	profB := session(prof, "staff", now.Add(-2*time.Minute))

	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("migrar: %v", err)
	}
	reason := func(id string) string {
		var r *string
		if err := pool.QueryRow(ctx, `SELECT revoke_reason FROM account_session WHERE id = $1`, id).Scan(&r); err != nil {
			t.Fatal(err)
		}
		if r == nil {
			return "ativa"
		}
		return *r
	}
	want := map[string]string{antiga: "owner_session_migration", recente: "ativa", cliente: "ativa", profA: "ativa", profB: "ativa"}
	for id, w := range want {
		if got := reason(id); got != w {
			t.Errorf("sessão %s: %s, esperado %s", id, got, w)
		}
	}
	var holder string
	if err := pool.QueryRow(ctx, `SELECT session_id FROM owner_session_slot WHERE account_id = $1`, dono).Scan(&holder); err != nil || holder != recente {
		t.Fatalf("vaga do dono: %s (%v)", holder, err)
	}
	var slots int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM owner_session_slot`).Scan(&slots)
	if slots != 1 {
		t.Fatalf("vagas criadas: %d (só contas donas têm vaga)", slots)
	}
}
