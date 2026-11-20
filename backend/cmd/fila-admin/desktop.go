package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/google/uuid"

	"fila/internal/identidade"
	"fila/internal/platform/app"
	"fila/internal/platform/clock"
	"fila/internal/platform/keys"
)

// desktop administra os computadores autorizados do programa Fila Gestão (ADR-0027).
//
//	fila-admin desktop emitir-codigo -email dono@x -nome "PC do balcão"   código de uso único, 15 min
//	fila-admin desktop listar -email dono@x                                computadores da conta
//	fila-admin desktop revogar -instalacao <id>                            revoga um computador
func desktop(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	fs := flag.NewFlagSet("desktop "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "e-mail da conta de dono")
	nome := fs.String("nome", "", "nome do computador (aparece no aviso de sessão)")
	inst := fs.String("instalacao", "", "id da instalação")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	switch {
	case args[0] == "emitir-codigo" && *email != "" && *nome != "":
	case args[0] == "listar" && *email != "":
	case args[0] == "revogar" && *inst != "":
	default:
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	rt, code := app.Start("fila-admin", environ, stderr, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	if err := rt.CheckDatabase(ctx, rt.Config.Database.ConnectTimeout); err != nil {
		rt.Log.Error("banco indisponível ou sem migrações", "error", err.Error())
		return app.ExitFailure
	}
	ring, err := keys.NewRotating(rt.Config.Security.KeyMaterial())
	if err != nil {
		rt.Log.Error("chave do servidor inválida", "error", err.Error())
		return app.ExitConfigError
	}
	svc := identidade.New(identidade.Deps{Pool: rt.Pool, Clock: clock.System{}, Ring: ring,
		Hasher: identidade.NewHasher(identidade.DefaultPasswordParams, 1), Log: rt.Log,
		Config: identidade.Config{CookieSecure: rt.Config.Security.CookieSecure, PublicBaseURL: rt.Config.Security.PublicBaseURL}})
	switch args[0] {
	case "emitir-codigo":
		act, err := svc.IssueDesktopActivation(ctx, *email, *nome)
		if err != nil {
			fmt.Fprintln(stderr, "erro:", err.Error())
			return app.ExitConfigError
		}
		// O código aparece só aqui (o banco guarda o hash). Não o registre em log nem envie por chat público.
		fmt.Fprintf(stdout, "código de ativação: %s\nvale até %s (UTC), uma única vez\n", act.Code, act.ExpiresAt.Format("02/01/2006 15:04"))
	case "listar":
		list, err := svc.ListDesktopInstallations(ctx, *email)
		if err != nil {
			fmt.Fprintln(stderr, "erro:", err.Error())
			return app.ExitConfigError
		}
		if len(list) == 0 {
			fmt.Fprintln(stdout, "nenhum computador ativado para esta conta")
		}
		for _, d := range list {
			state := "ativo"
			if d.RevokedAt != nil {
				state = "revogado em " + d.RevokedAt.UTC().Format("02/01/2006 15:04")
			}
			seen := "nunca usado"
			if d.LastSeenAt != nil {
				seen = "último uso " + d.LastSeenAt.UTC().Format("02/01/2006 15:04")
			}
			fmt.Fprintf(stdout, "%s  %-30s  ativado %s  %s  (%s)\n", d.ID, d.Label, d.CreatedAt.UTC().Format("02/01/2006"), seen, state)
		}
	case "revogar":
		id, err := uuid.Parse(*inst)
		if err != nil {
			fmt.Fprintln(stderr, "erro: id de instalação inválido")
			return app.ExitConfigError
		}
		if err := svc.RevokeDesktopInstallation(ctx, id); err != nil {
			fmt.Fprintln(stderr, "erro:", err.Error())
			return app.ExitConfigError
		}
		fmt.Fprintln(stdout, "instalação revogada: as consultas seguintes desse computador são recusadas")
	}
	return app.ExitOK
}
