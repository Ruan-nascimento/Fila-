package pagamento

import (
	"context"
	"errors"
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

// Estados do estorno (RefundStatus).
const (
	RefundRequested   = "requested"
	RefundProcessing  = "processing"
	RefundUnknown     = "unknown"
	RefundUnderReview = "under_review"
	RefundConfirmed   = "confirmed"
	RefundFailed      = "failed"
)

// Estorno (ADR-0024): três momentos separados.
//   - Intenção (requestRefund, só o dono, reautenticado): grava o pedido. Não muda saldo nenhum.
//   - Processamento (worker): pede o estorno integral ao provedor. Tempo esgotado = desconhecido.
//   - Confirmação: só com evidência do provedor — a cobrança aparece REFUNDED na consulta
//     autenticada ou no webhook autenticado. Dinheiro/cartão e casos sem resolução vão para
//     revisão do dono, que confirma com evidência documentada (ou registra que falhou).
//
// Repetir o pedido ao provedor é seguro: a documentação diz que estorno de cobrança já estornada
// é recusado sem criar outro. Por isso o worker pode retomar depois de queda ou reinício.

const (
	maxRefundChecks = 12 // consultas sem confirmação → revisão do dono
)

// refundView segue Refund.
type refundView struct {
	ID          uuid.UUID         `json:"id"`
	ReceiptID   uuid.UUID         `json:"receipt_id"`
	Amount      atendimento.Money `json:"amount"`
	Status      string            `json:"status"`
	Reason      string            `json:"reason"`
	RequestedAt time.Time         `json:"requested_at"`
	ResolvedAt  *time.Time        `json:"resolved_at"`
	Evidence    *string           `json:"evidence"`
}

type refundRow struct {
	refundView
	EstablishmentID uuid.UUID
	OrderID         uuid.UUID
	Method          string
	Attempts        int
	amount          int64
}

const refundSelect = `SELECT id, receipt_id, amount_cents, status, reason, requested_at, resolved_at, evidence,
	establishment_id, order_id, method, check_attempts FROM refund`

func scanRefund(r pgx.Row) (refundRow, error) {
	var v refundRow
	err := r.Scan(&v.ID, &v.ReceiptID, &v.amount, &v.Status, &v.Reason, &v.RequestedAt, &v.ResolvedAt, &v.Evidence,
		&v.EstablishmentID, &v.OrderID, &v.Method, &v.Attempts)
	v.Amount = brl(v.amount)
	return v, err
}

func refunds(ctx context.Context, q database.Querier, orderID uuid.UUID) ([]refundRow, error) {
	rows, err := q.Query(ctx, refundSelect+` WHERE order_id = $1 ORDER BY requested_at, id`, orderID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (refundRow, error) { return scanRefund(r) })
}

func reasonField(v *validate.Collector, field string, raw *string, required bool) string {
	if required {
		v.Required(field, raw == nil)
	}
	if raw == nil {
		return ""
	}
	text, fe := validate.Text(field, *raw, 3, 500)
	v.Add(fe)
	return text
}

// requestRefund (T-RF-01, T-RF-05): estorno integral de um recebimento, somente pelo dono.
func (s *Service) requestRefund(w http.ResponseWriter, r *http.Request) error {
	p, who, err := s.order(r, false, false, true)
	if err != nil {
		return err
	}
	receiptID, err := pathID(r, "receiptId")
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
	reason := reasonField(&v, "/reason", body.Reason, true)
	if err := v.Err(); err != nil {
		return err
	}
	if err := s.Auth.RequireReauth(r); err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: who.Key, Operation: "requestRefund", Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
		lp, err := atendimento.LockForPayment(ctx, tx, p.EstablishmentID, p.ID)
		if err != nil {
			return idempotency.Result{}, err
		}
		var method, class string
		var amount int64
		var conferred bool
		err = tx.QueryRow(ctx, `
			SELECT r.method, r.classification, r.amount_cents, c.receipt_id IS NOT NULL
			FROM receipt r LEFT JOIN receipt_conferral c ON c.receipt_id = r.id
			WHERE r.order_id = $1 AND r.id = $2 FOR UPDATE OF r`, p.ID, receiptID).Scan(&method, &class, &amount, &conferred)
		if errors.Is(err, pgx.ErrNoRows) {
			return idempotency.Result{}, hs.ErrNotFound()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		var existing int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM refund WHERE receipt_id = $1 AND status <> 'failed'`, receiptID).
			Scan(&existing); err != nil {
			return idempotency.Result{}, err
		}
		if existing > 0 {
			return idempotency.Result{}, orderConflict(ctx, tx, lp, "refund_exceeds_available", "Nada a devolver",
				"Este recebimento já tem estorno em andamento ou confirmado. O estorno é sempre do valor integral.")
		}
		// Recebimento principal ainda não conferido: confira antes (evita contar receita devolvida).
		if class == "principal" && !conferred {
			return idempotency.Result{}, orderConflict(ctx, tx, lp, "refund_not_allowed", "Confira o recebimento antes",
				"Confira o recebimento antes de estornar.")
		}
		now := s.Clock.Now()
		status := RefundUnderReview // dinheiro, cartão ou provedor sem estorno pela API (T-RF-05)
		var next *time.Time
		if method == "pix" && s.Provider != nil && s.Provider.Capabilities().RefundPix {
			status, next = RefundRequested, &now // o worker pede ao provedor (T-RF-02)
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO refund (establishment_id, order_id, receipt_id, method, amount_cents, status, reason, requested_at,
			                    requested_by_account_id, next_check_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $8) RETURNING id`,
			p.EstablishmentID, p.ID, receiptID, method, amount, status, reason, now, who.Staff.AccountID, next).Scan(&id); err != nil {
			return idempotency.Result{}, err
		}
		a := who.Audit(ctx, lp)
		a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &p.EstablishmentID, now, "T-RF-01", "refund", id
		a.ToState, a.Reason = status, reason
		if err := auditoria.Record(ctx, tx, a); err != nil {
			return idempotency.Result{}, err
		}
		if status == RefundUnderReview {
			amt := amount
			if _, err := s.openIssue(ctx, tx, p.EstablishmentID, p.ID, IssueRefundReview, "refund", id, &amt, now); err != nil {
				return idempotency.Result{}, err
			}
		}
		if err := s.pendenciesChanged(ctx, tx, p.EstablishmentID, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := atendimento.RecordPaymentEvents(ctx, tx, p.EstablishmentID, p.ID, now, true); err != nil {
			return idempotency.Result{}, err
		}
		return orderOK(ctx, tx, lp, http.StatusAccepted)
	})
}

// manualRefund é confirmRefundManually (T-RF-06) e markRefundFailed (T-RF-07): o dono,
// reautenticado, resolve um estorno em revisão com evidência documentada.
func (s *Service) manualRefund(to string) hs.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, who, err := s.order(r, false, false, true)
		if err != nil {
			return err
		}
		refundID, err := pathID(r, "refundId")
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
		op := "confirmRefundManually"
		if to == RefundFailed {
			op = "markRefundFailed"
		}
		ctx := r.Context()
		return s.idem.Run(w, r, idempotency.Command{ActorKey: who.Key, Operation: op, Body: raw}, func(tx pgx.Tx) (idempotency.Result, error) {
			lp, err := atendimento.LockForPayment(ctx, tx, p.EstablishmentID, p.ID)
			if err != nil {
				return idempotency.Result{}, err
			}
			cur, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE order_id = $1 AND id = $2 FOR UPDATE`, p.ID, refundID))
			if errors.Is(err, pgx.ErrNoRows) {
				return idempotency.Result{}, hs.ErrNotFound()
			}
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Status != RefundUnderReview {
				return idempotency.Result{}, orderConflict(ctx, tx, lp, "invalid_transition", "Estorno fora de revisão",
					"Só um estorno em revisão pode ser confirmado ou dado como falho manualmente.")
			}
			now := s.Clock.Now()
			evidence := "owner_documented"
			var evPtr *string
			if to == RefundConfirmed {
				evPtr = &evidence
			}
			if _, err := tx.Exec(ctx, `UPDATE refund SET status = $2, evidence = $3, evidence_note = $4, external_reference = $5,
				resolved_at = $6, resolved_by_account_id = $7, next_check_at = NULL, updated_at = $6 WHERE id = $1`,
				refundID, to, evPtr, note, ref, now, who.Staff.AccountID); err != nil {
				return idempotency.Result{}, err
			}
			action := "T-RF-06"
			if to == RefundFailed {
				action = "T-RF-07"
			} else if err := s.refundConfirmed(ctx, tx, p.EstablishmentID, p.ID, cur.ReceiptID, now); err != nil {
				return idempotency.Result{}, err
			}
			a := who.Audit(ctx, lp)
			a.EstablishmentID, a.OccurredAt, a.Action, a.TargetType, a.TargetID = &p.EstablishmentID, now, action, "refund", refundID
			a.FromState, a.ToState, a.Reason = cur.Status, to, note
			if err := auditoria.Record(ctx, tx, a); err != nil {
				return idempotency.Result{}, err
			}
			if err := s.pendenciesChanged(ctx, tx, p.EstablishmentID, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := atendimento.RecordPaymentEvents(ctx, tx, p.EstablishmentID, p.ID, now, true); err != nil {
				return idempotency.Result{}, err
			}
			return orderOK(ctx, tx, lp, http.StatusOK)
		})
	}
}

// refundTo muda o estado de um estorno pelo sistema (auditoria com ator sistema) e grava os eventos.
func (s *Service) refundTo(ctx context.Context, tx pgx.Tx, lp atendimento.PaymentInfo, rf *refundRow, to, action string,
	now time.Time, providerRefundID string) error {
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
		if _, err := s.openIssue(ctx, tx, rf.EstablishmentID, rf.OrderID, IssueRefundReview, "refund", rf.ID, &amt, now); err != nil {
			return err
		}
	}
	if to == RefundConfirmed {
		// Estado final não muda (gatilho no banco): a reversão acontece uma única vez por estorno.
		if err := s.refundConfirmed(ctx, tx, rf.EstablishmentID, rf.OrderID, rf.ReceiptID, now); err != nil {
			return err
		}
	}
	if err := s.pendenciesChanged(ctx, tx, rf.EstablishmentID, now); err != nil {
		return err
	}
	return atendimento.RecordPaymentEvents(ctx, tx, lp.EstablishmentID, lp.ID, now, true)
}

// refundConfirmedByProvider confirma (T-RF-03) o estorno ativo do recebimento desta cobrança,
// quando o provedor mostra a cobrança REFUNDED. Sem estorno nosso → estorno feito fora do
// sistema (painel do provedor): fica em revisão do dono, sem inventar quem pediu.
func (s *Service) refundConfirmedByProvider(ctx context.Context, tx pgx.Tx, lp atendimento.PaymentInfo, chargeID uuid.UUID, now time.Time) (bool, error) {
	var receiptID uuid.UUID
	var amount int64
	err := tx.QueryRow(ctx, `SELECT id, amount_cents FROM receipt WHERE pix_charge_id = $1`, chargeID).Scan(&receiptID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rf, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE receipt_id = $1 AND status <> 'failed' FOR UPDATE`, receiptID))
	if errors.Is(err, pgx.ErrNoRows) {
		s.Log.Warn("estorno no provedor sem pedido no sistema: revisão do dono", "cobranca", chargeID.String())
		opened, err := s.openIssue(ctx, tx, lp.EstablishmentID, lp.ID, IssueRefundReview, "receipt", receiptID, &amount, now)
		return opened, err
	}
	if err != nil {
		return false, err
	}
	if rf.Status == RefundConfirmed {
		return false, nil // já confirmado (aviso repetido / consulta repetida)
	}
	return true, s.refundTo(ctx, tx, lp, &rf, RefundConfirmed, "T-RF-03", now, "")
}

// ReconcileRefunds processa os estornos devidos (pedido ao provedor e confirmação).
func (s *Service) reconcileRefunds(ctx context.Context) (int, error) {
	if s.Provider == nil {
		return 0, nil
	}
	now := s.Clock.Now()
	var ids []uuid.UUID
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE refund SET next_check_at = $2
			WHERE id IN (SELECT id FROM refund WHERE status IN ('requested', 'processing', 'unknown') AND next_check_at <= $1
			             ORDER BY next_check_at LIMIT $3 FOR UPDATE SKIP LOCKED)
			RETURNING id`, now, now.Add(reconcileLease), reconcileBatch)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.reconcileRefund(ctx, id); err != nil {
			s.Log.Error("falha ao processar estorno", "estorno", id.String(), "erro", err.Error())
		}
	}
	return len(ids), nil
}

// reconcileRefund: uma rodada para um estorno. Fora de transação durante a rede.
func (s *Service) reconcileRefund(ctx context.Context, id uuid.UUID) error {
	rf, err := scanRefund(s.Pool.QueryRow(ctx, refundSelect+` WHERE id = $1`, id))
	if err != nil {
		return err
	}
	var providerID *string
	if err := s.Pool.QueryRow(ctx, `SELECT c.provider_charge_id FROM receipt r JOIN pix_charge c ON c.id = r.pix_charge_id
		WHERE r.id = $1`, rf.ReceiptID).Scan(&providerID); err != nil || providerID == nil {
		return s.finishRefund(ctx, rf, RefundUnderReview, "T-RF-04", "")
	}
	callCtx, cancel := context.WithTimeout(ctx, s.Timeout+2*time.Second)
	defer cancel()
	// Primeiro a verdade do provedor: se já está REFUNDED, confirma (inclusive depois de reinício).
	pc, qerr := s.Provider.GetPix(callCtx, *providerID)
	if qerr == nil && pc.Status == ProviderRefunded {
		return s.finishRefund(ctx, rf, RefundConfirmed, "T-RF-03", "")
	}
	if qerr == nil && pc.Status == ProviderDisputed {
		return s.finishRefund(ctx, rf, RefundUnderReview, "T-RF-04", "") // disputa: estorno pela API é recusado
	}
	switch rf.Status {
	case RefundRequested, RefundUnknown:
		if qerr != nil {
			return s.refundRetry(ctx, rf, qerr)
		}
		// Cobrança ainda paga: pede (ou repete) o estorno integral. Repetir é seguro (ver acima).
		res, err := s.Provider.RefundPix(callCtx, *providerID, rf.Reason)
		switch {
		case err == nil:
			return s.finishRefund(ctx, rf, RefundProcessing, "T-RF-02", res.ProviderRefundID)
		case errors.Is(err, ErrNotSent):
			return s.refundRetry(ctx, rf, err)
		case errors.Is(err, ErrRejected):
			// Recusa: saldo, disputa, trava do provedor… Não damos como falho sozinhos: o dono revisa.
			s.Log.Warn("provedor recusou o estorno; revisão do dono", "estorno", rf.ID.String())
			return s.finishRefund(ctx, rf, RefundUnderReview, "T-RF-04", "")
		default: // ambíguo: o estorno pode ter acontecido; consulta na próxima rodada
			if rf.Status == RefundUnknown {
				return s.refundRetry(ctx, rf, err)
			}
			return s.finishRefund(ctx, rf, RefundUnknown, "T-RF-04", "")
		}
	default: // processing: o provedor aceitou; espera a cobrança aparecer REFUNDED
		if qerr != nil {
			return s.refundRetry(ctx, rf, qerr)
		}
		return s.refundRetry(ctx, rf, nil)
	}
}

// finishRefund aplica uma transição decidida pelo worker (com a comanda e o estorno travados).
func (s *Service) finishRefund(ctx context.Context, rf refundRow, to, action, providerRefundID string) error {
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		lp, err := atendimento.LockForPayment(ctx, tx, rf.EstablishmentID, rf.OrderID)
		if err != nil {
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
			return nil // outra rodada (ou o webhook) já resolveu
		}
		return s.refundTo(ctx, tx, lp, &cur, to, action, s.Clock.Now(), providerRefundID)
	})
}

// refundRetry conta a consulta; sem confirmação depois do limite → revisão do dono.
func (s *Service) refundRetry(ctx context.Context, rf refundRow, cause error) error {
	if cause != nil {
		s.Log.Warn("estorno sem resposta conclusiva; nova tentativa agendada", "estorno", rf.ID.String(), "motivo", cause.Error())
	}
	if rf.Attempts+1 >= maxRefundChecks {
		s.Log.Error("estorno sem confirmação do provedor: revisão do dono", "estorno", rf.ID.String())
		return s.finishRefund(ctx, rf, RefundUnderReview, "T-RF-04", "")
	}
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		cur, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1 FOR UPDATE`, rf.ID))
		if err != nil {
			return err
		}
		return s.refundSchedule(ctx, tx, cur, s.Clock.Now())
	})
}

func (s *Service) refundSchedule(ctx context.Context, tx pgx.Tx, rf refundRow, now time.Time) error {
	if rf.Status != RefundRequested && rf.Status != RefundProcessing && rf.Status != RefundUnknown {
		return nil
	}
	attempts := rf.Attempts + 1
	_, err := tx.Exec(ctx, `UPDATE refund SET check_attempts = $2, next_check_at = $3, updated_at = $4 WHERE id = $1`,
		rf.ID, attempts, now.Add(backoff(attempts)), now)
	return err
}
