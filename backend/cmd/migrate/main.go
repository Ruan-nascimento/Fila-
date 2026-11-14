// Comando migrate: aplica ou lista as migrações versionadas do banco.
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/migrate up       aplica as migrações pendentes
//	go -C backend run ./cmd/migrate status   lista as migrações e se foram aplicadas
//	go -C backend run ./cmd/migrate permissoes -app fila_app   permissões mínimas do papel da aplicação
//	go -C backend run ./cmd/migrate permissoes -app fila_app -verificar   só confere (sai 1 se sobrar privilégio)
//
// Várias execuções simultâneas são seguras (trava no PostgreSQL). Não há "down" automático:
// correções entram como nova migração (ADR-0016).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fila/internal/platform/app"
	"fila/internal/platform/database"
)

const usage = `uso: migrate <comando>

comandos:
  up       aplica as migrações pendentes
  status   lista as migrações e se foram aplicadas
  permissoes -app <papel> [-verificar]
           dá ao papel da aplicação só o necessário (rodar depois de cada "up", como dono das tabelas);
           com -verificar, só confere e sai com 1 se o papel puder mais do que deveria`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	var appRole string
	var verifyOnly bool
	switch {
	case len(args) == 1 && (args[0] == "up" || args[0] == "status"):
	case len(args) >= 1 && args[0] == "permissoes":
		fs := flag.NewFlagSet("permissoes", flag.ContinueOnError)
		fs.SetOutput(stderr)
		fs.StringVar(&appRole, "app", "", "papel da aplicação")
		fs.BoolVar(&verifyOnly, "verificar", false, "só conferir")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || appRole == "" {
			fmt.Fprintln(stderr, usage)
			return app.ExitConfigError
		}
	default:
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
	if err := database.Ping(ctx, rt.Pool); err != nil {
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
	case "permissoes":
		if !verifyOnly {
			if err := database.ApplyGrants(ctx, rt.Pool, appRole); err != nil {
				rt.Log.Error("não foi possível aplicar as permissões", "error", err.Error())
				return app.ExitFailure
			}
			fmt.Fprintf(stdout, "permissões mínimas aplicadas ao papel %s\n", appRole)
		}
		extra, err := database.PrivilegeReport(ctx, rt.Pool, appRole)
		if err != nil {
			rt.Log.Error("não foi possível conferir as permissões", "error", err.Error())
			return app.ExitFailure
		}
		if len(extra) > 0 {
			fmt.Fprintf(stdout, "ATENÇÃO: o papel %s pode mais do que deveria:\n", appRole)
			for _, x := range extra {
				fmt.Fprintln(stdout, "  - "+x)
			}
			return app.ExitFailure
		}
		fmt.Fprintf(stdout, "conferido: o papel %s só lê e escreve dados (sem DDL, TRUNCATE nem reescrita de histórico)\n", appRole)
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
