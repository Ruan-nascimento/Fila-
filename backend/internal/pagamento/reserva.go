package pagamento

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Cobrança da reserva (ADR-0039). A obrigação financeira (booking_payment) nasce junto com a
// retenção do horário, antes de existir comanda; a cobrança Pix, o recebimento e o estorno
// apontam para ela (XOR com a comanda, conferido no banco). Nunca se cria comanda fictícia.
//
// Ordem de travas: identidade → fila → participação → reserva → comanda → obrigação → cobrança,
// recebimento e estorno. Caminhos que começam por aqui (worker e webhook) travam a RESERVA primeiro
// (pelo gancho Bookings, do módulo agenda) e só depois a obrigação.

// Estados da obrigação.
const (
	BookingAwaiting  = "awaiting_payment"
	BookingPaid      = "paid"
	BookingExpired   = "expired"
	BookingCancelled = "cancelled"
)

// Desfecho do valor pago.
const (
	OutcomeApplied   = "applied"
	OutcomeKept      = "kept"
	OutcomeRefundDue = "refund_due"
)

// Pendências próprias da reserva.
const (
	IssueBookingLate      = "booking_late_payment"
	IssueBookingRefundDue = "booking_refund_due"
	IssueBookingNotice    = "booking_notice_failed"
)

// holdMargin é a folga entre o vencimento do QR Code e o fim da retenção do horário: um Pix pago
// no último minuto do QR ainda chega (webhook ou consulta) com a retenção valendo.
const holdMargin = 3 * time.Minute

// BookingSlot é o que o pagamento precisa saber da reserva (módulo agenda).
type BookingSlot struct {
	Status        string
	HoldExpiresAt *time.Time
	StartsAt      time.Time
}

// Bookings é implementado pelo módulo agenda.
type Bookings interface {
	// LockAppointment trava a reserva (antes da obrigação) e devolve o estado atual.
	LockAppointment(ctx context.Context, tx pgx.Tx, estID, appointmentID uuid.UUID) (BookingSlot, error)
	// PaymentConfirmed passa a reserva de "aguardando pagamento" a confirmada se a retenção ainda
	// vale no instante at (relógio do servidor). false = retenção vencida ou reserva encerrada.
	PaymentConfirmed(ctx context.Context, tx pgx.Tx, estID, appointmentID uuid.UUID, at time.Time) (bool, error)
	// PaymentChanged grava o evento da reserva (o pagamento dela mudou).
	PaymentChanged(ctx context.Context, tx pgx.Tx, estID, appointmentID uuid.UUID, at time.Time) error
}

// Charging informa se há provedor Pix nesta instalação (sem ele, nenhuma reserva é cobrada).
func (s *Service) Charging() bool { return s.Provider != nil }

// HoldDuration é o tempo que um horário fica retido aguardando o Pix.
func (s *Service) HoldDuration() time.Duration { return s.ExpiresIn + holdMargin }

// MinPixCents é o menor valor aceito pelo provedor (0 sem Pix).
func (s *Service) MinPixCents() int64 {
	if s.Provider == nil {
		return 0
	}
	return s.Provider.Capabilities().MinPixCents
}

// ---------------------------------------------------------------------------------------------
// Cotação: o que o cliente paga agora e o que fica para a barbearia.

// Quote é a cotação mostrada na revisão (sempre calculada no servidor).
type Quote struct {
	Mode              string            `json:"mode"` // free | deposit | fee
	AmountCents       int64             `json:"-"`
	ServiceCents      int64             `json:"-"`
	DueNow            atendimento.Money `json:"due_now"`
	DueAtStore        atendimento.Money `json:"due_at_store"`
	Total             atendimento.Money `json:"estimated_total"`
	CancelWindowHours int               `json:"cancel_window_hours"`
	LateCancelPolicy  string            `json:"late_cancel_policy"`
	Terms             string            `json:"terms"`
}

// QuoteFor calcula a cotação de uma reserva com serviços de serviceCents. charging=false (sem Pix,
// sem liberação) ou valor abaixo do mínimo do Pix → reserva gratuita. O sinal nunca passa do valor
// dos serviços (sinal igual ao total = nada a pagar na barbearia depois do atendimento integral).
func QuoteFor(st estabelecimento.Settings, serviceCents int64, charging bool, minPix int64) Quote {
	q := Quote{Mode: estabelecimento.BookingFree, ServiceCents: serviceCents, CancelWindowHours: st.CancelWindowHours,
		LateCancelPolicy: st.LateCancelPolicy}
	amount := int64(0)
	if charging && st.BookingPaymentCents != nil && st.BookingPaymentMode != estabelecimento.BookingFree {
		amount = *st.BookingPaymentCents
		if st.BookingPaymentMode == estabelecimento.BookingDeposit {
			amount = min(amount, serviceCents)
		}
		if amount >= max(minPix, 100) {
			q.Mode = st.BookingPaymentMode
		} else {
			amount = 0
		}
	}
	q.AmountCents = amount
	switch q.Mode {
	case estabelecimento.BookingDeposit:
		q.DueNow, q.DueAtStore, q.Total = brl(amount), brl(serviceCents-amount), brl(serviceCents)
	case estabelecimento.BookingFee:
		q.DueNow, q.DueAtStore, q.Total = brl(amount), brl(serviceCents), brl(serviceCents+amount)
	default:
		q.DueNow, q.DueAtStore, q.Total = brl(0), brl(serviceCents), brl(serviceCents)
	}
	q.Terms = TermsFor(q.Mode, amount, serviceCents, st.CancelWindowHours, st.LateCancelPolicy)
	return q
}

// TermsFor é o texto mostrado antes de pagar (e guardado na visão da reserva).
func TermsFor(mode string, amount, serviceCents int64, windowHours int, late string) string {
	switch mode {
	case estabelecimento.BookingDeposit:
		return fmt.Sprintf("Pague %s agora. Esse valor será descontado do atendimento. Na barbearia ficam %s. ",
			reais(amount), reais(max(serviceCents-amount, 0))) + policyText(windowHours, late)
	case estabelecimento.BookingFee:
		return fmt.Sprintf("A reserva custa %s além do atendimento. O total é %s. ", reais(amount), reais(serviceCents+amount)) +
			policyText(windowHours, late)
	}
	return "Reserva gratuita. Você paga na barbearia o valor dos serviços feitos."
}

// policyText explica cancelamento, falta e devolução antes de pagar.
func policyText(windowHours int, late string) string {
	t := "Se a barbearia cancelar, o valor é devolvido."
	if windowHours > 0 {
		t += fmt.Sprintf(" Cancelando até %d h antes do horário, o valor é devolvido.", windowHours)
	} else {
		t += " Cancelando antes do horário, o valor é devolvido."
	}
	if late == "retain" {
		t += " Cancelamento depois disso ou falta: o valor fica com a barbearia."
	} else {
		t += " Cancelamento depois disso ou falta: o valor também é devolvido."
	}
	return t + " A devolução é feita pelo Pix e pode levar alguns dias para aparecer."
}

// ---------------------------------------------------------------------------------------------
// Obrigação

// Obligation são os dados de uma obrigação nova (na transação da reserva, que já travou a cadeira).
type Obligation struct {
	EstablishmentID uuid.UUID
	AppointmentID   uuid.UUID
	Quote           Quote
	HoldExpiresAt   time.Time
	PolicyVersion   int64
	Now             time.Time
}

type bookingRow struct {
	ID                uuid.UUID
	EstablishmentID   uuid.UUID
	AppointmentID     uuid.UUID
	Mode              string
	Amount            int64
	Status            string
	HoldExpiresAt     time.Time
	CancelWindowHours int
	LatePolicy        string
	PaidAt            *time.Time
	Outcome           *string
	OrderID           *uuid.UUID
	Version           int64
}

const bookingSelect = `SELECT id, establishment_id, appointment_id, mode, amount_cents, status, hold_expires_at,
	cancel_window_hours, late_cancel_policy, paid_at, outcome, order_id, version FROM booking_payment`

func scanBooking(r pgx.Row) (bookingRow, error) {
	var b bookingRow
	err := r.Scan(&b.ID, &b.EstablishmentID, &b.AppointmentID, &b.Mode, &b.Amount, &b.Status, &b.HoldExpiresAt,
		&b.CancelWindowHours, &b.LatePolicy, &b.PaidAt, &b.Outcome, &b.OrderID, &b.Version)
	return b, err
}

// CreateObligation grava a obrigação da reserva (T-BK-01). Só para cotação paga.
func (s *Service) CreateObligation(ctx context.Context, q database.Querier, in Obligation) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO booking_payment (establishment_id, appointment_id, mode, amount_cents, hold_expires_at, cancel_window_hours,
		                             late_cancel_policy, policy_version, accepted_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $9) RETURNING id`,
		in.EstablishmentID, in.AppointmentID, in.Quote.Mode, in.Quote.AmountCents, in.HoldExpiresAt, in.Quote.CancelWindowHours,
		in.Quote.LateCancelPolicy, in.PolicyVersion, in.Now).Scan(&id)
	if err != nil {
		return id, fmt.Errorf("criar obrigação da reserva: %w", err)
	}
	return id, auditoria.Record(ctx, q, auditoria.Event{EstablishmentID: &in.EstablishmentID, OccurredAt: in.Now,
		ActorKind: auditoria.ActorSystem, Action: "T-BK-01", TargetType: "booking_payment", TargetID: id, ToState: BookingAwaiting})
}

// AttachOrder liga a obrigação paga à comanda aberta na chamada da reserva (o crédito sai dela
// quando o total fechar). Sem obrigação, nada a fazer.
func AttachOrder(ctx context.Context, q database.Querier, estID, appointmentID, orderID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE booking_payment SET order_id = $3, version = version + 1, updated_at = $4
		WHERE establishment_id = $1 AND appointment_id = $2 AND order_id IS NULL`, estID, appointmentID, orderID, now)
	return err
}

// BookingView é o pagamento da reserva como o titular e a equipe veem.
type BookingView struct {
	Mode              string            `json:"mode"`
	Status            string            `json:"status"`
	Amount            atendimento.Money `json:"amount"`
	HoldExpiresAt     time.Time         `json:"hold_expires_at"`
	PaidAt            *time.Time        `json:"paid_at"`
	Outcome           *string           `json:"outcome"`
	CancelWindowHours int               `json:"cancel_window_hours"`
	LateCancelPolicy  string            `json:"late_cancel_policy"`
	Charge            *BookingCharge    `json:"charge"`
	Verifying         bool              `json:"verifying"`
	Refund            *BookingRefund    `json:"refund"`
	Credit            atendimento.Money `json:"credit_applied"`
}

// BookingCharge é a cobrança Pix ativa (ou a última) da reserva.
type BookingCharge struct {
	ID        uuid.UUID         `json:"id"`
	Status    string            `json:"status"`
	Amount    atendimento.Money `json:"amount"`
	BRCode    *string           `json:"br_code"`
	ExpiresAt *time.Time        `json:"expires_at"`
	CreatedAt time.Time         `json:"created_at"`
}

// BookingRefund resume a devolução (separada do estado da agenda: "solicitada" não é "devolvida").
type BookingRefund struct {
	Status string            `json:"status"`
	Amount atendimento.Money `json:"amount"`
	// Partial: devolução de parte do sinal, feita pela barbearia fora do provedor.
	Partial bool `json:"partial"`
}

// BookingViews devolve a visão do pagamento de cada reserva (as gratuitas não aparecem).
func BookingViews(ctx context.Context, q database.Querier, appointmentIDs []uuid.UUID) (map[uuid.UUID]BookingView, error) {
	out := map[uuid.UUID]BookingView{}
	if len(appointmentIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, bookingSelect+` WHERE appointment_id = ANY($1)`, appointmentIDs)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (bookingRow, error) { return scanBooking(r) })
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		v := BookingView{Mode: b.Mode, Status: b.Status, Amount: brl(b.Amount), HoldExpiresAt: b.HoldExpiresAt.UTC(), PaidAt: b.PaidAt,
			Outcome: b.Outcome, CancelWindowHours: b.CancelWindowHours, LateCancelPolicy: b.LatePolicy, Credit: brl(0)}
		if v.PaidAt != nil {
			p := v.PaidAt.UTC()
			v.PaidAt = &p
		}
		c, err := scanCharge(q.QueryRow(ctx, chargeSelect+` WHERE booking_payment_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, b.ID))
		if err == nil {
			v.Charge = &BookingCharge{ID: c.ID, Status: c.Status, Amount: c.Amount, BRCode: c.BRCode, ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt.UTC()}
			if v.Charge.ExpiresAt != nil {
				e := v.Charge.ExpiresAt.UTC()
				v.Charge.ExpiresAt = &e
			}
			v.Verifying = c.Status == ChargeUnknown || c.Status == ChargeCreating
			if c.Status != ChargePending && c.Status != ChargeCreating && c.Status != ChargeUnknown && c.Status != ChargePaid {
				v.Charge.BRCode = nil // QR vencido/cancelado: não se mostra para pagar
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var rf BookingRefund
		var cents int64
		err = q.QueryRow(ctx, `SELECT status, amount_cents, partial FROM refund WHERE booking_payment_id = $1 AND status <> 'failed'
			ORDER BY requested_at DESC LIMIT 1`, b.ID).Scan(&rf.Status, &cents, &rf.Partial)
		if err == nil {
			rf.Amount = brl(cents)
			v.Refund = &rf
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var credit int64
		if err := q.QueryRow(ctx, `SELECT coalesce(sum(amount_cents), 0) FROM booking_credit WHERE booking_payment_id = $1`, b.ID).
			Scan(&credit); err != nil {
			return nil, err
		}
		v.Credit = brl(credit)
		out[b.AppointmentID] = v
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------
// Pix da reserva (T-BK-02): três fases, como o Pix da comanda.

var errHoldOver = &hs.Error{Status: 409, Code: "booking_hold_expired", Title: "O prazo para pagar acabou",
	Detail: "O horário não ficou reservado porque o Pix não foi pago a tempo. Escolha um horário de novo."}

func (s *Service) bookingPixRequest(c chargeRow, amount int64) CreatePix {
	return CreatePix{ExternalID: c.ID.String(), AmountCents: amount, ExpiresIn: s.ExpiresIn, Description: "Reserva de horário na barbearia",
		Metadata: map[string]string{"charge_id": c.ID.String(), "booking_id": c.BookingID.String(), "establishment_id": c.EstablishmentID.String()}}
}

// StartBookingCharge cria (ou devolve) o Pix da reserva retida. keyHash identifica a intenção
// (mesma chave → mesma tentativa, sem nova chamada). createdBy: visitor | customer.
func (s *Service) StartBookingCharge(ctx context.Context, estID, appointmentID uuid.UUID, keyHash []byte, createdBy string) (BookingCharge, int, error) {
	if s.Provider == nil || s.Bookings == nil {
		return BookingCharge{}, 0, errProviderUnavailable
	}
	var ch chargeRow
	var b bookingRow
	reuse := false
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		slot, err := s.Bookings.LockAppointment(ctx, tx, estID, appointmentID)
		if err != nil {
			return err
		}
		b, err = scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE establishment_id = $1 AND appointment_id = $2 FOR UPDATE`, estID, appointmentID))
		if errors.Is(err, pgx.ErrNoRows) {
			return conflict("booking_not_payable", "Nada a pagar", "Esta reserva é gratuita.", nil)
		}
		if err != nil {
			return err
		}
		if c, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE booking_payment_id = $1 AND idem_key_hash = $2`, b.ID, keyHash)); err == nil {
			ch, reuse = c, true
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if cur, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE booking_payment_id = $1 AND status = ANY($2)`, b.ID, activeStatuses)); err == nil {
			if cur.Status == ChargeUnknown {
				return conflict("pix_charge_outcome_unknown", "Verificando pagamento",
					"Ainda estamos confirmando o Pix anterior com o banco. Você pode sair e voltar; o horário continua retido até o prazo.", nil)
			}
			ch, reuse = cur, true // a mesma cobrança aberta (outra aba, voltou à tela)
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := s.Clock.Now()
		if b.Status != BookingAwaiting || slot.Status != "held" || slot.HoldExpiresAt == nil || !now.Before(*slot.HoldExpiresAt) {
			if b.Status == BookingPaid {
				return conflict("booking_already_paid", "Reserva já paga", "O pagamento desta reserva já foi confirmado.", nil)
			}
			return errHoldOver
		}
		if min := s.Provider.Capabilities().MinPixCents; b.Amount < min {
			return conflict("payment_method_not_allowed", "Pix indisponível para este valor",
				fmt.Sprintf("O Pix exige no mínimo %s.", reais(min)), nil)
		}
		id, _ := uuid.NewV7()
		ch, err = scanCharge(tx.QueryRow(ctx, `
			INSERT INTO pix_charge (id, establishment_id, booking_payment_id, amount_cents, provider, test_mode, idem_key_hash, created_by,
			                        created_at, updated_at, next_check_at)
			VALUES ($1, $2, $3, $4, 'abacatepay', $5, $6, $7, $8, $8, $9)
			RETURNING id, coalesce(order_id, '00000000-0000-0000-0000-000000000000'), status, amount_cents, br_code, expires_at, created_at,
			          resolved_at, cancel_requested_at, establishment_id, provider_charge_id, test_mode, check_attempts, booking_payment_id`,
			id, estID, b.ID, b.Amount, s.Provider.TestMode(), keyHash, createdBy, now, now.Add(2*s.Timeout)))
		if database.IsUniqueViolation(err, "pix_charge_booking_active_uq") {
			return conflict("pix_charge_active", "Já existe um Pix em aberto", "Use o Pix já gerado para esta reserva.", nil)
		}
		if err != nil {
			return err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: createdBy,
			Action: "T-PX-01", TargetType: "pix_charge", TargetID: ch.ID, ToState: ChargeCreating}); err != nil {
			return err
		}
		return s.Bookings.PaymentChanged(ctx, tx, estID, appointmentID, now)
	})
	if err != nil {
		return BookingCharge{}, 0, err
	}
	view := func(c chargeRow) BookingCharge {
		v := BookingCharge{ID: c.ID, Status: c.Status, Amount: c.Amount, BRCode: c.BRCode, ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt.UTC()}
		if v.ExpiresAt != nil {
			e := v.ExpiresAt.UTC()
			v.ExpiresAt = &e
		}
		return v
	}
	status := func(c chargeRow) int {
		if c.Status == ChargeUnknown || c.Status == ChargeCreating {
			return http.StatusAccepted
		}
		return http.StatusCreated
	}
	if reuse {
		if ch.Status == ChargeFailed {
			return BookingCharge{}, 0, errProviderUnavailable
		}
		return view(ch), status(ch), nil
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Timeout+2*time.Second)
	defer cancel()
	in := s.bookingPixRequest(ch, ch.amount)
	// O QR vence antes da retenção (margem): o pagamento no último minuto ainda chega a tempo.
	if remaining := b.HoldExpiresAt.Sub(s.Clock.Now()) - holdMargin; remaining < in.ExpiresIn {
		in.ExpiresIn = max(remaining, time.Minute)
	}
	pc, callErr := s.Provider.CreatePix(callCtx, in)
	final, err := s.bookingRecordCreation(context.WithoutCancel(ctx), ch, b, pc, callErr)
	if err != nil {
		return BookingCharge{}, 0, err
	}
	if final.Status == ChargeFailed {
		return BookingCharge{}, 0, errProviderUnavailable
	}
	if pc.ExpiresAt != nil && pc.ExpiresAt.After(b.HoldExpiresAt) {
		// Prazo do provedor além da retenção: não se promete o horário além dela; um pagamento
		// depois do fim da retenção vira tardio (devolução), nunca toma o horário de outra pessoa.
		s.Log.Warn("QR do provedor vence depois da retenção do horário", "tentativa", ch.ID.String())
	}
	return view(final), status(final), nil
}

// bookingRecordCreation grava o resultado da criação (fase 3).
func (s *Service) bookingRecordCreation(ctx context.Context, ch chargeRow, b bookingRow, pc Charge, callErr error) (chargeRow, error) {
	var out chargeRow
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := s.Bookings.LockAppointment(ctx, tx, b.EstablishmentID, b.AppointmentID); err != nil {
			return err
		}
		cur, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE id = $1 FOR UPDATE`, b.ID))
		if err != nil {
			return err
		}
		c, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE id = $1 FOR UPDATE`, ch.ID))
		if err != nil {
			return err
		}
		out = c
		if c.Status != ChargeCreating {
			return nil
		}
		now := s.Clock.Now()
		switch {
		case callErr == nil:
			return s.bookingApply(ctx, tx, &cur, &out, pc, "provider_query", now)
		case errors.Is(callErr, ErrNotSent), errors.Is(callErr, ErrRejected):
			s.Log.Warn("Pix da reserva não criado pelo provedor", "tentativa", ch.ID.String(), "motivo", callErr.Error())
			if err := s.markStatus(ctx, tx, &out, ChargeFailed, "T-PX-08", now); err != nil {
				return err
			}
		default:
			s.Log.Warn("Pix da reserva com resultado desconhecido; será reconciliado", "tentativa", ch.ID.String(), "motivo", callErr.Error())
			if _, err := tx.Exec(ctx, `UPDATE pix_charge SET next_check_at = $2 WHERE id = $1`, ch.ID, now.Add(5*time.Second)); err != nil {
				return err
			}
			if err := s.markStatus(ctx, tx, &out, ChargeUnknown, "T-PX-03", now); err != nil {
				return err
			}
		}
		return s.Bookings.PaymentChanged(ctx, tx, cur.EstablishmentID, cur.AppointmentID, now)
	})
	return out, err
}

// lockBookingFor trava reserva → obrigação → cobrança (caminhos do worker e do webhook).
func (s *Service) lockBookingFor(ctx context.Context, tx pgx.Tx, c chargeRow) (bookingRow, chargeRow, error) {
	b, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE id = $1`, *c.BookingID))
	if err != nil {
		return b, c, err
	}
	if _, err := s.Bookings.LockAppointment(ctx, tx, b.EstablishmentID, b.AppointmentID); err != nil {
		return b, c, err
	}
	if b, err = scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE id = $1 FOR UPDATE`, b.ID)); err != nil {
		return b, c, err
	}
	cur, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE id = $1 FOR UPDATE`, c.ID))
	return b, cur, err
}

// bookingFinish é o finish da reconciliação para cobrança de reserva.
func (s *Service) bookingFinish(ctx context.Context, c chargeRow, pc *Charge, failTo string) error {
	if s.Bookings == nil {
		return errors.New("cobrança de reserva sem o módulo agenda ligado")
	}
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		b, cur, err := s.lockBookingFor(ctx, tx, c)
		if err != nil {
			return err
		}
		now := s.Clock.Now()
		if failTo != "" {
			if cur.Status == ChargeCreating || cur.Status == ChargeUnknown {
				if err := s.markStatus(ctx, tx, &cur, failTo, "T-PX-08", now); err != nil {
					return err
				}
				return s.Bookings.PaymentChanged(ctx, tx, b.EstablishmentID, b.AppointmentID, now)
			}
			return nil
		}
		if cur.Status == ChargePaid || cur.Status == ChargeExpired || cur.Status == ChargeCancelled || cur.Status == ChargeFailed {
			if pc.Status == ProviderPaid && cur.Status != ChargePaid {
				return s.bookingPaid(ctx, tx, &b, &cur, *pc, "provider_query", now)
			}
			return nil
		}
		if err := s.bookingApply(ctx, tx, &b, &cur, *pc, "provider_query", now); err != nil {
			if errors.Is(err, ErrIntegrity) {
				s.Log.Error("resposta do provedor não confere; Pix da reserva segue em verificação", "tentativa", c.ID.String(), "erro", err.Error())
				return s.schedule(ctx, tx, cur, now)
			}
			return err
		}
		return s.schedule(ctx, tx, cur, now)
	})
}

// bookingApply aplica o estado informado pelo provedor a uma cobrança de reserva travada.
func (s *Service) bookingApply(ctx context.Context, tx pgx.Tx, b *bookingRow, c *chargeRow, pc Charge, evidence string, now time.Time) error {
	if pc.Metadata["charge_id"] != "" && pc.Metadata["charge_id"] != c.ID.String() {
		return fmt.Errorf("%w: cobrança do provedor de outra tentativa", ErrIntegrity)
	}
	if c.ProviderID == nil && pc.ProviderID != "" {
		if _, err := tx.Exec(ctx, `UPDATE pix_charge SET provider_charge_id = $2, br_code = nullif($3, ''), expires_at = $4 WHERE id = $1`,
			c.ID, pc.ProviderID, pc.BRCode, pc.ExpiresAt); err != nil {
			return err
		}
		id := pc.ProviderID
		c.ProviderID, c.ExpiresAt = &id, pc.ExpiresAt
		if pc.BRCode != "" {
			br := pc.BRCode
			c.BRCode = &br
		}
	} else if c.ProviderID != nil && pc.ProviderID != "" && *c.ProviderID != pc.ProviderID {
		return fmt.Errorf("%w: id do provedor mudou", ErrIntegrity)
	}
	var to, action string
	switch pc.Status {
	case ProviderPending:
		if c.Status == ChargePending {
			return nil
		}
		to, action = ChargePending, "T-PX-02"
	case ProviderPaid:
		return s.bookingPaid(ctx, tx, b, c, pc, evidence, now)
	case ProviderExpired:
		to, action = ChargeExpired, "T-PX-06"
	case ProviderCanceled:
		to, action = ChargeCancelled, "T-PX-07"
	case ProviderFailed:
		to, action = ChargeFailed, "T-PX-08"
	default:
		s.Log.Warn("estado do provedor sem tratamento no Pix da reserva", "tentativa", c.ID.String(), "estado", pc.Status)
		return nil
	}
	if err := s.markStatus(ctx, tx, c, to, action, now); err != nil {
		return err
	}
	return s.Bookings.PaymentChanged(ctx, tx, b.EstablishmentID, b.AppointmentID, now)
}

// bookingPaid registra o Pix pago da reserva (T-BK-03). Dentro da retenção válida (relógio do
// servidor) → recebimento principal e a reserva confirma. Depois dela, com a reserva encerrada ou
// duplicado → excesso: recebimento registrado (nunca invisível), pendência e devolução integral
// pedida ao provedor. Nunca desloca outra reserva para acomodar o pagamento tardio.
func (s *Service) bookingPaid(ctx context.Context, tx pgx.Tx, b *bookingRow, c *chargeRow, pc Charge, evidence string, now time.Time) error {
	amount := pc.AmountCents
	if amount <= 0 {
		amount = c.amount
	}
	var principal int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM receipt WHERE booking_payment_id = $1 AND classification = 'principal'`, b.ID).
		Scan(&principal); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM receipt WHERE pix_charge_id = $1)`, c.ID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if c.Status != ChargePaid {
			if err := s.markStatus(ctx, tx, c, ChargePaid, "T-PX-05", now); err != nil {
				return err
			}
			return s.Bookings.PaymentChanged(ctx, tx, b.EstablishmentID, b.AppointmentID, now)
		}
		return nil // já registrado (aviso repetido, worker e webhook juntos)
	}
	accepted := false
	if b.Status == BookingAwaiting && principal == 0 && amount == b.Amount && amount == c.amount {
		var err error
		if accepted, err = s.Bookings.PaymentConfirmed(ctx, tx, b.EstablishmentID, b.AppointmentID, now); err != nil {
			return err
		}
	}
	class := "excess"
	if accepted {
		class = "principal"
	}
	var receiptID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO receipt (establishment_id, booking_payment_id, method, amount_cents, classification, pix_charge_id, evidence,
		                     registered_by, registered_at)
		VALUES ($1, $2, 'pix', $3, $4, $5, $6, 'provider', $7) RETURNING id`,
		b.EstablishmentID, b.ID, amount, class, c.ID, evidence, now).Scan(&receiptID); err != nil {
		return err
	}
	if err := recordReceiptFee(ctx, tx, b.EstablishmentID, receiptID, "pix", nil, amount, now); err != nil {
		return err
	}
	if c.Status != ChargePaid {
		if err := s.markStatus(ctx, tx, c, ChargePaid, "T-PX-05", now); err != nil {
			return err
		}
	}
	if accepted {
		if _, err := tx.Exec(ctx, `UPDATE booking_payment SET status = 'paid', paid_at = $2, version = version + 1, updated_at = $2 WHERE id = $1`,
			b.ID, now); err != nil {
			return err
		}
		b.Status = BookingPaid
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &b.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
			Action: "T-BK-03", TargetType: "booking_payment", TargetID: b.ID, FromState: BookingAwaiting, ToState: BookingPaid}); err != nil {
			return err
		}
	} else {
		// Tardio (retenção vencida ou reserva encerrada) ou duplicado: devolução integral.
		if b.Status == BookingAwaiting {
			if _, err := tx.Exec(ctx, `UPDATE booking_payment SET status = 'expired', version = version + 1, updated_at = $2 WHERE id = $1`,
				b.ID, now); err != nil {
				return err
			}
			b.Status = BookingExpired
		}
		amt := amount
		if _, err := s.openBookingIssue(ctx, tx, b.EstablishmentID, b.ID, IssueBookingLate, "receipt", receiptID, &amt, now); err != nil {
			return err
		}
		if err := s.requestBookingRefund(ctx, tx, *b, receiptID, amount, "Pix pago depois do prazo da reserva: devolução integral", now); err != nil {
			return err
		}
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &b.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-RC-02", TargetType: "pix_charge", TargetID: c.ID, ToState: "registered:" + class}); err != nil {
		return err
	}
	if err := s.pendenciesChanged(ctx, tx, b.EstablishmentID, now); err != nil {
		return err
	}
	return s.Bookings.PaymentChanged(ctx, tx, b.EstablishmentID, b.AppointmentID, now)
}

// requestBookingRefund pede a devolução INTEGRAL de um recebimento da reserva (sistema, pela
// política): pelo provedor quando ele estorna Pix pela API; senão, revisão do dono com pendência.
func (s *Service) requestBookingRefund(ctx context.Context, tx pgx.Tx, b bookingRow, receiptID uuid.UUID, amount int64, reason string, now time.Time) error {
	var existing int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM refund WHERE receipt_id = $1 AND status <> 'failed'`, receiptID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil // já pedida (repetição): nunca duas devoluções
	}
	status := RefundUnderReview
	var next *time.Time
	if s.Provider != nil && s.Provider.Capabilities().RefundPix {
		status, next = RefundRequested, &now
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO refund (establishment_id, booking_payment_id, receipt_id, method, amount_cents, status, reason, requested_at,
		                    next_check_at, updated_at)
		VALUES ($1, $2, $3, 'pix', $4, $5, $6, $7, $8, $7) RETURNING id`,
		b.EstablishmentID, b.ID, receiptID, amount, status, reason, now, next).Scan(&id); err != nil {
		return err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &b.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-RF-01", TargetType: "refund", TargetID: id, ToState: status, Reason: reason}); err != nil {
		return err
	}
	if status == RefundUnderReview {
		amt := amount
		if _, err := s.openBookingIssue(ctx, tx, b.EstablishmentID, b.ID, IssueRefundReview, "refund", id, &amt, now); err != nil {
			return err
		}
	}
	return nil
}

// openBookingIssue abre a pendência de uma obrigação de reserva (sem comanda).
func (s *Service) openBookingIssue(ctx context.Context, tx pgx.Tx, estID, bookingID uuid.UUID, kind, sourceType string, sourceID uuid.UUID,
	amount *int64, now time.Time) (bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO financial_issue (establishment_id, booking_payment_id, kind, amount_cents, source_type, source_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (kind, source_id) DO NOTHING RETURNING id`,
		estID, bookingID, kind, amount, sourceType, sourceID, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.Log.Warn("pendência financeira aberta (reserva)", "tipo", kind, "pendencia", id.String())
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-FI-01", TargetType: "financial_issue", TargetID: id, ToState: "open:" + kind}); err != nil {
		return false, err
	}
	v, err := scanIssue(tx.QueryRow(ctx, issueSelect+` WHERE id = $1`, id))
	if err != nil {
		return false, err
	}
	return true, s.issueEvents(ctx, tx, v, now)
}

// ---------------------------------------------------------------------------------------------
// Fim da reserva antes da chamada (T-BK-04): expirou, cliente cancelou, loja cancelou.

// Motivos de fim da reserva antes da chamada.
const (
	EndExpired          = "expired"
	EndCancelledByCust  = "cancelled_by_customer"
	EndCancelledByStore = "cancelled_by_store"
)

// BookingEnded aplica a política registrada quando a reserva termina sem atendimento antes de ser
// chamada (na transação da agenda, com a reserva travada). Aguardando pagamento → a obrigação
// encerra (Pix que chegar depois vira tardio e é devolvido). Paga → devolução integral, salvo
// cancelamento do cliente depois da janela com a política "reter" aceita na reserva.
func (s *Service) BookingEnded(ctx context.Context, tx pgx.Tx, estID, appointmentID uuid.UUID, reason string, startsAt, now time.Time) error {
	b, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE establishment_id = $1 AND appointment_id = $2 FOR UPDATE`, estID, appointmentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // reserva gratuita
	}
	if err != nil {
		return err
	}
	switch b.Status {
	case BookingAwaiting:
		to := BookingCancelled
		if reason == EndExpired {
			to = BookingExpired
		}
		if _, err := tx.Exec(ctx, `UPDATE booking_payment SET status = $2, version = version + 1, updated_at = $3 WHERE id = $1`, b.ID, to, now); err != nil {
			return err
		}
		// Pix ainda aberto: marca o pedido de cancelamento (um pagamento depois disso é tardio).
		if _, err := tx.Exec(ctx, `UPDATE pix_charge SET cancel_requested_at = coalesce(cancel_requested_at, $2), updated_at = $2
			WHERE booking_payment_id = $1 AND status IN ('creating', 'pending', 'unknown')`, b.ID, now); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
			Action: "T-BK-04", TargetType: "booking_payment", TargetID: b.ID, FromState: BookingAwaiting, ToState: to, Reason: reason})
	case BookingPaid:
		if b.Outcome != nil {
			return nil
		}
		retain := reason == EndCancelledByCust && b.LatePolicy == "retain" && lateCancel(b, startsAt, now)
		return s.settleBookingOutcome(ctx, tx, b, retain, "reserva "+reasonText(reason), now)
	}
	return nil
}

// lateCancel: o cliente cancelou depois da janela grátis. No limite exato (faltando exatamente a
// janela), ainda vale como dentro da janela.
func lateCancel(b bookingRow, startsAt, now time.Time) bool {
	return now.After(startsAt.Add(-time.Duration(b.CancelWindowHours) * time.Hour))
}

func reasonText(r string) string {
	switch r {
	case EndCancelledByCust:
		return "cancelada pelo cliente"
	case EndCancelledByStore:
		return "cancelada pela barbearia"
	case "no_show":
		return "com falta do cliente"
	case "not_performed":
		return "sem atendimento"
	}
	return "encerrada"
}

// settleBookingOutcome fecha o destino do valor pago: retido (kept) ou devolução integral.
func (s *Service) settleBookingOutcome(ctx context.Context, tx pgx.Tx, b bookingRow, retain bool, why string, now time.Time) error {
	var receiptID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM receipt WHERE booking_payment_id = $1 AND classification = 'principal'`, b.ID).Scan(&receiptID); err != nil {
		return err
	}
	outcome := OutcomeRefundDue
	if retain {
		outcome = OutcomeKept
	}
	if _, err := tx.Exec(ctx, `UPDATE booking_payment SET outcome = $2, version = version + 1, updated_at = $3 WHERE id = $1`, b.ID, outcome, now); err != nil {
		return err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &b.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-BK-05", TargetType: "booking_payment", TargetID: b.ID, FromState: BookingPaid, ToState: outcome, Reason: why}); err != nil {
		return err
	}
	if retain {
		return s.pendenciesChanged(ctx, tx, b.EstablishmentID, now)
	}
	if err := s.requestBookingRefund(ctx, tx, b, receiptID, b.Amount, "Devolução do valor da reserva: "+why, now); err != nil {
		return err
	}
	return s.pendenciesChanged(ctx, tx, b.EstablishmentID, now)
}

// ---------------------------------------------------------------------------------------------
// Crédito na comanda (T-BK-06): gancho chamado pelo atendimento quando o total fecha.

// orderCredit aplica o sinal pago quando a comanda da visita da reserva fecha o total.
//   - Sinal + comanda a pagar: abate min(sinal, total) por UM registro de crédito; sobra (ex.:
//     interrompido com R$ 10 devidos e sinal de R$ 15) vira devolução PARCIAL em revisão do dono
//     (o provedor só estorna o valor integral) — nunca estorno integral automático.
//     Crédito igual ao total → a comanda já fica paga (sem recebimento de R$ 0).
//   - Taxa + comanda a pagar: a taxa fica com a barbearia (nunca vira crédito do serviço).
//   - Comanda encerrada sem cobrança: falta com política "reter" → retido; senão devolução integral.
func (s *Service) orderCredit(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, now time.Time) error {
	tx, ok := q.(pgx.Tx)
	if !ok {
		return errors.New("crédito da reserva fora de transação")
	}
	b, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE establishment_id = $1 AND order_id = $2 FOR UPDATE`, estID, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if b.Status != BookingPaid || b.Outcome != nil {
		return nil
	}
	lp, err := atendimento.LockForPayment(ctx, tx, estID, orderID)
	if err != nil {
		return err
	}
	if lp.Status == atendimento.StatusClosedNoCharge {
		why, retain := "not_performed", false
		if lp.ClosureReason != nil {
			switch *lp.ClosureReason {
			case atendimento.ClosedNoShow:
				why, retain = "no_show", b.LatePolicy == "retain"
			case atendimento.ClosedByCustomer:
				why, retain = EndCancelledByCust, b.LatePolicy == "retain"
			case atendimento.ClosedByOwner:
				why = EndCancelledByStore
			}
		}
		return s.settleBookingOutcome(ctx, tx, b, retain, "reserva "+reasonText(why), now)
	}
	if lp.Status != atendimento.StatusAwaitingPayment {
		return nil
	}
	if b.Mode == "fee" {
		if _, err := tx.Exec(ctx, `UPDATE booking_payment SET outcome = 'kept', version = version + 1, updated_at = $2 WHERE id = $1`, b.ID, now); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
			Action: "T-BK-05", TargetType: "booking_payment", TargetID: b.ID, FromState: BookingPaid, ToState: OutcomeKept, Reason: "taxa da reserva"})
	}
	var receiptID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM receipt WHERE booking_payment_id = $1 AND classification = 'principal'`, b.ID).Scan(&receiptID); err != nil {
		return err
	}
	alloc := min(b.Amount, lp.TotalCents)
	if alloc > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO booking_credit (establishment_id, booking_payment_id, order_id, receipt_id, amount_cents, applied_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, estID, b.ID, orderID, receiptID, alloc, now); err != nil {
			return err
		}
		if _, err := atendimento.SetCredit(ctx, tx, orderID, alloc, now); err != nil {
			return err
		}
	}
	outcome := OutcomeApplied
	if _, err := tx.Exec(ctx, `UPDATE booking_payment SET outcome = $2, version = version + 1, updated_at = $3 WHERE id = $1`, b.ID, outcome, now); err != nil {
		return err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-BK-06", TargetType: "booking_payment", TargetID: b.ID, FromState: BookingPaid, ToState: outcome,
		Reason: fmt.Sprintf("sinal de %s abatido na comanda (%s)", reais(alloc), reais(lp.TotalCents))}); err != nil {
		return err
	}
	if rest := b.Amount - alloc; rest > 0 {
		// Sobra do sinal: devolução parcial com vínculo à origem, feita e confirmada pelo dono.
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO refund (establishment_id, booking_payment_id, receipt_id, method, amount_cents, status, reason, requested_at,
			                    updated_at, partial)
			VALUES ($1, $2, $3, 'pix', $4, 'under_review', $5, $6, $6, true) RETURNING id`,
			estID, b.ID, receiptID, rest, fmt.Sprintf("Sobra do sinal: devidos %s, sinal de %s", reais(lp.TotalCents), reais(b.Amount)), now).Scan(&id); err != nil {
			return err
		}
		amt := rest
		if _, err := s.openBookingIssue(ctx, tx, estID, b.ID, IssueBookingRefundDue, "refund", id, &amt, now); err != nil {
			return err
		}
	}
	if alloc == lp.TotalCents {
		// Sinal cobre tudo: a comanda já está paga (sem recebimento de R$ 0 e sem conferência
		// pendente: o crédito veio de um recebimento confirmado pelo provedor).
		cur, err := atendimento.LockForPayment(ctx, tx, estID, orderID)
		if err != nil {
			return err
		}
		if err := s.settle(ctx, tx, cur, now, auditoria.Event{ActorKind: auditoria.ActorSystem}); err != nil {
			return err
		}
	}
	// Sem PaymentChanged aqui: a reserva não é travada depois da comanda (ordem de travas). A
	// comanda já publica a mudança; a visão da reserva lê o crédito ao ser consultada.
	return s.pendenciesChanged(ctx, tx, estID, now)
}

// ---------------------------------------------------------------------------------------------
// Webhook e estorno da reserva

// webhookBooking é o applyWebhook para cobrança de reserva (metadados booking_id e loja).
func (s *Service) webhookBooking(ctx context.Context, tx pgx.Tx, env webhookEnvelope, c chargeRow, now time.Time) (webhookOutcome, *string, *string, *int64) {
	t := env.Data.Transparent
	chargeID, status, amount := &t.ID, &t.Status, &t.Amount
	if s.Bookings == nil || t.Metadata["charge_id"] != c.ID.String() || t.Metadata["booking_id"] != c.BookingID.String() ||
		t.Metadata["establishment_id"] != c.EstablishmentID.String() {
		s.Log.Warn("webhook não confere com o Pix da reserva", "tentativa", c.ID.String())
		return webhookOutcome{name: "mismatch"}, chargeID, status, amount
	}
	b, cur, err := s.lockBookingFor(ctx, tx, c)
	if err != nil {
		return webhookOutcome{err: err}, nil, nil, nil
	}
	pc := Charge{ProviderID: t.ID, Status: t.Status, AmountCents: t.Amount, Metadata: t.Metadata, DevMode: s.Provider.TestMode()}
	before := cur.Status
	var receiptsBefore int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM receipt WHERE pix_charge_id = $1`, cur.ID).Scan(&receiptsBefore); err != nil {
		return webhookOutcome{err: err}, nil, nil, nil
	}
	switch {
	case env.Event == "transparent.completed" && t.Status == ProviderPaid:
		if cur.Status == ChargePaid || cur.Status == ChargeExpired || cur.Status == ChargeCancelled || cur.Status == ChargeFailed {
			err = s.bookingPaid(ctx, tx, &b, &cur, pc, "provider_webhook", now)
		} else {
			err = s.bookingApply(ctx, tx, &b, &cur, pc, "provider_webhook", now)
		}
	case env.Event == "transparent.refunded" && t.Status == ProviderRefunded:
		if receiptsBefore == 0 {
			pc.Status = ProviderPaid
			if err = s.bookingPaid(ctx, tx, &b, &cur, pc, "provider_webhook", now); err != nil {
				return webhookOutcome{err: err}, nil, nil, nil
			}
		}
		var changed bool
		changed, err = s.bookingRefundConfirmedByProvider(ctx, tx, b, cur.ID, now)
		if err == nil && !changed && receiptsBefore > 0 {
			return webhookOutcome{name: "duplicate_effect"}, chargeID, status, amount
		}
		if err == nil {
			return webhookOutcome{name: "applied"}, chargeID, status, amount
		}
	default:
		s.Log.Warn("aviso do provedor sem tratamento automático (reserva)", "evento", env.Event, "tentativa", cur.ID.String())
		return webhookOutcome{name: "ignored"}, chargeID, status, amount
	}
	if errors.Is(err, ErrIntegrity) {
		return webhookOutcome{name: "mismatch"}, chargeID, status, amount
	}
	if err != nil {
		return webhookOutcome{err: err}, nil, nil, nil
	}
	var receiptsAfter int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM receipt WHERE pix_charge_id = $1`, cur.ID).Scan(&receiptsAfter); err != nil {
		return webhookOutcome{err: err}, nil, nil, nil
	}
	if cur.Status == before && receiptsAfter == receiptsBefore {
		return webhookOutcome{name: "duplicate_effect"}, chargeID, status, amount
	}
	return webhookOutcome{name: "applied"}, chargeID, status, amount
}

// bookingRefundConfirmedByProvider confirma a devolução quando o provedor mostra REFUNDED.
func (s *Service) bookingRefundConfirmedByProvider(ctx context.Context, tx pgx.Tx, b bookingRow, chargeID uuid.UUID, now time.Time) (bool, error) {
	var receiptID uuid.UUID
	var amount int64
	err := tx.QueryRow(ctx, `SELECT id, amount_cents FROM receipt WHERE pix_charge_id = $1`, chargeID).Scan(&receiptID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rf, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE receipt_id = $1 AND status <> 'failed' AND NOT partial FOR UPDATE`, receiptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return s.openBookingIssue(ctx, tx, b.EstablishmentID, b.ID, IssueRefundReview, "receipt", receiptID, &amount, now)
	}
	if err != nil {
		return false, err
	}
	if rf.Status == RefundConfirmed {
		return false, nil
	}
	return true, s.bookingRefundTo(ctx, tx, b, &rf, RefundConfirmed, "T-RF-03", now, "")
}

// finishBookingRefund aplica uma transição do worker a uma devolução de reserva.
func (s *Service) finishBookingRefund(ctx context.Context, rf refundRow, to, action, providerRefundID string) error {
	if s.Bookings == nil {
		return errors.New("devolução de reserva sem o módulo agenda ligado")
	}
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		b, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE id = $1`, *rf.BookingID))
		if err != nil {
			return err
		}
		if _, err := s.Bookings.LockAppointment(ctx, tx, b.EstablishmentID, b.AppointmentID); err != nil {
			return err
		}
		if b, err = scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE id = $1 FOR UPDATE`, b.ID)); err != nil {
			return err
		}
		cur, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1 FOR UPDATE`, rf.ID))
		if err != nil {
			return err
		}
		if cur.Status == RefundConfirmed || cur.Status == RefundFailed || cur.Status == RefundUnderReview || cur.Status == to {
			if cur.Status == to && (to == RefundProcessing || to == RefundUnknown) {
				return s.refundSchedule(ctx, tx, cur, s.Clock.Now())
			}
			return nil
		}
		return s.bookingRefundTo(ctx, tx, b, &cur, to, action, s.Clock.Now(), providerRefundID)
	})
}

// bookingRefundTo é o refundTo da reserva (sem comanda e sem pontos: a reserva nunca gera pontos).
func (s *Service) bookingRefundTo(ctx context.Context, tx pgx.Tx, b bookingRow, rf *refundRow, to, action string, now time.Time, providerRefundID string) error {
	var resolved *time.Time
	var evidence *string
	if to == RefundConfirmed || to == RefundFailed {
		resolved = &now
	}
	if to == RefundConfirmed {
		e := "provider"
		evidence = &e
	}
	var next *time.Time
	if to == RefundProcessing || to == RefundUnknown || to == RefundRequested {
		n := now.Add(5 * time.Second)
		next = &n
	}
	if _, err := tx.Exec(ctx, `UPDATE refund SET status = $2, resolved_at = $3, evidence = coalesce($4, evidence),
		provider_refund_id = coalesce(nullif($5, ''), provider_refund_id), next_check_at = $6, updated_at = $7 WHERE id = $1`,
		rf.ID, to, resolved, evidence, providerRefundID, next, now); err != nil {
		return err
	}
	from := rf.Status
	rf.Status = to
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &rf.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: action, TargetType: "refund", TargetID: rf.ID, FromState: from, ToState: to}); err != nil {
		return err
	}
	if to == RefundUnderReview {
		amt := rf.amount
		if _, err := s.openBookingIssue(ctx, tx, rf.EstablishmentID, b.ID, IssueRefundReview, "refund", rf.ID, &amt, now); err != nil {
			return err
		}
	}
	if err := s.pendenciesChanged(ctx, tx, rf.EstablishmentID, now); err != nil {
		return err
	}
	return s.Bookings.PaymentChanged(ctx, tx, b.EstablishmentID, b.AppointmentID, now)
}

// restoreLostBookingCharge: cobrança de reserva que o banco restaurado não tem. A retenção não é
// recriada; abre a pendência órfã para o dono devolver pelo painel do provedor.
func (s *Service) restoreLostBookingCharge(ctx context.Context, chargeID, estID uuid.UUID, pc Charge, rep *RestoreReport) error {
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		amount := pc.AmountCents
		opened, err := s.openIssueRef(ctx, tx, estID, nil, IssueRestoredOrphan, "pix_charge", chargeID, &amount, &pc.ProviderID, s.Clock.Now())
		if opened {
			rep.Orphans++
		}
		return err
	})
}

// ---------------------------------------------------------------------------------------------
// Devolução da reserva em revisão: o dono confirma (com evidência) ou dá como falha.

// bookingManualRefund (T-RF-06/07 da reserva): devolução parcial da sobra do sinal ou integral sem
// estorno pelo provedor. Reautenticação, nota de evidência; confirma também a pendência ligada.
func (s *Service) bookingManualRefund(to string) hs.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		estID, err := pathID(r, "establishmentId")
		if err != nil {
			return err
		}
		refundID, err := pathID(r, "refundId")
		if err != nil {
			return err
		}
		actor, err := s.Auth.Owner(r, estID)
		if err != nil {
			return err
		}
		var body struct {
			Note *string `json:"evidence_note"`
			Ref  *string `json:"external_reference"`
		}
		raw, err := hs.DecodeJSON(r, &body)
		if err != nil {
			return err
		}
		var v validate.Collector
		note := reasonField(&v, "/evidence_note", body.Note, true)
		var ref *string
		if body.Ref != nil && *body.Ref != "" {
			text, fe := validate.Text("/external_reference", *body.Ref, 1, 80)
			v.Add(fe)
			ref = &text
		}
		if err := v.Err(); err != nil {
			return err
		}
		if err := s.Auth.RequireReauth(r); err != nil {
			return err
		}
		if s.Bookings == nil {
			return hs.ErrNotFound()
		}
		op := "confirmBookingRefund"
		action := "T-RF-06"
		if to == RefundFailed {
			op, action = "markBookingRefundFailed", "T-RF-07"
		}
		ctx := r.Context()
		key := "conta:" + actor.AccountID.String()
		return s.idem.Run(w, r, idempotency.Command{ActorKey: key, Operation: op, Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
			pre, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE establishment_id = $1 AND id = $2 AND booking_payment_id IS NOT NULL`,
				estID, refundID))
			if errors.Is(err, pgx.ErrNoRows) {
				return idempotency.Result{}, hs.ErrNotFound()
			}
			if err != nil {
				return idempotency.Result{}, err
			}
			b, err := scanBooking(tx.QueryRow(ctx, bookingSelect+` WHERE id = $1`, *pre.BookingID))
			if err != nil {
				return idempotency.Result{}, err
			}
			if _, err := s.Bookings.LockAppointment(ctx, tx, estID, b.AppointmentID); err != nil {
				return idempotency.Result{}, err
			}
			if _, err := tx.Exec(ctx, `SELECT 1 FROM booking_payment WHERE id = $1 FOR UPDATE`, b.ID); err != nil {
				return idempotency.Result{}, err
			}
			cur, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1 FOR UPDATE`, refundID))
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Status != RefundUnderReview {
				return idempotency.Result{}, conflict("invalid_transition", "Devolução fora de revisão",
					"Só uma devolução em revisão pode ser confirmada ou dada como falha manualmente.", nil)
			}
			now := s.Clock.Now()
			var evPtr *string
			if to == RefundConfirmed {
				e := "owner_documented"
				evPtr = &e
			}
			if _, err := tx.Exec(ctx, `UPDATE refund SET status = $2, evidence = $3, evidence_note = $4, external_reference = $5,
				resolved_at = $6, resolved_by_account_id = $7, next_check_at = NULL, updated_at = $6 WHERE id = $1`,
				refundID, to, evPtr, note, ref, now, actor.AccountID); err != nil {
				return idempotency.Result{}, err
			}
			aid := actor.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
				ActorID: &aid, Action: action, TargetType: "refund", TargetID: refundID, FromState: cur.Status, ToState: to, Reason: note,
				RequestID: hs.RequestIDFrom(ctx)}); err != nil {
				return idempotency.Result{}, err
			}
			// A pendência da devolução sai do painel junto (a nota do dono é a resolução).
			rows, err := tx.Query(ctx, `UPDATE financial_issue SET status = 'resolved', resolved_at = $2, resolved_by_account_id = $3,
				resolution_note = $4 WHERE establishment_id = $1 AND source_id = $5 AND status = 'open' RETURNING id`,
				estID, now, actor.AccountID, note, refundID)
			if err != nil {
				return idempotency.Result{}, err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return idempotency.Result{}, err
			}
			for _, id := range ids {
				if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
					ActorID: &aid, Action: "T-FI-02", TargetType: "financial_issue", TargetID: id, FromState: "open", ToState: "resolved",
					Reason: note}); err != nil {
					return idempotency.Result{}, err
				}
				iv, err := scanIssue(tx.QueryRow(ctx, issueSelect+` WHERE id = $1`, id))
				if err != nil {
					return idempotency.Result{}, err
				}
				if err := s.issueEvents(ctx, tx, iv, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			if err := s.pendenciesChanged(ctx, tx, estID, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.Bookings.PaymentChanged(ctx, tx, estID, b.AppointmentID, now); err != nil {
				return idempotency.Result{}, err
			}
			views, err := BookingViews(ctx, tx, []uuid.UUID{b.AppointmentID})
			if err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[BookingView]{Data: views[b.AppointmentID]}}, nil
		})
	}
}
