// Package app concentra a partida comum dos executáveis (api, worker, migrate):
// configuração, logger, banco e códigos de saída.
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/config"
	"fila/internal/platform/database"
	"fila/internal/platform/logging"
)

// Códigos de saída dos executáveis.
const (
	ExitOK          = 0
	ExitFailure     = 1 // falha em tempo de execução (banco fora, porta ocupada…)
	ExitConfigError = 2 // configuração ou uso inválido: corrigir antes de tentar de novo
)

// Runtime reúne o que todo executável precisa depois da partida.
type Runtime struct {
	Config   *config.Config
	Log      *slog.Logger
	Pool     *pgxpool.Pool
	Migrator *database.Migrator
}

// Close libera os recursos na ordem inversa.
func (r *Runtime) Close() {
	if r.Migrator != nil {
		_ = r.Migrator.Close()
	}
	if r.Pool != nil {
		r.Pool.Close()
	}
}

// Start valida a configuração, cria o logger e abre o pool (sem conectar ainda).
// Em erro, escreve a explicação em stderr e devolve o código de saída adequado.
func Start(service string, environ []string, stdout, stderr io.Writer) (*Runtime, int) {
	cfg, envFile, err := config.Resolve(environ)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", service, err)
		return nil, ExitConfigError
	}
	log := logging.New(stdout, cfg.LogLevel, cfg.LogFormat).With("service", service, "env", string(cfg.Env))
	if envFile != "" {
		log.Info("variáveis lidas de arquivo .env (apenas fora de produção)", "arquivo", envFile)
	}
	pool, err := database.Open(cfg.Database, "fila-"+service)
	if err != nil {
		log.Error("não foi possível preparar a conexão com o banco", "error", err.Error())
		return nil, ExitConfigError
	}
	mig, err := database.NewMigrator(pool)
	if err != nil {
		pool.Close()
		log.Error("não foi possível carregar as migrações embutidas", "error", err.Error())
		return nil, ExitFailure
	}
	return &Runtime{Config: cfg, Log: log, Pool: pool, Migrator: mig}, ExitOK
}

// CheckDatabase confere conexão e versão do esquema dentro de timeout.
func (r *Runtime) CheckDatabase(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := database.Ping(ctx, r.Pool); err != nil {
		return fmt.Errorf("banco inacessível: %w", err)
	}
	return r.Migrator.CheckCurrent(ctx)
}
