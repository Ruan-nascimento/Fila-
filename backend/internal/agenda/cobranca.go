package agenda

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/estabelecimento"
	"fila/internal/pagamento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/outbox"
)

// Reserva com Pix (ADR-0039). A reserva paga nasce "aguardando pagamento" (held) e segura o
// horário até hold_expires_at; só o provedor confirma o pagamento (webhook ou consulta), nunca a
// tela. Sem pagamento no prazo, a retenção vence e o horário volta a ficar livre. Reserva de balcão
// continua gratuita (o cliente paga na barbearia).

const (
	StatusHeld    = "held"
	StatusExpired = "expired"
)

var _ pagamento.Bookings = (*Service)(nil)

// charging informa se esta loja cobra a reserva agora (liberação do servidor + Pix operacional).
func (s *Service) charging() bool {
	return s.Charge && s.Payments != nil && s.Payments.Charging()
}

// quote calcula a cotação da reserva (gratuita quando a cobrança não está disponível).
func (s *Service) quote(st estabelecimento.Settings, cents int64) pagamento.Quote {
	var min int64
	if s.Payments != nil {
		min = s.Payments.MinPixCents()
	}
	return pagamento.QuoteFor(st, cents, s.charging() && st.PixEnabled, min)
}

func (s *Service) holdDuration() time.Duration {
	if s.Payments == nil {
		return 18 * time.Minute
	}
	return s.Payments.HoldDuration()
}

// lockChairs trava as filas (cadeiras) dos profissionais e solta as retenções vencidas deles: quem
// ocupa um intervalo depois disso nunca esbarra numa retenção que já não vale.
func (s *Service) lockChairs(ctx context.Context, tx pgx.Tx, estID uuid.UUID, profs []estabelecimento.ProfessionalRef) error {
	if err := lockChairs(ctx, tx, estID, profs); err != nil {
		return err
	}
	ids := make([]uuid.UUID, len(profs))
	for i, p := range profs {
		ids[i] = p.ID
	}
	_, err := s.expireHolds(ctx, tx, estID, ids, s.Clock.Now())
	return err
}

// expireHolds vence as retenções sem pagamento dos profissionais (fila já travada). No instante
// exato do prazo a retenção já venceu.
func (s *Service) expireHolds(ctx context.Context, tx pgx.Tx, estID uuid.UUID, profIDs []uuid.UUID, now time.Time) (int, error) {
	if len(profIDs) == 0 {
		return 0, nil
	}
	rows, err := tx.Query(ctx, appointmentSelect+` WHERE establishment_id = $1 AND professional_id = ANY($2) AND status = 'held'
		AND hold_expires_at <= $3 ORDER BY id FOR UPDATE`, estID, profIDs, now)
	if err != nil {
		return 0, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (appointment, error) { return scanAppointment(r) })
	if err != nil {
		return 0, err
	}
	for _, a := range list {
		if err := s.expireOne(ctx, tx, a, now); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

// expireOne vence uma retenção (reserva travada).
func (s *Service) expireOne(ctx context.Context, tx pgx.Tx, a appointment, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE appointment SET status = 'expired', finished_at = $2, end_reason = 'pagamento não confirmado no prazo',
		version = version + 1, updated_at = $2 WHERE id = $1`, a.ID, now); err != nil {
		return err
	}
	if err := releaseOccupancy(ctx, tx, a.ID, now); err != nil {
		return err
	}
	if s.Payments != nil {
		if err := s.Payments.BookingEnded(ctx, tx, a.EstablishmentID, a.ID, pagamento.EndExpired, a.StartsAt, now); err != nil {
			return err
		}
	}
	return s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-09", a.Status, actorInfo{kind: "system"}, "pagamento não confirmado no prazo", now)
}

// LockAppointment implementa pagamento.Bookings: trava a cadeira e depois a reserva (ordem do
// ADR-0038: fila → reserva), para o webhook e o worker do pagamento.
func (s *Service) LockAppointment(ctx context.Context, tx pgx.Tx, estID, appointmentID uuid.UUID) (pagamento.BookingSlot, error) {
	for range 3 {
		before, err := loadAppointment(ctx, tx, estID, appointmentID, false)
		if err != nil {
			return pagamento.BookingSlot{}, err
		}
		queueID, err := estabelecimento.QueueOfProfessional(ctx, tx, before.ProfessionalID)
		if err != nil {
			return pagamento.BookingSlot{}, err
		}
		if _, err := estabelecimento.LockQueues(ctx, tx, estID, queueID); err != nil {
			return pagamento.BookingSlot{}, err
		}
		a, err := loadAppointment(ctx, tx, estID, appointmentID, true)
		if err != nil {
			return pagamento.BookingSlot{}, err
		}
		if a.ProfessionalID != before.ProfessionalID {
			continue // remarcada para outra cadeira no meio do caminho: trava a nova
		}
		return pagamento.BookingSlot{Status: a.Status, HoldExpiresAt: a.HoldExpiresAt, StartsAt: a.StartsAt}, nil
	}
	return pagamento.BookingSlot{}, errors.New("reserva mudando de profissional repetidamente")
}

// PaymentConfirmed implementa pagamento.Bookings: com a retenção valendo em at, a reserva fica
// confirmada; vencida, a retenção é solta aqui mesmo (o pagamento vira tardio e é devolvido).
func (s *Service) PaymentConfirmed(ctx context.Context, tx pgx.Tx, estID, appointmentID uuid.UUID, at time.Time) (bool, error) {
	a, err := loadAppointment(ctx, tx, estID, appointmentID, true)
	if err != nil {
		return false, err
	}
	if a.Status != StatusHeld || a.HoldExpiresAt == nil {
		return false, nil
	}
	if !at.Before(*a.HoldExpiresAt) {
		return false, s.expireOne(ctx, tx, a, at)
	}
	if _, err := tx.Exec(ctx, `UPDATE appointment SET status = 'confirmed', version = version + 1, updated_at = $2 WHERE id = $1`, a.ID, at); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agenda_occupancy SET hold_until = NULL WHERE appointment_id = $1 AND released_at IS NULL`, a.ID); err != nil {
		return false, err
	}
	return true, s.afterChange(ctx, tx, a.EstablishmentID, a.ID, "T-AP-10", a.Status, actorInfo{kind: "system"}, "pagamento confirmado pelo provedor", at)
}

// PaymentChanged implementa pagamento.Bookings: publica a reserva atualizada (nova versão) para o
// titular e a equipe. O histórico da reserva não muda; o pagamento tem a própria auditoria.
func (s *Service) PaymentChanged(ctx context.Context, tx pgx.Tx, estID, appointmentID uuid.UUID, at time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE appointment SET version = version + 1, updated_at = $3 WHERE establishment_id = $1 AND id = $2`,
		estID, appointmentID, at); err != nil {
		return err
	}
	a, err := loadAppointment(ctx, tx, estID, appointmentID, false)
	if err != nil {
		return err
	}
	if err := s.refundNotice(ctx, tx, a, at); err != nil {
		return err
	}
	return s.publish(ctx, tx, a, at)
}

// publish grava o evento appointment.updated (titular e equipe) com a visão atual.
func (s *Service) publish(ctx context.Context, q database.Querier, a appointment, at time.Time) error {
	forCustomer, err := view(ctx, q, a, false)
	if err != nil {
		return err
	}
	forStaff, err := view(ctx, q, a, true)
	if err != nil {
		return err
	}
	base := outbox.Event{EstablishmentID: a.EstablishmentID, Type: evAppointment, ResourceType: "appointment", ResourceID: a.ID,
		ResourceVersion: a.Version, OccurredAt: at}
	aud := outbox.Audience{Kind: outbox.Visit, AccountID: a.AccountID, VisitorID: a.VisitorID}
	return outbox.Record(ctx, q, outbox.ForVisitAndStaff(base, aud, a.ProfessionalID, forCustomer, forStaff)...)
}

// expireDue é a rodada do worker: retenções vencidas, por cadeira (trava fila → reserva).
func (s *Service) expireDue(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT establishment_id, professional_id FROM appointment
		WHERE status = 'held' AND hold_expires_at <= $1 LIMIT $2`, now, batch)
	if err != nil {
		return 0, err
	}
	type cand struct{ est, prof uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cand, error) {
		var c cand
		err := r.Scan(&c.est, &c.prof)
		return c, err
	})
	if err != nil {
		return 0, err
	}
	done := 0
	for _, c := range list {
		err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
			queueID, err := estabelecimento.QueueOfProfessional(ctx, tx, c.prof)
			if err != nil {
				return err
			}
			if _, err := estabelecimento.LockQueues(ctx, tx, c.est, queueID); err != nil {
				return err
			}
			n, err := s.expireHolds(ctx, tx, c.est, []uuid.UUID{c.prof}, s.Clock.Now())
			done += n
			return err
		})
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// bookingPayment monta o pagamento da reserva paga (valores aceitos na reserva).
func bookingPayment(a appointment, b pagamento.BookingView) paymentView {
	amount := b.Amount.AmountCents
	p := paymentView{Mode: b.Mode, DueNow: brl(amount), Booking: &b}
	if b.Mode == "fee" {
		p.DueAtStore, p.EstimatedFor = brl(a.PriceCents), brl(a.PriceCents+amount)
	} else {
		p.DueAtStore, p.EstimatedFor = brl(max(a.PriceCents-amount, 0)), brl(a.PriceCents)
	}
	until := a.StartsAt.Add(-time.Duration(b.CancelWindowHours) * time.Hour).UTC()
	p.FreeCancelUntil = &until
	return p
}

// reaisBR formata centavos como "R$ 15,00".
func reaisBR(c int64) string { return fmt.Sprintf("R$ %d,%02d", c/100, c%100) }

func bookingTerms(a appointment, b pagamento.BookingView) string {
	return pagamento.TermsFor(b.Mode, b.Amount.AmountCents, a.PriceCents, b.CancelWindowHours, b.LateCancelPolicy)
}

// ---------------------------------------------------------------------------------------------
// Pix da reserva (createAppointmentPixCharge)

type bookingChargeView struct {
	pagamento.BookingCharge
	Appointment appointmentView `json:"appointment"`
}

// createAppointmentPixCharge (T-BK-02): o titular gera (ou retoma) o Pix da reserva retida. A
// chamada ao provedor é feita fora da transação; repetir com a mesma chave não cria outra cobrança.
func (s *Service) createAppointmentPixCharge(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	id, err := pathID(r, "appointmentId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	a, err := loadAppointment(ctx, s.Pool, estID, id, false)
	if err != nil {
		return err
	}
	by, err := s.authorize(r, a, true, false)
	if err != nil {
		return err
	}
	key := r.Header.Get(idempotency.Header)
	if err := idempotency.CheckKey(key); err != nil {
		return err
	}
	keyHash := sha256.Sum256([]byte(by.key() + "|" + key))
	if s.Payments == nil {
		return conflict("provider_unavailable", "Pix indisponível", "O Pix está indisponível no momento.", nil)
	}
	createdBy := "customer"
	if by.customer.Kind != acesso.IdentityAccount {
		createdBy = "visitor"
	}
	ch, status, err := s.Payments.StartBookingCharge(ctx, estID, id, keyHash[:], createdBy)
	if err != nil {
		return err
	}
	cur, err := loadAppointment(ctx, s.Pool, estID, id, false)
	if err != nil {
		return err
	}
	v, err := view(ctx, s.Pool, cur, false)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, status, hs.Data[bookingChargeView]{Data: bookingChargeView{BookingCharge: ch, Appointment: v}})
	return nil
}
