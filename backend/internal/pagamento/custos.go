package pagamento

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Custos de recebimento (ADR-0034). São ESTIMATIVAS cadastradas pelo dono, por modalidade
// (débito, crédito, Pix integrado): percentual em pontos-base (1 pb = 0,01%) + componente fixo
// POR TRANSAÇÃO, em centavos. Não são tabela de provedor: o sistema não assume taxa comercial
// nenhuma. O custo NUNCA muda o valor cobrado do cliente; só aparece para o dono.
//
// Regra única de arredondamento (backend): custo = ⌊(valor × pb + 5 000) ÷ 10 000⌋ + fixo,
// isto é, a parte percentual é arredondada para o centavo mais próximo, metade para cima.
// Exemplo de teste: R$ 100,00 com 2,5% (250 pb) + R$ 0,50 → R$ 2,50 + R$ 0,50 = R$ 3,00.
const (
	FeeDebit  = "debit"
	FeeCredit = "credit"
	FeePix    = "pix"

	MaxFeeBasisPoints = 3000  // 30%
	MaxFeeFixedCents  = 10000 // R$ 100,00

	FeeNotConfigured = "not_configured"
	FeeEstimated     = "estimated"
	FeeActual        = "actual"
	FeeNotApplicable = "not_applicable"
)

var feeMethods = []string{FeeDebit, FeeCredit, FeePix}

// EstimateFee aplica a regra de arredondamento. amountCents e o resultado em centavos.
func EstimateFee(amountCents int64, basisPoints int, fixedCents int64) int64 {
	if amountCents <= 0 {
		return 0
	}
	return (amountCents*int64(basisPoints)+5000)/10000 + fixedCents
}

type feeConfig struct {
	ID            uuid.UUID `json:"id"`
	Method        string    `json:"method"`
	Version       int       `json:"version"`
	Cleared       bool      `json:"cleared"`
	PercentBP     int       `json:"percent_basis_points"`
	FixedCents    int64     `json:"fixed_cents"`
	EffectiveFrom time.Time `json:"effective_from"`
}

// feeView é a situação atual de uma modalidade: não configurada (nenhuma versão ou última
// "limpa"), ou configurada (inclusive explicitamente zero).
type feeView struct {
	Method     string     `json:"method"`
	Configured bool       `json:"configured"`
	Current    *feeConfig `json:"current"`
	Version    int        `json:"version"`
	// Estimate: os valores são estimativas cadastradas, não taxas informadas pelo provedor.
	Estimate bool        `json:"estimate"`
	History  []feeConfig `json:"history"`
}

func loadFees(ctx context.Context, q database.Querier, estID uuid.UUID) (map[string]feeView, error) {
	sql := `SELECT id, method, version, cleared, percent_bp, fixed_cents, effective_from FROM payment_fee_config
		WHERE establishment_id = $1 ORDER BY method, version DESC`
	rows, err := q.Query(ctx, sql, estID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (feeConfig, error) {
		var c feeConfig
		err := r.Scan(&c.ID, &c.Method, &c.Version, &c.Cleared, &c.PercentBP, &c.FixedCents, &c.EffectiveFrom)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	out := map[string]feeView{}
	for _, m := range feeMethods {
		out[m] = feeView{Method: m, Estimate: true, History: []feeConfig{}}
	}
	for _, c := range list {
		v := out[c.Method]
		if v.Version == 0 {
			v.Version = c.Version
			if !c.Cleared {
				cc := c
				v.Current, v.Configured = &cc, true
			}
		}
		if len(v.History) < 20 {
			v.History = append(v.History, c)
		}
		out[c.Method] = v
	}
	return out, nil
}

// listPaymentFees: só o dono (custo interno nunca aparece para cliente, visitante ou profissional).
func (s *Service) listPaymentFees(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Owner(r, estID); err != nil {
		return err
	}
	fees, err := loadFees(r.Context(), s.Pool, estID)
	if err != nil {
		return err
	}
	out := make([]feeView, 0, len(feeMethods))
	for _, m := range feeMethods {
		out = append(out, fees[m])
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]feeView]{Data: out})
	return nil
}

type setFeeRequest struct {
	// ExpectedVersion: versão vigente que a tela viu (0 = nunca configurado).
	ExpectedVersion *int64 `json:"expected_version"`
	PercentBP       *int64 `json:"percent_basis_points"`
	FixedCents      *int64 `json:"fixed_cents"`
	Clear           *bool  `json:"clear"`
}

// setPaymentFee (T-FE-01): nova versão do custo de uma modalidade, a partir de agora. Recebimentos
// anteriores guardam o custo calculado na hora e NUNCA são recalculados.
func (s *Service) setPaymentFee(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	method := r.PathValue("method")
	if method != FeeDebit && method != FeeCredit && method != FeePix {
		return hs.ErrNotFound()
	}
	var body setFeeRequest
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/expected_version", body.ExpectedVersion == nil)
	expected := int64(0)
	if body.ExpectedVersion != nil {
		expected = v.IntRange("/expected_version", body.ExpectedVersion, 0, 1_000_000)
	}
	clear := body.Clear != nil && *body.Clear
	if clear {
		if body.PercentBP != nil || body.FixedCents != nil {
			v.Add(&hs.FieldError{Field: "/clear", Code: hs.FieldNotAllowed, Message: "para limpar, não envie percentual nem valor fixo"})
		}
	} else {
		v.Required("/percent_basis_points", body.PercentBP == nil)
		v.Required("/fixed_cents", body.FixedCents == nil)
		v.IntRange("/percent_basis_points", body.PercentBP, 0, MaxFeeBasisPoints)
		v.IntRange("/fixed_cents", body.FixedCents, 0, MaxFeeFixedCents)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "setPaymentFee:" + method, Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		// Serializa as versões da mesma modalidade (o índice único é a última barreira).
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "custo|"+estID.String()+"|"+method); err != nil {
			return idempotency.Result{}, err
		}
		fees, err := loadFees(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		cur := fees[method]
		if int64(cur.Version) != expected {
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "version_conflict", Title: "Os dados mudaram",
				Detail: "Outra pessoa alterou este custo. Confira os valores atuais.", Current: cur}
		}
		if clear && !cur.Configured {
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[feeView]{Data: cur}}, nil
		}
		bp, fixed := int64(0), int64(0)
		if !clear {
			bp, fixed = *body.PercentBP, *body.FixedCents
		}
		if cur.Configured && !clear && int64(cur.Current.PercentBP) == bp && cur.Current.FixedCents == fixed {
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[feeView]{Data: cur}}, nil
		}
		now := s.Clock.Now()
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO payment_fee_config (establishment_id, method, version, cleared, percent_bp, fixed_cents, effective_from,
			                                created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $7) RETURNING id`,
			estID, method, cur.Version+1, clear, bp, fixed, now, actor.AccountID).Scan(&id); err != nil {
			return idempotency.Result{}, err
		}
		to := "estimate"
		if clear {
			to = "not_configured"
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(actor.AccountID), Action: "T-FE-01", TargetType: "payment_fee_config", TargetID: id,
			FromState: method, ToState: to, RequestID: hs.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		next, err := loadFees(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: hs.Data[feeView]{Data: next[method]}}, nil
	})
}

// recordReceiptFee grava o custo do recebimento com a configuração vigente NO MOMENTO (snapshot):
// dinheiro não tem custo; cartão sem modalidade (cliente antigo) e modalidade sem configuração
// ficam sem estimativa — nada é inventado. Uma linha por recebimento.
func recordReceiptFee(ctx context.Context, q database.Querier, estID, receiptID uuid.UUID, method string, cardType *string,
	amountCents int64, now time.Time) error {
	feeMethod := ""
	switch {
	case method == "cash":
		_, err := q.Exec(ctx, `INSERT INTO receipt_fee (receipt_id, establishment_id, status, created_at) VALUES ($1, $2, $3, $4)`,
			receiptID, estID, FeeNotApplicable, now)
		return err
	case method == "pix":
		feeMethod = FeePix
	case cardType != nil:
		feeMethod = *cardType
	default:
		return nil // cartão sem modalidade: custo desconhecido (sem linha)
	}
	var c feeConfig
	err := q.QueryRow(ctx, `SELECT id, cleared, percent_bp, fixed_cents FROM payment_fee_config
		WHERE establishment_id = $1 AND method = $2 ORDER BY version DESC LIMIT 1`, estID, feeMethod).
		Scan(&c.ID, &c.Cleared, &c.PercentBP, &c.FixedCents)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && c.Cleared) {
		_, err = q.Exec(ctx, `INSERT INTO receipt_fee (receipt_id, establishment_id, status, created_at) VALUES ($1, $2, $3, $4)`,
			receiptID, estID, FeeNotConfigured, now)
		return err
	}
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO receipt_fee (receipt_id, establishment_id, status, fee_config_id, percent_bp, fixed_cents, estimated_cents, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		receiptID, estID, FeeEstimated, c.ID, c.PercentBP, c.FixedCents, EstimateFee(amountCents, c.PercentBP, c.FixedCents), now)
	return err
}

// methodEnabled confere se a loja aceita o meio agora (configurações). Pix exige também a
// integração operacional (Provider), conferida por quem chama.
func methodEnabled(st estabelecimento.Settings, method string) bool {
	switch method {
	case "cash":
		return st.CashEnabled
	case "card":
		return st.CardEnabled
	case "pix":
		return st.PixEnabled
	}
	return false
}

func methodDisabled(method string) error {
	names := map[string]string{"cash": "Dinheiro", "card": "Cartão", "pix": "Pix"}
	return conflict("payment_method_disabled", "Meio de pagamento indisponível",
		names[method]+" não está sendo aceito por esta barbearia no momento. Escolha outro meio.", nil)
}

// receiptCostsView resume o período para o dono. NÃO é lucro: despesas da loja, salários e outros
// custos não entram. Quando há recebimento com custo desconhecido, o resultado é marcado como
// incompleto (cobertura < 100%), nunca apresentado como líquido total.
type receiptCostsView struct {
	From               time.Time `json:"from"`
	To                 time.Time `json:"to"`
	GrossCents         int64     `json:"gross_cents"`
	ReceiptCount       int       `json:"receipt_count"`
	RefundedCents      int64     `json:"refunded_cents"`
	EstimatedFeesCents int64     `json:"estimated_fees_cents"`
	ActualFeesCents    int64     `json:"actual_fees_cents"`
	FeeReturnedCents   int64     `json:"fee_returned_cents"`
	UnknownFeeCount    int       `json:"unknown_fee_count"`
	NotConfiguredCount int       `json:"not_configured_count"`
	RefundsUnknownFee  int       `json:"refunds_fee_return_unknown"`
	AfterCostsCents    int64     `json:"after_known_costs_cents"`
	Complete           bool      `json:"complete"`
	ByMethod           []costRow `json:"by_method"`
	Note               string    `json:"note"`
}

type costRow struct {
	Method             string `json:"method"` // cash | pix | debit | credit | card_unspecified
	GrossCents         int64  `json:"gross_cents"`
	Count              int    `json:"count"`
	EstimatedFeesCents int64  `json:"estimated_fees_cents"`
	ActualFeesCents    int64  `json:"actual_fees_cents"`
	UnknownFeeCount    int    `json:"unknown_fee_count"`
}

// getReceiptCosts: só o dono. Período por data de registro do recebimento (padrão: últimos 30 dias).
func (s *Service) getReceiptCosts(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Owner(r, estID); err != nil {
		return err
	}
	to := s.Clock.Now()
	from := to.AddDate(0, 0, -30)
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return hs.ErrValidation(hs.FieldError{Field: "/query/from", Code: hs.FieldInvalidFormat})
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return hs.ErrValidation(hs.FieldError{Field: "/query/to", Code: hs.FieldInvalidFormat})
		}
		to = t
	}
	if !from.Before(to) || to.Sub(from) > 400*24*time.Hour {
		return hs.ErrValidation(hs.FieldError{Field: "/query/from", Code: hs.FieldOutOfRange, Message: "período de até 400 dias"})
	}
	ctx := r.Context()
	view := receiptCostsView{From: from, To: to, ByMethod: []costRow{},
		Note: "Recebimentos menos estornos e custos de recebimento conhecidos ou estimados. Não é lucro: despesas, salários e outros custos não entram."}
	rows, err := s.Pool.Query(ctx, `
		SELECT CASE WHEN r.method = 'card' THEN coalesce(r.card_type, 'card_unspecified') ELSE r.method END AS m,
		       sum(r.amount_cents), count(*),
		       coalesce(sum(f.estimated_cents) FILTER (WHERE f.status = 'estimated'), 0),
		       coalesce(sum(f.actual_cents) FILTER (WHERE f.status = 'actual'), 0),
		       count(*) FILTER (WHERE f.receipt_id IS NULL AND r.method <> 'cash'),
		       count(*) FILTER (WHERE f.status = 'not_configured')
		FROM receipt r LEFT JOIN receipt_fee f ON f.receipt_id = r.id
		WHERE r.establishment_id = $1 AND r.registered_at >= $2 AND r.registered_at <= $3
		GROUP BY 1 ORDER BY 1`, estID, from, to)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c costRow
		var notConf int
		if err := rows.Scan(&c.Method, &c.GrossCents, &c.Count, &c.EstimatedFeesCents, &c.ActualFeesCents, &c.UnknownFeeCount, &notConf); err != nil {
			rows.Close()
			return err
		}
		c.UnknownFeeCount += notConf
		view.ByMethod = append(view.ByMethod, c)
		view.GrossCents += c.GrossCents
		view.ReceiptCount += c.Count
		view.EstimatedFeesCents += c.EstimatedFeesCents
		view.ActualFeesCents += c.ActualFeesCents
		view.UnknownFeeCount += c.UnknownFeeCount - notConf
		view.NotConfiguredCount += notConf
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Estornos confirmados no período. A taxa do recebimento estornado NÃO é tirada de novo do
	// custo (ela já foi contada no recebimento); devolução de taxa só entra quando informada.
	if err := s.Pool.QueryRow(ctx, `
		SELECT coalesce(sum(rf.amount_cents), 0), coalesce(sum(rf.fee_returned_cents), 0),
		       count(*) FILTER (WHERE rf.fee_returned_cents IS NULL AND f.status IN ('estimated', 'actual'))
		FROM refund rf LEFT JOIN receipt_fee f ON f.receipt_id = rf.receipt_id
		WHERE rf.establishment_id = $1 AND rf.status = 'confirmed' AND rf.resolved_at >= $2 AND rf.resolved_at <= $3`,
		estID, from, to).Scan(&view.RefundedCents, &view.FeeReturnedCents, &view.RefundsUnknownFee); err != nil {
		return err
	}
	view.AfterCostsCents = view.GrossCents - view.RefundedCents - view.EstimatedFeesCents - view.ActualFeesCents + view.FeeReturnedCents
	view.Complete = view.UnknownFeeCount == 0 && view.NotConfiguredCount == 0
	hs.WriteJSON(w, http.StatusOK, hs.Data[receiptCostsView]{Data: view})
	return nil
}
