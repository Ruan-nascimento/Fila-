package pagamento

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fila/internal/platform/config"
)

// Testes de contrato do adaptador: as respostas abaixo seguem o formato documentado pela
// AbacatePay (API v2, checkout transparente Pix) e não contêm segredo nenhum.
const fixtureKey = "abc_dev_" + "fixture-sem-segredo" // gitleaks:allow

func fixtureCharge(id string, amount int64, status string, devMode bool, chargeID string) string {
	return `{"data":{"id":"` + id + `","amount":` + itoa(amount) + `,"status":"` + status + `","devMode":` + btoa(devMode) +
		`,"brCode":"00020101021226950014br.gov.bcb.pix2573fixture","brCodeBase64":"data:image/png;base64,iVBORw0KGgo=",` +
		`"platformFee":80,"receiptUrl":null,"createdAt":"2026-11-13T12:00:00.000Z","updatedAt":"2026-11-13T12:00:00.000Z",` +
		`"expiresAt":"2026-11-13T12:15:00.000Z","metadata":{"charge_id":"` + chargeID + `"}},"error":null,"success":true}`
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
func btoa(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func adapter(t *testing.T, h http.HandlerFunc, timeout time.Duration) *AbacatePay {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewAbacatePay(config.PaymentsConfig{APIKey: config.Secret(fixtureKey), BaseURL: srv.URL + "/v2", Timeout: timeout})
}

func TestAbacatePayCreatePixRequestAndResponse(t *testing.T) {
	var got map[string]any
	a := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/transparents/create" {
			t.Errorf("rota: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+fixtureKey || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("cabeçalhos: %v", r.Header)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = io.WriteString(w, fixtureCharge("pix_char_abc123xyz", 7550, "PENDING", true, "tentativa-1"))
	}, 5*time.Second)
	c, err := a.CreatePix(context.Background(), CreatePix{ExternalID: "tentativa-1", AmountCents: 7550, ExpiresIn: 15 * time.Minute,
		Metadata: map[string]string{"charge_id": "tentativa-1", "order_id": "o1"}})
	if err != nil {
		t.Fatal(err)
	}
	data := got["data"].(map[string]any)
	if got["method"] != "PIX" || data["amount"] != float64(7550) || data["expiresIn"] != float64(900) || data["externalId"] != "tentativa-1" {
		t.Fatalf("corpo enviado: %v", got)
	}
	if c.ProviderID != "pix_char_abc123xyz" || c.Status != ProviderPending || c.AmountCents != 7550 || c.BRCode == "" || c.ExpiresAt == nil || !c.DevMode {
		t.Fatalf("cobrança lida: %+v", c)
	}
	if a.Capabilities().CancelPix || a.Capabilities().MinPixCents != 100 || !a.TestMode() {
		t.Fatal("capacidades registradas erradas")
	}
}

func TestAbacatePayErrorClassification(t *testing.T) {
	cases := map[string]struct {
		h       http.HandlerFunc
		timeout time.Duration
		want    error
	}{
		"5xx na criação é ambíguo": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, time.Second, ErrAmbiguous},
		"tempo esgotado depois de enviar é ambíguo": {func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(400 * time.Millisecond)
			_, _ = io.WriteString(w, fixtureCharge("pix_char_x", 7550, "PENDING", true, ""))
		}, 100 * time.Millisecond, ErrAmbiguous},
		"400 é recusa definitiva": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"data":null,"success":false,"error":"expiresIn must be at least 300 seconds"}`)
		}, time.Second, ErrRejected},
		"429 não foi processado": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }, time.Second, ErrNotSent},
		"success=false é recusa": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":null,"success":false,"error":"x"}`)
		}, time.Second, ErrRejected},
		"resposta ilegível é ambígua": {func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `<html>`) }, time.Second, ErrAmbiguous},
		"ambiente de produção com chave de teste": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, fixtureCharge("pix_char_x", 7550, "PENDING", false, ""))
		}, time.Second, ErrIntegrity},
		"valor diferente do pedido": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, fixtureCharge("pix_char_x", 755, "PENDING", true, ""))
		}, time.Second, ErrIntegrity},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a := adapter(t, c.h, c.timeout)
			_, err := a.CreatePix(context.Background(), CreatePix{ExternalID: "t", AmountCents: 7550, ExpiresIn: 15 * time.Minute})
			if !errors.Is(err, c.want) {
				t.Fatalf("erro %v, esperado %v", err, c.want)
			}
			if strings.Contains(err.Error(), fixtureKey) || strings.Contains(err.Error(), "expiresIn") {
				t.Fatal("erro expõe chave ou mensagem do provedor")
			}
		})
	}
	// Servidor fora do ar: o pedido nem saiu (seguro tentar de novo).
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	a := NewAbacatePay(config.PaymentsConfig{APIKey: config.Secret(fixtureKey), BaseURL: "http://" + addr + "/v2", Timeout: time.Second})
	if _, err := a.CreatePix(context.Background(), CreatePix{ExternalID: "t", AmountCents: 100}); !errors.Is(err, ErrNotSent) {
		t.Fatalf("sem conexão: %v", err)
	}
}

func TestAbacatePayQueries(t *testing.T) {
	a := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/transparents/list" {
			t.Errorf("rota: %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		switch {
		case q.Get("id") == "pix_char_pago":
			_, _ = io.WriteString(w, `{"data":[`+strings.TrimSuffix(strings.TrimPrefix(fixtureCharge("pix_char_pago", 7550, "PAID", true, "t1"), `{"data":`), `,"error":null,"success":true}`)+`],"pagination":{"hasMore":false,"next":null,"before":null},"error":null,"success":true}`)
		case q.Get("externalId") == "t1" && q.Get("method") == "PIX":
			_, _ = io.WriteString(w, `{"data":[],"pagination":{"hasMore":false,"next":null,"before":null},"error":null,"success":true}`)
		case q.Get("id") == "pix_char_erro":
			w.WriteHeader(503)
		default:
			_, _ = io.WriteString(w, `{"data":[],"error":null,"success":true}`)
		}
	}, time.Second)
	c, err := a.GetPix(context.Background(), "pix_char_pago")
	if err != nil || c.Status != ProviderPaid || c.AmountCents != 7550 || c.Metadata["charge_id"] != "t1" {
		t.Fatalf("consulta: %+v %v", c, err)
	}
	if list, err := a.FindPix(context.Background(), "t1"); err != nil || len(list) != 0 {
		t.Fatalf("busca por externalId: %v %v", list, err)
	}
	if _, err := a.GetPix(context.Background(), "pix_char_nada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("não encontrada: %v", err)
	}
	if _, err := a.GetPix(context.Background(), "pix_char_erro"); !errors.Is(err, ErrNotSent) {
		t.Fatalf("5xx em consulta (nada foi criado): %v", err)
	}
}

// Estorno (POST /transparents/refund): corpo {id, reason}; resposta documentada {data: {id
// "tran_…", status "COMPLETE", amount}}; já estornada responde 400 (recusa, sem novo estorno).
func TestAbacatePayRefund(t *testing.T) {
	var got map[string]any
	a := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/transparents/refund" {
			t.Errorf("rota: %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		if got["id"] == "pix_char_ja" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"data":null,"success":false,"error":"Esta cobrança já foi reembolsada."}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"id":"tran_abc123xyz","status":"COMPLETE","amount":7500,"reason":"duplicado",`+
			`"originalId":"pix_char_abc","createdAt":"2026-11-13T14:30:00.000Z"},"error":null,"success":true}`)
	}, 5*time.Second)
	res, err := a.RefundPix(context.Background(), "pix_char_abc", strings.Repeat("m", 600))
	if err != nil || res.ProviderRefundID != "tran_abc123xyz" || res.AmountCents != 7500 || res.Status != "COMPLETE" {
		t.Fatalf("estorno: %+v %v", res, err)
	}
	if got["id"] != "pix_char_abc" || len([]rune(got["reason"].(string))) != 500 || len(got) != 2 {
		t.Fatalf("corpo enviado: %v", got)
	}
	if _, err := a.RefundPix(context.Background(), "pix_char_ja", ""); !errors.Is(err, ErrRejected) {
		t.Fatalf("já estornada deveria ser recusa: %v", err)
	}
	if !a.Capabilities().RefundPix || a.Capabilities().PartialRefund {
		t.Fatal("capacidades de estorno")
	}
	slow := adapter(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }, 50*time.Millisecond)
	if _, err := slow.RefundPix(context.Background(), "pix_char_abc", ""); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("tempo esgotado no estorno deve ser ambíguo: %v", err)
	}
}
