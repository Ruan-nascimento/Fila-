package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/api"
	"fila/internal/pagamento/pagamentotest"
	"fila/internal/platform/config"
)

// ---------------------------------------------------------------------------------------------
// Ajudantes da etapa 08

// payable: Maria (conta verificada) atendida por Beto com Corte + Barba → comanda aguardando
// pagamento de 7500 centavos.
func (e *env) payable(t *testing.T) (barbearia, *browser, map[string]any) {
	t.Helper()
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	p, o := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	order := maria.order(b.id, o["id"])
	if order["status"] != "awaiting_payment" || due(order) != 7500 {
		t.Fatalf("comanda inicial: %v", order)
	}
	return b, maria, order
}

func (b *browser) createPix(est uuid.UUID, order map[string]any, key string) resp {
	b.e.t.Helper()
	return b.do("POST", orderPath(est, order["id"])+"/pix-charges", map[string]any{"expected_version": num(order["version"])},
		"Idempotency-Key", key)
}

func (b *browser) choose(est uuid.UUID, order map[string]any, method string, ack bool) resp {
	b.e.t.Helper()
	body := map[string]any{"expected_version": num(order["version"]), "method": method}
	if ack {
		body["acknowledge_active_pix"] = true
	}
	return b.cmd("POST", orderPath(est, order["id"])+"/payment-method", body)
}

func (b *browser) receive(est uuid.UUID, order map[string]any, body map[string]any) resp {
	b.e.t.Helper()
	full := map[string]any{"expected_version": num(order["version"])}
	for k, v := range body {
		full[k] = v
	}
	return b.cmd("POST", orderPath(est, order["id"])+"/receipts", full)
}

func (b *browser) confer(est uuid.UUID, order map[string]any, receipt any) resp {
	b.e.t.Helper()
	return b.cmd("POST", orderPath(est, order["id"])+"/receipts/"+receipt.(string)+"/confer", map[string]any{})
}

// reconcile avança o relógio além da maior espera e roda uma rodada do worker de pagamentos.
func (e *env) reconcile() int {
	e.t.Helper()
	e.clock.Advance(5*time.Minute + time.Second)
	n, err := e.api.Payments.ReconcileOnce(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func payment(order map[string]any) map[string]any { return order["payment"].(map[string]any) }

func amountOf(order map[string]any, which string) int64 {
	return num(payment(order)["amounts"].(map[string]any)[which].(map[string]any)["amount_cents"])
}

func receiptsOf(order map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range payment(order)["receipts"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

func chargeStatus(e *env, id any) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM pix_charge WHERE id = $1`, id).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// ---------------------------------------------------------------------------------------------

// C-PAG-01, C-PAG-03 — o servidor cria o Pix com o valor da comanda (centavos exatos), vinculado
// à loja e à comanda; o corpo não aceita valor; "já paguei" e o navegador não confirmam nada.
func TestPixChargeCreatedByServer(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	path := orderPath(b.id, order["id"]) + "/pix-charges"
	// Total adulterado: qualquer valor no corpo é recusado (campo desconhecido).
	for _, body := range []map[string]any{
		{"expected_version": num(order["version"]), "amount_cents": 1},
		{"expected_version": num(order["version"]), "amount": map[string]any{"amount_cents": 1, "currency": "BRL"}},
	} {
		if r := maria.cmd("POST", path, body); r.status != 400 {
			t.Fatalf("valor no corpo aceito: %d %s", r.status, r.raw)
		}
	}
	if r := maria.do("POST", path, map[string]any{"expected_version": num(order["version"])}); r.code() != "idempotency_key_missing" {
		t.Fatalf("sem chave: %s", r.raw)
	}
	if r := maria.createPix(b.id, map[string]any{"id": order["id"], "version": float64(num(order["version"]) - 1)}, newIdemKey()); r.code() != "version_conflict" {
		t.Fatalf("versão velha: %s", r.raw)
	}
	r := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201)
	ch := r.data()
	if ch["status"] != "pending" || num(ch["amount"].(map[string]any)["amount_cents"]) != 7500 || ch["br_code"] == nil ||
		ch["expires_at"] == nil || ch["order_id"] != order["id"] {
		t.Fatalf("cobrança: %s", r.raw)
	}
	remote := e.pix.Charges()
	if len(remote) != 1 || remote[0].Amount != 7500 || remote[0].Metadata["charge_id"] != ch["id"] ||
		remote[0].Metadata["order_id"] != order["id"] || remote[0].Metadata["establishment_id"] != b.id.String() {
		t.Fatalf("no provedor: %+v", remote)
	}
	if e.count(t, `SELECT count(*) FROM pix_charge WHERE id = $1 AND establishment_id = $2 AND order_id = $3 AND test_mode`,
		ch["id"], b.id, order["id"]) != 1 {
		t.Fatal("cobrança fora da loja/comanda certa")
	}
	// Chave da API nunca aparece em resposta nem log.
	if strings.Contains(string(r.raw), "simulador-local") || strings.Contains(e.logs.String(), "simulador-local") {
		t.Fatal("chave do provedor vazou")
	}
	// "Já paguei": não há operação para o cliente; consultar não muda nada; o worker só confia no provedor.
	if r := maria.cmd("POST", path+"/"+ch["id"].(string)+"/confirm", map[string]any{}); r.status != 404 && r.status != 405 {
		t.Fatalf("rota de confirmação pelo cliente: %d", r.status)
	}
	got := maria.mustStatus(maria.do("GET", path+"/"+ch["id"].(string), nil), 200).data()
	e.reconcile()
	o := maria.order(b.id, order["id"])
	if got["status"] != "pending" || chargeStatus(e, ch["id"]) != "pending" || o["status"] != "awaiting_payment" ||
		len(receiptsOf(o)) != 0 || payment(o)["method_choice"] != "pix" || amountOf(o, "outstanding") != 7500 {
		t.Fatalf("depois de consultar: %v / %v", got, payment(o))
	}
	// Cliente não registra nem confere recebimento.
	if r := maria.receive(b.id, o, map[string]any{"method": "cash", "amount_cents": 7500, "cash_tendered_cents": 7500}); r.status != 403 {
		t.Fatalf("cliente registrando dinheiro: %d %s", r.status, r.raw)
	}
	if r := maria.cmd("POST", path+"/"+ch["id"].(string)+"/cancel", map[string]any{"reason": "quero cancelar"}); r.status != 403 {
		t.Fatalf("cliente cancelando Pix: %d", r.status)
	}
	// Outra pessoa, outro profissional, outra loja: 404 (não revela a comanda).
	joao := e.verifiedCustomer("joao@exemplo.com")
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	outra := e.shop("outra-loja", "bia@exemplo.com")
	for who, br := range map[string]*browser{"outro cliente": joao, "Caio": caio, "dona de outra loja": outra.owner} {
		if r := br.createPix(b.id, o, newIdemKey()); r.status != 404 {
			t.Fatalf("%s criando Pix: %d", who, r.status)
		}
		if r := br.do("GET", path+"/"+ch["id"].(string), nil); r.status != 404 {
			t.Fatalf("%s lendo Pix: %d", who, r.status)
		}
	}
	if r := outra.owner.createPix(outra.id, o, newIdemKey()); r.status != 404 {
		t.Fatalf("comanda por outra loja: %d", r.status)
	}
}

// C-PAG-02, C-PAG-09 — Pix pago (consulta autenticada ao provedor) → aguardando conferência,
// nunca volta a "não pago"; conferências simultâneas geram uma só conferência e um só "paga".
func TestPixPaidThenConferred(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	remote := e.pix.ByExternalID(ch["id"].(string))
	e.pix.Set(remote.ID, "PAID", 0)
	e.reconcile()
	e.reconcile() // repetir a consulta não duplica nada
	o := maria.order(b.id, order["id"])
	recs := receiptsOf(o)
	if o["status"] != "awaiting_confirmation" || len(recs) != 1 || recs[0]["method"] != "pix" || recs[0]["status"] != "registered" ||
		recs[0]["classification"] != "principal" || recs[0]["registered_by"] != "provider" || amountOf(o, "received") != 7500 ||
		amountOf(o, "outstanding") != 0 || chargeStatus(e, ch["id"]) != "paid" {
		t.Fatalf("depois do pagamento: %s %v", o["status"], payment(o))
	}
	// Expirar no provedor depois de pago não volta atrás.
	e.pix.Set(remote.ID, "EXPIRED", 0)
	e.reconcile()
	if chargeStatus(e, ch["id"]) != "paid" || maria.order(b.id, order["id"])["status"] != "awaiting_confirmation" {
		t.Fatal("pago voltou atrás")
	}
	// O banco recusa "paga" com recebimento ainda não conferido (defesa além do código).
	if _, err := e.pool.Exec(context.Background(), `UPDATE service_order SET status = 'settled' WHERE id = $1`, order["id"]); err == nil {
		t.Fatal("comanda paga sem conferência")
	}
	// Maria não confere o próprio pagamento.
	if r := maria.confer(b.id, o, recs[0]["id"]); r.status != 403 {
		t.Fatalf("cliente conferindo: %d", r.status)
	}
	var wg sync.WaitGroup
	results := make([]resp, 2)
	for i, br := range []*browser{b.betoB, b.owner} {
		wg.Go(func() { results[i] = br.confer(b.id, o, recs[0]["id"]) })
	}
	wg.Wait()
	for _, r := range results {
		if r.status != 200 || r.data()["status"] != "settled" {
			t.Fatalf("conferência: %d %s", r.status, r.raw)
		}
	}
	if e.count(t, `SELECT count(*) FROM receipt_conferral WHERE receipt_id = $1`, recs[0]["id"]) != 1 ||
		e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-O-10' AND target_id = $1`, order["id"]) != 1 ||
		e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-RC-03' AND target_id = $1`, recs[0]["id"]) != 1 {
		t.Fatal("conferência duplicada")
	}
	o = maria.order(b.id, order["id"])
	if o["status"] != "settled" || receiptsOf(o)[0]["status"] != "conferred" || amountOf(o, "conferred") != 7500 || o["closed_at"] == nil {
		t.Fatalf("comanda paga: %v", o)
	}
	if e.count(t, `SELECT count(*) FROM outbox WHERE type = 'payment.updated' AND resource_id = $1`, order["id"]) == 0 {
		t.Fatal("sem evento payment.updated")
	}
	// Sem pendência, Maria pode entrar de novo.
	maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201)
}

// C-PAG-04 — duplo clique: mesma chave devolve a mesma cobrança; outra chave recebe 409 com a
// cobrança atual; o provedor recebe um único pedido.
func TestPixDoubleClick(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	key := newIdemKey()
	var wg sync.WaitGroup
	results := make([]resp, 5)
	for i := range results {
		wg.Go(func() { results[i] = maria.createPix(b.id, order, key) })
	}
	wg.Wait()
	ids := map[any]bool{}
	for i, r := range results {
		if r.status != 201 && r.status != 202 {
			t.Fatalf("pedido %d: %d %s", i, r.status, r.raw)
		}
		ids[r.data()["id"]] = true
	}
	if len(ids) != 1 || e.count(t, `SELECT count(*) FROM pix_charge WHERE order_id = $1`, order["id"]) != 1 || e.pix.Creates() != 1 {
		t.Fatalf("cobranças: ids=%v locais=%d pedidos ao provedor=%d", ids, e.count(t, `SELECT count(*) FROM pix_charge`), e.pix.Creates())
	}
	// Outras abas (chaves novas, ao mesmo tempo): 409 com a cobrança atual.
	others := make([]resp, 3)
	for i := range others {
		wg.Go(func() { others[i] = maria.createPix(b.id, order, newIdemKey()) })
	}
	wg.Wait()
	for i, r := range others {
		if r.status != 409 || r.code() != "pix_charge_active" || !ids[r.body["current"].(map[string]any)["id"]] {
			t.Fatalf("outra aba %d: %d %s", i, r.status, r.raw)
		}
	}
	if e.pix.Creates() != 1 {
		t.Fatal("outra aba chamou o provedor")
	}
	// Repetição depois de pronta: mesma resposta, marcada como repetição.
	r := maria.createPix(b.id, order, key)
	if r.status != 201 || r.header.Get("Idempotent-Replayed") != "true" || !ids[r.data()["id"]] {
		t.Fatalf("repetição: %d %s", r.status, r.raw)
	}
	if r := maria.createPix(b.id, maria.order(b.id, order["id"]), newIdemKey()); r.code() != "pix_charge_active" {
		t.Fatalf("segunda cobrança: %s", r.raw)
	}
}

// C-PAG-06 — tempo esgotado na criação: resultado desconhecido, nenhuma nova cobrança até a
// reconciliação; o worker resolve sem o navegador; nunca duas cobranças por falha de rede.
func TestPixAmbiguousTimeout(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	e.pix.SetCreateMode(pagamentotest.CreateSlow, 3*time.Second) // cria, mas responde depois do timeout (1 s)
	r := maria.createPix(b.id, order, newIdemKey())
	if r.status != 202 || r.data()["status"] != "unknown" {
		t.Fatalf("timeout: %d %s", r.status, r.raw)
	}
	chID := r.data()["id"]
	o := maria.order(b.id, order["id"])
	if payment(o)["verifying"] != true || o["status"] != "awaiting_payment" {
		t.Fatalf("verificando: %v", payment(o))
	}
	e.pix.SetCreateMode(pagamentotest.CreateOK, 0)
	// Nada novo enquanto desconhecido: outro Pix, troca de meio, dinheiro, correção.
	if r := maria.createPix(b.id, o, newIdemKey()); r.code() != "pix_charge_outcome_unknown" {
		t.Fatalf("novo Pix com desconhecido: %s", r.raw)
	}
	if r := maria.choose(b.id, o, "cash", true); r.code() != "pix_charge_outcome_unknown" {
		t.Fatalf("troca com desconhecido: %s", r.raw)
	}
	if r := b.betoB.receive(b.id, o, map[string]any{"method": "card", "amount_cents": 7500}); r.code() != "pix_charge_outcome_unknown" {
		t.Fatalf("cartão com desconhecido: %s", r.raw)
	}
	// Consulta ao provedor fora do ar: continua desconhecido, com nova consulta agendada.
	e.pix.SetListDown(true)
	if e.reconcile() != 1 || chargeStatus(e, chID) != "unknown" {
		t.Fatal("deveria seguir desconhecido")
	}
	if e.count(t, `SELECT check_attempts FROM pix_charge WHERE id = $1`, chID) != 1 {
		t.Fatal("consulta não contada")
	}
	e.pix.SetListDown(false)
	// O worker acha a cobrança criada pelo pedido que demorou (pelo externalId) e a adota.
	e.reconcile()
	o = maria.order(b.id, order["id"])
	if chargeStatus(e, chID) != "pending" || payment(o)["verifying"] != false || len(e.pix.Charges()) != 1 || e.pix.Creates() != 1 {
		t.Fatalf("reconciliado: %s, remotas=%d pedidos=%d", chargeStatus(e, chID), len(e.pix.Charges()), e.pix.Creates())
	}
	if r := maria.mustStatus(maria.do("GET", orderPath(b.id, order["id"])+"/pix-charges/"+chID.(string), nil), 200); r.data()["br_code"] == nil {
		t.Fatalf("sem copia-e-cola depois de reconciliar: %s", r.raw)
	}
}

// Erro 5xx sem nada criado é ambíguo do nosso lado: a reconciliação procura, não acha e repete a
// criação com o MESMO externalId (o provedor devolveria a pendente se existisse). Erro 5xx depois
// de criar: a procura acha a cobrança. Em ambos, uma única cobrança remota.
func TestPixServerErrorsReconcileWithoutDuplicates(t *testing.T) {
	for _, mode := range []string{pagamentotest.CreateDownBefore, pagamentotest.CreateErrAfter, pagamentotest.CreateWrongAmount} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			b, maria, order := e.payable(t)
			e.pix.SetCreateMode(mode, 0)
			r := maria.createPix(b.id, order, newIdemKey())
			if r.status != 202 || r.data()["status"] != "unknown" || r.data()["br_code"] != nil {
				t.Fatalf("resposta: %d %s", r.status, r.raw)
			}
			e.pix.SetCreateMode(pagamentotest.CreateOK, 0)
			e.reconcile()
			chID := r.data()["id"].(string)
			remote := e.pix.Charges()
			if chargeStatus(e, chID) != "pending" || len(remote) != 1 || remote[0].Amount != 7500 || remote[0].Metadata["charge_id"] != chID {
				t.Fatalf("depois: %s %+v", chargeStatus(e, chID), remote)
			}
			e.reconcile()
			if len(e.pix.Charges()) != 1 || e.count(t, `SELECT count(*) FROM pix_charge WHERE order_id = $1`, order["id"]) != 1 {
				t.Fatal("duplicou")
			}
		})
	}
}

// Recusa definitiva do provedor: tentativa "failed", comanda intacta, nova tentativa permitida.
func TestPixRejectedAllowsNewAttempt(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	e.pix.SetCreateMode(pagamentotest.CreateReject, 0)
	key := newIdemKey()
	if r := maria.createPix(b.id, order, key); r.status != 503 || r.code() != "provider_unavailable" {
		t.Fatalf("recusa: %d %s", r.status, r.raw)
	}
	// Repetir a mesma chave devolve a mesma resposta (sem chamar o provedor de novo).
	if r := maria.createPix(b.id, order, key); r.status != 503 || e.pix.Creates() != 1 {
		t.Fatalf("repetição da recusa: %d %s (pedidos %d)", r.status, r.raw, e.pix.Creates())
	}
	o := maria.order(b.id, order["id"])
	ch := payment(o)["charges"].([]any)
	if o["status"] != "awaiting_payment" || due(o) != 7500 || len(ch) != 1 || ch[0].(map[string]any)["status"] != "failed" {
		t.Fatalf("comanda depois da recusa: %v", payment(o))
	}
	e.pix.SetCreateMode(pagamentotest.CreateOK, 0)
	maria.mustStatus(maria.createPix(b.id, o, newIdemKey()), 201)
}

// C-PAG-13 — cobrança expirada (confirmada pelo provedor) permite nova cobrança.
func TestPixExpiredAllowsNewCharge(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	first := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	o := maria.order(b.id, order["id"])
	if r := maria.createPix(b.id, o, newIdemKey()); r.code() != "pix_charge_active" {
		t.Fatalf("antes de expirar: %s", r.raw)
	}
	e.pix.Set(e.pix.ByExternalID(first["id"].(string)).ID, "EXPIRED", 0)
	e.reconcile()
	if chargeStatus(e, first["id"]) != "expired" {
		t.Fatal("não expirou")
	}
	second := maria.mustStatus(maria.createPix(b.id, maria.order(b.id, order["id"]), newIdemKey()), 201).data()
	if second["id"] == first["id"] || len(e.pix.Charges()) != 2 {
		t.Fatal("nova cobrança não criada")
	}
}

// C-PAG-05, C-PAG-09 — dinheiro com troco; valor adulterado; repetição; quem pode registrar.
// Cenários: C-CMD-01.
func TestCashWithChange(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	joao := e.visitorIn(b.id, "João")
	p, o := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	order := b.betoB.order(b.id, o["id"])
	for name, body := range map[string]map[string]any{
		"valor menor":      {"method": "cash", "amount_cents": 4400, "cash_tendered_cents": 5000},
		"valor maior":      {"method": "cash", "amount_cents": 4600, "cash_tendered_cents": 5000},
		"entregue a menos": {"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 4000},
	} {
		if r := b.betoB.receive(b.id, order, body); r.status != 409 || r.code() != "payment_amount_mismatch" {
			t.Fatalf("%s: %d %s", name, r.status, r.raw)
		}
	}
	for name, body := range map[string]map[string]any{
		"centavos fracionados": {"method": "cash", "amount_cents": 4500.5, "cash_tendered_cents": 5000},
		"total no corpo":       {"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000, "total_cents": 1},
		"cartão com número":    {"method": "card", "amount_cents": 4500, "card_reference": "4111 1111 1111 1111"},
		"troco no cartão":      {"method": "card", "amount_cents": 4500, "cash_tendered_cents": 5000},
		"meio pix":             {"method": "pix", "amount_cents": 4500},
		"sem entregue":         {"method": "cash", "amount_cents": 4500},
	} {
		if r := b.betoB.receive(b.id, order, body); r.status != 400 {
			t.Fatalf("%s: %d %s", name, r.status, r.raw)
		}
	}
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	if r := caio.receive(b.id, order, map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000}); r.status != 404 {
		t.Fatalf("Caio (não atendeu): %d", r.status)
	}
	if r := joao.receive(b.id, order, map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000}); r.status != 403 {
		t.Fatalf("cliente: %d", r.status)
	}
	body := map[string]any{"expected_version": num(order["version"]), "method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000}
	key := newIdemKey()
	var wg sync.WaitGroup
	results := make([]resp, 3)
	for i := range results {
		wg.Go(func() {
			results[i] = b.betoB.do("POST", orderPath(b.id, order["id"])+"/receipts", body, "Idempotency-Key", key)
		})
	}
	wg.Wait()
	for _, r := range results {
		if r.status != 201 {
			t.Fatalf("registro: %d %s", r.status, r.raw)
		}
	}
	final := results[0].data()
	recs := receiptsOf(final)
	if final["status"] != "settled" || len(recs) != 1 || recs[0]["status"] != "conferred" || num(recs[0]["amount"].(map[string]any)["amount_cents"]) != 4500 ||
		num(recs[0]["change_given"].(map[string]any)["amount_cents"]) != 500 || num(recs[0]["cash_tendered"].(map[string]any)["amount_cents"]) != 5000 ||
		amountOf(final, "received") != 4500 || amountOf(final, "conferred") != 4500 {
		t.Fatalf("comanda paga: %s", results[0].raw)
	}
	// Recebimento e conferência: registros distintos; um só de cada.
	if e.count(t, `SELECT count(*) FROM receipt WHERE order_id = $1`, order["id"]) != 1 ||
		e.count(t, `SELECT count(*) FROM receipt_conferral c JOIN receipt r ON r.id = c.receipt_id WHERE r.order_id = $1`, order["id"]) != 1 {
		t.Fatal("registros duplicados ou faltando")
	}
	if r := b.betoB.receive(b.id, b.betoB.order(b.id, order["id"]), map[string]any{"method": "card", "amount_cents": 4500}); r.code() != "order_not_payable" {
		t.Fatalf("segundo recebimento: %s", r.raw)
	}
	// Banco recusa apagar ou alterar recebimento.
	if _, err := e.pool.Exec(context.Background(), `UPDATE receipt SET amount_cents = 1 WHERE order_id = $1`, order["id"]); err == nil {
		t.Fatal("recebimento alterado")
	}
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM receipt WHERE order_id = $1`, order["id"]); err == nil {
		t.Fatal("recebimento apagado")
	}
	// Comanda paga sem recebimento: o banco recusa (outra comanda, aguardando pagamento).
	lia := e.verifiedCustomer("lia@exemplo.com")
	pl, ol := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, pl["id"].(string))
	if _, err := e.pool.Exec(context.Background(), `UPDATE service_order SET status = 'settled' WHERE id = $1`, ol["id"]); err == nil {
		t.Fatal("comanda paga sem recebimento")
	}
	// Cartão com referência opcional sem dados sensíveis.
	r := b.owner.mustStatus(b.owner.receive(b.id, b.owner.order(b.id, ol["id"]), map[string]any{"method": "card", "amount_cents": 4500, "card_reference": "NSU-123456"}), 201)
	if rc := receiptsOf(r.data()); r.data()["status"] != "settled" || rc[0]["card_reference"] != "NSU-123456" || rc[0]["cash_tendered"] != nil {
		t.Fatalf("cartão: %s", r.raw)
	}
}

// C-PAG-10 (parte da etapa 08) — trocar de Pix para dinheiro exige ciência; o provedor não
// cancela Pix, então a cobrança segue válida; se for paga depois, os dois recebimentos ficam e o
// Pix é excesso (pendência e devolução: etapa 09). Nada é apagado.
func TestSwitchFromPixAndLatePayment(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	o := maria.order(b.id, order["id"])
	if r := b.betoB.receive(b.id, o, map[string]any{"method": "cash", "amount_cents": 7500, "cash_tendered_cents": 8000}); r.code() != "pix_charge_active" {
		t.Fatalf("dinheiro com Pix ativo: %s", r.raw)
	}
	r := maria.choose(b.id, o, "cash", false)
	if r.status != 409 || r.code() != "pix_charge_active" || !strings.Contains(string(r.raw), "continua válido") {
		t.Fatalf("troca sem ciência: %d %s", r.status, r.raw)
	}
	o = maria.mustStatus(maria.choose(b.id, o, "cash", true), 200).data()
	if payment(o)["method_choice"] != "cash" || chargeStatus(e, ch["id"]) != "pending" {
		t.Fatalf("troca com ciência: %v", payment(o))
	}
	if e.count(t, `SELECT count(*) FROM pix_charge WHERE id = $1 AND cancel_requested_at IS NOT NULL`, ch["id"]) != 1 {
		t.Fatal("pedido de cancelamento não registrado")
	}
	paid := b.betoB.mustStatus(b.betoB.receive(b.id, o, map[string]any{"method": "cash", "amount_cents": 7500, "cash_tendered_cents": 8000}), 201).data()
	if paid["status"] != "settled" {
		t.Fatalf("dinheiro: %v", paid)
	}
	// O Pix é pago depois (o provedor não cancelou).
	e.pix.Set(e.pix.ByExternalID(ch["id"].(string)).ID, "PAID", 0)
	e.reconcile()
	final := maria.order(b.id, order["id"])
	recs := receiptsOf(final)
	if final["status"] != "settled" || len(recs) != 2 || chargeStatus(e, ch["id"]) != "paid" {
		t.Fatalf("depois do Pix tardio: %v", payment(final))
	}
	classes := map[any]any{}
	for _, rc := range recs {
		classes[rc["method"]] = rc["classification"]
	}
	if classes["cash"] != "principal" || classes["pix"] != "excess" || amountOf(final, "received") != 15000 || amountOf(final, "outstanding") != 0 {
		t.Fatalf("classificação: %v %v", classes, payment(final)["amounts"])
	}
	if e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-RC-04' AND target_id = $1`, ch["id"]) != 1 {
		t.Fatal("excesso sem auditoria")
	}
}

// Comanda zero e abaixo do mínimo do provedor: zero nunca cria cobrança; Pix desligado não
// impede dinheiro.
func TestZeroOrderAndPixDisabled(t *testing.T) {
	e := newEnvDeps(t, func(d *api.Deps) { d.Config.Payments.Pix = config.PixDisabled })
	b := e.barbearia(t)
	lia := e.visitorIn(b.id, "Lia")
	pl, ol := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	visit := b.betoB.startServing(b.id, b.filaBeto, pl["id"].(string))
	r := b.betoB.mustStatus(b.betoB.closeVisit(b.id, visit, "interrupt", func(string) string { return "not_started" }, map[string]any{"reason": "máquina quebrou"}), 200)
	_, oi := visitOf(r)
	items := []map[string]any{{"item_id": itemByName(ol, "Corte")["id"], "outcome": "not_started"}}
	zero := b.owner.mustStatus(b.owner.cmd("POST", orderPath(b.id, ol["id"])+"/close-interruption",
		map[string]any{"expected_version": num(oi["version"]), "review_note": "nada foi feito", "items": items}), 200).data()
	for _, rr := range []resp{
		lia.createPix(b.id, zero, newIdemKey()),
		b.betoB.receive(b.id, zero, map[string]any{"method": "cash", "amount_cents": 0, "cash_tendered_cents": 0}),
	} {
		if rr.status != 409 && rr.status != 400 {
			t.Fatalf("comanda zero: %d %s", rr.status, rr.raw)
		}
	}
	if r := lia.createPix(b.id, zero, newIdemKey()); r.status != 503 && r.code() != "order_not_payable" {
		t.Fatalf("zero: %s", r.raw)
	}
	if e.count(t, `SELECT count(*) FROM pix_charge`)+e.count(t, `SELECT count(*) FROM receipt`) != 0 {
		t.Fatal("zero criou cobrança ou recebimento")
	}
	// Pix desligado: 503 claro; dinheiro funciona.
	joao := e.visitorIn(b.id, "João")
	pj, oj := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, pj["id"].(string))
	order := joao.order(b.id, oj["id"])
	if r := joao.createPix(b.id, order, newIdemKey()); r.status != 503 || r.code() != "provider_unavailable" {
		t.Fatalf("Pix desligado: %d %s", r.status, r.raw)
	}
	if r := joao.choose(b.id, order, "pix", false); r.code() != "provider_unavailable" {
		t.Fatalf("escolher Pix desligado: %s", r.raw)
	}
	order = joao.mustStatus(joao.choose(b.id, order, "cash", false), 200).data()
	b.betoB.mustStatus(b.betoB.receive(b.id, order, map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 4500}), 201)
	if e.pix.Creates() != 0 {
		t.Fatal("chamou o provedor desligado")
	}
}

// Abaixo do mínimo do Pix (R$ 1,00): Pix recusado com motivo claro; dinheiro aceito.
func TestPixBelowMinimum(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	tiny := b.owner.createService(b.id, map[string]any{"name": "Sobrancelha", "price_cents": 99, "duration_minutes": 5, "points": 0,
		"professional_ids": []string{b.beto.String()}})["id"].(string)
	lia := e.visitorIn(b.id, "Lia")
	p, o := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, tiny), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	order := lia.order(b.id, o["id"])
	if r := lia.createPix(b.id, order, newIdemKey()); r.code() != "payment_method_not_allowed" {
		t.Fatalf("Pix de 99 centavos: %s", r.raw)
	}
	if e.pix.Creates() != 0 {
		t.Fatal("pediu ao provedor")
	}
	b.betoB.mustStatus(b.betoB.receive(b.id, order, map[string]any{"method": "cash", "amount_cents": 99, "cash_tendered_cents": 100}), 201)
}

// Respostas do provedor que não conferem (ambiente trocado) não são aceitas como verdade.
func TestPixProviderIntegrity(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	e.pix.SetCreateMode(pagamentotest.CreateWrongEnv, 0)
	r := maria.createPix(b.id, order, newIdemKey())
	if r.status != 202 || r.data()["status"] != "unknown" || r.data()["br_code"] != nil {
		t.Fatalf("produção respondendo a chave de teste: %d %s", r.status, r.raw)
	}
	if !strings.Contains(e.logs.String(), "resultado desconhecido") {
		t.Fatal("sem diagnóstico no log")
	}
}

// C-ATD-05 com Pix de verdade: a correção do valor fica bloqueada com Pix pendente ou de
// resultado desconhecido e volta a valer quando o provedor confirma a expiração.
func TestAdjustBlockedByRealPix(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	op := orderPath(b.id, order["id"]) + "/adjustments"
	adjust := func() resp {
		o := b.owner.order(b.id, order["id"])
		return b.owner.cmd("POST", op, map[string]any{"expected_version": num(o["version"]), "reason": "barba não foi feita",
			"uncharge_item_ids": []any{itemByName(o, "Barba")["id"]}})
	}
	e.pix.SetCreateMode(pagamentotest.CreateErrAfter, 0)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 202).data()
	b.owner.reauth()
	if r := adjust(); r.code() != "pix_charge_outcome_unknown" {
		t.Fatalf("correção com Pix desconhecido: %s", r.raw)
	}
	e.pix.SetCreateMode(pagamentotest.CreateOK, 0)
	e.reconcile()
	b.owner.reauth()
	if r := adjust(); r.code() != "pix_charge_active" {
		t.Fatalf("correção com Pix pendente: %s", r.raw)
	}
	e.pix.Set(e.pix.ByExternalID(ch["id"].(string)).ID, "EXPIRED", 0)
	e.reconcile()
	b.owner.reauth()
	o := b.owner.mustStatus(adjust(), 200).data()
	if due(o) != 4500 {
		t.Fatalf("devido depois da correção: %d", due(o))
	}
	// Nova cobrança já com o valor corrigido (calculado no servidor).
	if c := maria.mustStatus(maria.createPix(b.id, o, newIdemKey()), 201).data(); num(c["amount"].(map[string]any)["amount_cents"]) != 4500 {
		t.Fatalf("nova cobrança: %v", c)
	}
}
