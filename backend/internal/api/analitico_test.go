package api_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Cálculos do Dashboard com dados controlados (fluxo real da API, relógio simulado, fuso
// America/Sao_Paulo = UTC−3). Dicionário: docs/desktop/metricas.md.

func val(t *testing.T, v any) (float64, string) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("valor fora do formato: %v", v)
	}
	st, _ := m["status"].(string)
	f, _ := m["value"].(float64)
	return f, st
}

func mustVal(t *testing.T, what string, v any, want float64) {
	t.Helper()
	got, st := val(t, v)
	if st != "ok" || got != want {
		t.Fatalf("%s: %v (%s), esperado %v", what, got, st, want)
	}
}

func mustStatus(t *testing.T, what string, v any, want string) {
	t.Helper()
	if _, st := val(t, v); st != want {
		t.Fatalf("%s: status %s, esperado %s", what, st, want)
	}
}

func bucketByLabel(t *testing.T, series map[string]any, label string) map[string]any {
	t.Helper()
	for _, b := range series["buckets"].([]any) {
		m := b.(map[string]any)
		if m["label"] == label {
			return m
		}
	}
	t.Fatalf("bucket %s ausente: %v", label, series["buckets"])
	return nil
}

func TestAnalyticsCalculations(t *testing.T) {
	e := newEnv(t) // relógio: 2026-11-12 12:00 UTC = 09:00 em São Paulo
	b := e.barbearia(t)
	pezinho := b.owner.createService(b.id, map[string]any{"name": "Pezinho", "price_cents": 3500, "duration_minutes": 10, "points": 0,
		"professional_ids": []string{b.beto.String()}})["id"].(string)
	pay := func(order map[string]any, amount, tendered int64) map[string]any {
		o := b.betoB.mustStatus(b.betoB.receive(b.id, order, map[string]any{"method": "cash", "amount_cents": amount, "cash_tendered_cents": tendered}), 201).data()
		rec := receiptsOf(o)[0]
		b.betoB.mustStatus(b.betoB.confer(b.id, o, rec["id"]), 200)
		return o
	}
	// Sessões de equipe vencem em 12 h: depois de cada salto do relógio, a equipe entra de novo
	// (a vaga de dono está livre porque a sessão anterior venceu: sem conflito).
	relogin := func() {
		b.betoB.mustStatus(b.betoB.login("beto@exemplo.com", goodPassword, "staff"), 200)
		b.owner.mustStatus(b.owner.login(b.email, goodPassword, "staff"), 200)
	}
	visit := func(nick string, services ...string) (string, map[string]any) {
		v := e.visitorIn(b.id, nick)
		p, o := visitOf(v.mustStatus(v.enter(b.id, b.filaBeto, services...), 201))
		return p["id"].(string), o
	}

	// 1) Comanda com DOIS serviços (Corte + Barba = 75,00) e UM recebimento; dinheiro com troco.
	p1, o1 := visit("Um", b.corte, b.barba)
	e.clock.Advance(10 * time.Minute) // espera 600 s
	b.betoB.serve(b.id, b.filaBeto, p1)
	pay(b.betoB.order(b.id, o1["id"]), 7500, 10000)
	// 2) R$ 35 pagos com R$ 50 (troco 15): entra 35 no caixa.
	p2, o2 := visit("Dois", pezinho)
	e.clock.Advance(20 * time.Minute) // espera 1200 s
	b.betoB.serve(b.id, b.filaBeto, p2)
	o2paid := pay(b.betoB.order(b.id, o2["id"]), 3500, 5000)
	// 3) Entrou na fila do Caio e nunca foi chamado: fora da espera.
	v3 := e.visitorIn(b.id, "Tres")
	v3.mustStatus(v3.enter(b.id, b.filaCaio, b.corte), 201)
	// 4) Pago às 23:30 de 12/11 em São Paulo (02:30 UTC de 13/11): conta no dia 12.
	e.clock.Advance(time.Date(2026, 11, 13, 2, 20, 0, 0, time.UTC).Sub(e.clock.Now()))
	relogin()
	p4, o4 := visit("Quatro", b.corte)
	e.clock.Advance(10 * time.Minute)
	b.betoB.serve(b.id, b.filaBeto, p4)
	pay(b.betoB.order(b.id, o4["id"]), 4500, 4500)
	// 5) Pix pendente (cobrança criada, não paga) em outra comanda: não entra.
	p5, o5 := visit("Cinco", b.corte)
	b.betoB.serve(b.id, b.filaBeto, p5)
	order5 := b.betoB.order(b.id, o5["id"])
	b.betoB.mustStatus(b.betoB.createPix(b.id, order5, newIdemKey()), 201)

	// 13/11 15:00 UTC (12:00 local): estorno do recebimento de 35,00 — primeiro em análise.
	e.clock.Advance(time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC).Sub(e.clock.Now()))
	relogin()
	b.owner.reauth()
	rf := refundsOf(b.owner.mustStatus(b.owner.refund(b.id.String(), o2paid, receiptsOf(o2paid)[0]["id"], "serviço refeito"), 202).data())[0]

	pc := e.pc()
	pc.activate(b.email, "PC")
	pc.b.mustStatus(pc.link(b.owner), 201)
	get := func(rest string, q url.Values) resp {
		t.Helper()
		return pc.b.mustStatus(pc.call("GET", analyticsPath(b.id, rest+"?"+q.Encode()), nil), 200)
	}
	q := url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}, "granularity": {"day"}}

	cash := get("/series/cash", q).data()
	day12, day13 := bucketByLabel(t, cash, "12/11"), bucketByLabel(t, cash, "13/11")
	mustVal(t, "recebido 12/11", day12["values"].(map[string]any)["received_cents"], 15500)
	mustVal(t, "estorno em análise não reduz", day13["values"].(map[string]any)["refunded_cents"], 0)
	mustVal(t, "líquido antes da confirmação", cash["totals"].(map[string]any)["net_cents"], 15500)

	// Confirma o estorno (evidência documentada): entra no dia da efetivação (13/11).
	b.owner.mustStatus(b.owner.cmd("POST", orderPath(b.id, o2paid["id"])+"/refunds/"+rf["id"].(string)+"/confirm",
		map[string]any{"evidence_note": "devolvido em espécie"}), 200)
	cash = get("/series/cash", q).data()
	day12, day13 = bucketByLabel(t, cash, "12/11"), bucketByLabel(t, cash, "13/11")
	mustVal(t, "recebido 12/11 (sem duplicar itens; Pix pendente fora)", day12["values"].(map[string]any)["received_cents"], 15500)
	mustVal(t, "estorno no dia da efetivação", day13["values"].(map[string]any)["refunded_cents"], 3500)
	mustVal(t, "nada de estorno no dia 12", day12["values"].(map[string]any)["refunded_cents"], 0)
	mustVal(t, "líquido do intervalo", cash["totals"].(map[string]any)["net_cents"], 12000)

	// Resumo, tabela e detalhamento reconciliam o mesmo valor.
	sum := get("/summary", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}}).data()
	cur := sum["current"].(map[string]any)
	mustVal(t, "resumo líquido", cur["cash"].(map[string]any)["net_cents"], 12000)
	mustVal(t, "concluídos", cur["completed"].(map[string]any)["count"], 4)
	wait := cur["wait"].(map[string]any)["median_seconds"].(map[string]any)
	if wait["value"].(float64) != 600 || num(wait["sample"]) != 4 {
		t.Fatalf("espera mediana: %v", wait)
	}
	recs := get("/records/receipts", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"},
		"bucket_start": {day12["start"].(string)}, "bucket_end": {day12["end"].(string)}}).data()
	if num(recs["total"]) != 3 || num(recs["total_cents"]) != 15500 {
		t.Fatalf("registros do dia 12: %v", recs)
	}
	for _, it := range recs["items"].([]any) {
		m := it.(map[string]any)
		if num(m["amount_cents"]) == 3500 && (num(m["cash_tendered_cents"]) != 5000 || num(m["change_cents"]) != 1500) {
			t.Fatalf("troco: %v", m)
		}
	}
	pm := get("/breakdowns/payment_methods", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}}).data()["rows"].([]any)
	if c := pm[0].(map[string]any); c["method"] != "cash" || num(c["received_cents"]) != 15500 || num(c["net_cents"]) != 12000 {
		t.Fatalf("meios de pagamento: %v", pm)
	}

	// Filtros: profissional restringe; serviço é inaplicável ao caixa (sem rateio).
	qc := url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}, "professional_id": {b.caio.String()}}
	mustVal(t, "caixa do Caio", get("/series/cash", qc).data()["totals"].(map[string]any)["received_cents"], 0)
	mustStatus(t, "espera do Caio (sem chamadas)", get("/series/wait", qc).data()["totals"].(map[string]any)["median_seconds"], "empty")
	qs := url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}, "service_id": {b.corte}}
	sv := get("/series/cash", qs)
	mustStatus(t, "caixa filtrado por serviço", sv.data()["totals"].(map[string]any)["net_cents"], "not_applicable")
	if ign := sv.body["meta"].(map[string]any)["ignored_filters"].([]any); len(ign) != 1 || ign[0] != "service_id" {
		t.Fatalf("filtro inaplicável não sinalizado: %v", ign)
	}
	// Ticket médio: 3 comandas liquidadas (a do Pix pendente não), 155,00 / 3.
	tk := get("/series/ticket", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}}).data()["totals"].(map[string]any)
	mustVal(t, "comandas liquidadas", tk["settled_orders"], 3)
	if avg, _ := val(t, tk["average_cents"]); avg < 5166 || avg > 5167 {
		t.Fatalf("ticket médio: %v", avg)
	}
	// Antes do início da coleta e comparação com base zero.
	old := get("/summary", url.Values{"from": {"2026-01-01"}, "to": {"2026-01-02"}}).data()
	mustStatus(t, "antes da coleta", old["current"].(map[string]any)["cash"].(map[string]any)["net_cents"], "not_collected")
	cmp := get("/summary", url.Values{"from": {"2026-11-13"}, "to": {"2026-11-13"}, "compare": {"previous"}})
	mp := cmp.body["meta"].(map[string]any)["compare_period"].(map[string]any)
	if mp["from"] != "2026-11-12" || !strings.HasPrefix(mp["effective_end"].(string), "2026-11-12T15:00:00") {
		t.Fatalf("comparação com o mesmo tempo transcorrido: %v", mp)
	}
	// Desfechos: denominador explícito e participações abertas à parte (Caio ainda esperando).
	oc := get("/breakdowns/outcomes", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}}).data()["rows"].(map[string]any)
	if num(oc["open_count"]) != 1 || num(oc["denominator_count"]) != 4 {
		t.Fatalf("desfechos: %v", oc)
	}
	// Mapa de calor: entradas às 09h de quinta (12/11/2026 é quinta, isodow 4) — no fuso da loja.
	cells := get("/breakdowns/peak_hours", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}}).data()["rows"].([]any)
	found := false
	for _, c := range cells {
		m := c.(map[string]any)
		if num(m["dow"]) == 4 && num(m["hour"]) == 9 {
			found = num(m["entries"]) == 3
		}
	}
	if !found {
		t.Fatalf("pico às 09h de quinta: %v", cells)
	}
	pp := get("/records/participations", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-13"}, "dow": {"4"}, "hour": {"9"}}).data()
	if num(pp["total"]) != 3 {
		t.Fatalf("detalhe da célula: %v", pp)
	}
}
