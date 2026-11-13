package pagamento

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/auditoria"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

func orderOK(ctx context.Context, tx pgx.Tx, p atendimento.PaymentInfo, status int) (idempotency.Result, error) {
	res, err := atendimento.OrderResult(ctx, tx, p.EstablishmentID, p.ID)
	return idempotency.Result{Status: status, Body: res}, err
}

// choosePaymentMethod (T-O-12): registra o meio escolhido. Trocar para dinheiro/cartão com Pix em
// aberto exige ciência explícita (acknowledge_active_pix); com ciência, o cancelamento é pedido
// (o provedor não cancela Pix: a cobrança segue válida até expirar e, se for paga, vira excesso).
// Com Pix de resultado desconhecido, a troca espera a verificação.
func (s *Service) choosePaymentMethod(w http.ResponseWriter, r *http.Request) error {
	p, who, err := s.order(r, true, true, true)
	if err != nil {
		return err
	}
	var body struct {
		ExpectedVersion *int64  `json:"expected_version"`
		Method          *string `json:"method"`
		Acknowledge     *bool   `json:"acknowledge_active_pix"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", body.ExpectedVersion)
	v.Required("/method", body.Method == nil)
	if body.Method != nil && *body.Method != "pix" && *body.Method != "cash" && *body.Method != "card" {
		v.Add(&hs.FieldError{Field: "/method", Code: hs.FieldInvalidFormat, Message: "use pix, cash ou card"})
	}
	if err := v.Err(); err != nil {
		return err
	}
	method, ack := *body.Method, body.Acknowledge != nil && *body.Acknowledge
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: who.Key, Operation: "choosePaymentMethod", Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
		lp, err := atendimento.LockForPayment(ctx, tx, p.EstablishmentID, p.ID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if err := payableCheck(ctx, tx, lp, expected); err != nil {
			return idempotency.Result{}, err
		}
		now := s.Clock.Now()
		if method == "pix" {
			if s.Provider == nil {
				return idempotency.Result{}, errProviderUnavailable
			}
			if min := s.Provider.Capabilities().MinPixCents; lp.DueCents() < min {
				return idempotency.Result{}, orderConflict(ctx, tx, lp, "payment_method_not_allowed", "Pix indisponível para este valor",
					"O Pix exige no mínimo "+reais(min)+". Pague em dinheiro ou cartão com a equipe.")
			}
		} else {
			cur, err := activeCharge(ctx, tx, p.ID, true)
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur != nil {
				if cur.Status != ChargePending {
					return idempotency.Result{}, conflict("pix_charge_outcome_unknown", "Verificando pagamento",
						"Ainda estamos confirmando o Pix com o banco. Aguarde a verificação antes de trocar o meio.", cur.chargeView)
				}
				if !ack && cur.CancelRequestedAt == nil {
					return idempotency.Result{}, conflict("pix_charge_active", "Existe um Pix em aberto",
						"Este Pix continua válido até expirar e ainda pode ser pago. Se trocar o meio e o Pix for pago também, "+
							"o valor a mais ficará registrado para devolução. Confirme a ciência para trocar.", cur.chargeView)
				}
				if err := s.requestCancel(ctx, tx, lp, cur, who, "troca de meio de pagamento", now); err != nil {
					return idempotency.Result{}, err
				}
				// Alerta à equipe (R-PAG-11): o Pix segue válido até expirar (o provedor não cancela).
				amt := cur.amount
				if _, err := s.openIssue(ctx, tx, p.EstablishmentID, p.ID, IssueActivePixSwitch, "pix_charge", cur.ID, &amt, now); err != nil {
					return idempotency.Result{}, err
				}
			}
		}
		if lp.Method == nil || *lp.Method != method {
			if _, err := atendimento.SetPaymentMethod(ctx, tx, p.ID, method, now); err != nil {
				return idempotency.Result{}, err
			}
			a := who.Audit(ctx, lp)
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &p.EstablishmentID, now, "T-O-12", "order", p.ID
			a.ToState = "method:" + method
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if err := atendimento.RecordPaymentEvents(ctx, tx, p.EstablishmentID, p.ID, now, false); err != nil {
				return idempotency.Result{}, err
			}
		}
		return orderOK(ctx, tx, lp, http.StatusOK)
	})
}

var cardRef = regexp.MustCompile(`^[A-Za-z0-9-]{1,40}$`)

// registerInPersonReceipt (T-RC-01 + T-O-10): dinheiro ou maquininha. A equipe registra o
// recebimento INTEGRAL e a conferência na mesma ação, em dois registros distintos (R-PAG-05).
// O valor enviado precisa ser igual ao devido calculado pelo servidor (nunca é usado como verdade).
func (s *Service) registerInPersonReceipt(w http.ResponseWriter, r *http.Request) error {
	p, who, err := s.order(r, false, true, true)
	if err != nil {
		return err
	}
	var body struct {
		ExpectedVersion *int64  `json:"expected_version"`
		Method          *string `json:"method"`
		AmountCents     *int64  `json:"amount_cents"`
		CashTendered    *int64  `json:"cash_tendered_cents"`
		CardReference   *string `json:"card_reference"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", body.ExpectedVersion)
	v.Required("/method", body.Method == nil)
	method := ""
	if body.Method != nil {
		method = *body.Method
		if method != "cash" && method != "card" {
			v.Add(&hs.FieldError{Field: "/method", Code: hs.FieldInvalidFormat, Message: "use cash ou card"})
		}
	}
	v.Required("/amount_cents", body.AmountCents == nil)
	amount := v.IntRange("/amount_cents", body.AmountCents, 1, 100_000_000)
	var tendered *int64
	if method == "cash" {
		v.Required("/cash_tendered_cents", body.CashTendered == nil)
		t := v.IntRange("/cash_tendered_cents", body.CashTendered, 1, 100_000_000)
		tendered = &t
	} else if body.CashTendered != nil {
		v.Add(&hs.FieldError{Field: "/cash_tendered_cents", Code: hs.FieldInvalidFormat, Message: "só para dinheiro"})
	}
	var ref *string
	if body.CardReference != nil && *body.CardReference != "" {
		switch {
		case method != "card":
			v.Add(&hs.FieldError{Field: "/card_reference", Code: hs.FieldInvalidFormat, Message: "só para cartão"})
		case !cardRef.MatchString(*body.CardReference):
			v.Add(&hs.FieldError{Field: "/card_reference", Code: hs.FieldInvalidFormat,
				Message: "até 40 letras, números ou hífen (nunca dados do cartão)"})
		default:
			ref = body.CardReference
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: who.Key, Operation: "registerInPersonReceipt", Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
		lp, err := atendimento.LockForPayment(ctx, tx, p.EstablishmentID, p.ID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if err := payableCheck(ctx, tx, lp, expected); err != nil {
			return idempotency.Result{}, err
		}
		due := lp.DueCents()
		if amount != due {
			return idempotency.Result{}, orderConflict(ctx, tx, lp, "payment_amount_mismatch", "Valor diferente do devido",
				"Registre exatamente o valor devido ("+reais(due)+"). Recebimento parcial ou a mais não é aceito.")
		}
		if tendered != nil && *tendered < due {
			return idempotency.Result{}, orderConflict(ctx, tx, lp, "payment_amount_mismatch", "Valor entregue insuficiente",
				"O valor entregue em dinheiro precisa ser pelo menos "+reais(due)+".")
		}
		cur, err := activeCharge(ctx, tx, p.ID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur != nil {
			if cur.Status != ChargePending {
				return idempotency.Result{}, conflict("pix_charge_outcome_unknown", "Verificando pagamento",
					"Ainda estamos confirmando o Pix com o banco. Aguarde a verificação antes de receber por outro meio.", cur.chargeView)
			}
			if cur.CancelRequestedAt == nil {
				return idempotency.Result{}, conflict("pix_charge_active", "Existe um Pix em aberto",
					"Troque o meio de pagamento (confirmando a ciência do Pix em aberto) antes de receber em dinheiro ou cartão.", cur.chargeView)
			}
		}
		now := s.Clock.Now()
		var change *int64
		if tendered != nil {
			c := *tendered - due
			change = &c
		}
		var receiptID uuid.UUID
		accountID := who.Staff.AccountID
		if err := tx.QueryRow(ctx, `
			INSERT INTO receipt (establishment_id, order_id, method, amount_cents, classification, evidence, registered_by,
			                     registered_by_account_id, registered_at, cash_tendered_cents, change_cents, card_reference)
			VALUES ($1, $2, $3, $4, 'principal', 'in_person', $5, $6, $7, $8, $9, $10) RETURNING id`,
			p.EstablishmentID, p.ID, method, due, who.Role(), accountID, now, tendered, change, ref).Scan(&receiptID); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO receipt_conferral (receipt_id, establishment_id, conferred_at, conferred_by_role, conferred_by_account_id)
			VALUES ($1, $2, $3, $4, $5)`, receiptID, p.EstablishmentID, now, who.Role(), accountID); err != nil {
			return idempotency.Result{}, err
		}
		if lp.Method == nil || *lp.Method != method {
			if _, err := atendimento.SetPaymentMethod(ctx, tx, p.ID, method, now); err != nil {
				return idempotency.Result{}, err
			}
		}
		a := who.Audit(ctx, lp)
		a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &p.EstablishmentID, now, "T-RC-01", "receipt", receiptID
		a.ToState = "conferred:" + method
		if err := auditoria.Record(ctx, tx, a); err != nil {
			return idempotency.Result{}, err
		}
		if err := atendimento.Settle(ctx, tx, lp, now, who.Audit(ctx, lp), s.VisitClosed); err != nil {
			return idempotency.Result{}, err
		}
		if err := atendimento.RecordPaymentEvents(ctx, tx, p.EstablishmentID, p.ID, now, false); err != nil {
			return idempotency.Result{}, err
		}
		return orderOK(ctx, tx, lp, http.StatusCreated)
	})
}

// conferReceipt (T-RC-03, e T-O-10 quando tudo está coberto e conferido): a equipe confere um
// recebimento Pix registrado pelo provedor. A conferência é um registro à parte; conferir de novo
// não cria outro (repetição ou corrida devolve o estado atual).
func (s *Service) conferReceipt(w http.ResponseWriter, r *http.Request) error {
	p, who, err := s.order(r, false, true, true)
	if err != nil {
		return err
	}
	receiptID, err := pathID(r, "receiptId")
	if err != nil {
		return err
	}
	raw, err := hs.DecodeJSON(r, &struct{}{})
	if err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: who.Key, Operation: "conferReceipt", Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
		lp, err := atendimento.LockForPayment(ctx, tx, p.EstablishmentID, p.ID)
		if err != nil {
			return idempotency.Result{}, err
		}
		var conferred bool
		err = tx.QueryRow(ctx, `
			SELECT c.receipt_id IS NOT NULL
			FROM receipt r LEFT JOIN receipt_conferral c ON c.receipt_id = r.id
			WHERE r.order_id = $1 AND r.id = $2`, p.ID, receiptID).Scan(&conferred)
		if errors.Is(err, pgx.ErrNoRows) {
			return idempotency.Result{}, hs.ErrNotFound()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		if conferred {
			return orderOK(ctx, tx, lp, http.StatusOK) // já conferido (outra pessoa ou repetição)
		}
		// Estorno em andamento ou confirmado: conferir agora contaria receita devolvida.
		var refunding int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM refund WHERE receipt_id = $1 AND status <> 'failed'`, receiptID).
			Scan(&refunding); err != nil {
			return idempotency.Result{}, err
		}
		if refunding > 0 {
			return idempotency.Result{}, orderConflict(ctx, tx, lp, "invalid_transition", "Recebimento em estorno",
				"Este recebimento tem estorno em andamento ou confirmado e não pode ser conferido.")
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `
			INSERT INTO receipt_conferral (receipt_id, establishment_id, conferred_at, conferred_by_role, conferred_by_account_id)
			VALUES ($1, $2, $3, $4, $5)`, receiptID, p.EstablishmentID, now, who.Role(), who.Staff.AccountID); err != nil {
			return idempotency.Result{}, err
		}
		a := who.Audit(ctx, lp)
		a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &p.EstablishmentID, now, "T-RC-03", "receipt", receiptID
		a.FromState, a.ToState = "registered", "conferred"
		if err := auditoria.Record(ctx, tx, a); err != nil {
			return idempotency.Result{}, err
		}
		settle := false
		if lp.Status == atendimento.StatusAwaitingConfirmation {
			var principal int64
			var open int
			if err := tx.QueryRow(ctx, `
				SELECT coalesce(sum(r.amount_cents), 0), count(*) FILTER (WHERE c.receipt_id IS NULL)
				FROM receipt r LEFT JOIN receipt_conferral c ON c.receipt_id = r.id
				WHERE r.order_id = $1 AND r.classification = 'principal'`, p.ID).Scan(&principal, &open); err != nil {
				return idempotency.Result{}, err
			}
			settle = principal == lp.TotalCents && open == 0
		}
		if settle {
			if err := atendimento.Settle(ctx, tx, lp, now, who.Audit(ctx, lp), s.VisitClosed); err != nil {
				return idempotency.Result{}, err
			}
		}
		if err := atendimento.RecordPaymentEvents(ctx, tx, p.EstablishmentID, p.ID, now, !settle); err != nil {
			return idempotency.Result{}, err
		}
		return orderOK(ctx, tx, lp, http.StatusOK)
	})
}
