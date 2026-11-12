// Comando worker: processo de tarefas em segundo plano (mesmo código da API, executável separado).
//
// Nesta etapa ele só faz a partida e o encerramento corretos: valida configuração, exige banco
// acessível e migrado, e espera o sinal de desligamento. As tarefas reais entram nas etapas
// seguintes (publicação da outbox na 06, reconciliação de pagamentos na 09, expiração de reservas
// na 10, exclusões na 07). Não há tarefa simulada.
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
	"syscall"

	"fila/internal/platform/app"
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

	// Diferente da API, o worker não começa sem banco migrado: tarefas em esquema errado
	// poderiam gravar dados inconsistentes.
	if err := rt.CheckDatabase(ctx, rt.Config.Database.ConnectTimeout); err != nil {
		rt.Log.Error("worker não iniciado: banco indisponível ou sem migrações", "error", err.Error())
		return app.ExitFailure
	}
	rt.Log.Info("worker pronto; nenhuma tarefa registrada nesta etapa")
	if started != nil {
		close(started)
	}
	<-ctx.Done()
	rt.Log.Info("worker encerrado")
	return app.ExitOK
}
