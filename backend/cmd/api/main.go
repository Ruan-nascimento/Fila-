// Comando api: servidor HTTP do MVP (comandos HTTP e, a partir da etapa 06, eventos SSE).
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/api
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fila/internal/api"
	"fila/internal/platform/app"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/metrics"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(os.Getenv("FILA_HTTP_ADDR"), os.Stderr))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Environ(), os.Stdout, os.Stderr, nil))
}

// healthcheck consulta /healthz/ready da própria API (HEALTHCHECK do contêiner: a imagem mínima
// não tem curl). Saída 0 = pronta, 1 = não pronta.
func healthcheck(addr string, stderr io.Writer) int {
	port := "8080"
	if _, p, err := net.SplitHostPort(addr); err == nil && p != "" && p != "0" {
		port = p
	}
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get("http://127.0.0.1:" + port + "/healthz/ready")
	if err != nil {
		fmt.Fprintln(stderr, "api: não respondeu:", err.Error())
		return 1
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "api: não pronta:", res.StatusCode)
		return 1
	}
	return 0
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
	a, err := api.New(api.Deps{Config: cfg, Pool: rt.Pool, Log: log, Clock: clock.System{}},
		func(mux *http.ServeMux) { health.Register(mux) })
	if err != nil {
		log.Error("não foi possível montar a API", "error", err.Error())
		return app.ExitConfigError
	}
	if !cfg.Security.CookieSecure {
		log.Warn("cookies sem Secure (somente desenvolvimento com http://); em produção FILA_COOKIE_SECURE é obrigatório")
	}
	// Métricas (FILA_METRICS_ADDR, endereço separado e interno) e limite geral por IP.
	reg := metrics.NewRegistry()
	database.RegisterRuntimeMetrics(reg, rt.Pool)
	hm := httpserver.NewHTTPMetrics(reg)
	reg.NewGaugeFunc("fila_sse_connections", "Conexões SSE abertas neste processo.", func() float64 { return float64(a.Stream.Active()) })
	if cfg.Ops.MetricsAddr != "" {
		go func() {
			if err := metrics.Serve(ctx, cfg.Ops.MetricsAddr, reg, log); err != nil {
				log.Error("servidor de métricas parou", "error", err.Error())
			}
		}()
	}
	handler := httpserver.ChainWith(a.Handler(), log, httpserver.Options{
		Limiter:           httpserver.NewIPLimiter(cfg.Ops.RequestsPerMinute),
		TrustForwardedFor: cfg.Security.TrustForwardedFor,
		Observe:           hm.Observe,
		Limited:           hm.Limited,
	})
	srv := httpserver.New(cfg.HTTP, handler, log)
	// Ao parar, encerra as conexões SSE (senão o desligamento esperaria por elas); os navegadores
	// reconectam com o cursor e não perdem eventos.
	srv.RegisterOnShutdown(a.Stream.Shutdown)
	hubCtx, stopHub := context.WithCancel(ctx)
	defer stopHub()
	go a.Run(hubCtx)

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
