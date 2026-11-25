// Comando worker: processo de tarefas em segundo plano (mesmo código da API, executável separado).
//
// Tarefas: entregar e-mails; limpeza técnica; publicar a outbox de eventos e aplicar a retenção
// do event_log; processar exclusões de conta, minimizar nicks e gravar o diário de exclusões;
// vencer reservas de prêmio; reconciliar Pix e processar estornos.
//
// Etapa 12: todas as tarefas rodam sob o supervisor de operação. Com a trava ativa (manutenção
// ou banco restaurado, detectado sozinho) nenhuma tarefa roda até o operador liberar
// (fila-admin manutencao / restauracao). Os indicadores de negócio são lidos a cada 15 s,
// mesmo travado, e servidos em FILA_METRICS_ADDR.
//
// Uso (a partir da raiz do repositório):
//
//	go -C backend run ./cmd/worker
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"fila/internal/agenda"
	"fila/internal/api"
	"fila/internal/exclusao"
	"fila/internal/fidelidade"
	"fila/internal/fila"
	"fila/internal/identidade"
	"fila/internal/midia"
	"fila/internal/operacao"
	"fila/internal/pagamento"
	"fila/internal/painel"
	"fila/internal/platform/app"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	"fila/internal/platform/database"
	"fila/internal/platform/keys"
	"fila/internal/platform/mail"
	"fila/internal/platform/maintenance"
	"fila/internal/platform/metrics"
	"fila/internal/platform/outbox"
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
	ring, err := keys.NewRotating(cfg.Security.KeyMaterial())
	if err != nil {
		log.Error("chave do servidor inválida", "error", err.Error())
		return app.ExitConfigError
	}
	clk := clock.System{}

	// Métricas (FILA_METRICS_ADDR, endereço interno) e indicadores de negócio a cada 15 s. Os
	// indicadores rodam SEMPRE, mesmo com o worker travado: é assim que o alerta da trava aparece.
	reg := metrics.NewRegistry()
	database.RegisterRuntimeMetrics(reg, rt.Pool)
	gauges := operacao.NewGauges(reg)
	if cfg.Ops.MetricsAddr != "" {
		go func() {
			if err := metrics.Serve(ctx, cfg.Ops.MetricsAddr, reg, log); err != nil {
				log.Error("servidor de métricas parou", "error", err.Error())
			}
		}()
	}
	var wg sync.WaitGroup
	hb := &operacao.Heartbeat{URL: cfg.Ops.HeartbeatURL.Reveal(), Every: time.Minute}
	if hb.URL != "" {
		log.Info("sinal de vida para monitor externo ligado")
	} else if cfg.Env.Hardened() {
		log.Warn("sem monitor externo do worker (FILA_HEARTBEAT_URL): uma parada do worker não avisará ninguém")
	}
	wg.Go(func() { collectIndicators(ctx, rt, gauges, hb, log) })

	switch cfg.Mail.Mode {
	case config.MailDisabled:
		log.Warn("envio de e-mail desativado: verificação, redefinição de senha e convites não chegarão às pessoas")
	case config.MailResend:
		log.Info("e-mails serão entregues pelo Resend")
	}
	if cfg.Ops.DeletionJournalDir == "" && cfg.Ops.DeletionJournalBucket == "" && cfg.Env.Hardened() {
		log.Warn("diário de exclusões desligado (FILA_DELETION_JOURNAL_DIR): exclusões pedidas depois do último backup não poderão ser reaplicadas após uma restauração")
	}
	prov := api.PixProvider(cfg.Payments, nil)
	if prov != nil {
		log.Info("reconciliação de Pix ligada", "provedor", cfg.Payments.Pix, "modo_teste", prov.TestMode())
	} else {
		log.Info("Pix desligado: só dinheiro e cartão presencial")
	}

	// Todas as tarefas rodam sob o supervisor: com a trava (manutenção ou banco restaurado) nada
	// roda; ao soltar, tudo volta. Cada tarefa é retomável e idempotente.
	sup := &operacao.Supervisor{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "supervisor"), Every: 10 * time.Second,
		Start: func(ctx context.Context) { runJobs(ctx, rt, ring, clk, prov) }}
	wg.Go(func() { sup.Run(ctx) })

	log.Info("worker pronto")
	if started != nil {
		close(started)
	}
	<-ctx.Done()
	wg.Wait()
	log.Info("worker encerrado")
	return app.ExitOK
}

// runJobs roda as tarefas até ctx terminar (o supervisor cancela ctx ao travar).
func runJobs(ctx context.Context, rt *app.Runtime, ring *keys.Ring, clk clock.Clock, prov pagamento.Provider) {
	log, cfg := rt.Log, rt.Config
	var wg sync.WaitGroup
	if sender := mailSender(cfg.Mail); sender != nil {
		d := &mail.Dispatcher{Pool: rt.Pool, Ring: ring, Sender: sender, Clock: clk, Log: log.With("tarefa", "email"),
			MaxAttempts: 8, BatchSize: 20, Lease: 2 * time.Minute, SendTimeout: cfg.Mail.Timeout, MaxAge: mail.DefaultMaxAge}
		wg.Go(func() { d.Run(ctx, 2*time.Second) })
		if cfg.Mail.Mode == config.MailFile {
			log.Info("e-mails serão gravados como arquivos (somente desenvolvimento)", "pasta", cfg.Mail.Dir)
		}
	}
	cleaner := &maintenance.Cleaner{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "limpeza")}
	wg.Go(func() { cleaner.Run(ctx, 10*time.Minute) })
	// Eventos: publica a outbox no event_log (pode rodar em mais de um worker) e aplica a
	// retenção de 24 h do event_log.
	publisher := &outbox.Publisher{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "eventos")}
	wg.Go(func() { publisher.Run(ctx, time.Second) })
	wg.Go(func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			if n, err := outbox.Prune(ctx, rt.Pool, clk.Now()); err != nil && ctx.Err() == nil {
				log.Warn("retenção de eventos falhou", "error", err.Error())
			} else if n > 0 {
				log.Info("eventos antigos apagados (retenção de 24 h)", "quantidade", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})

	// Exclusões de conta: reavalia bloqueios e anonimiza quando livres (retomável); minimiza nicks.
	ident := identidade.New(identidade.Deps{Pool: rt.Pool, Clock: clk, Ring: ring, Log: log.With("tarefa", "exclusao"),
		Hasher: identidade.NewHasher(identidade.DefaultPasswordParams, 1),
		Config: identidade.Config{CookieSecure: cfg.Security.CookieSecure, PublicBaseURL: cfg.Security.PublicBaseURL}})
	loyalty := fidelidade.New(fidelidade.Deps{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "fidelidade")})
	deletion := exclusao.New(exclusao.Deps{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "exclusao"), Ring: ring,
		Identity: ident, Queue: fila.New(fila.Deps{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "exclusao")}), Benefits: loyalty})
	wg.Go(func() { deletion.Run(ctx, time.Minute) })
	// Diário de exclusões fora do banco (reaplicado depois de restaurar um backup).
	if j := journal(cfg); j != nil {
		wg.Go(func() { deletion.RunJournal(ctx, j, 5*time.Second) })
	}

	// Agenda (ADR-0038): falta automática de horário chamado sem chegada e "atraso da loja". O
	// módulo fila recebe o guarda da agenda para que a reserva acompanhe a visita encerrada.
	agendaFila := fila.New(fila.Deps{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "agenda"), AgendaEnabled: cfg.Ops.AgendaEnabled})
	agendaSvc := agenda.New(agenda.Deps{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "agenda"), Visits: agendaFila,
		Enabled: cfg.Ops.AgendaEnabled, Mail: mail.NewOutbox(ring), NoticeEmail: ident.NoticeEmail, PublicBaseURL: cfg.Security.PublicBaseURL})
	agendaFila.Agenda = agendaSvc
	agendaFila.VisitClosed = ident.VisitClosed

	// Pagamentos: reconsulta cobranças Pix pendentes ou de resultado desconhecido (R-PAG-09/16).
	// O serviço existe mesmo sem Pix: o crédito do sinal na comanda (falta, fechamento pelo worker)
	// é dele. Reserva paga (ADR-0039): retenção vencida, Pix e devolução da reserva passam pela agenda.
	pay := pagamento.New(pagamento.Deps{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "pagamentos"), Provider: prov, Loyalty: loyalty,
		ExpiresIn: cfg.Payments.ExpiresIn, Timeout: cfg.Payments.Timeout, Pendencies: painel.Record, VisitClosed: ident.VisitClosed})
	pay.Bookings = agendaSvc
	agendaSvc.Payments, agendaSvc.Charge = pay, cfg.Ops.AgendaEnabled && cfg.Ops.AgendaCharge
	wg.Go(func() { agendaSvc.Run(ctx, 30*time.Second) })

	// Fidelidade: vence reservas de prêmio (24 h) e libera pontos e unidade uma vez (T-RW-03).
	wg.Go(func() { loyalty.Run(ctx, 30*time.Second) })

	// Fotos e logos (ADR-0028): processa os envios com concorrência limitada e limpa os arquivos
	// não associados ou removidos. Desligado sem FILA_MEDIA_STORAGE.
	if store := api.MediaStore(cfg.Media, nil); store != nil {
		media := midia.New(midia.Deps{Pool: rt.Pool, Clock: clk, Log: log.With("tarefa", "imagens"), Store: store})
		wg.Go(func() { media.Run(ctx, cfg.Media.Workers, 2*time.Second) })
		log.Info("processamento de imagens ligado", "simultaneas", cfg.Media.Workers)
	}
	// Transações vencidas do login com Google.
	wg.Go(func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			if _, err := identidade.PruneOIDC(ctx, rt.Pool, clk.Now()); err != nil && ctx.Err() == nil {
				log.Warn("limpeza das transações do Google falhou", "error", err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})

	if prov != nil {
		wg.Go(func() { pay.Run(ctx, 2*time.Second) })
	}
	wg.Wait()
}

// journal monta o diário de exclusões (pasta e/ou bucket); nil = desligado.
func journal(cfg *config.Config) *exclusao.Journal {
	if cfg.Ops.DeletionJournalDir == "" && cfg.Ops.DeletionJournalBucket == "" {
		return nil
	}
	j := &exclusao.Journal{Dir: cfg.Ops.DeletionJournalDir}
	if b := cfg.Ops.DeletionJournalBucket; b != "" {
		j.Remote, j.RemotePrefix = api.ObjectClient(cfg.Media.S3, b), cfg.Media.S3.Prefix
	}
	return j
}

// mailSender escolhe a entrega de e-mail pela configuração (nil = desligada).
func mailSender(c config.MailConfig) mail.Sender {
	switch c.Mode {
	case config.MailFile:
		return mail.FileSender{Dir: c.Dir}
	case config.MailResend:
		return mail.ResendSender{APIKey: c.ResendAPIKey.Reveal(), From: c.From, ReplyTo: c.ReplyTo,
			Client: &http.Client{Timeout: c.Timeout}}
	}
	return nil
}

// collectIndicators lê os indicadores a cada 15 s para as métricas e registra os alertas no log
// quando mudam (para quem não tem Prometheus, o log já mostra o problema).
func collectIndicators(ctx context.Context, rt *app.Runtime, g *operacao.Gauges, hb *operacao.Heartbeat, log *slog.Logger) {
	// Primeira leitura depois da primeira passagem do supervisor (que grava o sinal de vida):
	// sem isso, toda partida mostraria um falso "worker sem sinal".
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	last := ""
	for {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ind, err := operacao.Collect(cctx, rt.Pool, time.Now())
		cancel()
		var alerts []operacao.Alert
		if err != nil {
			if ctx.Err() == nil {
				g.Failed()
				log.Warn("indicadores: leitura falhou", "error", database.Describe(err))
			}
		} else {
			g.Update(ind)
			alerts = operacao.Evaluate(ind)
			var names []string
			for _, a := range alerts {
				names = append(names, a.Name)
			}
			if cur := strings.Join(names, ","); cur != last {
				last = cur
				if cur == "" {
					log.Info("alertas internos: nenhum ativo")
				} else {
					log.Warn("alertas internos ativos (detalhes: fila-admin verificar)", "alertas", cur)
				}
			}
		}
		if ctx.Err() == nil {
			if _, herr := hb.Report(ctx, alerts, err != nil, time.Now()); herr != nil {
				log.Warn("monitor externo: sinal de vida não enviado", "error", herr.Error())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
