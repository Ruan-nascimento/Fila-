package pagamento

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

var errProviderUnavailable = &hs.Error{Status: 503, Code: "provider_unavailable", Title: "Pix indisponível no momento",
	Detail: "Não foi possível gerar o Pix agora. Tente de novo em instantes ou pague em dinheiro ou cartão com a equipe."}

func chargeResult(status int, c chargeView) idempotency.Result {
	return idempotency.Result{Status: status, Body: hs.Data[chargeView]{Data: c}}
}

func writeCharge(w http.ResponseWriter, status int, c chargeView) {
	hs.WriteJSON(w, status, hs.Data[chargeView]{Data: c})
}

// createPixCharge (T-PX-01/02/03/08): cobrança Pix do valor DEVIDO calculado pelo servidor. O
// corpo só tem expected_version (valor no corpo é recusado: campo desconhecido).
//
// Fase 1 (transação): trava a comanda, confere, grava a tentativa "creating" e confirma — a
// tentativa existe ANTES da chamada ao provedor. Fase 2: chama o provedor fora da transação
// (externalId = id da tentativa). Fase 3: grava o resultado. Tempo esgotado/resposta ambígua →
// "unknown": nenhuma outra tentativa até a reconciliação descobrir o que aconteceu.
//
// Idempotência: a chave do pedido fica gravada na tentativa (mesma comanda + mesma chave →
// mesma tentativa, sem nova chamada ao provedor). A garantia remota é o externalId.
func (s *Service) createPixCharge(w http.ResponseWriter, r *http.Request) error {
	p, who, err := s.order(r, true, true, true)
	if err != nil {
		return err
	}
	var body struct {
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if _, err := hs.DecodeJSON(r, &body); err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", body.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	key := r.Header.Get(idempotency.Header)
	if err := idempotency.CheckKey(key); err != nil {
		return err
	}
	ctx := r.Context()
	keyHash := sha256.Sum256([]byte(who.Key + "|" + key))
	now := s.Clock.Now()
	var ch chargeRow
	replay := false
	err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		lp, err := atendimento.LockForPayment(ctx, tx, p.EstablishmentID, p.ID)
		if err != nil {
			return err
		}
		if c, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE order_id = $1 AND idem_key_hash = $2`, p.ID, keyHash[:])); err == nil {
			ch, replay = c, true // mesma intenção repetida (clique duplo, rede caiu): mesma tentativa
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Cobrança ativa primeiro: a outra aba recebe a cobrança atual, não "versão mudou".
		if cur, err := activeCharge(ctx, tx, p.ID, false); err != nil {
			return err
		} else if cur != nil {
			if cur.Status == ChargeUnknown {
				return conflict("pix_charge_outcome_unknown", "Verificando pagamento",
					"Ainda estamos confirmando a cobrança anterior com o banco. Aguarde antes de gerar outro Pix.", cur.chargeView)
			}
			return conflict("pix_charge_active", "Já existe um Pix em aberto", "Use o Pix já gerado para esta comanda.", cur.chargeView)
		}
		if err := payableCheck(ctx, tx, lp, expected); err != nil {
			return err
		}
		if s.Provider == nil {
			return errProviderUnavailable
		}
		due := lp.DueCents()
		if min := s.Provider.Capabilities().MinPixCents; due < min {
			return orderConflict(ctx, tx, lp, "payment_method_not_allowed", "Pix indisponível para este valor",
				fmt.Sprintf("O Pix exige no mínimo %s. Pague em dinheiro ou cartão com a equipe.", reais(min)))
		}
		ch, err = scanCharge(tx.QueryRow(ctx, `
			INSERT INTO pix_charge (establishment_id, order_id, amount_cents, provider, test_mode, idem_key_hash, created_by,
			                        created_at, updated_at, next_check_at)
			VALUES ($1, $2, $3, 'abacatepay', $4, $5, $6, $7, $7, $8)
			RETURNING id, order_id, status, amount_cents, br_code, expires_at, created_at, resolved_at, cancel_requested_at,
			          establishment_id, provider_charge_id, test_mode, check_attempts`,
			p.EstablishmentID, p.ID, due, s.Provider.TestMode(), keyHash[:], who.CreatedBy(lp), now, now.Add(2*s.Timeout)))
		if database.IsUniqueViolation(err, "pix_charge_one_active_uq") {
			return conflict("pix_charge_active", "Já existe um Pix em aberto", "Use o Pix já gerado para esta comanda.", nil)
		}
		if err != nil {
			return err
		}
		methodChanged := lp.Method == nil || *lp.Method != "pix"
		if methodChanged {
			if _, err := atendimento.SetPaymentMethod(ctx, tx, p.ID, "pix", now); err != nil {
				return err
			}
		}
		a := who.Audit(ctx, lp)
		a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID, a.ToState = &p.EstablishmentID, now, "T-PX-01", "pix_charge", ch.ID, ChargeCreating
		if err := auditoria.Record(ctx, tx, a); err != nil {
			return err
		}
		// A visão da comanda mudou (nova cobrança): versão nova, mesmo sem troca de meio.
		return atendimento.RecordPaymentEvents(ctx, tx, p.EstablishmentID, p.ID, now, !methodChanged)
	})
	if err != nil {
		return err
	}
	if replay {
		if ch.Status == ChargeFailed {
			return errProviderUnavailable // mesma resposta da primeira vez; nova tentativa = nova chave
		}
		w.Header().Set("Idempotent-Replayed", "true")
		return respondCharge(w, ch.chargeView)
	}
	// Fase 2: o pedido ao provedor não depende do navegador continuar conectado.
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Timeout+2*time.Second)
	defer cancel()
	pc, callErr := s.Provider.CreatePix(callCtx, CreatePix{ExternalID: ch.ID.String(), AmountCents: ch.amount,
		ExpiresIn: s.ExpiresIn, Description: "Atendimento na barbearia",
		Metadata: map[string]string{"charge_id": ch.ID.String(), "order_id": p.ID.String(), "establishment_id": p.EstablishmentID.String()}})
	// Fase 3.
	final, err := s.recordCreation(context.WithoutCancel(ctx), ch, pc, callErr)
	if err != nil {
		return err
	}
	if final.Status == ChargeFailed {
		return errProviderUnavailable // a comanda continua aguardando pagamento; nada se perdeu
	}
	return respondCharge(w, final.chargeView)
}

func respondCharge(w http.ResponseWriter, c chargeView) error {
	status := http.StatusCreated
	if c.Status == ChargeUnknown || c.Status == ChargeCreating {
		status = http.StatusAccepted // "verificando pagamento"
	}
	writeCharge(w, status, c)
	return nil
}

// recordCreation grava o resultado da chamada de criação (só se a tentativa ainda estiver
// "creating": a reconciliação pode ter chegado antes).
func (s *Service) recordCreation(ctx context.Context, ch chargeRow, pc Charge, callErr error) (chargeRow, error) {
	var out chargeRow
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		lp, err := atendimento.LockForPayment(ctx, tx, ch.EstablishmentID, ch.OrderID)
		if err != nil {
			return err
		}
		cur, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE id = $1 FOR UPDATE`, ch.ID))
		if err != nil {
			return err
		}
		out = cur
		if cur.Status != ChargeCreating {
			return nil
		}
		now := s.Clock.Now()
		switch {
		case callErr == nil:
			return s.apply(ctx, tx, lp, &out, pc, "provider_query", now)
		case errors.Is(callErr, ErrNotSent), errors.Is(callErr, ErrRejected):
			s.Log.Warn("cobrança Pix não criada pelo provedor", "tentativa", ch.ID.String(), "motivo", callErr.Error())
			return s.setStatus(ctx, tx, lp, &out, ChargeFailed, "T-PX-08", now)
		default: // ErrAmbiguous, ErrIntegrity ou qualquer outro: não sabemos se existe
			s.Log.Warn("cobrança Pix com resultado desconhecido; será reconciliada", "tentativa", ch.ID.String(), "motivo", callErr.Error())
			if _, err := tx.Exec(ctx, `UPDATE pix_charge SET next_check_at = $2 WHERE id = $1`, ch.ID, now.Add(5*time.Second)); err != nil {
				return err
			}
			return s.setStatus(ctx, tx, lp, &out, ChargeUnknown, "T-PX-03", now)
		}
	})
	return out, err
}

// setStatus muda o estado da cobrança, audita (ator sistema) e grava os eventos da comanda.
func (s *Service) setStatus(ctx context.Context, tx pgx.Tx, lp atendimento.PaymentInfo, c *chargeRow, to, action string, now time.Time) error {
	if err := s.markStatus(ctx, tx, c, to, action, now); err != nil {
		return err
	}
	return atendimento.RecordPaymentEvents(ctx, tx, lp.EstablishmentID, lp.ID, now, true)
}

// markStatus muda o estado e audita, sem eventos (quem chama grava os eventos uma vez).
func (s *Service) markStatus(ctx context.Context, tx pgx.Tx, c *chargeRow, to, action string, now time.Time) error {
	var resolved *time.Time
	if to == ChargePaid || to == ChargeExpired || to == ChargeCancelled || to == ChargeFailed {
		resolved = &now
	}
	if _, err := tx.Exec(ctx, `UPDATE pix_charge SET status = $2, resolved_at = $3, updated_at = $4 WHERE id = $1`, c.ID, to, resolved, now); err != nil {
		return err
	}
	from := c.Status
	c.Status, c.ResolvedAt = to, resolved
	return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &c.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: action, TargetType: "pix_charge", TargetID: c.ID, FromState: from, ToState: to})
}

// apply aplica o estado informado pelo provedor (consulta autenticada) a uma cobrança travada.
// Só "PAID" do provedor cria recebimento — nunca o navegador, comprovante ou clique (R-PAG-03).
func (s *Service) apply(ctx context.Context, tx pgx.Tx, lp atendimento.PaymentInfo, c *chargeRow, pc Charge, evidence string, now time.Time) error {
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
	switch pc.Status {
	case ProviderPending:
		if c.Status == ChargePending {
			return nil
		}
		return s.setStatus(ctx, tx, lp, c, ChargePending, "T-PX-02", now)
	case ProviderPaid:
		return s.paid(ctx, tx, lp, c, pc, evidence, now)
	case ProviderExpired:
		return s.setStatus(ctx, tx, lp, c, ChargeExpired, "T-PX-06", now)
	case ProviderCanceled:
		return s.setStatus(ctx, tx, lp, c, ChargeCancelled, "T-PX-07", now)
	case ProviderFailed:
		return s.setStatus(ctx, tx, lp, c, ChargeFailed, "T-PX-08", now)
	default:
		// REFUNDED / UNDER_DISPUTE etc.: estorno e disputa são da etapa 09; não se inventa estado.
		s.Log.Warn("estado do provedor ainda não tratado (etapa 09)", "tentativa", c.ID.String(), "estado", pc.Status)
		return nil
	}
}

// paid registra o recebimento Pix (T-PX-05 + T-RC-02, ou T-RC-04 como excesso). Um recebimento
// por cobrança (índice único): aviso repetido não duplica. Se o Pix cobre exatamente o devido de
// uma comanda ainda aguardando pagamento → principal e a comanda passa a aguardar conferência;
// senão (tardio, duplicado, comanda já paga por outro meio, valor divergente) → excesso: fica
// registrado, nada é apagado, e a comanda não muda (tratamento do excesso: etapa 09).
func (s *Service) paid(ctx context.Context, tx pgx.Tx, lp atendimento.PaymentInfo, c *chargeRow, pc Charge, evidence string, now time.Time) error {
	amount := pc.AmountCents
	if amount <= 0 {
		amount = c.amount
	}
	var principal int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(sum(amount_cents), 0) FROM receipt WHERE order_id = $1 AND classification = 'principal'`,
		lp.ID).Scan(&principal); err != nil {
		return err
	}
	class := "excess"
	if lp.Status == atendimento.StatusAwaitingPayment && principal == 0 && amount == lp.DueCents() && amount == c.amount {
		class = "principal"
	}
	var receiptID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO receipt (establishment_id, order_id, method, amount_cents, classification, pix_charge_id, evidence,
		                     registered_by, registered_at)
		VALUES ($1, $2, 'pix', $3, $4, $5, $6, 'provider', $7) ON CONFLICT (pix_charge_id) DO NOTHING RETURNING id`,
		lp.EstablishmentID, lp.ID, amount, class, c.ID, evidence, now).Scan(&receiptID)
	inserted := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// Tardio: pago depois de expirar/cancelar/falhar, ou depois do pedido de cancelamento (troca de meio).
	late := c.CancelRequestedAt != nil || c.Status == ChargeExpired || c.Status == ChargeCancelled || c.Status == ChargeFailed
	changed := c.Status != ChargePaid
	if changed {
		if err := s.markStatus(ctx, tx, c, ChargePaid, "T-PX-05", now); err != nil {
			return err
		}
	}
	if !inserted {
		if changed {
			return atendimento.RecordPaymentEvents(ctx, tx, lp.EstablishmentID, lp.ID, now, true)
		}
		return nil // já registrado
	}
	action := "T-RC-02"
	if class == "excess" {
		// Nada é apagado: o recebimento fica registrado e abre pendência para o dono tratar
		// (devolução do excesso por estorno ou nota de devolução presencial) — T-RC-04, T-FI-01.
		action = "T-RC-04"
		kind := IssueDuplicate
		switch {
		case amount != c.amount || (lp.Status == atendimento.StatusAwaitingPayment && principal == 0 && amount != lp.DueCents()):
			kind = IssueAmountMismatch
		case late:
			kind = IssueLate
		}
		amt := amount
		if _, err := s.openIssue(ctx, tx, lp.EstablishmentID, lp.ID, kind, "receipt", receiptID, &amt, now); err != nil {
			return err
		}
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &lp.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: action, TargetType: "pix_charge", TargetID: c.ID, ToState: "registered:" + class}); err != nil {
		return err
	}
	if class == "principal" {
		if err := atendimento.MarkAwaitingConfirmation(ctx, tx, lp, now, auditoria.Event{ActorKind: auditoria.ActorSystem}); err != nil {
			return err
		}
	}
	return atendimento.RecordPaymentEvents(ctx, tx, lp.EstablishmentID, lp.ID, now, class == "excess")
}

// getPixCharge: situação conforme o banco (não consulta o provedor na hora).
func (s *Service) getPixCharge(w http.ResponseWriter, r *http.Request) error {
	p, _, err := s.order(r, true, true, true)
	if err != nil {
		return err
	}
	chargeID, err := pathID(r, "chargeId")
	if err != nil {
		return err
	}
	c, err := scanCharge(s.Pool.QueryRow(r.Context(), chargeSelect+` WHERE order_id = $1 AND id = $2`, p.ID, chargeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return hs.ErrNotFound()
	}
	if err != nil {
		return err
	}
	writeCharge(w, http.StatusOK, c.chargeView)
	return nil
}

// cancelPixCharge (T-PX-07): a AbacatePay NÃO oferece cancelamento de Pix (só expiração). O
// pedido fica registrado (cancel_requested_at) e a cobrança segue "pending" até expirar; se for
// paga mesmo assim, o recebimento vira excesso (R-PAG-11).
func (s *Service) cancelPixCharge(w http.ResponseWriter, r *http.Request) error {
	p, who, err := s.order(r, false, true, true)
	if err != nil {
		return err
	}
	chargeID, err := pathID(r, "chargeId")
	if err != nil {
		return err
	}
	var body struct {
		Reason *string `json:"reason"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/reason", body.Reason == nil)
	var reason string
	if body.Reason != nil {
		var fe *hs.FieldError
		reason, fe = validate.Text("/reason", *body.Reason, 3, 500)
		v.Add(fe)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: who.Key, Operation: "cancelPixCharge", Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
		lp, err := atendimento.LockForPayment(ctx, tx, p.EstablishmentID, p.ID)
		if err != nil {
			return idempotency.Result{}, err
		}
		c, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE order_id = $1 AND id = $2 FOR UPDATE`, p.ID, chargeID))
		if errors.Is(err, pgx.ErrNoRows) {
			return idempotency.Result{}, hs.ErrNotFound()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		if c.Status != ChargePending && c.Status != ChargeUnknown && c.Status != ChargeCreating {
			return idempotency.Result{}, conflict("invalid_transition", "Cobrança já resolvida", "Esta cobrança não está mais em aberto.", c.chargeView)
		}
		now := s.Clock.Now()
		if err := s.requestCancel(ctx, tx, lp, &c, who, reason, now); err != nil {
			return idempotency.Result{}, err
		}
		return chargeResult(http.StatusAccepted, c.chargeView), nil
	})
}

// requestCancel registra o pedido de cancelamento (o provedor não cancela: só expira).
func (s *Service) requestCancel(ctx context.Context, tx pgx.Tx, lp atendimento.PaymentInfo, c *chargeRow, who atendimento.Who, reason string, now time.Time) error {
	if c.CancelRequestedAt != nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE pix_charge SET cancel_requested_at = $2, updated_at = $2 WHERE id = $1`, c.ID, now); err != nil {
		return err
	}
	c.CancelRequestedAt = &now
	a := who.Audit(ctx, lp)
	a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &lp.EstablishmentID, now, "T-PX-07", "pix_charge", c.ID
	a.FromState, a.ToState, a.Reason = c.Status, c.Status+":cancel_requested", reason // provedor sem cancelamento de Pix
	if err := auditoria.Record(ctx, tx, a); err != nil {
		return err
	}
	return atendimento.RecordPaymentEvents(ctx, tx, lp.EstablishmentID, lp.ID, now, true)
}

func reais(cents int64) string { return fmt.Sprintf("R$ %d,%02d", cents/100, cents%100) }
