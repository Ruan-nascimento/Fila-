// Comando api: servidor HTTP do MVP (comandos HTTP e, a partir da etapa 06, eventos SSE).
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/api
package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fila/internal/platform/app"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Environ(), os.Stdout, os.Stderr, nil))
}

// run devolve o código de saída. listening (opcional, para testes) recebe o endereço em uso.
func run(ctx context.Context, environ []string, stdout, stderr io.Writer, listening chan<- string) int {
	rt, code := app.Start("api", environ, stdout, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	log, cfg := rt.Log, rt.Config

	// O banco fora do ar na partida não impede a API de subir: /healthz/live responde e
	// /healthz/ready fica 503 até o banco voltar. Assim o orquestrador não entra em ciclo de reinício.
	if err := rt.CheckDatabase(ctx, cfg.Database.ConnectTimeout); err != nil {
		log.Warn("dependência não pronta na partida; /healthz/ready responderá 503 até resolver", "error", err.Error())
	}

	health := httpserver.NewHealth(log, 2*time.Second,
		httpserver.Check{Name: "database", Fn: func(ctx context.Context) error { return database.Ping(ctx, rt.Pool) }},
		httpserver.Check{Name: "migrations", Fn: rt.Migrator.CheckCurrent},
	)
	mux := http.NewServeMux()
	health.Register(mux)
	mux.HandleFunc("/", httpserver.NotFound)
	srv := httpserver.New(cfg.HTTP, httpserver.Chain(mux, log), log)

	ln, err := net.Listen("tcp", cfg.HTTP.Addr)
	if err != nil {
		log.Error("não foi possível abrir o endereço HTTP (porta em uso ou sem permissão?)",
			"addr", cfg.HTTP.Addr, "error", err.Error())
		return app.ExitFailure
	}
	log.Info("API ouvindo", "addr", ln.Addr().String())
	if listening != nil {
		listening <- ln.Addr().String()
	}

	if err := httpserver.Run(ctx, srv, ln, cfg.HTTP.ShutdownTimeout, health.StartDraining); err != nil {
		log.Error("encerramento com erro", "error", err.Error())
		return app.ExitFailure
	}
	log.Info("API encerrada")
	return app.ExitOK
}
