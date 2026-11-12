// Package httpserver contém a infraestrutura HTTP compartilhada: servidor com timeouts,
// encerramento gracioso, middlewares, saúde e envelope de erro.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"fila/internal/platform/config"
)

// New cria o http.Server com os limites de tempo da configuração.
//
// WriteTimeout vale para rotas comuns. O fluxo SSE (etapa 06) ajustará o prazo por mensagem
// com http.ResponseController, sem aumentar o limite global.
func New(cfg config.HTTPConfig, handler http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Run atende em ln até ctx ser cancelado. Então:
//  1. chama beforeShutdown (ex.: readiness passa a falhar);
//  2. para de aceitar conexões e espera as requisições em andamento por até shutdownTimeout;
//  3. se o prazo estourar, fecha as conexões restantes e devolve erro.
func Run(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, beforeShutdown func()) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("servidor HTTP parou: %w", err)
	case <-ctx.Done():
	}

	if beforeShutdown != nil {
		beforeShutdown()
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("encerramento excedeu %s: %w", shutdownTimeout, err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
