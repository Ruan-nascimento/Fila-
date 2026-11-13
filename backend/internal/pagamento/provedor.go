package pagamento

import (
	"context"
	"errors"
	"time"
)

// Situação de uma cobrança no provedor (AbacatePay, campo status).
const (
	ProviderPending  = "PENDING"
	ProviderPaid     = "PAID"
	ProviderExpired  = "EXPIRED"
	ProviderCanceled = "CANCELLED"
	ProviderFailed   = "FAILED"
	ProviderRefunded = "REFUNDED"
	ProviderDisputed = "UNDER_DISPUTE"
)

// Erros do adaptador, por consequência para quem chama:
var (
	// ErrAmbiguous: o pedido pode ter chegado e criado a cobrança (tempo esgotado depois de
	// enviar, conexão caiu no meio, erro 5xx). NUNCA criar outra cobrança às cegas: reconciliar.
	ErrAmbiguous = errors.New("resultado desconhecido no provedor")
	// ErrNotSent: o pedido não chegou a ser processado (não conectou, 429). Seguro tentar de novo.
	ErrNotSent = errors.New("pedido não chegou ao provedor")
	// ErrRejected: o provedor recusou de forma definitiva (4xx); nenhuma cobrança foi criada.
	ErrRejected = errors.New("pedido recusado pelo provedor")
	// ErrNotFound: consulta não encontrou a cobrança.
	ErrNotFound = errors.New("cobrança não encontrada no provedor")
	// ErrIntegrity: a resposta não confere com o pedido (valor, ambiente, vínculo). Não confiar.
	ErrIntegrity = errors.New("resposta do provedor não confere")
)

// CreatePix é o pedido de cobrança Pix. ExternalID é o id da nossa tentativa: o provedor devolve
// a mesma cobrança pendente se receber o mesmo ExternalID de novo (garantia remota).
type CreatePix struct {
	ExternalID  string
	AmountCents int64
	ExpiresIn   time.Duration
	Description string
	Metadata    map[string]string // só identificadores; nunca dado pessoal
}

// Charge é a cobrança como o provedor a descreve.
type Charge struct {
	ProviderID  string
	Status      string
	AmountCents int64
	BRCode      string
	ExpiresAt   *time.Time
	DevMode     bool
	Metadata    map[string]string
}

// Capabilities registra o que o provedor realmente suporta (docs/pagamentos-abacatepay.md).
type Capabilities struct {
	CancelPix     bool // há rota para cancelar Pix pendente?
	RefundPix     bool // há estorno de Pix pela API (integral)?
	PartialRefund bool
	MinPixCents   int64
}

// RefundResult é a resposta do pedido de estorno. Não é a confirmação: o estorno só vale como
// confirmado quando a cobrança aparece REFUNDED (consulta autenticada ou webhook).
type RefundResult struct {
	ProviderRefundID string
	Status           string
	AmountCents      int64
}

// Provider é o adaptador do provedor de Pix (isolado atrás desta interface; testes de contrato
// usam fixtures sem segredo).
type Provider interface {
	CreatePix(ctx context.Context, in CreatePix) (Charge, error)
	// GetPix consulta a cobrança pelo id do provedor (consulta AUTENTICADA: evidência confiável).
	GetPix(ctx context.Context, providerID string) (Charge, error)
	// FindPix procura cobranças criadas com o ExternalID (reconciliar resultado desconhecido).
	FindPix(ctx context.Context, externalID string) ([]Charge, error)
	// RefundPix pede o estorno INTEGRAL da cobrança. Repetir para cobrança já estornada não cria
	// outro estorno (o provedor recusa): seguro repetir depois de resultado desconhecido.
	RefundPix(ctx context.Context, providerID, reason string) (RefundResult, error)
	Capabilities() Capabilities
	// TestMode informa se a chave é de teste (nada é cobrado de verdade).
	TestMode() bool
}
