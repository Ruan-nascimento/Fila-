// Package pagamentotest tem um simulador HTTP da API v2 da AbacatePay, SOMENTE para testes e
// E2E locais. Ele imita os formatos e comportamentos documentados (envelope {data, success,
// error}, Bearer, externalId que devolve a cobrança pendente existente, sem cancelamento de Pix)
// para exercitar o adaptador real (HTTP, tempo esgotado, 5xx). Não prova compatibilidade com a
// AbacatePay de verdade: isso exige a validação no ambiente de teste deles
// (docs/pagamentos-abacatepay.md). Nunca é ligado pela API ou pelo worker.
package pagamentotest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Modos de resposta para a criação.
const (
	CreateOK          = "ok"
	CreateSlow        = "slow"          // cria e só responde depois de Delay (cliente desiste: ambíguo)
	CreateErrAfter    = "error_after"   // cria e responde 500 (ambíguo)
	CreateDownBefore  = "down_before"   // 500 sem criar nada
	CreateReject      = "reject"        // 400: recusa definitiva
	CreateWrongAmount = "wrong_amount"  // responde com valor diferente (integridade)
	CreateWrongEnv    = "wrong_devmode" // responde como produção (integridade)
)

// Charge é a cobrança guardada pelo simulador.
type Charge struct {
	ID          string            `json:"id"`
	Amount      int64             `json:"amount"`
	Status      string            `json:"status"`
	DevMode     bool              `json:"devMode"`
	BRCode      string            `json:"brCode"`
	BRCodeB64   string            `json:"brCodeBase64"`
	PlatformFee int64             `json:"platformFee"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
	ExpiresAt   time.Time         `json:"expiresAt"`
	Metadata    map[string]string `json:"metadata"`
	externalID  string
}

// Fake é o simulador. Key é a chave esperada no Bearer.
type Fake struct {
	Key string

	mu       sync.Mutex
	charges  []*Charge
	mode     string
	delay    time.Duration
	listDown bool
	creates  int
	seq      int
	now      func() time.Time
}

// New cria o simulador com a chave de teste informada.
func New(key string, now func() time.Time) *Fake {
	if now == nil {
		now = time.Now
	}
	return &Fake{Key: key, mode: CreateOK, now: now}
}

// SetCreateMode muda a resposta das próximas criações (delay para CreateSlow).
func (f *Fake) SetCreateMode(mode string, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode, f.delay = mode, delay
}

// SetListDown faz as consultas responderem 503.
func (f *Fake) SetListDown(down bool) { f.mu.Lock(); f.listDown = down; f.mu.Unlock() }

// Creates conta os pedidos de criação recebidos (inclusive repetidos).
func (f *Fake) Creates() int { f.mu.Lock(); defer f.mu.Unlock(); return f.creates }

// Charges devolve cópias das cobranças existentes.
func (f *Fake) Charges() []Charge {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Charge, 0, len(f.charges))
	for _, c := range f.charges {
		out = append(out, *c)
	}
	return out
}

// Set muda a situação de uma cobrança (como se o pagador pagasse, ela expirasse etc.) e
// opcionalmente o valor pago.
func (f *Fake) Set(id, status string, amount int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.charges {
		if c.ID == id {
			c.Status, c.UpdatedAt = status, f.now()
			if amount > 0 {
				c.Amount = amount
			}
			return true
		}
	}
	return false
}

// ByExternalID devolve a cobrança criada para a tentativa (ou nil).
func (f *Fake) ByExternalID(ext string) *Charge {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.charges {
		if c.externalID == ext {
			cp := *c
			return &cp
		}
	}
	return nil
}

func write(w http.ResponseWriter, status int, data any, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var e any
	if errMsg != "" {
		e = errMsg
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "success": errMsg == "", "error": e})
}

// ServeHTTP atende /v2/transparents/create e /v2/transparents/list (e /transparents/… sem prefixo).
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.Key {
		write(w, 401, nil, "Unauthorized")
		return
	}
	path := r.URL.Path
	if len(path) > 3 && path[:3] == "/v2" {
		path = path[3:]
	}
	switch {
	case r.Method == http.MethodPost && path == "/transparents/create":
		f.create(w, r)
	case r.Method == http.MethodGet && path == "/transparents/list":
		f.list(w, r)
	default:
		write(w, 404, nil, "Not found")
	}
}

func (f *Fake) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Method string `json:"method"`
		Data   struct {
			Amount      int64             `json:"amount"`
			ExpiresIn   int64             `json:"expiresIn"`
			Description string            `json:"description"`
			ExternalID  string            `json:"externalId"`
			Metadata    map[string]string `json:"metadata"`
		} `json:"data"`
	}
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, &body); err != nil || body.Method != "PIX" || body.Data.Amount < 100 ||
		(body.Data.ExpiresIn != 0 && body.Data.ExpiresIn < 300) {
		write(w, 400, nil, "Invalid body")
		return
	}
	f.mu.Lock()
	f.creates++
	mode, delay := f.mode, f.delay
	if mode == CreateReject || mode == CreateDownBefore {
		f.mu.Unlock()
		if mode == CreateReject {
			write(w, 400, nil, "Rejected")
		} else {
			write(w, 500, nil, "Internal error")
		}
		return
	}
	// Documentado: cobrança Pix PENDENTE com o mesmo externalId é devolvida em vez de criar outra.
	var c *Charge
	for _, ex := range f.charges {
		if body.Data.ExternalID != "" && ex.externalID == body.Data.ExternalID && ex.Status == "PENDING" {
			c = ex
		}
	}
	if c == nil {
		f.seq++
		now := f.now()
		exp := body.Data.ExpiresIn
		if exp == 0 {
			exp = 86400
		}
		c = &Charge{ID: fmt.Sprintf("pix_char_fake%06d", f.seq), Amount: body.Data.Amount, Status: "PENDING", DevMode: true,
			BRCode:    "00020101021226950014br.gov.bcb.pix2573fake.teste/" + strconv.Itoa(f.seq) + "5204000053039865802BR6304ABCD",
			BRCodeB64: "data:image/png;base64,ZmFrZQ==", PlatformFee: 80, CreatedAt: now, UpdatedAt: now,
			ExpiresAt: now.Add(time.Duration(exp) * time.Second), Metadata: body.Data.Metadata, externalID: body.Data.ExternalID}
		f.charges = append(f.charges, c)
	}
	resp := *c
	f.mu.Unlock()
	switch mode {
	case CreateSlow:
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	case CreateErrAfter:
		write(w, 500, nil, "Internal error")
		return
	case CreateWrongAmount:
		resp.Amount++
	case CreateWrongEnv:
		resp.DevMode = false
	}
	write(w, 200, resp, "")
}

func (f *Fake) list(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listDown {
		write(w, 503, nil, "Unavailable")
		return
	}
	q := r.URL.Query()
	out := []Charge{}
	for _, c := range f.charges {
		if id := q.Get("id"); id != "" && c.ID != id {
			continue
		}
		if ext := q.Get("externalId"); ext != "" && c.externalID != ext {
			continue
		}
		if st := q.Get("status"); st != "" && c.Status != st {
			continue
		}
		out = append(out, *c)
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && len(out) > l {
		out = out[:l]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": out, "success": true, "error": nil,
		"pagination": map[string]any{"hasMore": false, "next": nil}})
}
