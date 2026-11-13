package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"fila/internal/api"
	"fila/internal/pagamento"
	"fila/internal/pagamento/pagamentotest"
	"fila/internal/platform/logging"
)

// ---------------------------------------------------------------------------------------------
// Ajudantes da etapa 09

// webhook envia um aviso como a AbacatePay: sem cookie, sem Origin; secret no cabeçalho (ou na
// query) e assinatura base64(HMAC-SHA256) do corpo bruto.
func (e *env) webhook(body []byte, secret, signature string, viaQuery bool) resp {
	e.t.Helper()
	url := e.srv.URL + "/v1/webhooks/abacatepay"
	if viaQuery {
		url += "?webhookSecret=" + secret
	}
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AbacatePay/1.0")
	if !viaQuery && secret != "" {
		req.Header.Set("X-Webhook-Secret", secret)
	}
	if signature != "" {
		req.Header.Set("X-Webhook-Signature", signature)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: raw}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// signed envia um aviso autêntico.
func (e *env) signed(body []byte) resp {
	e.t.Helper()
	return e.webhook(body, webhookSecret, pagamento.SignWebhook(body), false)
}

// remote devolve a cobrança no simulador para a nossa tentativa.
func (e *env) remote(chargeID any) pagamentotest.Charge {
	e.t.Helper()
	c := e.pix.ByExternalID(chargeID.(string))
	if c == nil {
		e.t.Fatalf("cobrança %v não existe no provedor", chargeID)
	}
	return *c
}

// settledByPix: Pix pago (consulta do worker) e conferido por Beto → comanda paga.
func (e *env) settledByPix(t *testing.T) (barbearia, *browser, map[string]any, map[string]any) {
	t.Helper()
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	e.pix.Set(e.remote(ch["id"]).ID, "PAID", 0)
	e.reconcile()
	o := b.betoB.order(b.id, order["id"])
	rec := receiptsOf(o)[0]
	o = b.betoB.mustStatus(b.betoB.confer(b.id, o, rec["id"]), 200).data()
	if o["status"] != "settled" {
		t.Fatalf("comanda deveria estar paga: %v", o["status"])
	}
	return b, maria, o, rec
}

func (b *browser) refund(est any, order map[string]any, receipt any, reason string) resp {
	b.e.t.Helper()
	return b.cmd("POST", "/v1/establishments/"+est.(string)+"/orders/"+order["id"].(string)+"/receipts/"+receipt.(string)+"/refunds",
		map[string]any{"reason": reason})
}

func refundsOf(order map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range payment(order)["refunds"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

func (e *env) issues(b *browser, est string, status string) []map[string]any {
	e.t.Helper()
	path := "/v1/establishments/" + est + "/financial-issues"
	if status != "" {
		path += "?status=" + status
	}
	r := b.mustStatus(b.do("GET", path, nil), 200)
	var out []map[string]any
	for _, x := range r.body["data"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func kindsOf(list []map[string]any) map[string]int {
	m := map[string]int{}
	for _, i := range list {
		m[i["kind"].(string)]++
	}
	return m
}

// ---------------------------------------------------------------------------------------------

// C-PAG-07 — aviso autenticado confirma o Pix; repetido (mesmo id, retentativa, ao mesmo tempo ou
// reenvio manual com outro id) não duplica recebimento nem mudança de estado; dados do pagador
// não são guardados; o recebimento é gravado antes do 200.
func TestWebhookConfirmsPixOnceAndDeduplicates(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	e.pix.SetListDown(true) // prova que foi o aviso (e não a consulta) que confirmou
	rc := e.remote(ch["id"])
	rc.Status = "PAID"
	body := e.pix.Webhook("log_pago_0001", "transparent.completed", rc)
	var wg sync.WaitGroup
	results := make([]resp, 4)
	for i := range results {
		wg.Go(func() { results[i] = e.signed(body) })
	}
	wg.Wait()
	dups := 0
	for _, r := range results {
		if r.status != 200 {
			t.Fatalf("aviso: %d %s", r.status, r.raw)
		}
		if r.body["duplicate"] == true {
			dups++
		}
	}
	if dups != 3 {
		t.Fatalf("esperava 3 duplicatas, veio %d", dups)
	}
	// Reenvio manual pelo painel do provedor: id novo, mesmo efeito → nada muda.
	if r := e.signed(e.pix.Webhook("log_reenvio_0002", "transparent.completed", rc)); r.status != 200 || r.body["duplicate"] != false {
		t.Fatalf("reenvio manual: %d %s", r.status, r.raw)
	}
	o := maria.order(b.id, order["id"])
	recs := receiptsOf(o)
	if o["status"] != "awaiting_confirmation" || len(recs) != 1 || recs[0]["classification"] != "principal" || chargeStatus(e, ch["id"]) != "paid" {
		t.Fatalf("depois dos avisos: %s %v", o["status"], payment(o))
	}
	if e.count(t, `SELECT count(*) FROM receipt WHERE pix_charge_id = $1 AND evidence = 'provider_webhook'`, ch["id"]) != 1 ||
		e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-O-09' AND target_id = $1`, order["id"]) != 1 {
		t.Fatal("recebimento ou transição duplicados")
	}
	if e.count(t, `SELECT count(*) FROM provider_event WHERE authenticated`) != 2 ||
		e.count(t, `SELECT count(*) FROM provider_event WHERE event_id = 'log_pago_0001' AND outcome = 'applied'`) != 1 ||
		e.count(t, `SELECT count(*) FROM provider_event WHERE event_id = 'log_reenvio_0002' AND outcome = 'duplicate_effect'`) != 1 {
		t.Fatal("registro dos avisos")
	}
	// Nada do pagador (nome, e-mail, documento) fica no banco nem nos logs.
	var dump string
	_ = e.pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(row_to_json(p)::text, ''), '') FROM provider_event p`).Scan(&dump)
	if strings.Contains(dump, "Pagadora") || strings.Contains(dump, "simulado.test") || strings.Contains(e.logs.String(), "Pagadora") ||
		strings.Contains(e.logs.String(), webhookSecret) {
		t.Fatal("dado pessoal do pagador ou secret gravado")
	}
	// "Conferência humana não fabrica Pix": só confere o que o provedor confirmou.
	b.betoB.mustStatus(b.betoB.confer(b.id, o, recs[0]["id"]), 200)
	// Aviso velho de pagamento depois de conferido: nada muda.
	if r := e.signed(e.pix.Webhook("log_atrasado_0003", "transparent.completed", rc)); r.status != 200 {
		t.Fatalf("aviso atrasado: %d", r.status)
	}
	if o := maria.order(b.id, order["id"]); o["status"] != "settled" || len(receiptsOf(o)) != 1 {
		t.Fatalf("aviso atrasado mudou a comanda: %v", o["status"])
	}
}

// C-PAG-07 — aviso sem secret certo ou com assinatura inválida: 401, nada reconhecido; se cita
// cobrança nossa em aberto, a consulta autenticada é antecipada e registra só o que ela comprova.
func TestWebhookUnauthenticatedTriggersAuthenticatedQuery(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	rc := e.remote(ch["id"])
	rc.Status = "PAID"
	body := e.pix.Webhook("log_falso_0001", "transparent.completed", rc)
	for name, r := range map[string]resp{
		"sem secret":          e.webhook(body, "", pagamento.SignWebhook(body), false),
		"secret errado":       e.webhook(body, strings.Repeat("x", 40), pagamento.SignWebhook(body), false),
		"sem assinatura":      e.webhook(body, webhookSecret, "", false),
		"assinatura de outro": e.webhook(body, webhookSecret, pagamento.SignWebhook([]byte(`{"outro":1}`)), false),
		"corpo alterado":      e.webhook(bytes.Replace(body, []byte(`"amount":7500`), []byte(`"amount":1`), 1), webhookSecret, pagamento.SignWebhook(body), false),
		"assinatura com hex":  e.webhook(body, webhookSecret, "deadbeef", false),
		"secret só no começo": e.webhook(body, webhookSecret[:20], pagamento.SignWebhook(body), false),
	} {
		if r.status != 401 || r.code() != "webhook_unauthenticated" {
			t.Fatalf("%s: %d %s", name, r.status, r.raw)
		}
	}
	o := maria.order(b.id, order["id"])
	if o["status"] != "awaiting_payment" || len(receiptsOf(o)) != 0 || chargeStatus(e, ch["id"]) != "pending" {
		t.Fatal("aviso não autenticado reconheceu pagamento")
	}
	if e.count(t, `SELECT count(*) FROM provider_event WHERE authenticated`) != 0 ||
		e.count(t, `SELECT count(*) FROM provider_event WHERE NOT authenticated AND outcome = 'check_scheduled'`) == 0 {
		t.Fatal("registro do aviso não autenticado")
	}
	// A consulta autenticada roda já (sem esperar o intervalo) e registra o que o provedor diz.
	e.pix.Set(rc.ID, "PAID", 0)
	if _, err := e.api.Payments.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM receipt WHERE pix_charge_id = $1 AND evidence = 'provider_query'`, ch["id"]) != 1 {
		t.Fatal("consulta antecipada não registrou o pagamento comprovado")
	}
	// Aviso falso para cobrança desconhecida: 401 e nada gravado.
	before := e.count(t, `SELECT count(*) FROM provider_event`)
	fake := rc
	fake.ID = "pix_char_inexistente"
	e.webhook(e.pix.Webhook("log_inventado", "transparent.completed", fake), "errado-errado-errado-errado-errado-1", "", false)
	if e.count(t, `SELECT count(*) FROM provider_event`) != before {
		t.Fatal("aviso inventado ocupou o banco")
	}
	// Secret pela query (como a AbacatePay também envia) vale; o secret não vai para o log.
	other := e.pix.Webhook("log_query_0002", "transparent.completed", rc)
	if r := e.webhook(other, webhookSecret, pagamento.SignWebhook(other), true); r.status != 200 {
		t.Fatalf("secret na query: %d %s", r.status, r.raw)
	}
	if strings.Contains(e.logs.String(), webhookSecret) {
		t.Fatal("secret do webhook no log")
	}
}

// Webhook desligado (sem secret configurado): 404, só a consulta do worker confirma.
func TestWebhookDisabledWithoutSecret(t *testing.T) {
	e := newEnvDeps(t, func(d *api.Deps) { d.Config.Payments.WebhookSecret = "" })
	body := []byte(`{"id":"log_x","event":"transparent.completed","data":{}}`)
	if r := e.webhook(body, "", pagamento.SignWebhook(body), false); r.status != 404 {
		t.Fatalf("webhook desligado: %d", r.status)
	}
}

// C-PAG-08 — valor divergente, outro ambiente, pedido trocado, cobrança desconhecida.
func TestWebhookDivergences(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	e.pix.SetListDown(true)
	rc := e.remote(ch["id"])
	rc.Status = "PAID"

	prod := rc
	prod.DevMode = false
	if r := e.signed(e.pix.Webhook("log_prod", "transparent.completed", prod)); r.status != 200 {
		t.Fatalf("aviso de produção: %d", r.status)
	}
	swapped := rc
	swapped.Metadata = map[string]string{"charge_id": rc.Metadata["charge_id"], "order_id": "00000000-0000-7000-8000-000000000000",
		"establishment_id": rc.Metadata["establishment_id"]}
	if r := e.signed(e.pix.Webhook("log_pedido", "transparent.completed", swapped)); r.status != 200 {
		t.Fatalf("pedido trocado: %d", r.status)
	}
	unknown := rc
	unknown.ID = "pix_char_de_outra_loja"
	unknown.Metadata = map[string]string{}
	if r := e.signed(e.pix.Webhook("log_outra", "transparent.completed", unknown)); r.status != 200 {
		t.Fatalf("cobrança desconhecida: %d", r.status)
	}
	if o := maria.order(b.id, order["id"]); len(receiptsOf(o)) != 0 || o["status"] != "awaiting_payment" {
		t.Fatal("aviso que não confere registrou pagamento")
	}
	if e.count(t, `SELECT count(*) FROM provider_event WHERE outcome = 'mismatch'`) != 2 ||
		e.count(t, `SELECT count(*) FROM provider_event WHERE outcome = 'unknown_charge'`) != 1 {
		t.Fatal("resultado dos avisos")
	}
	// Valor menor que o cobrado: registrado (nada é descartado), não encerra, pendência para a dona.
	short := rc
	short.Amount = 7000
	e.signed(e.pix.Webhook("log_valor", "transparent.completed", short))
	o := maria.order(b.id, order["id"])
	recs := receiptsOf(o)
	if o["status"] != "awaiting_payment" || len(recs) != 1 || recs[0]["classification"] != "excess" ||
		num(recs[0]["amount"].(map[string]any)["amount_cents"]) != 7000 {
		t.Fatalf("valor divergente: %s %v", o["status"], payment(o))
	}
	list := e.issues(b.owner, b.id.String(), "open")
	if kindsOf(list)["amount_mismatch"] != 1 || num(list[0]["amount"].(map[string]any)["amount_cents"]) != 7000 {
		t.Fatalf("pendência: %v", list)
	}
	if k := payment(o)["open_issue_kinds"].([]any); len(k) != 1 || k[0] != "amount_mismatch" {
		t.Fatalf("pendência visível na comanda: %v", k)
	}
}

// C-PAG-10, C-PAG-13 — troca com Pix ativo alerta; Pix tardio: dois recebimentos e pendência;
// pendências visíveis (dono no painel; equipe na comanda); resolver não apaga nada.
func TestLatePixIssuesAndResolution(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	o := maria.mustStatus(maria.choose(b.id, maria.order(b.id, order["id"]), "cash", true), 200).data()
	b.betoB.mustStatus(b.betoB.receive(b.id, o, map[string]any{"method": "cash", "amount_cents": 7500, "cash_tendered_cents": 7500}), 201)
	rc := e.remote(ch["id"])
	rc.Status = "PAID"
	e.signed(e.pix.Webhook("log_tardio", "transparent.completed", rc))
	o = b.betoB.order(b.id, order["id"])
	if len(receiptsOf(o)) != 2 || o["status"] != "settled" {
		t.Fatalf("Pix tardio: %v", payment(o))
	}
	kinds := map[string]bool{}
	for _, k := range payment(o)["open_issue_kinds"].([]any) {
		kinds[k.(string)] = true
	}
	if !kinds["late_receipt"] || !kinds["active_pix_on_method_switch"] {
		t.Fatalf("equipe deveria ver as pendências na comanda: %v", kinds)
	}
	if r := b.betoB.do("GET", estPath(b.id, "/financial-issues"), nil); r.status != 403 {
		t.Fatalf("profissional no painel do dono: %d", r.status)
	}
	if r := maria.do("GET", estPath(b.id, "/financial-issues"), nil); r.status != 403 && r.status != 401 && r.status != 404 {
		t.Fatalf("cliente no painel: %d", r.status)
	}
	list := e.issues(b.owner, b.id.String(), "open")
	if k := kindsOf(list); k["late_receipt"] != 1 || k["active_pix_on_method_switch"] != 1 || len(list) != 2 {
		t.Fatalf("painel: %v", k)
	}
	pend := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/pendencies"), nil), 200).data()
	if num(pend["open_financial_issues"]) != 2 {
		t.Fatalf("contadores: %v", pend)
	}
	if r := b.betoB.do("GET", estPath(b.id, "/pendencies"), nil); r.status != 403 {
		t.Fatalf("profissional nos contadores: %d", r.status)
	}
	var late map[string]any
	for _, i := range list {
		if i["kind"] == "late_receipt" {
			late = i
		}
	}
	path := estPath(b.id, "/financial-issues/"+late["id"].(string)+"/resolve")
	if r := b.owner.cmd("POST", path, map[string]any{"resolution_note": "x"}); r.status != 400 {
		t.Fatalf("nota curta: %d", r.status)
	}
	if r := b.betoB.cmd("POST", path, map[string]any{"resolution_note": "devolvido em dinheiro na hora"}); r.status != 403 {
		t.Fatalf("profissional resolvendo: %d", r.status)
	}
	done := b.owner.mustStatus(b.owner.cmd("POST", path, map[string]any{"resolution_note": "devolvido em dinheiro na hora"}), 200).data()
	if done["status"] != "resolved" || done["resolution_note"] != "devolvido em dinheiro na hora" {
		t.Fatalf("resolução: %v", done)
	}
	if r := b.owner.cmd("POST", path, map[string]any{"resolution_note": "de novo"}); r.code() != "invalid_transition" {
		t.Fatalf("resolver duas vezes: %s", r.raw)
	}
	if len(receiptsOf(b.owner.order(b.id, order["id"]))) != 2 {
		t.Fatal("resolver apagou recebimento")
	}
	if e.count(t, `SELECT count(*) FROM outbox WHERE type = 'pendencies.updated'`) == 0 ||
		e.count(t, `SELECT count(*) FROM outbox WHERE type = 'financial_issue.updated' AND audience = 'owner'`) < 3 ||
		e.count(t, `SELECT count(*) FROM outbox WHERE type = 'financial_issue.updated' AND audience <> 'owner'`) != 0 {
		t.Fatal("eventos das pendências")
	}
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM financial_issue`); err == nil {
		t.Fatal("pendência apagada")
	}
}

// C-PAG-11 — estorno integral de Pix: só o dono, reautenticado; confirmado só com evidência do
// provedor; o recebimento original continua; repetição não estorna duas vezes.
func TestRefundPixConfirmedByProvider(t *testing.T) {
	e := newEnv(t)
	b, maria, o, rec := e.settledByPix(t)
	est := b.id.String()
	if r := b.betoB.refund(est, o, rec["id"], "cliente desistiu"); r.status != 403 {
		t.Fatalf("profissional estornando: %d", r.status)
	}
	if r := maria.refund(est, o, rec["id"], "quero meu dinheiro"); r.status != 403 && r.status != 404 {
		t.Fatalf("cliente estornando: %d", r.status)
	}
	e.clock.Advance(11 * time.Minute)
	if r := b.owner.refund(est, o, rec["id"], "cliente desistiu"); r.code() != "reauthentication_required" {
		t.Fatalf("sem reautenticação: %s", r.raw)
	}
	b.owner.reauth()
	if r := b.owner.refund(est, o, rec["id"], "x"); r.status != 400 {
		t.Fatalf("motivo curto: %d", r.status)
	}
	r := b.owner.mustStatus(b.owner.refund(est, o, rec["id"], "serviço refeito sem custo"), 202)
	rf := refundsOf(r.data())
	if len(rf) != 1 || rf[0]["status"] != "requested" || amountOf(r.data(), "refunded") != 0 {
		t.Fatalf("intenção: %s", r.raw)
	}
	if r := b.owner.refund(est, o, rec["id"], "de novo"); r.code() != "refund_exceeds_available" {
		t.Fatalf("segundo estorno: %s", r.raw)
	}
	if e.pix.Refunds() != 0 {
		t.Fatal("o pedido HTTP não deve chamar o provedor (o worker processa)")
	}
	e.reconcile() // pede ao provedor → processing
	if s := refundsOf(maria.order(b.id, o["id"]))[0]["status"]; s != "processing" {
		t.Fatalf("depois do pedido ao provedor: %v", s)
	}
	e.reconcile() // consulta: REFUNDED → confirmado
	final := maria.order(b.id, o["id"])
	rf = refundsOf(final)
	recs := receiptsOf(final)
	if rf[0]["status"] != "confirmed" || rf[0]["evidence"] != "provider" || amountOf(final, "refunded") != 7500 ||
		len(recs) != 1 || num(recs[0]["refunded"].(map[string]any)["amount_cents"]) != 7500 || final["status"] != "settled" {
		t.Fatalf("confirmação: %v", payment(final))
	}
	// Aviso de estorno depois: nada muda (já confirmado).
	rc := e.remote(rec["pix_charge_id"])
	if r := e.signed(e.pix.Webhook("log_estorno", "transparent.refunded", rc)); r.status != 200 {
		t.Fatalf("aviso de estorno: %d", r.status)
	}
	e.reconcile()
	if e.pix.Refunds() != 1 || e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-RF-03'`) != 1 {
		t.Fatal("estorno repetido")
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE refund SET status = 'failed' WHERE order_id = $1`, o["id"]); err == nil {
		t.Fatal("estorno confirmado voltou atrás")
	}
	// Conferir de novo / conferir recebimento estornado não é possível.
	if r := b.betoB.confer(b.id, final, rec["id"]); r.status != 200 {
		t.Fatalf("conferência já existente: %d", r.status)
	}
}

// C-PAG-12 — estorno com resposta ambígua (tempo esgotado ou 5xx depois de estornar): fica
// desconhecido, o worker consulta e confirma pela situação real; o provedor estorna uma vez.
func TestRefundAmbiguousResolvedByQuery(t *testing.T) {
	for _, mode := range []string{pagamentotest.RefundSlow, pagamentotest.RefundErrAfter} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			b, _, o, rec := e.settledByPix(t)
			b.owner.reauth()
			b.owner.mustStatus(b.owner.refund(b.id.String(), o, rec["id"], "cobrança em duplicidade"), 202)
			e.pix.SetRefundMode(mode, 3*time.Second)
			e.reconcile()
			if s := refundsOf(b.owner.order(b.id, o["id"]))[0]["status"]; s != "unknown" {
				t.Fatalf("resposta ambígua: %v", s)
			}
			e.pix.SetRefundMode(pagamentotest.RefundOK, 0)
			e.reconcile()
			final := b.owner.order(b.id, o["id"])
			if refundsOf(final)[0]["status"] != "confirmed" || e.pix.Refunds() != 1 {
				t.Fatalf("depois da consulta: %v (estornos no provedor %d)", refundsOf(final), e.pix.Refunds())
			}
		})
	}
}

// Recuperação após reinício: o pedido de estorno foi gravado, o processo caiu antes de falar com
// o provedor; um worker novo (outro processo) retoma e conclui, sem estornar duas vezes.
func TestRefundRecoveryAfterRestart(t *testing.T) {
	e := newEnv(t)
	b, _, o, rec := e.settledByPix(t)
	b.owner.reauth()
	b.owner.mustStatus(b.owner.refund(b.id.String(), o, rec["id"], "cliente pediu devolução"), 202)
	// "Reinício": um serviço de pagamentos novo, com o mesmo banco e o provedor real configurado.
	worker := pagamento.New(pagamento.Deps{Pool: e.pool, Clock: e.clock, Log: logging.New(io.Discard, slog.LevelError, "json"),
		Provider: api.PixProvider(e.deps.Config.Payments, nil), Timeout: time.Second})
	for range 3 {
		e.clock.Advance(6 * time.Minute)
		if _, err := worker.ReconcileOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if s := refundsOf(b.owner.order(b.id, o["id"]))[0]["status"]; s != "confirmed" || e.pix.Refunds() != 1 {
		t.Fatalf("depois do reinício: %v (estornos %d)", s, e.pix.Refunds())
	}
	// Caso extremo: o provedor estornou mas a resposta se perdeu e o processo caiu com o estorno
	// ainda "requested": a consulta encontra REFUNDED e confirma sem pedir de novo.
	b2 := e.verifiedCustomer("lia@exemplo.com")
	p, ol := visitOf(b2.mustStatus(b2.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	ch := b2.mustStatus(b2.createPix(b.id, b2.order(b.id, ol["id"]), newIdemKey()), 201).data()
	e.pix.Set(e.remote(ch["id"]).ID, "PAID", 0)
	e.reconcile()
	ord := b.betoB.order(b.id, ol["id"])
	ord = b.betoB.mustStatus(b.betoB.confer(b.id, ord, receiptsOf(ord)[0]["id"]), 200).data()
	b.owner.reauth()
	b.owner.mustStatus(b.owner.refund(b.id.String(), ord, receiptsOf(ord)[0]["id"], "devolução pedida"), 202)
	e.pix.Set(e.remote(ch["id"]).ID, "REFUNDED", 0) // estornado "fora" da nossa resposta
	e.reconcile()
	if s := refundsOf(b.owner.order(b.id, ol["id"]))[0]["status"]; s != "confirmed" || e.pix.Refunds() != 1 {
		t.Fatalf("retomada com estorno já feito: %v (pedidos de estorno feitos %d)", s, e.pix.Refunds())
	}
}

// Recusa do provedor e falta de confirmação vão para revisão do dono (com pendência); dono
// registra que falhou (com evidência) e pode pedir de novo.
func TestRefundRejectedGoesToReview(t *testing.T) {
	e := newEnv(t)
	b, _, o, rec := e.settledByPix(t)
	est := b.id.String()
	b.owner.reauth()
	b.owner.mustStatus(b.owner.refund(est, o, rec["id"], "cliente desistiu"), 202)
	e.pix.SetRefundMode(pagamentotest.RefundReject, 0)
	e.reconcile()
	cur := b.owner.order(b.id, o["id"])
	rf := refundsOf(cur)[0]
	if rf["status"] != "under_review" || kindsOf(e.issues(b.owner, est, "open"))["refund_under_review"] != 1 || amountOf(cur, "refunded") != 0 {
		t.Fatalf("recusa: %v", rf)
	}
	path := orderPath(b.id, o["id"]) + "/refunds/" + rf["id"].(string)
	if r := b.betoB.cmd("POST", path+"/mark-failed", map[string]any{"evidence_note": "saldo insuficiente"}); r.status != 403 {
		t.Fatalf("profissional: %d", r.status)
	}
	b.owner.reauth()
	if r := b.owner.cmd("POST", path+"/mark-failed", map[string]any{"evidence_note": "provedor recusou: saldo insuficiente"}); r.status != 200 {
		t.Fatalf("marcar falho: %d %s", r.status, r.raw)
	}
	if r := b.owner.cmd("POST", path+"/confirm", map[string]any{"evidence_note": "não"}); r.code() != "invalid_transition" {
		t.Fatalf("confirmar estorno falho: %s", r.raw)
	}
	// Falho não conta: novo pedido permitido; desta vez o provedor aceita, mas nunca confirma.
	e.pix.SetRefundMode(pagamentotest.RefundAccepted, 0)
	b.owner.reauth()
	b.owner.mustStatus(b.owner.refund(est, o, rec["id"], "nova tentativa de estorno"), 202)
	for range 14 {
		e.reconcile()
	}
	list := refundsOf(b.owner.order(b.id, o["id"]))
	if len(list) != 2 || list[1]["status"] != "under_review" || kindsOf(e.issues(b.owner, est, "open"))["refund_under_review"] != 2 {
		t.Fatalf("sem confirmação depois do limite: %v", list)
	}
}

// C-PAG-12 — dinheiro: estorno vai direto para revisão; o dono confirma com evidência documentada.
func TestRefundCashNeedsDocumentedEvidence(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	joao := e.visitorIn(b.id, "João")
	p, oj := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	o := b.betoB.mustStatus(b.betoB.receive(b.id, b.betoB.order(b.id, oj["id"]),
		map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000}), 201).data()
	rec := receiptsOf(o)[0]
	b.owner.reauth()
	r := b.owner.mustStatus(b.owner.refund(b.id.String(), o, rec["id"], "corte refeito; devolução"), 202)
	rf := refundsOf(r.data())[0]
	if rf["status"] != "under_review" || e.pix.Refunds() != 0 {
		t.Fatalf("estorno em dinheiro: %v", rf)
	}
	e.reconcile()
	if refundsOf(b.owner.order(b.id, o["id"]))[0]["status"] != "under_review" {
		t.Fatal("worker não deve mexer em estorno presencial")
	}
	path := orderPath(b.id, o["id"]) + "/refunds/" + rf["id"].(string) + "/confirm"
	if r := b.owner.cmd("POST", path, map[string]any{}); r.status != 400 {
		t.Fatalf("sem evidência: %d", r.status)
	}
	final := b.owner.mustStatus(b.owner.cmd("POST", path, map[string]any{"evidence_note": "devolvido em espécie, recibo assinado",
		"external_reference": "RECIBO-12"}), 200).data()
	got := refundsOf(final)[0]
	if got["status"] != "confirmed" || got["evidence"] != "owner_documented" || amountOf(final, "refunded") != 4500 {
		t.Fatalf("confirmação manual: %v", got)
	}
	if e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-RF-06' AND reason = 'devolvido em espécie, recibo assinado'`) != 1 {
		t.Fatal("auditoria da confirmação manual")
	}
}

// C-PAG-12 — conferência e estorno ao mesmo tempo: serializados; o estado final é coerente
// (nunca receita conferida contada depois de estorno, nunca estorno de recebimento não conferido).
func TestConferAndRefundRace(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	e.pix.Set(e.remote(ch["id"]).ID, "PAID", 0)
	e.reconcile()
	o := b.owner.order(b.id, order["id"])
	rec := receiptsOf(o)[0]
	b.owner.reauth()
	if r := b.owner.refund(b.id.String(), o, rec["id"], "antes de conferir"); r.code() != "refund_not_allowed" {
		t.Fatalf("estorno de recebimento principal não conferido: %s", r.raw)
	}
	var wg sync.WaitGroup
	var conf, ref resp
	wg.Go(func() { conf = b.betoB.confer(b.id, o, rec["id"]) })
	wg.Go(func() { ref = b.owner.refund(b.id.String(), o, rec["id"], "cobrança indevida") })
	wg.Wait()
	final := b.owner.order(b.id, order["id"])
	switch {
	case conf.status == 200 && ref.status == 202:
		// Conferiu primeiro, depois estornou: a comanda está paga e o estorno segue o fluxo.
		if final["status"] != "settled" || len(refundsOf(final)) != 1 {
			t.Fatalf("conferência → estorno: %v", payment(final))
		}
	case conf.status == 200 && ref.code() == "refund_not_allowed":
		if final["status"] != "settled" || len(refundsOf(final)) != 0 {
			t.Fatalf("estorno recusado: %v", payment(final))
		}
	default:
		t.Fatalf("combinação incoerente: conferência %d %s / estorno %d %s", conf.status, conf.raw, ref.status, ref.raw)
	}
	// Excesso com estorno em andamento não pode ser conferido (seria receita devolvida).
	e2 := newEnv(t)
	b2, m2, ord2 := e2.payable(t)
	c2 := m2.mustStatus(m2.createPix(b2.id, ord2, newIdemKey()), 201).data()
	o2 := m2.mustStatus(m2.choose(b2.id, m2.order(b2.id, ord2["id"]), "card", true), 200).data()
	b2.betoB.mustStatus(b2.betoB.receive(b2.id, o2, map[string]any{"method": "card", "amount_cents": 7500}), 201)
	e2.pix.Set(e2.remote(c2["id"]).ID, "PAID", 0)
	e2.reconcile()
	o2 = b2.owner.order(b2.id, ord2["id"])
	var excess map[string]any
	for _, r := range receiptsOf(o2) {
		if r["classification"] == "excess" {
			excess = r
		}
	}
	b2.owner.reauth()
	b2.owner.mustStatus(b2.owner.refund(b2.id.String(), o2, excess["id"], "Pix tardio: devolver"), 202)
	if r := b2.betoB.confer(b2.id, o2, excess["id"]); r.code() != "invalid_transition" {
		t.Fatalf("conferir excesso em estorno: %s", r.raw)
	}
}

// Worker: cobrança que segue desconhecida depois do limite abre UMA pendência para a dona e
// continua consultando.
func TestUnresolvedChargeAlertsOnce(t *testing.T) {
	e := newEnv(t)
	b, maria, order := e.payable(t)
	e.pix.SetCreateMode(pagamentotest.CreateErrAfter, 0)
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 202).data()
	e.pix.SetListDown(true)
	for range 16 {
		e.reconcile()
	}
	if chargeStatus(e, ch["id"]) != "unknown" {
		t.Fatal("deveria seguir desconhecida")
	}
	if k := kindsOf(e.issues(b.owner, b.id.String(), "open")); k["charge_outcome_unresolved"] != 1 {
		t.Fatalf("alerta: %v", k)
	}
	pend := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/pendencies"), nil), 200)
	if num(pend.data()["charges_unknown"]) != 1 || num(pend.data()["orders_awaiting_payment"]) != 1 || pend.body["cursor"] == "" {
		t.Fatalf("painel: %s", pend.raw)
	}
	e.pix.SetListDown(false)
	e.reconcile()
	if chargeStatus(e, ch["id"]) != "pending" {
		t.Fatal("consulta deveria resolver quando o provedor voltar")
	}
}
