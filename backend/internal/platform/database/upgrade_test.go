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
