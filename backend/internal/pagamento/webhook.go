package pagamento

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
)

// Webhook da AbacatePay (docs/pagamentos-abacatepay.md, ADR-0024), conforme a documentação
// oficial "Segurança dos webhooks":
//   - ORIGEM: o secret cadastrado no webhook chega no cabeçalho X-Webhook-Secret (e na query
//     webhookSecret); comparado em tempo constante. É ele que prova que o aviso veio da AbacatePay.
//   - INTEGRIDADE: X-Webhook-Signature = base64(HMAC-SHA256(corpo bruto)) com a chave PÚBLICA da
//     AbacatePay (a mesma para todas as contas, publicada nos exemplos da documentação). Não é
//     segredo: prova só que o corpo chegou intacto.
// Falhou qualquer um dos dois → 401 (a AbacatePay não repete 401) e nada é reconhecido; se o
// aviso cita uma cobrança nossa, o worker faz a consulta autenticada já (registra só o que ela
// comprovar).

// abacatePayWebhookKey é a chave PÚBLICA de assinatura publicada pela AbacatePay (não é segredo).
const abacatePayWebhookKey = "t9dXRhHHo3yDEj5pVDYz0frf7q6bMKyMRmxxCPIPp3RCplBfXRxqlC6ZpiWmOqj4L63qEaeUOtrCI8P0VMUgo6iIga2ri9ogaHFs0WIIywSMg0q7RmBfybe1E5XJcfC4IW3alNqym0tXoAKkzvfEjZxV6bE0oG2zJrNNYmUCKZyV0KZ3JS8Votf9EAWWYdiDkMkpbMdPggfh1EqHlVkMiTady6jOR3hyzGEHrIz2Ret0xHKMbiqkr9HS1JhNHDX9" // gitleaks:allow (chave pública documentada)

const maxWebhookBody = 256 << 10

// SignWebhook calcula a assinatura documentada (usado nos testes e no simulador local).
func SignWebhook(body []byte) string {
	mac := hmac.New(sha256.New, []byte(abacatePayWebhookKey))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// webhookEnvelope: só os campos usados. Dados do pagador (nome, e-mail, documento) são
// ignorados e nunca gravados.
type webhookEnvelope struct {
	ID         string `json:"id"`
	Event      string `json:"event"`
	APIVersion *int   `json:"apiVersion"`
	DevMode    *bool  `json:"devMode"`
	Data       struct {
		Transparent *struct {
			ID       string            `json:"id"`
			Amount   int64             `json:"amount"`
			Status   string            `json:"status"`
			DevMode  *bool             `json:"devMode"`
			Metadata map[string]string `json:"metadata"`
		} `json:"transparent"`
		Payer *struct {
			Method string `json:"method"`
		} `json:"payerInformation"`
	} `json:"data"`
}

var errWebhookUnauthenticated = &hs.Error{Status: 401, Code: "webhook_unauthenticated", Title: "Aviso não autenticado",
	Detail: "Secret ou assinatura do webhook não conferem."}

// receiveAbacatePayWebhook: persiste e aplica o aviso numa transação ANTES de responder 200.
func (s *Service) receiveAbacatePayWebhook(w http.ResponseWriter, r *http.Request) error {
	secret := s.WebhookSecret.Reveal()
	if s.Provider == nil || secret == "" {
		return hs.ErrNotFound() // webhook desligado: só a consulta do worker confirma
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		return &hs.Error{Status: 413, Code: "payload_too_large", Title: "Aviso grande demais", Detail: "Corpo acima do limite."}
	}
	got := r.Header.Get("X-Webhook-Secret")
	if got == "" {
		got = r.URL.Query().Get("webhookSecret")
	}
	secretOK := subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
	sigOK := hmac.Equal([]byte(r.Header.Get("X-Webhook-Signature")), []byte(SignWebhook(body)))
	var env webhookEnvelope
	parsed := json.Unmarshal(body, &env) == nil
	ctx := r.Context()
	now := s.Clock.Now()
	sum := sha256.Sum256(body)
	if !secretOK || !sigOK {
		s.Log.Warn("webhook não autenticado recusado", "secret_ok", secretOK, "assinatura_ok", sigOK)
		if parsed && env.Data.Transparent != nil && env.Data.Transparent.ID != "" {
			if err := s.scheduleCheckFromUnauthenticated(ctx, env, sum[:], now); err != nil {
				return err
			}
		}
		return errWebhookUnauthenticated
	}
	if !parsed || env.ID == "" || env.Event == "" || len(env.ID) > 100 || len(env.Event) > 60 {
		return hs.ErrValidation(hs.FieldError{Field: "/body", Code: hs.FieldInvalidFormat, Message: "aviso ilegível"})
	}
	duplicate := false
	err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		// 1) Deduplicação: o mesmo aviso (mesmo id, inclusive retentativas) só uma vez. Dois avisos
		// iguais ao mesmo tempo: o segundo espera o primeiro e vira duplicata.
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO provider_event (provider, event_id, event_type, authenticated, dev_mode, body_sha256, received_at, outcome)
			VALUES ('abacatepay', $1, $2, true, $3, $4, $5, 'ignored')
			ON CONFLICT (provider, event_id) WHERE authenticated DO NOTHING RETURNING id`,
			env.ID, env.Event, env.DevMode, sum[:], now).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			duplicate = true
			return nil
		}
		if err != nil {
			return err
		}
		// 2) Efeito (recebimento, estorno, pendência) na MESMA transação: só responde 200 depois de gravar.
		outcome, chargeID, status, amount := s.applyWebhook(ctx, tx, env, now)
		if outcome.err != nil {
			return outcome.err
		}
		_, err = tx.Exec(ctx, `UPDATE provider_event SET outcome = $2, provider_charge_id = $3, charge_status = $4, amount_cents = $5
			WHERE id = $1`, id, outcome.name, chargeID, status, amount)
		return err
	})
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, map[string]any{"received": true, "duplicate": duplicate})
	return nil
}

type webhookOutcome struct {
	name string
	err  error
}

// applyWebhook confere o aviso (ambiente, cobrança, pedido, método) e aplica o efeito. Devolve
// o resultado para o registro do aviso. Um aviso que não confere não muda nada.
func (s *Service) applyWebhook(ctx context.Context, tx pgx.Tx, env webhookEnvelope, now time.Time) (webhookOutcome, *string, *string, *int64) {
	t := env.Data.Transparent
	if t == nil || t.ID == "" || len(t.ID) > 100 {
		return webhookOutcome{name: "ignored"}, nil, nil, nil
	}
	chargeID, status, amount := &t.ID, &t.Status, &t.Amount
	if len(t.Status) > 30 {
		status = nil
	}
	// Ambiente (destinatário): aviso de produção numa instalação de teste (ou o contrário) não vale.
	if (env.DevMode != nil && *env.DevMode != s.Provider.TestMode()) || (t.DevMode != nil && *t.DevMode != s.Provider.TestMode()) ||
		(env.APIVersion != nil && *env.APIVersion != 2) {
		s.Log.Warn("webhook de outro ambiente ou versão ignorado", "evento", env.Event)
		return webhookOutcome{name: "mismatch"}, chargeID, status, amount
	}
	if env.Data.Payer != nil && env.Data.Payer.Method != "" && env.Data.Payer.Method != "PIX" {
		return webhookOutcome{name: "ignored"}, chargeID, status, amount
	}
	c, ok, err := s.chargeForWebhook(ctx, tx, t.ID, t.Metadata)
	if err != nil {
		return webhookOutcome{err: err}, nil, nil, nil
	}
	if !ok {
		s.Log.Warn("webhook de cobrança desconhecida", "evento", env.Event)
		return webhookOutcome{name: "unknown_charge"}, chargeID, status, amount
	}
	// Pedido: os metadados que enviamos na criação precisam apontar para a mesma comanda e loja.
	if t.Metadata["charge_id"] != c.ID.String() || t.Metadata["order_id"] != c.OrderID.String() ||
		t.Metadata["establishment_id"] != c.EstablishmentID.String() {
		s.Log.Warn("webhook não confere com a cobrança (pedido/loja)", "tentativa", c.ID.String())
		return webhookOutcome{name: "mismatch"}, chargeID, status, amount
	}
	lp, err := atendimento.LockForPayment(ctx, tx, c.EstablishmentID, c.OrderID)
	if err != nil {
		return webhookOutcome{err: err}, nil, nil, nil
	}
	cur, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE id = $1 FOR UPDATE`, c.ID))
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
			err = s.paid(ctx, tx, lp, &cur, pc, "provider_webhook", now) // tardio: registra (excesso)
		} else {
			err = s.apply(ctx, tx, lp, &cur, pc, "provider_webhook", now)
		}
	case env.Event == "transparent.refunded" && t.Status == ProviderRefunded:
		// Fora de ordem (estorno antes do pagamento): REFUNDED implica que foi pago; registra o
		// recebimento primeiro (evidência do mesmo aviso autenticado) e depois o estorno.
		if receiptsBefore == 0 {
			pc.Status = ProviderPaid
			if err = s.paid(ctx, tx, lp, &cur, pc, "provider_webhook", now); err != nil {
				return webhookOutcome{err: err}, nil, nil, nil
			}
		}
		var changed bool
		changed, err = s.refundConfirmedByProvider(ctx, tx, lp, cur.ID, now)
		if err == nil && !changed && receiptsBefore > 0 {
			return webhookOutcome{name: "duplicate_effect"}, chargeID, status, amount
		}
		if err == nil {
			return webhookOutcome{name: "applied"}, chargeID, status, amount
		}
	default:
		// Disputa (MED) e outros: não há tratamento automático no MVP; fica registrado e logado.
		s.Log.Warn("aviso do provedor sem tratamento automático", "evento", env.Event, "tentativa", cur.ID.String())
		return webhookOutcome{name: "ignored"}, chargeID, status, amount
	}
	if errors.Is(err, ErrIntegrity) {
		s.Log.Warn("webhook não confere com a cobrança", "tentativa", cur.ID.String(), "erro", err.Error())
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

// chargeForWebhook acha a nossa cobrança pelo id do provedor ou, se a criação ainda não foi
// gravada (resultado desconhecido), pelo id da tentativa nos metadados.
func (s *Service) chargeForWebhook(ctx context.Context, q database.Querier, providerID string, meta map[string]string) (chargeRow, bool, error) {
	c, err := scanCharge(q.QueryRow(ctx, chargeSelect+` WHERE provider_charge_id = $1`, providerID))
	if err == nil {
		return c, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return c, false, err
	}
	id, perr := uuid.Parse(meta["charge_id"])
	if perr != nil {
		return c, false, nil
	}
	c, err = scanCharge(q.QueryRow(ctx, chargeSelect+` WHERE id = $1 AND provider_charge_id IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

// scheduleCheckFromUnauthenticated: o aviso não autenticado não vale como evidência, mas, se cita
// uma cobrança nossa ainda em aberto, antecipa a consulta autenticada do worker. Só grava algo
// quando a cobrança existe (aviso inventado para id desconhecido não ocupa o banco).
func (s *Service) scheduleCheckFromUnauthenticated(ctx context.Context, env webhookEnvelope, sum []byte, now time.Time) error {
	t := env.Data.Transparent
	if len(t.ID) > 100 {
		return nil
	}
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE pix_charge SET next_check_at = $2
			WHERE provider_charge_id = $1 AND status IN ('creating', 'pending', 'unknown') RETURNING id`, t.ID, now).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		eventID, eventType := env.ID, env.Event
		if eventID == "" || len(eventID) > 100 {
			eventID = "sem-id"
		}
		if eventType == "" || len(eventType) > 60 {
			eventType = "desconhecido"
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO provider_event (provider, event_id, event_type, authenticated, provider_charge_id, body_sha256, received_at, outcome)
			VALUES ('abacatepay', $1, $2, false, $3, $4, $5, 'check_scheduled')`, eventID, eventType, t.ID, sum, now)
		return err
	})
}
