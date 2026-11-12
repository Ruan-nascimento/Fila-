package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"fila/migrations"
)

// Migrator aplica e consulta migrações versionadas (goose), embutidas no executável.
// Várias instâncias rodando "up" ao mesmo tempo são serializadas por advisory lock do PostgreSQL.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
	latest   int64
}

// MigrationStatus é a situação de uma migração.
type MigrationStatus struct {
	Version int64
	Name    string
	Applied bool
}

// NewMigrator usa as migrações embutidas no executável.
func NewMigrator(pool *pgxpool.Pool) (*Migrator, error) {
	return newMigrator(pool, migrations.FS)
}

func newMigrator(pool *pgxpool.Pool, fsys fs.FS) (*Migrator, error) {
	db := stdlib.OpenDBFromPool(pool)
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("criar trava de migração: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("carregar migrações: %w", err)
	}
	m := &Migrator{db: db, provider: p}
	for _, s := range p.ListSources() {
		m.latest = max(m.latest, s.Version)
	}
	return m, nil
}

// Close libera a conexão database/sql usada pelo goose (o pool continua aberto).
func (m *Migrator) Close() error { return m.db.Close() }

// LatestVersion é a maior versão embutida no executável.
func (m *Migrator) LatestVersion() int64 { return m.latest }

// Up aplica todas as migrações pendentes e devolve as versões aplicadas agora.
func (m *Migrator) Up(ctx context.Context) ([]int64, error) {
	res, err := m.provider.Up(ctx)
	applied := make([]int64, 0, len(res))
	for _, r := range res {
		if r.Error == nil && !r.Empty {
			applied = append(applied, r.Source.Version)
		}
	}
	if err != nil {
		return applied, fmt.Errorf("aplicar migrações: %w", err)
	}
	return applied, nil
}

// Status lista todas as migrações e se foram aplicadas.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	st, err := m.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("consultar migrações: %w", err)
	}
	out := make([]MigrationStatus, 0, len(st))
	for _, s := range st {
		out = append(out, MigrationStatus{
			Version: s.Source.Version,
			Name:    s.Source.Path,
			Applied: s.State == goose.StateApplied,
		})
	}
	return out, nil
}

// ErrSchemaBehind indica que o banco não tem todas as migrações deste executável.
var ErrSchemaBehind = errors.New("banco sem todas as migrações; rode: go -C backend run ./cmd/migrate up")

// CheckCurrent confere se o banco está na versão esperada. Não usa a trava de migração,
// então pode ser chamado com frequência (readiness).
func (m *Migrator) CheckCurrent(ctx context.Context) error {
	v, err := m.provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("ler versão do banco: %w", err)
	}
	if v < m.latest {
		return fmt.Errorf("%w (banco na versão %d, esperado %d)", ErrSchemaBehind, v, m.latest)
	}
	return nil
}
