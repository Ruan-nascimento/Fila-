// Comando fila-admin: processos controlados do operador (ADR-0013). Não há rota HTTP para eles.
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/fila-admin gerar-chave
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"fila/internal/platform/app"
)

const usage = `uso: fila-admin <comando> [opções]

comandos:
  gerar-chave   imprime uma chave secreta nova para FILA_SECRET_KEY`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func run(_ context.Context, args, _ []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
	switch args[0] {
	case "gerar-chave":
		if len(args) != 1 {
			fmt.Fprintln(stderr, usage)
			return app.ExitConfigError
		}
		return generateKey(stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return app.ExitConfigError
	}
}

// generateKey imprime 32 bytes aleatórios em base64url. A chave aparece só na saída do
// terminal de quem rodou o comando; nada é gravado.
func generateKey(stdout, stderr io.Writer) int {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintln(stderr, "não foi possível gerar números aleatórios:", err)
		return app.ExitFailure
	}
	fmt.Fprintln(stdout, base64.RawURLEncoding.EncodeToString(b))
	return app.ExitOK
}
