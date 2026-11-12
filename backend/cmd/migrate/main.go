// Comando migrate: aplica ou lista as migrações versionadas do banco.
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/migrate up       aplica as migrações pendentes
//	go -C backend run ./cmd/migrate status   lista as migrações e se foram aplicadas
//
// Várias execuções simultâneas são seguras (trava no PostgreSQL). Não há "down" automático:
// correções entram como nova migração (ADR-0016).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fila/internal/platform/app"
)

const usage = `uso: migrate <comando>

comandos:
  up       aplica as migrações pendentes
  status   lista as migrações e se foram aplicadas`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "up" && args[0] != "status") {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	rt, code := app.Start("migrate", environ, stderr, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := rt.Pool.Ping(ctx); err != nil {
		rt.Log.Error("banco inacessível; confira se o PostgreSQL está rodando e a FILA_DATABASE_URL", "error", err.Error())
		return app.ExitFailure
	}

	switch args[0] {
	case "up":
		applied, err := rt.Migrator.Up(ctx)
		for _, v := range applied {
			fmt.Fprintf(stdout, "aplicada: %05d\n", v)
		}
		if err != nil {
			rt.Log.Error("migração falhou; nada além das versões listadas foi aplicado", "error", err.Error())
			return app.ExitFailure
		}
		if len(applied) == 0 {
			fmt.Fprintf(stdout, "nada a aplicar: banco já está na versão %05d\n", rt.Migrator.LatestVersion())
		}
	case "status":
		st, err := rt.Migrator.Status(ctx)
		if err != nil {
			rt.Log.Error("não foi possível consultar as migrações", "error", err.Error())
			return app.ExitFailure
		}
		for _, s := range st {
			state := "PENDENTE"
			if s.Applied {
				state = "aplicada"
			}
			fmt.Fprintf(stdout, "%05d  %-9s %s\n", s.Version, state, s.Name)
		}
	}
	return app.ExitOK
}
