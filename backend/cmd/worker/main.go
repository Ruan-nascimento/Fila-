// Comando worker: processo de tarefas em segundo plano (mesmo código da API, executável separado).
//
// Tarefas desta etapa:
//   - entregar os e-mails da caixa de saída (verificação, redefinição de senha, convites);
//   - apagar registros vencidos (contadores de tentativas, respostas de idempotência,
//     e-mails já entregues).
//
// As demais tarefas entram nas etapas seguintes (publicação da outbox de eventos na 06,
// reconciliação de pagamentos na 09, expiração de reservas na 10, exclusões na 07).
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/worker
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"fila/internal/platform/app"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	"fila/internal/platform/keys"
	"fila/internal/platform/mail"
	"fila/internal/platform/maintenance"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Environ(), os.Stdout, os.Stderr, nil))
}

// run devolve o código de saída. started (opcional, para testes) é fechado quando o worker fica pronto.
func run(ctx context.Context, environ []string, stdout, stderr io.Writer, started chan<- struct{}) int {
	rt, code := app.Start("worker", environ, stdout, stderr)
	if rt == nil {
		return code
	}
	defer rt.Close()
	log, cfg := rt.Log, rt.Config

	// Diferente da API, o worker não começa sem banco migrado: tarefas em esquema errado
	// poderiam gravar dados inconsistentes.
	if err := rt.CheckDatabase(ctx, cfg.Database.ConnectTimeout); err != nil {
		log.Error("worker não iniciado: banco indisponível ou sem migrações", "error", err.Error())
		return app.ExitFailure
	}
	ring, err := keys.New([]byte(cfg.Security.SecretKey.Reveal()))
	if err != nil {
		log.Error("chave do servidor inválida", "error", err.Error())
		return app.ExitConfigError
	}
	clk := clock.System{}

	var wg sync.WaitGroup
	switch cfg.Mail.Mode {
	case config.MailFile:
		d := &mail.Dispatcher{Pool: rt.Pool, Ring: ring, Sender: mail.FileSender{Dir: cfg.Mail.Dir},
			Clock: clk, Log: log.With("tarefa", "email"), MaxAttempts: 8, BatchSize: 20}
		wg.Go(func() { d.Run(ctx, 2*time.Second) })
		log.Info("e-mails serão gravados como arquivos (somente desenvolvimento)", "pasta", cfg.Mail.Dir)
	default:
		log.Warn("envio de e-mail desativado: verificação, redefinição de senha e convites não chegarão às pessoas")
	}
	cleaner := &maintenance.Cleaner{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "limpeza")}
	wg.Go(func() { cleaner.Run(ctx, 10*time.Minute) })

	log.Info("worker pronto")
	if started != nil {
		close(started)
	}
	<-ctx.Done()
	wg.Wait()
	log.Info("worker encerrado")
	return app.ExitOK
}
