package agenda

import (
	"context"
	"time"

	"fila/internal/platform/database"
)

// Health são os indicadores de operação da agenda (sem dado pessoal; contagens e idades), lidos
// pelo worker para métricas e alertas (docs/operacao/monitoramento.md).
type Health struct {
	// HoldsActive: reservas aguardando o Pix agora. HoldsOverdue: retenção vencida há mais de 2 min
	// e ainda não solta (worker parado?).
	HoldsActive  int
	HoldsOverdue int
	// CalledStale: chamadas vencidas além da janela da falta automática (a equipe precisa decidir).
	CalledStale int
	// Avisos pendentes vencidos e a idade do mais antigo; pulados por vencimento nas últimas 24 h.
	NoticesPending   int
	NoticesOldest    time.Duration
	NoticesStale24h  int
	NoticesQueued24h int
	// StoreLateOpen: horários que a loja não chamou e ainda estão confirmados.
	StoreLateOpen int
}

// ReadHealth lê os indicadores (consultas por índice parcial; nenhuma varredura completa).
func ReadHealth(ctx context.Context, q database.Querier, now time.Time) (Health, error) {
	var h Health
	var oldest *time.Time
	err := q.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM appointment WHERE status = 'held'),
		  (SELECT count(*) FROM appointment WHERE status = 'held' AND hold_expires_at < $1::timestamptz - interval '2 minutes'),
		  (SELECT count(*) FROM appointment WHERE status = 'called' AND arrived_at IS NULL AND deadline_at <= $2),
		  (SELECT count(*) FROM appointment_notice WHERE status = 'pending' AND due_at <= $1),
		  (SELECT min(due_at) FROM appointment_notice WHERE status = 'pending' AND due_at <= $1),
		  (SELECT count(*) FROM appointment_notice WHERE status = 'skipped' AND skip_reason = 'vencido' AND done_at > $1::timestamptz - interval '24 hours'),
		  (SELECT count(*) FROM appointment_notice WHERE status = 'queued' AND done_at > $1::timestamptz - interval '24 hours'),
		  (SELECT count(*) FROM appointment WHERE status = 'confirmed' AND store_late_at IS NOT NULL)`,
		now, now.Add(-noShowGrace)).Scan(&h.HoldsActive, &h.HoldsOverdue, &h.CalledStale, &h.NoticesPending, &oldest,
		&h.NoticesStale24h, &h.NoticesQueued24h, &h.StoreLateOpen)
	if oldest != nil {
		h.NoticesOldest = now.Sub(*oldest)
	}
	return h, err
}
