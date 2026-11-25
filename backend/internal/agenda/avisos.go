package agenda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/estabelecimento"
	"fila/internal/pagamento"
	"fila/internal/platform/database"
	"fila/internal/platform/mail"
)

// Avisos da reserva por e-mail (ADR-0039). O comando que muda a reserva grava o aviso
// (appointment_notice) na MESMA transação: um por (reserva, revisão, tipo), então repetir o
// comando, reprocessar o worker ou remarcar várias vezes nunca duplica nem perde aviso. O worker
// revalida antes de enviar (reserva remarcada, cancelada ou já chamada → aviso velho é pulado) e
// grava o e-mail na caixa de saída cifrada; a entrega (fora de transação, com reenvio) é a do
// despachante de e-mail. "Enviado" = aceito pelo provedor de e-mail, nunca "lido".

// Tipos de aviso.
const (
	noticeConfirmed   = "confirmed"
	noticeReminder    = "reminder"
	noticeCalled      = "called"
	noticeRescheduled = "rescheduled"
	noticeCancelled   = "cancelled"
	noticeNoShow      = "no_show"
	noticeExpired     = "expired"
	noticeRefund      = "refund"
)

// queueNotice grava um aviso pendente (idempotente por reserva, revisão e tipo).
func queueNotice(ctx context.Context, q database.Querier, a appointment, kind string, due, now time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO appointment_notice (establishment_id, appointment_id, revision, kind, due_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (appointment_id, revision, kind) DO NOTHING`,
		a.EstablishmentID, a.ID, a.Revision, kind, due, now)
	return err
}

// planNotices decide os avisos de uma mudança (chamado por afterChange, já com o estado novo).
func (s *Service) planNotices(ctx context.Context, q database.Querier, a appointment, action string, at time.Time) error {
	var kinds []string
	switch {
	case (action == "T-AP-01" || action == "T-AP-10") && a.Status == StatusConfirmed:
		kinds = []string{noticeConfirmed}
	case action == "T-AP-02":
		kinds = []string{noticeRescheduled}
	case action == "T-AP-03":
		kinds = []string{noticeCancelled}
	case action == "T-AP-06":
		kinds = []string{noticeCalled}
	case action == "T-AP-07" && a.Status == StatusNoShow:
		kinds = []string{noticeNoShow}
	case action == "T-AP-09":
		kinds = []string{noticeExpired}
	default:
		return nil
	}
	for _, k := range kinds {
		if err := queueNotice(ctx, q, a, k, at, at); err != nil {
			return err
		}
	}
	if a.Status != StatusConfirmed || (action != "T-AP-01" && action != "T-AP-10" && action != "T-AP-02") {
		return nil
	}
	st, err := estabelecimento.LoadSettings(ctx, q, a.EstablishmentID)
	if err != nil {
		return err
	}
	if st.ReminderHours <= 0 {
		return nil
	}
	due := a.StartsAt.Add(-time.Duration(st.ReminderHours) * time.Hour)
	if !due.After(at.Add(30 * time.Minute)) {
		return nil // marcado em cima da hora: a confirmação já é o aviso
	}
	return queueNotice(ctx, q, a, noticeReminder, due, at)
}

// refundNotice: devolução confirmada (pelo provedor ou pelo dono) → aviso ao cliente.
func (s *Service) refundNotice(ctx context.Context, q database.Querier, a appointment, at time.Time) error {
	bv, err := pagamento.BookingViews(ctx, q, []uuid.UUID{a.ID})
	if err != nil {
		return err
	}
	if b, ok := bv[a.ID]; ok && b.Refund != nil && b.Refund.Status == "confirmed" {
		return queueNotice(ctx, q, a, noticeRefund, at, at)
	}
	return nil
}

// deliverNotices é a rodada do worker: avisos vencidos, um por transação.
func (s *Service) deliverNotices(ctx context.Context, now time.Time) (int, error) {
	if s.Mail == nil || s.NoticeEmail == nil {
		return 0, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM appointment_notice WHERE status = 'pending' AND due_at <= $1
		ORDER BY due_at LIMIT $2`, now, batch)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
			sent, err := s.deliverOne(ctx, tx, id, s.Clock.Now())
			if sent {
				done++
			}
			return err
		})
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

func (s *Service) deliverOne(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time) (bool, error) {
	var apptID uuid.UUID
	var revision int
	var kind string
	err := tx.QueryRow(ctx, `SELECT appointment_id, revision, kind FROM appointment_notice WHERE id = $1 AND status = 'pending'
		FOR UPDATE SKIP LOCKED`, id).Scan(&apptID, &revision, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // outro worker pegou
	}
	if err != nil {
		return false, err
	}
	a, err := loadAppointmentBy(ctx, tx, `id = $1`, false, apptID)
	if err != nil {
		return false, err
	}
	skip := func(reason string) (bool, error) {
		_, err := tx.Exec(ctx, `UPDATE appointment_notice SET status = 'skipped', skip_reason = $2, done_at = $3 WHERE id = $1`, id, reason, now)
		return false, err
	}
	if reason := staleNotice(a, revision, kind, now); reason != "" {
		return skip(reason)
	}
	to, ok, err := s.NoticeEmail(ctx, tx, a.AccountID, a.VisitorID)
	if err != nil {
		return false, err
	}
	if !ok {
		return skip("sem e-mail verificado")
	}
	store, err := estabelecimento.ActiveByID(ctx, tx, a.EstablishmentID)
	if err != nil {
		return skip("loja indisponível")
	}
	names, err := estabelecimento.ProfessionalNames(ctx, tx, []uuid.UUID{a.ProfessionalID})
	if err != nil {
		return false, err
	}
	subject, body := s.noticeText(tx, ctx, a, kind, store, names[a.ProfessionalID])
	if err := s.Mail.Enqueue(ctx, tx, "aviso_reserva_"+kind, mail.Message{To: to, Subject: subject, Body: body}, now); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE appointment_notice SET status = 'queued', attempts = attempts + 1, done_at = $2 WHERE id = $1`, id, now)
	return err == nil, err
}

// staleNotice devolve o motivo para não enviar um aviso que deixou de valer ("" = enviar).
func staleNotice(a appointment, revision int, kind string, now time.Time) string {
	switch kind {
	case noticeConfirmed, noticeRescheduled:
		if a.Revision != revision || a.Status != StatusConfirmed {
			return "reserva mudou"
		}
	case noticeReminder:
		if a.Revision != revision || a.Status != StatusConfirmed || !now.Before(a.StartsAt) {
			return "reserva mudou"
		}
	case noticeCalled:
		if a.Status != StatusCalled {
			return "chamada encerrada"
		}
	case noticeCancelled:
		if a.Status != StatusCancelled {
			return "reserva mudou"
		}
	case noticeNoShow:
		if a.Status != StatusNoShow {
			return "reserva mudou"
		}
	case noticeExpired:
		if a.Status != StatusExpired {
			return "reserva mudou"
		}
	}
	return ""
}

func (s *Service) noticeText(q database.Querier, ctx context.Context, a appointment, kind string, store estabelecimento.Public,
	professional string) (string, string) {
	loc, err := time.LoadLocation(a.Timezone)
	if err != nil {
		loc = time.UTC
	}
	when := a.StartsAt.In(loc)
	date := fmt.Sprintf("%s, %s às %s", weekdayPT(when.Weekday()), when.Format("02/01"), hora(a.StartsAt, loc))
	link := strings.TrimRight(s.PublicBaseURL, "/") + "/loja/" + store.Slug + "/reservas"
	head := "Olá, " + a.Nick + "."
	what := fmt.Sprintf("%s com %s, %s.", store.Name, professional, date)
	var subject, msg string
	switch kind {
	case noticeConfirmed:
		subject, msg = "Reserva confirmada", "Sua reserva está confirmada: "+what
		if bv, err := pagamento.BookingViews(ctx, q, []uuid.UUID{a.ID}); err == nil {
			if b, ok := bv[a.ID]; ok && b.Status == pagamento.BookingPaid {
				msg += fmt.Sprintf("\n\nPagamento da reserva confirmado: %s pelo Pix.", reaisBR(b.Amount.AmountCents))
			}
		}
	case noticeReminder:
		subject, msg = "Lembrete do seu horário", "Lembrete: "+what+"\n\nSe não puder ir, cancele pelo link abaixo para liberar o horário."
	case noticeCalled:
		subject, msg = "Chegou a sua vez", fmt.Sprintf("%s está chamando você agora (%s).", store.Name, professional)
	case noticeRescheduled:
		subject, msg = "Reserva remarcada", "Sua reserva foi remarcada: "+what
	case noticeCancelled:
		subject, msg = "Reserva cancelada", "A reserva de "+what+" foi cancelada."
		if a.CancelledBy != nil && *a.CancelledBy == "store" {
			msg = "A barbearia cancelou a reserva de " + what + " Se houve pagamento, a devolução é feita pelo Pix."
		}
	case noticeNoShow:
		subject, msg = "Reserva encerrada por ausência", "O prazo de chegada da reserva de "+what+" venceu e ela foi encerrada."
	case noticeExpired:
		subject, msg = "Reserva não concluída", "O pagamento da reserva de "+what+" não foi confirmado no prazo e o horário foi liberado. Se o Pix for pago depois, o valor é devolvido."
	case noticeRefund:
		subject, msg = "Devolução confirmada", "A devolução do valor da reserva de "+what+" foi confirmada. Pelo Pix, pode levar alguns dias para aparecer no extrato."
	}
	return subject, head + "\n\n" + msg + "\n\nAcompanhe em: " + link + "\n"
}

func weekdayPT(d time.Weekday) string {
	return [...]string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}[d]
}
