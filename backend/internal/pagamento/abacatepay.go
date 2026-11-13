package pagamento

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"fila/internal/platform/config"
)

// AbacatePay é o adaptador da API v2 da AbacatePay (checkout transparente Pix), conforme a
// documentação oficial consultada em 2026-11-13 (docs/pagamentos-abacatepay.md):
//   - POST /v2/transparents/create  {method: "PIX", data: {amount (centavos, mín. 100), expiresIn (s, mín. 300),
//     externalId, description, metadata}} → {data: {id "pix_char_…", status, amount, brCode, expiresAt, devMode…}}
//   - GET  /v2/transparents/list?id=… | ?externalId=…  → {data: [ … ], pagination}
//   - Autenticação: "Authorization: Bearer <chave>"; ambiente vem da chave (abc_dev_ teste, abc_prod_ produção).
//   - Sem rota de cancelamento de Pix (só expira). Sem cabeçalho de idempotência: a garantia remota
//     documentada é "se uma cobrança Pix pendente já tem o mesmo externalId, a API devolve ela".
type AbacatePay struct {
	baseURL string
	key     config.Secret
	client  *http.Client
	test    bool
}

// NewAbacatePay cria o adaptador. A chave nunca sai do backend nem aparece em log/erro.
func NewAbacatePay(c config.PaymentsConfig) *AbacatePay {
	return &AbacatePay{
		baseURL: strings.TrimRight(c.BaseURL, "/"),
		key:     c.APIKey,
		client:  &http.Client{Timeout: c.Timeout},
		test:    strings.HasPrefix(c.APIKey.Reveal(), "abc_dev_"),
	}
}

// Capabilities: o que a documentação oficial confirma.
func (a *AbacatePay) Capabilities() Capabilities {
	return Capabilities{CancelPix: false, PartialRefund: false, MinPixCents: 100}
}

// TestMode informa se a chave é de teste.
func (a *AbacatePay) TestMode() bool { return a.test }

type envelope struct {
	Data    json.RawMessage `json:"data"`
	Success bool            `json:"success"`
	Error   *string         `json:"error"`
}

type pixQRCode struct {
	ID        string            `json:"id"`
	Amount    int64             `json:"amount"`
	Status    string            `json:"status"`
	DevMode   bool              `json:"devMode"`
	BRCode    string            `json:"brCode"`
	ExpiresAt *time.Time        `json:"expiresAt"`
	Metadata  map[string]string `json:"metadata"`
}

func (p pixQRCode) charge() Charge {
	return Charge{ProviderID: p.ID, Status: p.Status, AmountCents: p.Amount, BRCode: p.BRCode, ExpiresAt: p.ExpiresAt,
		DevMode: p.DevMode, Metadata: p.Metadata}
}

// do envia a requisição e classifica a falha. read=true para consultas (5xx em consulta não
// é ambíguo: nada foi criado).
func (a *AbacatePay) do(ctx context.Context, method, path string, body any, read bool) (*envelope, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.key.Reveal())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, fmt.Errorf("%w: não conectou", ErrNotSent)
		}
		if read {
			return nil, fmt.Errorf("%w: consulta falhou", ErrNotSent)
		}
		// Tempo esgotado ou conexão caiu depois de enviar: a cobrança pode existir.
		return nil, fmt.Errorf("%w: sem resposta", ErrAmbiguous)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil && !read {
		return nil, fmt.Errorf("%w: resposta interrompida", ErrAmbiguous)
	}
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: limite de requisições (429)", ErrNotSent)
	case res.StatusCode >= 500:
		if read {
			return nil, fmt.Errorf("%w: erro %d na consulta", ErrNotSent, res.StatusCode)
		}
		return nil, fmt.Errorf("%w: erro %d do provedor", ErrAmbiguous, res.StatusCode)
	case res.StatusCode >= 400:
		// Mensagem do provedor fica fora do erro: pode conter dados do pedido.
		return nil, fmt.Errorf("%w: status %d", ErrRejected, res.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if read {
			return nil, fmt.Errorf("%w: resposta ilegível", ErrNotSent)
		}
		return nil, fmt.Errorf("%w: resposta ilegível", ErrAmbiguous)
	}
	if !env.Success {
		return nil, fmt.Errorf("%w: success=false", ErrRejected)
	}
	return &env, nil
}

// CreatePix cria a cobrança Pix (POST /transparents/create).
func (a *AbacatePay) CreatePix(ctx context.Context, in CreatePix) (Charge, error) {
	data := map[string]any{"amount": in.AmountCents, "expiresIn": int64(in.ExpiresIn / time.Second),
		"externalId": in.ExternalID, "metadata": in.Metadata}
	if in.Description != "" {
		data["description"] = in.Description
	}
	env, err := a.do(ctx, http.MethodPost, "/transparents/create", map[string]any{"method": "PIX", "data": data}, false)
	if err != nil {
		return Charge{}, err
	}
	var p pixQRCode
	if err := json.Unmarshal(env.Data, &p); err != nil || p.ID == "" {
		return Charge{}, fmt.Errorf("%w: cobrança sem id", ErrAmbiguous)
	}
	c := p.charge()
	if err := a.check(c, in.AmountCents, in.ExternalID); err != nil {
		return c, err
	}
	return c, nil
}

// check confere a resposta com o pedido: ambiente certo, valor certo e vínculo com a tentativa.
func (a *AbacatePay) check(c Charge, amount int64, externalID string) error {
	switch {
	case c.DevMode != a.test:
		return fmt.Errorf("%w: ambiente da cobrança diferente do da chave", ErrIntegrity)
	case amount > 0 && c.AmountCents != amount:
		return fmt.Errorf("%w: valor diferente do pedido", ErrIntegrity)
	case externalID != "" && c.Metadata["charge_id"] != "" && c.Metadata["charge_id"] != externalID:
		return fmt.Errorf("%w: cobrança de outra tentativa", ErrIntegrity)
	}
	return nil
}

func (a *AbacatePay) list(ctx context.Context, q url.Values) ([]Charge, error) {
	env, err := a.do(ctx, http.MethodGet, "/transparents/list?"+q.Encode(), nil, true)
	if err != nil {
		return nil, err
	}
	var items []pixQRCode
	if err := json.Unmarshal(env.Data, &items); err != nil {
		return nil, fmt.Errorf("%w: lista ilegível", ErrNotSent)
	}
	out := make([]Charge, 0, len(items))
	for _, it := range items {
		c := it.charge()
		if c.DevMode != a.test {
			return nil, fmt.Errorf("%w: ambiente da cobrança diferente do da chave", ErrIntegrity)
		}
		out = append(out, c)
	}
	return out, nil
}

// GetPix consulta a cobrança pelo id (GET /transparents/list?id=…: traz valor, status e metadados;
// /transparents/check só traz status e validade).
func (a *AbacatePay) GetPix(ctx context.Context, providerID string) (Charge, error) {
	list, err := a.list(ctx, url.Values{"id": {providerID}, "limit": {"1"}})
	if err != nil {
		return Charge{}, err
	}
	for _, c := range list {
		if c.ProviderID == providerID {
			return c, nil
		}
	}
	return Charge{}, ErrNotFound
}

// FindPix procura as cobranças criadas com o externalId (GET /transparents/list?externalId=…).
func (a *AbacatePay) FindPix(ctx context.Context, externalID string) ([]Charge, error) {
	return a.list(ctx, url.Values{"externalId": {externalID}, "method": {"PIX"}, "limit": {"10"}})
}

var _ Provider = (*AbacatePay)(nil)
