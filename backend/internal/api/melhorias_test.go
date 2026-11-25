package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/contrato"
)

// Testes das melhorias da versão 1.0.0 (ADR-0032 a ADR-0036): quantidades, produtos, execução
// parcial, custos de recebimento, entrada/capacidade/tolerância e configurações.

func line(service string, qty int) map[string]any {
	return map[string]any{"service_id": service, "quantity": qty}
}
func productLine(product string, qty int) map[string]any {
	return map[string]any{"product_id": product, "quantity": qty}
}

func (b *browser) enterItems(est, queue uuid.UUID, items ...map[string]any) resp {
	b.e.t.Helper()
	return b.cmd("POST", estPath(est, "/participations"), map[string]any{"queue_id": queue.String(), "items": items})
}

func (b *browser) settings(est uuid.UUID) map[string]any {
	b.e.t.Helper()
	return b.mustStatus(b.do("GET", estPath(est, "/settings"), nil), 200).data()
}

func (b *browser) patchSettings(est uuid.UUID, section string, body map[string]any) resp {
	b.e.t.Helper()
	cur := b.settings(est)[section].(map[string]any)
	full := map[string]any{"expected_version": num(cur["version"])}
	for k, v := range body {
		full[k] = v
	}
	return b.cmd("PATCH", estPath(est, "/settings/"+section), full)
}

func (b *browser) newProduct(est uuid.UUID, name string, cents int) map[string]any {
	b.e.t.Helper()
	return b.mustStatus(b.cmd("POST", estPath(est, "/products"), map[string]any{"name": name, "price_cents": cents}), 201).data()
}

func lineQty(order map[string]any, name, field string) int64 {
	it := itemByName(order, name)
	if it == nil {
		return -1
	}
	return num(it[field])
}

func staffQueue(t *testing.T, b *browser, est, queue uuid.UUID) map[string]any {
	t.Helper()
	for _, q := range b.mustStatus(b.do("GET", estPath(est, "/queues"), nil), 200).data2() {
		if q["queue_id"] == queue.String() {
			return q
		}
	}
	t.Fatalf("fila %s não encontrada", queue)
	return nil
}

// C-QTD-01 — três cortes e uma barba numa única participação: total, duração, unidades e uma
// posição na fila; pedido com preço adulterado, quantidade inválida ou acima do limite recusado.
func TestQuantitiesInOneParticipation(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	pai := e.verifiedCustomer("pai@exemplo.com")
	for name, items := range map[string][]map[string]any{
		"negativa":      {line(b.corte, -1)},
		"zero":          {line(b.corte, 0)},
		"gigante":       {line(b.corte, 1_000_000)},
		"preço no item": {{"service_id": b.corte, "quantity": 1, "unit_price_cents": 1}},
		"repetida":      {line(b.corte, 1), line(b.corte, 2)},
		"sem id":        {{"quantity": 2}},
		"os dois ids":   {{"service_id": b.corte, "product_id": b.barba, "quantity": 1}},
	} {
		if r := pai.enterItems(b.id, b.filaBeto, items...); r.status != 400 {
			t.Fatalf("%s: %d %s", name, r.status, r.raw)
		}
	}
	if r := pai.cmd("POST", estPath(b.id, "/participations"), map[string]any{"queue_id": b.filaBeto.String(),
		"items": []map[string]any{{"service_id": b.corte, "quantity": 2.5}}}); r.status != 400 {
		t.Fatalf("fracionária: %d %s", r.status, r.raw)
	}
	if r := pai.enterItems(b.id, b.filaBeto, line(b.corte, 11)); r.status != 400 {
		t.Fatalf("acima do padrão (10): %d %s", r.status, r.raw)
	}
	r := pai.mustStatus(pai.enterItems(b.id, b.filaBeto, line(b.corte, 3), line(b.barba, 1)), 201)
	var v struct{ Data contrato.Visit }
	strictDecode(t, r.raw, &v)
	p, o := visitOf(r)
	if total(o) != 3*4500+3000 || lineQty(o, "Corte", "quantity") != 3 {
		t.Fatalf("total com quantidade: %s", r.raw)
	}
	tot := o["totals"].(map[string]any)
	if num(tot["service_units"]) != 4 || num(tot["duration_minutes"]) != 3*30+20 || num(tot["product_units"]) != 0 {
		t.Fatalf("unidades e duração: %v", tot)
	}
	if n := e.count(t, `SELECT count(*) FROM participation WHERE order_id = $1`, o["id"]); n != 1 {
		t.Fatalf("um pedido = uma participação: %d", n)
	}
	explained(t, o)
	// Equipe vê "Corte ×3", uma participação pendente, 4 unidades e 110 min acumulados.
	q := staffQueue(t, b.betoB, b.id, b.filaBeto)
	entry := q["entries"].([]any)[0].(map[string]any)
	if names := fmt.Sprint(entry["service_names"]); !strings.Contains(names, "Corte ×3") || !strings.Contains(names, "Barba") {
		t.Fatalf("nome com quantidade: %v", entry["service_names"])
	}
	load := q["load"].(map[string]any)
	if num(load["pending_participations"]) != 1 || num(load["pending_service_units"]) != 4 || num(load["estimated_minutes"]) != 110 {
		t.Fatalf("carga: %v", load)
	}
	// Quem chega depois espera os 110 minutos (estimativa), mas só há uma participação à frente.
	lia := e.visitorIn(b.id, "Lia")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	if num(pl["position"]) != 2 || num(pl["estimated_wait"].(map[string]any)["minutes"]) != 110 {
		t.Fatalf("posição e espera de quem chega depois: %v", pl)
	}

	// Preço do catálogo muda: a linha existente mantém o preço aceito, também ao aumentar.
	order := pai.order(b.id, o["id"])
	b.owner.mustStatus(b.owner.cmd("PATCH", estPath(b.id, "/services/"+b.corte), map[string]any{"expected_version": 1, "price_cents": 6000}), 200)
	body := map[string]any{"expected_version": num(order["version"]), "items": []map[string]any{line(b.corte, 4), line(b.barba, 1)}}
	key := newIdemKey()
	up := pai.mustStatus(pai.do("PUT", orderPath(b.id, o["id"])+"/items", body, "Idempotency-Key", key), 200).data()
	if total(up) != 4*4500+3000 || num(itemByName(up, "Corte")["unit_price"].(map[string]any)["amount_cents"]) != 4500 {
		t.Fatalf("aumentar mantém o preço aceito: %v", up["items"])
	}
	// Repetir o mesmo comando (mesma chave) não soma de novo; o valor é absoluto.
	again := pai.mustStatus(pai.do("PUT", orderPath(b.id, o["id"])+"/items", body, "Idempotency-Key", key), 200).data()
	if lineQty(again, "Corte", "quantity") != 4 || num(again["version"]) != num(up["version"]) {
		t.Fatalf("repetição: %v", again["items"])
	}
	if n := e.count(t, `SELECT count(*) FROM order_item_quantity_change WHERE order_id = $1 AND field = 'quantity' AND from_value = 3 AND to_value = 4`, o["id"]); n != 1 {
		t.Fatalf("histórico da quantidade: %d", n)
	}
	// Limite da loja: reduzido para 3, a linha com 4 continua válida (sem efeito retroativo), mas não cresce.
	b.owner.mustStatus(b.owner.patchSettings(b.id, "catalog", map[string]any{"max_service_quantity": 3}), 200)
	cur := pai.order(b.id, o["id"])
	same := pai.mustStatus(pai.cmd("PUT", orderPath(b.id, o["id"])+"/items", map[string]any{"expected_version": num(cur["version"]),
		"items": []map[string]any{line(b.corte, 4), line(b.barba, 2)}}), 200).data()
	if lineQty(same, "Corte", "quantity") != 4 || lineQty(same, "Barba", "quantity") != 2 {
		t.Fatalf("limite não retroativo: %v", same["items"])
	}
	if r := pai.cmd("PUT", orderPath(b.id, o["id"])+"/items", map[string]any{"expected_version": num(same["version"]),
		"items": []map[string]any{line(b.corte, 5), line(b.barba, 2)}}); r.status != 400 || r.code() != "validation_failed" {
		t.Fatalf("aumentar acima do limite: %d %s", r.status, r.raw)
	}
	// Formato antigo (service_ids) continua aceito: quantidade 1.
	old := pai.mustStatus(pai.cmd("PUT", orderPath(b.id, o["id"])+"/items", map[string]any{"expected_version": num(same["version"]),
		"service_ids": []string{b.corte}}), 200).data()
	if lineQty(old, "Corte", "quantity") != 1 || itemByName(old, "Barba") != nil || total(old) != 4500 {
		t.Fatalf("formato anterior: %v", old["items"])
	}
	explained(t, old)
	_ = p
}

// C-QTD-02 — execução parcial ("2 de 3 cortes"), interrupção com revisão do dono e conclusão
// com pontos por unidade cobrada, creditados ao titular com conta.
func TestPartialExecutionReviewAndPoints(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	pomada := b.owner.newProduct(b.id, "Pomada", 2500)
	pai := e.verifiedCustomer("pai@exemplo.com")
	pv, _ := visitOf(pai.mustStatus(pai.enterItems(b.id, b.filaBeto, line(b.corte, 3), productLine(pomada["id"].(string), 2)), 201))
	visit := b.betoB.startServing(b.id, b.filaBeto, pv["id"].(string))
	_, o := b.betoB.currentVisit(b.id, visit)
	corte := itemByName(o, "Corte")
	path := orderPath(b.id, o["id"]) + "/items/" + corte["id"].(string) + "/performed-quantity"
	key := newIdemKey()
	body := map[string]any{"expected_version": num(o["version"]), "performed_quantity": 2}
	o2 := b.betoB.mustStatus(b.betoB.do("PUT", path, body, "Idempotency-Key", key), 200).data()
	if lineQty(o2, "Corte", "performed_quantity") != 2 || itemByName(o2, "Corte")["status"] != "accepted" {
		t.Fatalf("2 de 3: %v", itemByName(o2, "Corte"))
	}
	// Repetição não incrementa; diminuir o já realizado é recusado; acima do pedido também.
	if r := b.betoB.do("PUT", path, body, "Idempotency-Key", key); r.status != 200 || lineQty(r.data(), "Corte", "performed_quantity") != 2 {
		t.Fatalf("repetição: %d %s", r.status, r.raw)
	}
	if r := b.betoB.cmd("PUT", path, map[string]any{"expected_version": num(o2["version"]), "performed_quantity": 1}); r.code() != "item_already_performed" {
		t.Fatalf("diminuir realizado: %d %s", r.status, r.raw)
	}
	if r := b.betoB.cmd("PUT", path, map[string]any{"expected_version": num(o2["version"]), "performed_quantity": 4}); r.status != 400 {
		t.Fatalf("acima do pedido: %d %s", r.status, r.raw)
	}
	// A equipe não reduz o pedido abaixo do realizado.
	if r := b.betoB.cmd("PUT", orderPath(b.id, o["id"])+"/items", map[string]any{"expected_version": num(o2["version"]), "reason": "engano",
		"items": []map[string]any{line(b.corte, 1), productLine(pomada["id"].(string), 2)}}); r.code() != "item_already_performed" {
		t.Fatalf("reduzir abaixo do realizado: %d %s", r.status, r.raw)
	}
	// Concluir com só 2 dos 3 cortes exige motivo; a conclusão não registra menos que o realizado.
	p, cur := b.betoB.currentVisit(b.id, visit)
	items := func(corteQty int, reason bool) []map[string]any {
		c := map[string]any{"item_id": corte["id"], "outcome": "performed", "quantity": corteQty}
		if reason {
			c["reason"] = "o terceiro filho desistiu"
		}
		return []map[string]any{c, {"item_id": itemByName(cur, "Pomada")["id"], "outcome": "performed", "quantity": 2}}
	}
	completeBody := func(it []map[string]any) map[string]any {
		return map[string]any{"expected_version": num(p["version"]), "order_expected_version": num(cur["version"]), "items": it}
	}
	if r := b.betoB.partCmd(b.id, p["id"].(string), "complete", completeBody(items(2, false))); r.status != 400 {
		t.Fatalf("menos unidades sem motivo: %d %s", r.status, r.raw)
	}
	if r := b.betoB.partCmd(b.id, p["id"].(string), "complete", completeBody(items(1, true))); r.code() != "item_already_performed" {
		t.Fatalf("conclusão abaixo do realizado: %d %s", r.status, r.raw)
	}
	r := b.betoB.mustStatus(b.betoB.partCmd(b.id, p["id"].(string), "complete", completeBody(items(2, true))), 200)
	_, oc := visitOf(r)
	if total(oc) != 2*4500+2*2500 || lineQty(oc, "Corte", "charged_quantity") != 2 || lineQty(oc, "Pomada", "charged_quantity") != 2 {
		t.Fatalf("conclusão parcial por unidade: %s", r.raw)
	}
	explained(t, oc)
	// Pontos previstos: só unidades de serviço cobradas (2 × 10); produto não pontua.
	po := pai.order(b.id, oc["id"])
	if num(po["points_expected"]) != 20 {
		t.Fatalf("pontos previstos: %v", po["points_expected"])
	}
	b.betoB.mustStatus(b.betoB.receive(b.id, oc, map[string]any{"method": "card", "card_type": "debit", "amount_cents": total(oc)}), 201)
	if l := pai.loyalty(b.id); num(l["balance"]) != 20 {
		t.Fatalf("pontos creditados ao titular: %v", l)
	}

	// Interrupção: 2 de 3 cortes feitos, o terceiro começado; dono revisa e cobra 2 unidades.
	irmao := e.verifiedCustomer("irmao@exemplo.com")
	pi, _ := visitOf(irmao.mustStatus(irmao.enterItems(b.id, b.filaBeto, line(b.corte, 3), productLine(pomada["id"].(string), 1)), 201))
	vi := b.betoB.startServing(b.id, b.filaBeto, pi["id"].(string))
	ppi, oi := b.betoB.currentVisit(b.id, vi)
	c3, pom := itemByName(oi, "Corte"), itemByName(oi, "Pomada")
	interrupt := map[string]any{"expected_version": num(ppi["version"]), "order_expected_version": num(oi["version"]), "reason": "falta de energia",
		"items": []map[string]any{{"item_id": c3["id"], "outcome": "partially_performed", "quantity": 2}, {"item_id": pom["id"], "outcome": "not_started"}}}
	if r := b.betoB.partCmd(b.id, ppi["id"].(string), "interrupt", map[string]any{"expected_version": num(ppi["version"]),
		"order_expected_version": num(oi["version"]), "reason": "falta de energia",
		"items": []map[string]any{{"item_id": c3["id"], "outcome": "fully_performed", "quantity": 2}, {"item_id": pom["id"], "outcome": "not_started"}}}); r.status != 400 {
		t.Fatalf("feito por inteiro com menos unidades: %d %s", r.status, r.raw)
	}
	ri := b.betoB.mustStatus(b.betoB.partCmd(b.id, ppi["id"].(string), "interrupt", interrupt), 200)
	_, oi = visitOf(ri)
	if oi["status"] != "pending_review" || num(itemByName(oi, "Corte")["reported_quantity"]) != 2 || total(oi) != 3*4500+2500 {
		t.Fatalf("interrupção informada: %s", ri.raw)
	}
	review := map[string]any{"expected_version": num(oi["version"]), "review_note": "conferido com o profissional",
		"items": []map[string]any{{"item_id": c3["id"], "outcome": "partially_performed", "quantity": 2}, {"item_id": pom["id"], "outcome": "fully_performed"}}}
	rv := b.owner.mustStatus(b.owner.cmd("POST", orderPath(b.id, oi["id"])+"/close-interruption", review), 200).data()
	if total(rv) != 2*4500+2500 || rv["status"] != "awaiting_payment" || lineQty(rv, "Corte", "charged_quantity") != 2 {
		t.Fatalf("revisão por unidades: %v", rv)
	}
	explained(t, rv)
	if po := irmao.order(b.id, oi["id"]); num(po["points_expected"]) != 0 {
		t.Fatalf("interrupção não gera pontos: %v", po["points_expected"])
	}
}

// C-PROD-01 — produtos simples: catálogo, público, nunca sozinho na entrada, sem duração, sem
// pontos; desativado bloqueia novas inclusões sem tirar das comandas; desligado esconde tudo.
func TestProductsAsComplement(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	cat := b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/catalog-categories"), map[string]any{"kind": "product", "name": "Finalizadores"}), 201).data()
	if r := b.owner.cmd("POST", estPath(b.id, "/catalog-categories"), map[string]any{"kind": "product", "name": "finalizadores"}); r.status != 400 {
		t.Fatalf("categoria repetida: %d %s", r.status, r.raw)
	}
	pomada := b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/products"), map[string]any{"name": "Pomada", "price_cents": 2500,
		"category_id": cat["id"], "description": "Fixação média"}), 201).data()
	if r := b.betoB.cmd("POST", estPath(b.id, "/products"), map[string]any{"name": "X", "price_cents": 100}); r.status != 403 {
		t.Fatalf("profissional cadastrando produto: %d", r.status)
	}
	if r := b.owner.cmd("POST", estPath(b.id, "/products"), map[string]any{"name": "X", "price_cents": 0}); r.status != 400 {
		t.Fatalf("preço zero: %d", r.status)
	}
	// Outra loja não vê nem usa.
	outra := e.shop("outra-loja", "olga@exemplo.com")
	if r := outra.owner.do("GET", estPath(b.id, "/products"), nil); r.status != 404 && r.status != 403 {
		t.Fatalf("catálogo privado de outra loja: %d", r.status)
	}
	anon := e.browser()
	pub := anon.mustStatus(anon.do("GET", "/v1/public/establishments/"+b.id.String()+"/products", nil), 200)
	if len(pub.data2()) != 1 || pub.data2()[0]["category_id"] != cat["id"] {
		t.Fatalf("produtos públicos: %s", pub.raw)
	}
	var lp struct{ Data []contrato.PublicProduct }
	strictDecode(t, pub.raw, &lp)

	lia := e.visitorIn(b.id, "Lia")
	if r := lia.enterItems(b.id, b.filaBeto, productLine(pomada["id"].(string), 1)); r.status != 400 {
		t.Fatalf("só produto: %d %s", r.status, r.raw)
	}
	pl, ol := visitOf(lia.mustStatus(lia.enterItems(b.id, b.filaBeto, line(b.corte, 1), productLine(pomada["id"].(string), 2)), 201))
	if total(ol) != 4500+2*2500 || num(ol["totals"].(map[string]any)["duration_minutes"]) != 30 || num(ol["totals"].(map[string]any)["product_units"]) != 2 {
		t.Fatalf("produto não entra na duração: %v", ol)
	}
	if pr := itemByName(ol, "Pomada"); pr["kind"] != "product" || num(pr["points"]) != 0 || num(pr["duration_minutes"]) != 0 {
		t.Fatalf("linha de produto: %v", pr)
	}
	if q := staffQueue(t, b.betoB, b.id, b.filaBeto); num(q["load"].(map[string]any)["estimated_minutes"]) != 30 {
		t.Fatalf("estimativa sem produto: %v", q["load"])
	}
	// Desativado: continua na comanda de Lia; nova inclusão recusada.
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/products/"+pomada["id"].(string)+"/deactivate"), map[string]any{"expected_version": num(pomada["version"])}), 200)
	if o := lia.order(b.id, ol["id"]); itemByName(o, "Pomada") == nil {
		t.Fatalf("produto desativado sumiu da comanda: %v", o["items"])
	}
	joao := e.visitorIn(b.id, "João")
	if r := joao.enterItems(b.id, b.filaBeto, line(b.corte, 1), productLine(pomada["id"].(string), 1)); r.code() != "product_inactive" {
		t.Fatalf("produto inativo: %d %s", r.status, r.raw)
	}
	// Produtos desligados na loja: lista pública vazia e nenhuma inclusão.
	gel := b.owner.newProduct(b.id, "Gel", 1500)
	b.owner.mustStatus(b.owner.patchSettings(b.id, "catalog", map[string]any{"products_enabled": false}), 200)
	if pub := anon.mustStatus(anon.do("GET", "/v1/public/establishments/"+b.id.String()+"/products", nil), 200); len(pub.data2()) != 0 {
		t.Fatalf("produtos desligados ainda públicos: %s", pub.raw)
	}
	if r := joao.enterItems(b.id, b.filaBeto, line(b.corte, 1), productLine(gel["id"].(string), 1)); r.code() != "products_disabled" {
		t.Fatalf("produtos desligados: %d %s", r.status, r.raw)
	}
	// A comanda de Lia segue com o produto até o fim: entrega registrada à parte do serviço.
	v := b.betoB.startServing(b.id, b.filaBeto, pl["id"].(string))
	_, o := b.betoB.currentVisit(b.id, v)
	path := orderPath(b.id, o["id"]) + "/items/" + itemByName(o, "Pomada")["id"].(string) + "/mark-performed"
	o = b.betoB.mustStatus(b.betoB.cmd("POST", path, map[string]any{"expected_version": num(o["version"])}), 200).data()
	if lineQty(o, "Pomada", "performed_quantity") != 2 || lineQty(o, "Corte", "performed_quantity") != 0 {
		t.Fatalf("entrega de produto ≠ serviço realizado: %v", o["items"])
	}
}

// C-QTD-03 — solicitação de mudança com quantidade depois da chamada; aprovar recalcula total,
// duração e fila; recusar mantém tudo.
func TestChangeRequestWithQuantities(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	pomada := b.owner.newProduct(b.id, "Pomada", 2500)
	maria := e.verifiedCustomer("maria@exemplo.com")
	p, o := visitOf(maria.mustStatus(maria.enterItems(b.id, b.filaBeto, line(b.corte, 1)), 201))
	b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
	order := maria.order(b.id, o["id"])
	corte := itemByName(order, "Corte")
	cr := maria.mustStatus(maria.cmd("POST", orderPath(b.id, o["id"])+"/change-requests", map[string]any{"expected_version": num(order["version"]),
		"set_quantities": []map[string]any{{"item_id": corte["id"], "quantity": 2}}, "add_items": []map[string]any{productLine(pomada["id"].(string), 1)}}), 201).data()
	pend := cr["pending_change_request"].(map[string]any)
	if total(cr) != 4500 || len(pend["set_quantities"].([]any)) != 1 || num(pend["add"].([]any)[0].(map[string]any)["quantity"]) != 1 {
		t.Fatalf("solicitação pendente não muda o total: %v", cr)
	}
	ap := b.betoB.mustStatus(b.betoB.cmd("POST", orderPath(b.id, o["id"])+"/change-requests/"+pend["id"].(string)+"/approve",
		map[string]any{"expected_version": num(cr["version"])}), 200).data()
	if total(ap) != 2*4500+2500 || lineQty(ap, "Corte", "quantity") != 2 || num(ap["totals"].(map[string]any)["duration_minutes"]) != 60 {
		t.Fatalf("aprovada: %v", ap)
	}
	if n := e.count(t, `SELECT count(*) FROM order_item_quantity_change WHERE order_id = $1 AND source = 'change_request'`, o["id"]); n != 1 {
		t.Fatalf("histórico da solicitação: %d", n)
	}
	cr2 := maria.mustStatus(maria.cmd("POST", orderPath(b.id, o["id"])+"/change-requests", map[string]any{"expected_version": num(ap["version"]),
		"set_quantities": []map[string]any{{"item_id": corte["id"], "quantity": 5}}}), 201).data()
	rj := b.betoB.mustStatus(b.betoB.cmd("POST", orderPath(b.id, o["id"])+"/change-requests/"+cr2["pending_change_request"].(map[string]any)["id"].(string)+"/reject",
		map[string]any{"expected_version": num(cr2["version"]), "reason": "não dá tempo hoje"}), 200).data()
	if total(rj) != total(ap) || lineQty(rj, "Corte", "quantity") != 2 {
		t.Fatalf("recusada mantém: %v", rj)
	}
	_ = p
}

// C-ENT-01 — capacidade, lotação e liberação automática, bloqueio manual persistente,
// reabertura sem ignorar a capacidade, transferência recusada sem perder o lugar e a disputa
// concorrente pela última vaga.
func TestAdmissionAndCapacity(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	qPath := func(q uuid.UUID, op string) string { return estPath(b.id, "/queues/"+q.String()+"/"+op) }
	version := func(q uuid.UUID) int64 { return num(staffQueue(t, b.owner, b.id, q)["version"]) }
	if r := b.betoB.cmd("PUT", qPath(b.filaBeto, "capacity"), map[string]any{"expected_version": version(b.filaBeto), "capacity": 2}); r.status != 403 {
		t.Fatalf("profissional mexendo na capacidade: %d", r.status)
	}
	sq := b.owner.mustStatus(b.owner.cmd("PUT", qPath(b.filaBeto, "capacity"), map[string]any{"expected_version": version(b.filaBeto), "capacity": 2}), 200).data()
	if num(sq["admission"].(map[string]any)["capacity"]) != 2 {
		t.Fatalf("capacidade: %v", sq["admission"])
	}
	ana := e.visitorIn(b.id, "Ana")
	pa, _ := visitOf(ana.mustStatus(ana.enterItems(b.id, b.filaBeto, line(b.corte, 3)), 201)) // 3 cortes = 1 vaga
	// Disputa simultânea pela última vaga: só uma entra.
	var wg sync.WaitGroup
	results := make([]resp, 6)
	for i := range results {
		br := e.visitorIn(b.id, fmt.Sprintf("Rival%d", i))
		wg.Go(func() { results[i] = br.enter(b.id, b.filaBeto, b.corte) })
	}
	wg.Wait()
	ok, full := 0, 0
	for _, r := range results {
		switch {
		case r.status == 201:
			ok++
		case r.code() == "queue_full":
			full++
		default:
			t.Fatalf("disputa: %d %s", r.status, r.raw)
		}
	}
	if ok != 1 || full != len(results)-1 {
		t.Fatalf("última vaga: %d entraram, %d cheias", ok, full)
	}
	lia := e.visitorIn(b.id, "Lia")
	if r := lia.enter(b.id, b.filaBeto, b.corte); r.code() != "queue_full" || r.problem()["detail"] != "A fila está cheia no momento. Tente novamente quando uma vaga for liberada." {
		t.Fatalf("lotada: %d %s", r.status, r.raw)
	}
	pub := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments/"+b.id.String()+"/queues", nil), 200)
	for _, q := range pub.data2() {
		if q["queue_id"] == b.filaBeto.String() && q["entry_state"] != "full" {
			t.Fatalf("público deveria ver lotação: %v", q)
		}
	}
	// Chamar não libera (chamado ocupa vaga); iniciar o atendimento libera automaticamente.
	called, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	if r := lia.enter(b.id, b.filaBeto, b.corte); r.code() != "queue_full" {
		t.Fatalf("chamado ainda ocupa vaga: %d", r.status)
	}
	started := b.betoB.mustStatus(b.betoB.partCmd(b.id, called["id"].(string), "start", map[string]any{"expected_version": num(called["version"])}), 200)
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	// Fechar a entrada (profissional da própria fila, permitido por padrão): ninguém sai.
	b.betoB.mustStatus(b.betoB.cmd("POST", qPath(b.filaBeto, "close-admission"), map[string]any{"expected_version": version(b.filaBeto)}), 200)
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	if r := caio.cmd("POST", qPath(b.filaBeto, "reopen-admission"), map[string]any{"expected_version": version(b.filaBeto)}); r.status != 404 && r.status != 403 {
		t.Fatalf("profissional de outra fila: %d", r.status)
	}
	// Desistência libera vaga, mas NÃO remove o bloqueio manual.
	b.owner.mustStatus(b.owner.partCmd(b.id, pl["id"].(string), "cancel", map[string]any{"expected_version": num(pl["version"]), "reason": "pediu para sair"}), 200)
	ze := e.visitorIn(b.id, "Zé")
	if r := ze.enter(b.id, b.filaBeto, b.corte); r.code() != "queue_entry_closed" ||
		r.problem()["detail"] != "A entrada na fila foi encerrada por enquanto. Quem já está na fila será atendido normalmente." {
		t.Fatalf("entrada fechada: %d %s", r.status, r.raw)
	}
	// A operação continua com a entrada fechada: concluir o atendimento e chamar o próximo.
	b.betoB.mustStatus(b.betoB.closeVisit(b.id, started, "complete", func(string) string { return "performed" }, nil), 200)
	b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
	// Sem permissão do dono, o profissional não reabre.
	b.owner.mustStatus(b.owner.patchSettings(b.id, "queue", map[string]any{"professionals_control_admission": false}), 200)
	if r := b.betoB.cmd("POST", qPath(b.filaBeto, "reopen-admission"), map[string]any{"expected_version": version(b.filaBeto)}); r.status != 403 {
		t.Fatalf("profissional sem permissão: %d %s", r.status, r.raw)
	}
	b.owner.mustStatus(b.owner.patchSettings(b.id, "queue", map[string]any{"professionals_control_admission": true}), 200)
	// Reabrir com a fila lotada não ignora a capacidade.
	b.owner.mustStatus(b.owner.cmd("PUT", qPath(b.filaBeto, "capacity"), map[string]any{"expected_version": version(b.filaBeto), "capacity": 1}), 200)
	ro := b.betoB.mustStatus(b.betoB.cmd("POST", qPath(b.filaBeto, "reopen-admission"), map[string]any{"expected_version": version(b.filaBeto)}), 200).data()
	if ro["admission"].(map[string]any)["status"] != "open" || ro["admission"].(map[string]any)["state"] != "full" {
		t.Fatalf("reaberta, mas lotada: %v", ro["admission"])
	}
	if r := ze.enter(b.id, b.filaBeto, b.corte); r.code() != "queue_full" {
		t.Fatalf("reabrir não ignora a capacidade: %d %s", r.status, r.raw)
	}
	// Transferência para fila lotada é recusada e a pessoa continua no mesmo lugar.
	b.owner.mustStatus(b.owner.cmd("PUT", qPath(b.filaCaio, "capacity"), map[string]any{"expected_version": version(b.filaCaio), "capacity": 1}), 200)
	x := e.visitorIn(b.id, "Xavier")
	x.mustStatus(x.enter(b.id, b.filaCaio, b.corte), 201)
	b.owner.mustStatus(b.owner.cmd("PUT", qPath(b.filaBeto, "capacity"), map[string]any{"expected_version": version(b.filaBeto), "capacity": nil}), 200)
	pz, _ := visitOf(ze.mustStatus(ze.enter(b.id, b.filaBeto, b.corte), 201))
	tr := b.owner.partCmd(b.id, pz["id"].(string), "transfer", map[string]any{"expected_version": num(pz["version"]), "target_queue_id": b.filaCaio.String(),
		"reason": "Caio está livre", "customer_consent": map[string]any{"given": true, "method": "in_person_verbal"}})
	if tr.code() != "queue_full" {
		t.Fatalf("transferência para fila lotada: %d %s", tr.status, tr.raw)
	}
	if st, q := e.partStatus(t, pz["id"].(string)); st != "waiting" || q != b.filaBeto.String() {
		t.Fatalf("perdeu o lugar: %s %s", st, q)
	}
	if after, _ := visitOf(ze.mustStatus(ze.do("GET", estPath(b.id, "/my-visit"), nil), 200)); after["ticket_code"] != pz["ticket_code"] || num(after["position"]) != num(pz["position"]) {
		t.Fatalf("posição original mudou: %v → %v", pz, after)
	}
	_ = pa
}

// C-ENT-02 — tolerância configurável gravada na chamada; mudar a configuração não altera quem
// já foi chamado; o relógio do cliente não conta.
func TestConfigurableTolerance(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	for _, bad := range []any{0, 61, 2.5} {
		if r := b.owner.patchSettings(b.id, "queue", map[string]any{"call_tolerance_minutes": bad}); r.status != 400 {
			t.Fatalf("tolerância %v: %d", bad, r.status)
		}
	}
	b.owner.mustStatus(b.owner.patchSettings(b.id, "queue", map[string]any{"call_tolerance_minutes": 3}), 200)
	lia := e.visitorIn(b.id, "Lia")
	lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201)
	p, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	calledAt, _ := time.Parse(time.RFC3339Nano, p["called_at"].(string))
	ends, _ := time.Parse(time.RFC3339Nano, p["tolerance_ends_at"].(string))
	if ends.Sub(calledAt) != 3*time.Minute || num(p["tolerance_minutes"]) != 3 {
		t.Fatalf("tolerância aplicada: %v", p)
	}
	b.owner.mustStatus(b.owner.patchSettings(b.id, "queue", map[string]any{"call_tolerance_minutes": 20}), 200)
	cur, _ := visitOf(lia.mustStatus(lia.do("GET", estPath(b.id, "/my-visit"), nil), 200))
	if cur["tolerance_ends_at"] != p["tolerance_ends_at"] || num(cur["tolerance_minutes"]) != 3 {
		t.Fatalf("mudança retroativa: %v", cur)
	}
	e.clock.Advance(2 * time.Minute)
	if r := b.betoB.partCmd(b.id, p["id"].(string), "no-show", map[string]any{"expected_version": num(p["version"])}); r.code() != "tolerance_not_elapsed" ||
		r.problem()["detail"] != "A ausência só pode ser marcada 3 minuto(s) depois da chamada." {
		t.Fatalf("antes do prazo: %s", r.raw)
	}
	e.clock.Advance(time.Minute)
	b.betoB.mustStatus(b.betoB.partCmd(b.id, p["id"].(string), "no-show", map[string]any{"expected_version": num(p["version"])}), 200)
	// Nova chamada usa a tolerância vigente (20).
	joao := e.visitorIn(b.id, "João")
	joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201)
	pj, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	if num(pj["tolerance_minutes"]) != 20 {
		t.Fatalf("nova chamada: %v", pj)
	}
}

// C-LOJA-01 — configurações por seção: só o dono, versão por seção, conflito, nada salvo sem
// meio de pagamento utilizável, visitante desligado só para novas entradas.
func TestStoreSettingsSections(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	if r := b.betoB.do("GET", estPath(b.id, "/settings"), nil); r.status != 403 {
		t.Fatalf("profissional lendo configurações: %d", r.status)
	}
	st := b.owner.settings(b.id)
	var sv struct {
		Data contrato.EstablishmentSettings
	}
	strictDecode(t, b.owner.do("GET", estPath(b.id, "/settings"), nil).raw, &sv)
	q := st["queue"].(map[string]any)
	if num(q["call_tolerance_minutes"]) != 10 || q["default_queue_capacity"] != nil || q["professionals_control_admission"] != true ||
		num(st["catalog"].(map[string]any)["max_service_quantity"]) != 10 {
		t.Fatalf("padrões: %v", st)
	}
	// Versões independentes: salvar pagamentos não conflita com quem editava a fila.
	qv := num(q["version"])
	b.owner.mustStatus(b.owner.patchSettings(b.id, "payments", map[string]any{"card_enabled": false}), 200)
	b.owner.mustStatus(b.owner.cmd("PATCH", estPath(b.id, "/settings/queue"), map[string]any{"expected_version": qv, "default_queue_capacity": 30}), 200)
	if r := b.owner.cmd("PATCH", estPath(b.id, "/settings/queue"), map[string]any{"expected_version": qv, "default_queue_capacity": 40}); r.code() != "version_conflict" {
		t.Fatalf("conflito na mesma seção: %d %s", r.status, r.raw)
	}
	if r := b.owner.patchSettings(b.id, "payments", map[string]any{"cash_enabled": false, "pix_enabled": false}); r.code() != "no_payment_method" {
		t.Fatalf("loja sem meio de pagamento: %d %s", r.status, r.raw)
	}
	// Página pública mostra só as opções públicas.
	anon := e.browser()
	pub := anon.mustStatus(anon.do("GET", "/v1/public/slugs/barbearia-teste", nil), 200).data()
	ord := pub["ordering"].(map[string]any)
	if ord["visitors_enabled"] != true || len(ord["payment_methods"].([]any)) == 0 {
		t.Fatalf("opções públicas: %v", ord)
	}
	for _, m := range ord["payment_methods"].([]any) {
		if m == "card" {
			t.Fatalf("cartão desligado aparece: %v", ord)
		}
	}
	// Visitante desligado: sessões novas recusadas; quem já tem visita continua.
	lia := e.visitorIn(b.id, "Lia")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	semVisita := e.visitorIn(b.id, "Sem visita")
	b.owner.mustStatus(b.owner.patchSettings(b.id, "queue", map[string]any{"visitors_enabled": false}), 200)
	novo := e.browser()
	if r := novo.cmd("POST", estPath(b.id, "/visitor-sessions"), map[string]any{"nick": "Novo"}); r.code() != "visitors_disabled" {
		t.Fatalf("nova sessão de visitante: %d %s", r.status, r.raw)
	}
	if r := semVisita.enter(b.id, b.filaBeto, b.corte); r.code() != "visitors_disabled" {
		t.Fatalf("visitante sem visita entrando: %d %s", r.status, r.raw)
	}
	if v, _ := visitOf(lia.mustStatus(lia.do("GET", estPath(b.id, "/my-visit"), nil), 200)); v["id"] != pl["id"] {
		t.Fatalf("visita ativa preservada: %v", v)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_event WHERE action IN ('T-ST-02', 'T-ST-03') AND establishment_id = $1`, b.id); n < 3 {
		t.Fatalf("auditoria das configurações: %d", n)
	}
}

// C-TAXA-01 — custos de recebimento: percentual + fixo por transação, arredondamento único,
// zero explícito, não configurado, cartão sem modalidade, mudança sem recalcular o passado e
// nada disso visível para cliente ou profissional.
func TestReceiptFees(t *testing.T) {
	if got := estimate(10000, 250, 50); got != 300 {
		t.Fatalf("exemplo do ADR: %d", got)
	}
	if got := estimate(7500, 250, 0); got != 188 { // 187,5 → 188 (metade para cima)
		t.Fatalf("arredondamento: %d", got)
	}
	e := newEnv(t)
	b := e.barbearia(t)
	pacote := b.owner.createService(b.id, map[string]any{"name": "Pacote", "price_cents": 10000, "duration_minutes": 60, "points": 0,
		"professional_ids": []string{b.beto.String()}})["id"].(string)
	fees := estPath(b.id, "/payment-fees")
	if r := b.betoB.do("GET", fees, nil); r.status != 403 {
		t.Fatalf("profissional vendo custos: %d", r.status)
	}
	if r := b.betoB.cmd("PUT", fees+"/debit", map[string]any{"expected_version": 0, "percent_basis_points": 1, "fixed_cents": 0}); r.status != 403 {
		t.Fatalf("profissional alterando custo: %d", r.status)
	}
	for _, bad := range []map[string]any{{"expected_version": 0, "percent_basis_points": -1, "fixed_cents": 0},
		{"expected_version": 0, "percent_basis_points": 3001, "fixed_cents": 0}, {"expected_version": 0, "percent_basis_points": 2.5, "fixed_cents": 0}} {
		if r := b.owner.cmd("PUT", fees+"/debit", bad); r.status != 400 {
			t.Fatalf("custo inválido %v: %d", bad, r.status)
		}
	}
	b.owner.mustStatus(b.owner.cmd("PUT", fees+"/debit", map[string]any{"expected_version": 0, "percent_basis_points": 250, "fixed_cents": 50}), 200)
	b.owner.mustStatus(b.owner.cmd("PUT", fees+"/pix", map[string]any{"expected_version": 0, "percent_basis_points": 0, "fixed_cents": 0}), 200)
	list := b.owner.mustStatus(b.owner.do("GET", fees, nil), 200)
	var lf struct{ Data []contrato.PaymentFee }
	strictDecode(t, list.raw, &lf)
	serve := func(br *browser, items ...map[string]any) map[string]any {
		p, o := visitOf(br.mustStatus(br.enterItems(b.id, b.filaBeto, items...), 201))
		b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
		return b.betoB.order(b.id, o["id"])
	}
	// Três unidades pagas numa transação: um componente fixo só.
	um := serve(e.visitorIn(b.id, "Um"), line(pacote, 1))
	b.betoB.mustStatus(b.betoB.receive(b.id, um, map[string]any{"method": "card", "card_type": "debit", "amount_cents": 10000}), 201)
	tres := serve(e.visitorIn(b.id, "Três"), line(b.corte, 3))
	b.betoB.mustStatus(b.betoB.receive(b.id, tres, map[string]any{"method": "card", "card_type": "debit", "amount_cents": 13500}), 201)
	var feeTres int64
	if err := e.pool.QueryRow(t.Context(), `SELECT estimated_cents FROM receipt_fee f JOIN receipt r ON r.id = f.receipt_id WHERE r.order_id = $1`, tres["id"]).Scan(&feeTres); err != nil {
		t.Fatal(err)
	}
	if feeTres != estimate(13500, 250, 50) {
		t.Fatalf("fixo por transação: %d", feeTres)
	}
	// Cliente e profissional não veem custo nenhum na comanda.
	if r := b.betoB.do("GET", orderPath(b.id, um["id"]), nil); bytesContains(r.raw, `"estimated`) || bytesContains(r.raw, `"fee`) { // chaves JSON (um UUID pode conter "fee")
		t.Fatalf("custo exposto na comanda: %s", r.raw)
	}
	// Custo muda depois: o recebimento antigo não é recalculado.
	b.owner.mustStatus(b.owner.cmd("PUT", fees+"/debit", map[string]any{"expected_version": 1, "percent_basis_points": 500, "fixed_cents": 0}), 200)
	if r := b.owner.cmd("PUT", fees+"/debit", map[string]any{"expected_version": 1, "percent_basis_points": 1, "fixed_cents": 0}); r.code() != "version_conflict" {
		t.Fatalf("versão velha do custo: %d %s", r.status, r.raw)
	}
	var fee int64
	if err := e.pool.QueryRow(t.Context(), `SELECT estimated_cents FROM receipt_fee f JOIN receipt r ON r.id = f.receipt_id WHERE r.order_id = $1`, um["id"]).Scan(&fee); err != nil {
		t.Fatal(err)
	}
	if fee != 300 {
		t.Fatalf("custo histórico recalculado: %d", fee)
	}
	// Dinheiro: não se aplica. Crédito: não configurado → resultado incompleto.
	cash := serve(e.visitorIn(b.id, "Dinheiro"), line(b.corte, 1))
	b.betoB.mustStatus(b.betoB.receive(b.id, cash, map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000}), 201)
	costs := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/receipt-costs"), nil), 200)
	var rc struct{ Data contrato.ReceiptCosts }
	strictDecode(t, costs.raw, &rc)
	c := costs.data()
	if num(c["gross_cents"]) != 10000+13500+4500 || num(c["estimated_fees_cents"]) != 300+feeTres || c["complete"] != true ||
		num(c["after_known_costs_cents"]) != 28000-300-feeTres {
		t.Fatalf("resumo completo: %s", costs.raw)
	}
	credito := serve(e.visitorIn(b.id, "Crédito"), line(b.corte, 1))
	b.betoB.mustStatus(b.betoB.receive(b.id, credito, map[string]any{"method": "card", "card_type": "credit", "amount_cents": 4500}), 201)
	semModalidade := serve(e.visitorIn(b.id, "Antigo"), line(b.corte, 1))
	b.betoB.mustStatus(b.betoB.receive(b.id, semModalidade, map[string]any{"method": "card", "amount_cents": 4500}), 201)
	c = b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/receipt-costs"), nil), 200).data()
	if c["complete"] != false || num(c["not_configured_count"]) != 1 || num(c["unknown_fee_count"]) != 1 {
		t.Fatalf("cobertura incompleta: %v", c)
	}
	if r := b.betoB.do("GET", estPath(b.id, "/receipt-costs"), nil); r.status != 403 {
		t.Fatalf("profissional vendo resumo de custos: %d", r.status)
	}
	// Meio desligado: nova escolha recusada.
	b.owner.mustStatus(b.owner.patchSettings(b.id, "payments", map[string]any{"cash_enabled": false}), 200)
	ultima := serve(e.verifiedCustomer("ultima@exemplo.com"), line(b.corte, 1))
	if r := b.betoB.choose(b.id, ultima, "cash", false); r.code() != "payment_method_disabled" {
		t.Fatalf("meio desligado: %d %s", r.status, r.raw)
	}
	if r := b.betoB.receive(b.id, ultima, map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000}); r.code() != "payment_method_disabled" {
		t.Fatalf("recebimento em meio desligado: %d %s", r.status, r.raw)
	}
	_ = http.MethodGet
}

// data2 devolve "data" quando é uma lista.
func (r resp) data2() []map[string]any {
	var out []map[string]any
	list, _ := r.body["data"].([]any)
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// problem devolve o corpo do erro (Problem).
func (r resp) problem() map[string]any { return r.body }

func estimate(amount int64, bp int, fixed int64) int64 { return (amount*int64(bp)+5000)/10000 + fixed }

func bytesContains(b []byte, s string) bool { return strings.Contains(string(b), s) }

// C-IND-01 — indicadores com quantidade: três cortes de uma comanda = 1 participação concluída e
// 3 unidades realizadas; produto entregue à parte; custos estimados e cobertura no resumo.
func TestAnalyticsWithQuantitiesProductsAndFees(t *testing.T) {
	e := newEnv(t) // 2026-11-12 12:00 UTC
	b := e.barbearia(t)
	pomada := b.owner.newProduct(b.id, "Pomada", 2500)
	b.owner.mustStatus(b.owner.cmd("PUT", estPath(b.id, "/payment-fees/debit"), map[string]any{"expected_version": 0, "percent_basis_points": 200, "fixed_cents": 30}), 200)
	v := e.visitorIn(b.id, "Família")
	p, o := visitOf(v.mustStatus(v.enterItems(b.id, b.filaBeto, line(b.corte, 3), productLine(pomada["id"].(string), 1)), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	order := b.betoB.order(b.id, o["id"])
	b.betoB.mustStatus(b.betoB.receive(b.id, order, map[string]any{"method": "card", "card_type": "debit", "amount_cents": 16000}), 201)

	pc := e.pc()
	pc.activate(b.email, "PC")
	pc.b.mustStatus(pc.link(b.owner), 201)
	get := func(rest string) map[string]any {
		t.Helper()
		q := url.Values{"from": {"2026-11-12"}, "to": {"2026-11-12"}}
		return pc.b.mustStatus(pc.call("GET", analyticsPath(b.id, rest+"?"+q.Encode()), nil), 200).data()
	}
	cur := get("/summary")["current"].(map[string]any)
	mustVal(t, "uma participação concluída", cur["completed"].(map[string]any)["count"], 1)
	cash := cur["cash"].(map[string]any)
	fee := estimate(16000, 200, 30)
	mustVal(t, "custo estimado", cash["fees_estimated_cents"], float64(fee))
	mustVal(t, "após custos", cash["after_costs_cents"], float64(16000-fee))
	mustVal(t, "cobertura", cash["fee_coverage"], 1)
	services := get("/breakdowns/services")["rows"].([]any)
	if len(services) != 1 || num(services[0].(map[string]any)["performed"]) != 3 || num(services[0].(map[string]any)["performed_value_cents"]) != 13500 {
		t.Fatalf("unidades de serviço: %v", services)
	}
	products := get("/breakdowns/products")["rows"].([]any)
	if len(products) != 1 || num(products[0].(map[string]any)["delivered"]) != 1 || num(products[0].(map[string]any)["delivered_value_cents"]) != 2500 {
		t.Fatalf("produtos entregues: %v", products)
	}
}

// C-BUSCA-01 — busca de comandas no servidor: senha, nick ou item, sem diferenciar maiúsculas
// nem acentos; o profissional continua vendo só os próprios atendimentos.
func TestOrderSearchOnServer(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	joao := e.visitorIn(b.id, "João Paulo")
	_, oj := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.barba), 201))
	ana := e.visitorIn(b.id, "Ana")
	_, oa := visitOf(ana.mustStatus(ana.enter(b.id, b.filaCaio, b.corte), 201))
	find := func(br *browser, term string) []string {
		t.Helper()
		var ids []string
		for _, o := range br.mustStatus(br.do("GET", estPath(b.id, "/orders?q="+url.QueryEscape(term)), nil), 200).data2() {
			ids = append(ids, o["id"].(string))
		}
		return ids
	}
	if got := find(b.owner, "  JOAO "); len(got) != 1 || got[0] != oj["id"] {
		t.Fatalf("nick sem acento: %v", got)
	}
	if got := find(b.owner, "córte"); len(got) != 1 || got[0] != oa["id"] {
		t.Fatalf("item com acento digitado: %v", got)
	}
	if got := find(b.owner, "b001"); len(got) != 1 || got[0] != oa["id"] {
		t.Fatalf("senha: %v", got)
	}
	if got := find(b.betoB, "ana"); len(got) != 0 {
		t.Fatalf("profissional vendo atendimento de outro: %v", got)
	}
	if got := find(b.owner, "%"); len(got) != 0 {
		t.Fatalf("curinga literal: %v", got)
	}
}
