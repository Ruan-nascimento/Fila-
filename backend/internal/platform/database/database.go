// Package database abre o pool de conexões com o PostgreSQL e aplica migrações.
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/config"
)

// Open cria o pool. Não conecta imediatamente: a primeira conexão acontece no primeiro uso
// (use Ping para verificar). Toda sessão usa fuso UTC e identifica a aplicação no servidor.
func Open(cfg config.DatabaseConfig, appName string) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.URL.Reveal())
	if err != nil {
		// Não propagamos o texto original: ele pode conter partes da URL.
		return nil, errors.New("FILA_DATABASE_URL não pôde ser interpretada pelo driver do PostgreSQL")
	}
	pc.MaxConns = cfg.MaxConns
	pc.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	pc.ConnConfig.RuntimeParams["application_name"] = appName
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		return nil, fmt.Errorf("criar pool do PostgreSQL: %w", err)
	}
	return pool, nil
}
