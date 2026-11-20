package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ResendBaseURL é o endereço oficial da API do Resend.
const ResendBaseURL = "https://api.resend.com"

// ResendSender entrega pelo Resend (POST /emails). Só o worker usa: a API apenas grava na caixa
// de saída (resposta pública com o mesmo tempo, exista ou não o e-mail).
//
//   - Idempotency-Key = "fila-mail/<id da mensagem>": uma nova tentativa da MESMA mensagem
//     (ex.: o worker caiu depois do aceite e antes de confirmar) é deduplicada pelo Resend dentro
//     da janela dele (24 h). Fora dela não há garantia de "uma vez só".
//   - Nada do conteúdo, destinatário ou resposta do provedor vai para erro ou log: só categorias.
//   - Sucesso = aceito pelo provedor, não entregue na caixa de entrada.
type ResendSender struct {
	APIKey  string
	From    string
	ReplyTo string
	// BaseURL: padrão ResendBaseURL (testes usam um servidor local).
	BaseURL string
	Client  *http.Client
}

// sendError é um erro de envio com categoria fixa e sem dados pessoais.
type sendError struct {
	category string
	status   int
}

func (e *sendError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("resend: %s (HTTP %d)", e.category, e.status)
	}
	return "resend: " + e.category
}
func (e *sendError) Category() string { return e.category }

// Send implementa Sender.
func (r ResendSender) Send(ctx context.Context, id uuid.UUID, m Message) error {
	_, err := r.SendReceipt(ctx, id, m)
	return err
}

// SendReceipt envia e devolve o id do Resend.
func (r ResendSender) SendReceipt(ctx context.Context, id uuid.UUID, m Message) (string, error) {
	body := map[string]any{
		"from":    r.From,
		"to":      []string{m.To},
		"subject": m.Subject,
		"text":    m.Body,
	}
	if r.ReplyTo != "" {
		body["reply_to"] = r.ReplyTo
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", &Permanent{Err: &sendError{category: "mensagem inválida"}}
	}
	base := r.BaseURL
	if base == "" {
		base = ResendBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/emails", bytes.NewReader(payload))
	if err != nil {
		return "", &sendError{category: "pedido inválido"}
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "fila-worker/1")
	req.Header.Set("Idempotency-Key", "fila-mail/"+id.String())

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		// Erro de rede pode carregar a URL, nunca o corpo; ainda assim só a categoria sai daqui.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &sendError{category: "tempo esgotado"}
		}
		return "", &sendError{category: "falha de rede"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	var parsed struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &parsed)

	switch s := resp.StatusCode; {
	case s >= 200 && s < 300:
		if parsed.ID == "" {
			// Aceito sem id: tratamos como aceito (reenviar arriscaria duplicar).
			return "", nil
		}
		return parsed.ID, nil
	case s == http.StatusTooManyRequests:
		return "", &sendError{category: "limite do provedor", status: s}
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		// Chave inválida, domínio não verificado ou remetente não autorizado: configuração.
		// Não é culpa da mensagem; tenta de novo (o operador corrige e as próximas tentativas saem).
		return "", &sendError{category: "credencial ou remetente recusado", status: s}
	case s == http.StatusConflict:
		if parsed.Name == "invalid_idempotent_request" {
			return "", &Permanent{Err: &sendError{category: "chave de idempotência em conflito", status: s}}
		}
		return "", &sendError{category: "envio concorrente em andamento", status: s}
	case s == http.StatusBadRequest || s == http.StatusUnprocessableEntity:
		return "", &Permanent{Err: &sendError{category: "recusado pelo provedor", status: s}}
	case s >= 500:
		return "", &sendError{category: "provedor indisponível", status: s}
	default:
		return "", &sendError{category: "resposta inesperada", status: s}
	}
}
