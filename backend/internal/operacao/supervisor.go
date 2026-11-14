package operacao

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/clock"
	"fila/internal/platform/database"
)

// Supervisor liga e desliga as tarefas do worker conforme a trava (operation_state).
//
//   - Antes de iniciar: confere a trava (e detecta banco restaurado). Travado → espera.
//   - Rodando: confere a cada Every. Se alguém travou (manutenção, restauração), cancela as
//     tarefas, espera elas terminarem a rodada atual e volta a esperar.
//   - Banco fora do ar: não liga as tarefas (elas falhariam); tenta de novo.
//
// Cada tarefa é retomável e idempotente (etapas 06–10), então parar e voltar não perde nada.
type Supervisor struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	Every time.Duration
	// Start inicia as tarefas com ctx próprio; deve devolver só quando todas terminarem.
	Start func(ctx context.Context)
	// OnState (opcional) recebe cada leitura do estado (métricas).
	OnState func(State)
}

// Run supervisiona até ctx terminar.
func (s *Supervisor) Run(ctx context.Context) {
	every := s.Every
	if every <= 0 {
		every = 10 * time.Second
	}
	var (
		running bool
		cancel  context.CancelFunc
		wg      sync.WaitGroup
		lastLog time.Time
	)
	stop := func() {
		if running {
			cancel()
			wg.Wait()
			running = false
		}
	}
	defer stop()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		st, err := Gate(ctx, s.Pool, s.Clock.Now())
		switch {
		case err != nil && ctx.Err() == nil:
			s.Log.Error("trava do worker: não foi possível ler o estado; tarefas seguem como estavam", "error", database.Describe(err))
		case err == nil:
			if s.OnState != nil {
				s.OnState(st)
			}
			if st.Held() {
				if running {
					s.Log.Warn("worker travado: parando as tarefas", "motivo", reason(st))
					stop()
				}
				if time.Since(lastLog) >= time.Minute {
					lastLog = time.Now()
					s.Log.Error("worker TRAVADO: nenhuma tarefa roda até liberar (veja docs/operacao/backup-restauracao.md)",
						"motivo", reason(st), "restauracao", st.RestoreHold())
				}
			} else if !running {
				child, c := context.WithCancel(ctx)
				cancel, running = c, true
				wg.Go(func() { s.Start(child) })
				s.Log.Info("tarefas do worker ligadas")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func reason(s State) string {
	if s.HoldReason != nil {
		return *s.HoldReason
	}
	if s.BoundMismatch {
		return HoldRestoreDetected
	}
	return ""
}
