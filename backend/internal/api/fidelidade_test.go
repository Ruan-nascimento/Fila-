package api_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------------------------
// Ajudantes da etapa 10

func (b *browser) setProgram(est uuid.UUID, enabled bool) map[string]any {
	b.e.t.Helper()
	cur := b.mustStatus(b.do("GET", estPath(est, "/loyalty-program"), nil), 200).data()
	return b.mustStatus(b.cmd("PUT", estPath(est, "/loyalty-program"), map[string]any{"expected_version": num(cur["version"]), "enabled": enabled}), 200).data()
}

func (b *browser) newReward(est uuid.UUID, name string, cost int, units *int) map[string]any {
	b.e.t.Helper()
	stock := map[string]any{"kind": "unlimited"}
	if units != nil {
		stock = map[string]any{"kind": "limited", "units_available": *units}
	}
	return b.mustStatus(b.cmd("POST", estPath(est, "/rewards"), map[string]any{"name": name, "description": "Cortesia da casa",
		"usage_instructions": "Peça na recepção. Não dá prioridade na fila.", "cost_points": cost, "stock": stock}), 201).data()
}

func ptr(n int) *int { return &n }

// loyalty lê o resumo de pontos da própria conta.
func (b *browser) loyalty(est uuid.UUID) map[string]any {
	b.e.t.Helper()
	return b.mustStatus(b.do("GET", "/v1/me/loyalty/"+est.String(), nil), 200).data()
}

func (b *browser) reserve(est uuid.UUID, reward map[string]any) resp {
	b.e.t.Helper()
	return b.cmd("POST", estPath(est, "/reward-reservations"), map[string]any{"reward_id": reward["id"], "expected_reward_version": num(reward["version"])})
}

func (b *browser) reservationCmd(est uuid.UUID, res map[string]any, op string, body map[string]any) resp {
	b.e.t.Helper()
	full := map[string]any{"expected_version": num(res["version"])}
	for k, v := range body {
		full[k] = v
	}
	return b.cmd("POST", estPath(est, "/reward-reservations/"+res["id"].(string)+"/"+op), full)
}

// earn: a conta entra com os serviços, Beto conclui tudo e a equipe recebe em dinheiro (pago e
// conferido na mesma ação) → crédito dos pontos. Devolve a comanda paga.
func (e *env) earn(b barbearia, c *browser, services ...string) map[string]any {
	e.t.Helper()
	p, o := visitOf(c.mustStatus(c.enter(b.id, b.filaBeto, services...), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	ord := b.betoB.order(b.id, o["id"])
	return b.betoB.mustStatus(b.betoB.receive(b.id, ord, map[string]any{"method": "cash", "amount_cents": due(ord),
		"cash_tendered_cents": due(ord)}), 201).data()
}

// explainable confere o aceite da etapa: saldo e retido de cada conta = soma do extrato;
// estoque de cada prêmio limitado = soma dos movimentos; retido = custo das reservas ativas.
func (e *env) explainable(t *testing.T) {
	t.Helper()
	if n := e.count(t, `SELECT count(*) FROM loyalty_account a WHERE (a.balance, a.held) <> (
		SELECT coalesce(sum(balance_delta), 0), coalesce(sum(held_delta), 0) FROM loyalty_ledger l
		WHERE l.establishment_id = a.establishment_id AND l.account_id = a.account_id)`); n != 0 {
		t.Fatalf("%d saldo(s) não explicado(s) pelo extrato", n)
	}
	if n := e.count(t, `SELECT count(*) FROM loyalty_account a WHERE a.held <> (SELECT coalesce(sum(accepted_cost_points), 0)
		FROM reward_reservation r WHERE r.establishment_id = a.establishment_id AND r.account_id = a.account_id AND r.status = 'active')`); n != 0 {
		t.Fatalf("%d retido(s) diferente(s) das reservas ativas", n)
	}
	if n := e.count(t, `SELECT count(*) FROM reward w WHERE w.stock_kind = 'limited' AND w.units_available <>
		(SELECT coalesce(sum(delta), 0) FROM reward_stock_movement m WHERE m.reward_id = w.id)`); n != 0 {
		t.Fatalf("%d estoque(s) não explicado(s) pelos movimentos", n)
	}
	// Unidades fora do estoque = reservas ativas + entregas não revertidas.
	if n := e.count(t, `SELECT count(*) FROM reward w WHERE w.stock_kind = 'limited' AND
		(SELECT coalesce(sum(delta), 0) FROM reward_stock_movement m WHERE m.reward_id = w.id AND m.kind = 'set_by_owner') - w.units_available <>
		(SELECT count(*) FROM reward_reservation r WHERE r.reward_id = w.id AND r.limited_stock AND r.status IN ('active', 'delivered'))`); n != 0 {
		t.Fatalf("%d estoque(s) não batem com reservas e entregas", n)
	}
}

func ledgerKinds(t *testing.T, e *env, account uuid.UUID) map[string]int {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT kind, count(*) FROM loyalty_ledger WHERE account_id = $1 GROUP BY kind`, account)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		_ = rows.Scan(&k, &n)
		out[k] = n
	}
	return out
}

// ---------------------------------------------------------------------------------------------

// C-FID-01 — programa começa desligado: sem acúmulo nem reserva; só o dono configura.
func TestLoyaltyProgramStartsDisabled(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	prog := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/loyalty-program"), nil), 200).data()
	if prog["enabled"] != false || num(prog["version"]) != 1 {
		t.Fatalf("programa novo: %v", prog)
	}
	if r := b.betoB.do("GET", estPath(b.id, "/loyalty-program"), nil); r.status != 403 {
		t.Fatalf("profissional vendo o programa: %d", r.status)
	}
	if r := b.betoB.cmd("POST", estPath(b.id, "/rewards"), map[string]any{"name": "x"}); r.status != 403 {
		t.Fatalf("profissional criando prêmio: %d", r.status)
	}
	maria := e.verifiedCustomer("maria@exemplo.com")
	e.earn(b, maria, b.corte, b.barba)
	l := maria.loyalty(b.id)
	if l["program_enabled"] != false || num(l["balance"]) != 0 {
		t.Fatalf("acúmulo com programa desligado: %v", l)
	}
	cafe := b.owner.newReward(b.id, "Café", 1, nil)
	if r := maria.reserve(b.id, cafe); r.code() != "loyalty_program_disabled" {
		t.Fatalf("reserva com programa desligado: %s", r.raw)
	}
	if r := b.owner.cmd("PUT", estPath(b.id, "/loyalty-program"), map[string]any{"expected_version": 9, "enabled": true}); r.code() != "version_conflict" {
		t.Fatalf("versão velha: %s", r.raw)
	}
	if r := b.owner.cmd("PUT", estPath(b.id, "/loyalty-program"), map[string]any{"expected_version": 1, "enabled": true, "pontos": 5}); r.status != 400 {
		t.Fatalf("campo desconhecido: %d", r.status)
	}
	if on := b.owner.setProgram(b.id, true); on["enabled"] != true || num(on["version"]) != 2 {
		t.Fatalf("ligar: %v", on)
	}
}

// R-FID-02…05 — crédito uma única vez, só depois de pago E conferido; elegibilidade é a da
// entrada; visitante, interrupção, cancelamento e serviço não feito não geram pontos.
// Cenários: C-FID-02, C-FID-03, C-FID-04, C-FID-05.
func TestCreditOnceAfterPaidAndConferred(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t) // Corte = 10 pontos, Barba = 5
	b.owner.setProgram(b.id, true)
	maria := e.verifiedCustomer("maria@exemplo.com")
	p, o := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	if p["loyalty_eligible"] != true {
		t.Fatalf("conta com programa ligado deveria ser elegível: %v", p)
	}
	// Desligar depois da entrada não retira o crédito prometido (R-FID-03/13).
	b.owner.setProgram(b.id, false)
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	ord := maria.order(b.id, o["id"])
	ch := maria.mustStatus(maria.createPix(b.id, ord, newIdemKey()), 201).data()
	e.pix.Set(e.remote(ch["id"]).ID, "PAID", 0)
	e.reconcile()
	if l := maria.loyalty(b.id); num(l["balance"]) != 0 {
		t.Fatalf("crédito antes da conferência: %v", l)
	}
	ord = b.betoB.order(b.id, o["id"])
	rec := receiptsOf(ord)[0]
	// Conferências simultâneas e aviso repetido: um crédito só.
	var wg sync.WaitGroup
	for _, br := range []*browser{b.betoB, b.owner} {
		wg.Go(func() { br.confer(b.id, ord, rec["id"]) })
	}
	wg.Wait()
	rc := e.remote(ch["id"])
	rc.Status = "PAID"
	e.signed(e.pix.Webhook("log_fid_repetido", "transparent.completed", rc))
	e.reconcile()
	l := maria.loyalty(b.id)
	acc := accountIDOf(t, e, "maria@exemplo.com")
	if num(l["balance"]) != 15 || num(l["available"]) != 15 || ledgerKinds(t, e, acc)["credit"] != 1 {
		t.Fatalf("crédito: %v %v", l, ledgerKinds(t, e, acc))
	}
	led := maria.mustStatus(maria.do("GET", "/v1/me/loyalty/"+b.id.String()+"/ledger", nil), 200).body["data"].([]any)
	first := led[0].(map[string]any)
	if len(led) != 1 || first["kind"] != "credit" || num(first["balance_delta"]) != 15 ||
		first["source"].(map[string]any)["id"] != o["id"] {
		t.Fatalf("extrato: %v", led)
	}
	hist := maria.mustStatus(maria.do("GET", "/v1/me/history?establishment_id="+b.id.String(), nil), 200).body["data"].([]any)
	if num(hist[0].(map[string]any)["points_credited"]) != 15 {
		t.Fatalf("histórico: %v", hist[0])
	}
	// Nova visita com o programa desligado: não elegível, sem pontos.
	e.earn(b, maria, b.corte)
	if l := maria.loyalty(b.id); num(l["balance"]) != 15 {
		t.Fatalf("visita com programa desligado creditou: %v", l)
	}
	b.owner.setProgram(b.id, true)
	// Serviço não realizado não conta: só o Corte (10) é creditado.
	p2, o2 := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	v := b.betoB.startServing(b.id, b.filaBeto, p2["id"].(string))
	b.betoB.mustStatus(b.betoB.closeVisit(b.id, v, "complete", func(n string) string {
		if n == "Barba" {
			return "not_performed"
		}
		return "performed"
	}, nil), 200)
	ord2 := b.betoB.order(b.id, o2["id"])
	b.betoB.mustStatus(b.betoB.receive(b.id, ord2, map[string]any{"method": "card", "amount_cents": due(ord2)}), 201)
	if l := maria.loyalty(b.id); num(l["balance"]) != 25 {
		t.Fatalf("só serviços realizados contam: %v", l)
	}
	// Interrupção (mesmo com valor cobrado) não gera pontos.
	p3, o3 := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	v3 := b.betoB.startServing(b.id, b.filaBeto, p3["id"].(string))
	ri := b.betoB.mustStatus(b.betoB.closeVisit(b.id, v3, "interrupt", func(n string) string {
		if n == "Corte" {
			return "fully_performed"
		}
		return "not_started"
	}, map[string]any{"reason": "faltou luz"}), 200)
	_, oi := visitOf(ri)
	items := []map[string]any{}
	for _, it := range oi["items"].([]any) {
		m := it.(map[string]any)
		out := "not_started"
		if m["name"] == "Corte" {
			out = "fully_performed"
		}
		items = append(items, map[string]any{"item_id": m["id"], "outcome": out})
	}
	ci := b.owner.mustStatus(b.owner.cmd("POST", orderPath(b.id, o3["id"])+"/close-interruption",
		map[string]any{"expected_version": num(oi["version"]), "review_note": "só o corte foi feito", "items": items}), 200).data()
	b.owner.mustStatus(b.owner.receive(b.id, ci, map[string]any{"method": "cash", "amount_cents": due(ci), "cash_tendered_cents": due(ci)}), 201)
	if l := maria.loyalty(b.id); num(l["balance"]) != 25 {
		t.Fatalf("interrupção gerou pontos: %v", l)
	}
	// Visitante não ganha pontos, nem criando conta depois (com o mesmo e-mail ou não).
	lia := e.visitorIn(b.id, "Lia")
	e.earn(b, lia, b.corte)
	if e.count(t, `SELECT count(*) FROM participation WHERE visitor_id IS NOT NULL AND loyalty_eligible`)+
		e.count(t, `SELECT count(*) FROM service_order WHERE visitor_id IS NOT NULL AND loyalty_eligible`) != 0 {
		t.Fatal("visitante marcado como elegível")
	}
	if r := lia.do("GET", "/v1/me/loyalty/"+b.id.String(), nil); r.status != 401 {
		t.Fatalf("visitante consultando pontos: %d", r.status)
	}
	liaConta := e.verifiedCustomer("lia@exemplo.com")
	if l := liaConta.loyalty(b.id); num(l["balance"]) != 0 {
		t.Fatalf("visitante ganhou pontos ao criar conta: %v", l)
	}
	if e.count(t, `SELECT count(*) FROM loyalty_ledger WHERE kind = 'credit'`) != 2 {
		t.Fatal("créditos a mais")
	}
	e.explainable(t)
}

// R-FID-09…12 — reserva trava pontos e unidade; uma ativa por conta; cancelar/entregar/corrigir
// uma vez; condições aceitas preservadas; só a equipe da loja entrega; só o dono corrige.
// Cenários: C-FID-05, C-FID-07, C-FID-08.
func TestRewardReservationLifecycle(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	maria := e.verifiedCustomer("maria@exemplo.com")
	e.earn(b, maria, b.corte, b.barba) // 15 pontos
	cerveja := b.owner.newReward(b.id, "Cerveja", 10, ptr(1))
	cafe := b.owner.newReward(b.id, "Café", 3, nil)
	if r := b.owner.cmd("POST", estPath(b.id, "/rewards"), map[string]any{"name": "Grátis", "description": "x", "usage_instructions": "x",
		"cost_points": 0, "stock": map[string]any{"kind": "unlimited"}}); r.status != 400 {
		t.Fatalf("custo zero: %d", r.status)
	}
	cat := maria.mustStatus(maria.do("GET", estPath(b.id, "/reward-catalog"), nil), 200).body["data"].([]any)
	if len(cat) != 2 {
		t.Fatalf("catálogo: %v", cat)
	}
	r := maria.mustStatus(maria.reserve(b.id, cerveja), 201)
	res := r.data()
	if res["status"] != "active" || res["accepted_terms"].(map[string]any)["name"] != "Cerveja" || res["holder_display_name"] != nil {
		t.Fatalf("reserva: %s", r.raw)
	}
	if exp, _ := time.Parse(time.RFC3339, res["expires_at"].(string)); !exp.Equal(e.clock.Now().Add(24 * time.Hour)) {
		t.Fatalf("validade de 24 h: %v", res["expires_at"])
	}
	l := maria.loyalty(b.id)
	if num(l["balance"]) != 15 || num(l["held"]) != 10 || num(l["available"]) != 5 || l["active_reservation"] == nil {
		t.Fatalf("retido: %v", l)
	}
	if r := maria.reserve(b.id, cafe); r.code() != "reservation_already_active" {
		t.Fatalf("segunda reserva: %s", r.raw)
	}
	// O dono muda o prêmio: a reserva mantém as condições aceitas; reservar com versão velha falha.
	cerveja = b.owner.mustStatus(b.owner.cmd("PATCH", estPath(b.id, "/rewards/"+cerveja["id"].(string)),
		map[string]any{"expected_version": num(cerveja["version"]), "name": "Cerveja artesanal", "cost_points": 20}), 200).data()
	staffList := b.betoB.mustStatus(b.betoB.do("GET", estPath(b.id, "/reward-reservations?status=active"), nil), 200).body["data"].([]any)
	sr := staffList[0].(map[string]any)
	if sr["accepted_terms"].(map[string]any)["name"] != "Cerveja" || num(sr["accepted_terms"].(map[string]any)["cost_points"]) != 10 ||
		sr["holder_display_name"] == nil {
		t.Fatalf("condições aceitas / nome para a equipe: %v", sr)
	}
	// Outra loja não vê; cliente não lista as reservas da loja.
	outra := e.shop("outra-loja", "bia@exemplo.com")
	if r := outra.owner.do("GET", estPath(b.id, "/reward-reservations"), nil); r.status != 403 && r.status != 404 {
		t.Fatalf("dono de outra loja: %d", r.status)
	}
	if r := maria.do("GET", estPath(b.id, "/reward-reservations"), nil); r.status != 403 && r.status != 401 && r.status != 404 {
		t.Fatalf("cliente listando reservas: %d", r.status)
	}
	// Cliente não entrega a si mesmo; outra conta não cancela.
	if r := maria.reservationCmd(b.id, res, "deliver", nil); r.status != 403 && r.status != 401 && r.status != 404 {
		t.Fatalf("cliente entregando: %d", r.status)
	}
	joao := e.verifiedCustomer("joao@exemplo.com")
	if r := joao.reservationCmd(b.id, res, "cancel", nil); r.status != 404 {
		t.Fatalf("outra conta cancelando: %d", r.status)
	}
	res = maria.mustStatus(maria.reservationCmd(b.id, res, "cancel", nil), 200).data()
	if res["status"] != "cancelled" {
		t.Fatalf("cancelar: %v", res)
	}
	if r := maria.reservationCmd(b.id, res, "cancel", nil); r.code() != "invalid_transition" {
		t.Fatalf("cancelar de novo: %s", r.raw)
	}
	if l := maria.loyalty(b.id); num(l["held"]) != 0 || num(l["available"]) != 15 {
		t.Fatalf("liberação: %v", l)
	}
	if r := maria.reserve(b.id, map[string]any{"id": cerveja["id"], "version": float64(1)}); r.code() != "version_conflict" {
		t.Fatalf("condições velhas: %s", r.raw)
	}
	if r := maria.reserve(b.id, cerveja); r.code() != "insufficient_points" {
		t.Fatalf("custo novo (20) com 15 pontos: %s", r.raw)
	}
	res = maria.mustStatus(maria.reserve(b.id, cafe), 201).data()
	// Profissional da loja entrega; repetir a mesma chave devolve o mesmo; entregar de novo não.
	key := newIdemKey()
	body := map[string]any{"expected_version": num(res["version"])}
	path := estPath(b.id, "/reward-reservations/"+res["id"].(string)+"/deliver")
	d1 := b.betoB.mustStatus(b.betoB.do("POST", path, body, "Idempotency-Key", key), 200)
	d2 := b.betoB.do("POST", path, body, "Idempotency-Key", key)
	if d1.data()["status"] != "delivered" || d2.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("entrega: %s / %s", d1.raw, d2.raw)
	}
	if r := b.owner.reservationCmd(b.id, d1.data(), "deliver", nil); r.code() != "invalid_transition" {
		t.Fatalf("entregar duas vezes: %s", r.raw)
	}
	if l := maria.loyalty(b.id); num(l["balance"]) != 12 || num(l["held"]) != 0 {
		t.Fatalf("consumo: %v", l)
	}
	// Correção da entrega: só o dono, com motivo; devolve os pontos uma vez.
	if r := b.betoB.reservationCmd(b.id, d1.data(), "revert-delivery", map[string]any{"reason": "engano"}); r.status != 403 {
		t.Fatalf("profissional corrigindo: %d", r.status)
	}
	if r := b.owner.reservationCmd(b.id, d1.data(), "revert-delivery", nil); r.status != 400 {
		t.Fatalf("correção sem motivo: %d", r.status)
	}
	rev := b.owner.mustStatus(b.owner.reservationCmd(b.id, d1.data(), "revert-delivery", map[string]any{"reason": "registrado por engano"}), 200).data()
	if rev["status"] != "delivery_reverted" {
		t.Fatalf("correção: %v", rev)
	}
	if r := b.owner.reservationCmd(b.id, rev, "revert-delivery", map[string]any{"reason": "de novo"}); r.code() != "invalid_transition" {
		t.Fatalf("corrigir duas vezes: %s", r.raw)
	}
	acc := accountIDOf(t, e, "maria@exemplo.com")
	if l := maria.loyalty(b.id); num(l["balance"]) != 15 || ledgerKinds(t, e, acc)["consume_reversal"] != 1 {
		t.Fatalf("pontos devolvidos: %v", l)
	}
	if e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-RW-05' AND reason = 'registrado por engano'`) != 1 {
		t.Fatal("auditoria da correção")
	}
	// Eventos: titular recebe a própria reserva sem o nome; equipe recebe com o nome.
	if e.count(t, `SELECT count(*) FROM outbox WHERE type = 'reward_reservation.updated' AND audience = 'account' AND payload ? 'holder_display_name'`) != 0 ||
		e.count(t, `SELECT count(*) FROM outbox WHERE type = 'reward_reservation.updated' AND audience = 'owner' AND payload ? 'holder_display_name'`) == 0 ||
		e.count(t, `SELECT count(*) FROM outbox WHERE type = 'reward_reservation.updated' AND audience = 'professional'`) == 0 ||
		e.count(t, `SELECT count(*) FROM outbox WHERE type = 'loyalty.updated' AND audience <> 'account'`) != 0 {
		t.Fatal("eventos por audiência")
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE loyalty_ledger SET balance_delta = 999`); err == nil {
		t.Fatal("extrato alterado")
	}
	e.explainable(t)
}

// R-FID-14 — última unidade disputada: exatamente uma reserva; ninguém fica com estoque negativo.
// Cenários: C-FID-09.
func TestLastUnitConcurrency(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	var clientes []*browser
	for i := range 5 {
		c := e.verifiedCustomer(fmt.Sprintf("cliente%d@exemplo.com", i))
		e.earn(b, c, b.corte)
		clientes = append(clientes, c)
	}
	ultima := b.owner.newReward(b.id, "Pomada", 10, ptr(1))
	var wg sync.WaitGroup
	results := make([]resp, len(clientes))
	for i, c := range clientes {
		wg.Go(func() { results[i] = c.reserve(b.id, ultima) })
	}
	wg.Wait()
	ok, out := 0, 0
	for _, r := range results {
		switch {
		case r.status == 201:
			ok++
		case r.code() == "reward_out_of_stock":
			out++
		default:
			t.Fatalf("resposta inesperada: %d %s", r.status, r.raw)
		}
	}
	if ok != 1 || out != 4 {
		t.Fatalf("última unidade: %d reservas, %d esgotados", ok, out)
	}
	if e.count(t, `SELECT units_available FROM reward WHERE id = $1`, ultima["id"]) != 0 {
		t.Fatal("estoque")
	}
	e.explainable(t)
}

// Gasto duplo: a mesma conta tentando reservar dois prêmios ao mesmo tempo com saldo para um só,
// e o banco recusando reserva acima do disponível mesmo se o código falhasse.
// Cenários: C-FID-09.
func TestNoDoubleSpend(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	maria := e.verifiedCustomer("maria@exemplo.com")
	e.earn(b, maria, b.corte) // 10 pontos
	a := b.owner.newReward(b.id, "Toalha quente", 10, nil)
	c := b.owner.newReward(b.id, "Pomada", 10, nil)
	var wg sync.WaitGroup
	var r1, r2 resp
	wg.Go(func() { r1 = maria.reserve(b.id, a) })
	wg.Go(func() { r2 = maria.reserve(b.id, c) })
	wg.Wait()
	if (r1.status == 201) == (r2.status == 201) {
		t.Fatalf("gasto duplo: %d %s / %d %s", r1.status, r1.raw, r2.status, r2.raw)
	}
	if l := maria.loyalty(b.id); num(l["held"]) != 10 || num(l["available"]) != 0 {
		t.Fatalf("saldo: %v", l)
	}
	acc := accountIDOf(t, e, "maria@exemplo.com")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO loyalty_ledger (establishment_id, account_id, kind, balance_delta, held_delta,
		source_type, source_id, occurred_at) VALUES ($1, $2, 'hold', 0, 5, 'reservation', $3, now())`, b.id, acc, uuid.New()); err == nil {
		t.Fatal("o banco aceitou reserva acima do disponível")
	}
	e.explainable(t)
}

// T-RW-03 × T-RW-04 — entrega contra vencimento: depois do prazo a entrega é recusada e o
// vencimento libera uma vez; antes do prazo a entrega vence a corrida e o vencimento não faz nada.
// Cenários: C-FID-08, C-FID-10.
func TestDeliveryVersusExpiry(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	maria := e.verifiedCustomer("maria@exemplo.com")
	joao := e.verifiedCustomer("joao@exemplo.com")
	e.earn(b, maria, b.corte)
	e.earn(b, joao, b.corte)
	premio := b.owner.newReward(b.id, "Pomada", 10, ptr(5))
	rm := maria.mustStatus(maria.reserve(b.id, premio), 201).data()
	rj := joao.mustStatus(joao.reserve(b.id, premio), 201).data()

	e.clock.Advance(24*time.Hour - time.Second)              // Joao: ainda dentro do prazo
	b.betoB.login("beto@exemplo.com", goodPassword, "staff") // a sessão da equipe vence antes de 24 h
	var wg sync.WaitGroup
	var dj resp
	wg.Go(func() { dj = b.betoB.reservationCmd(b.id, rj, "deliver", nil) })
	wg.Go(func() { _, _ = e.api.Loyalty.ExpireDue(context.Background()) })
	wg.Wait()
	if dj.status != 200 {
		t.Fatalf("entrega antes do prazo: %d %s", dj.status, dj.raw)
	}
	e.clock.Advance(2 * time.Second) // Maria: prazo passou
	// Sem o worker ter rodado: a entrega confere o prazo pelo horário, na mesma transação.
	if r := b.betoB.reservationCmd(b.id, rm, "deliver", nil); r.code() != "reservation_expired" {
		t.Fatalf("entrega vencida (sem worker): %s", r.raw)
	}
	var dm resp
	wg.Go(func() { dm = b.betoB.reservationCmd(b.id, rm, "deliver", nil) })
	wg.Go(func() { _, _ = e.api.Loyalty.ExpireDue(context.Background()) })
	wg.Wait()
	if dm.code() != "reservation_expired" {
		t.Fatalf("entrega depois do prazo: %d %s", dm.status, dm.raw)
	}
	for range 3 {
		if _, err := e.api.Loyalty.ExpireDue(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	accM, accJ := accountIDOf(t, e, "maria@exemplo.com"), accountIDOf(t, e, "joao@exemplo.com")
	km, kj := ledgerKinds(t, e, accM), ledgerKinds(t, e, accJ)
	if km["hold_release"] != 1 || km["consume"] != 0 || kj["consume"] != 1 || kj["hold_release"] != 0 {
		t.Fatalf("liberação/consumo: maria %v joão %v", km, kj)
	}
	if l := maria.loyalty(b.id); num(l["available"]) != 10 || l["active_reservation"] != nil {
		t.Fatalf("pontos da Maria voltaram: %v", l)
	}
	if e.count(t, `SELECT units_available FROM reward WHERE id = $1`, premio["id"]) != 4 {
		t.Fatal("unidade da reserva vencida voltou ao estoque uma vez")
	}
	e.explainable(t)
}

// R-FID-08 — estorno integral do principal reverte o crédito uma vez (saldo pode ficar negativo
// sem dívida; novo resgate bloqueado); devolver um excedente não reverte a compra válida.
// Cenários: C-FID-06.
func TestRefundAfterRedemption(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	maria := e.verifiedCustomer("maria@exemplo.com")
	ord := e.earn(b, maria, b.corte, b.barba) // 15 pontos, pago em dinheiro
	premio := b.owner.newReward(b.id, "Pomada", 10, nil)
	res := maria.mustStatus(maria.reserve(b.id, premio), 201).data()
	b.betoB.mustStatus(b.betoB.reservationCmd(b.id, res, "deliver", nil), 200) // saldo 5
	rec := receiptsOf(ord)[0]
	b.owner.reauth()
	rf := refundsOf(b.owner.mustStatus(b.owner.refund(b.id.String(), ord, rec["id"], "serviço refeito"), 202).data())[0]
	path := orderPath(b.id, ord["id"]) + "/refunds/" + rf["id"].(string) + "/confirm"
	key := newIdemKey()
	b.owner.mustStatus(b.owner.do("POST", path, map[string]any{"evidence_note": "devolvido em espécie"}, "Idempotency-Key", key), 200)
	b.owner.do("POST", path, map[string]any{"evidence_note": "devolvido em espécie"}, "Idempotency-Key", key)
	l := maria.loyalty(b.id)
	acc := accountIDOf(t, e, "maria@exemplo.com")
	if num(l["balance"]) != -10 || ledgerKinds(t, e, acc)["credit_reversal"] != 1 {
		t.Fatalf("reversão: %v %v", l, ledgerKinds(t, e, acc))
	}
	cafe := b.owner.newReward(b.id, "Café", 1, nil)
	if r := maria.reserve(b.id, cafe); r.code() != "insufficient_points" {
		t.Fatalf("resgate com saldo negativo: %s", r.raw)
	}
	hist := maria.mustStatus(maria.do("GET", "/v1/me/history?establishment_id="+b.id.String(), nil), 200).body["data"].([]any)
	if num(hist[0].(map[string]any)["points_credited"]) != 0 {
		t.Fatalf("histórico depois da reversão: %v", hist[0])
	}
	// Excesso (Pix tardio) devolvido não reverte a compra válida.
	joao := e.verifiedCustomer("joao@exemplo.com")
	p, o := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	ch := joao.mustStatus(joao.createPix(b.id, joao.order(b.id, o["id"]), newIdemKey()), 201).data()
	oj := joao.mustStatus(joao.choose(b.id, joao.order(b.id, o["id"]), "cash", true), 200).data()
	b.betoB.mustStatus(b.betoB.receive(b.id, oj, map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 4500}), 201)
	e.pix.Set(e.remote(ch["id"]).ID, "PAID", 0)
	e.reconcile()
	oj = b.owner.order(b.id, o["id"])
	var excess map[string]any
	for _, r := range receiptsOf(oj) {
		if r["classification"] == "excess" {
			excess = r
		}
	}
	b.owner.reauth()
	b.owner.mustStatus(b.owner.refund(b.id.String(), oj, excess["id"], "Pix tardio: devolver"), 202)
	e.reconcile()
	e.reconcile()
	if s := refundsOf(b.owner.order(b.id, o["id"]))[0]["status"]; s != "confirmed" {
		t.Fatalf("estorno do excesso: %v", s)
	}
	accJ := accountIDOf(t, e, "joao@exemplo.com")
	if l := joao.loyalty(b.id); num(l["balance"]) != 10 || ledgerKinds(t, e, accJ)["credit_reversal"] != 0 {
		t.Fatalf("devolução do excesso reverteu a compra: %v", l)
	}
	e.explainable(t)
}

// R-FID-13 — desligar preserva saldo, consulta e reservas ativas (cancelar e entregar seguem
// valendo); só impede novas reservas e novos acúmulos.
// Cenários: C-FID-07, C-FID-11.
func TestDisablingProgramKeepsRights(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	maria := e.verifiedCustomer("maria@exemplo.com")
	joao := e.verifiedCustomer("joao@exemplo.com")
	e.earn(b, maria, b.corte, b.barba)
	e.earn(b, joao, b.corte)
	premio := b.owner.newReward(b.id, "Pomada", 5, ptr(3))
	rm := maria.mustStatus(maria.reserve(b.id, premio), 201).data()
	rj := joao.mustStatus(joao.reserve(b.id, premio), 201).data()
	b.owner.setProgram(b.id, false)
	l := maria.loyalty(b.id)
	if l["program_enabled"] != false || num(l["balance"]) != 15 || l["active_reservation"] == nil {
		t.Fatalf("consulta com programa desligado: %v", l)
	}
	b.betoB.mustStatus(b.betoB.reservationCmd(b.id, rm, "deliver", nil), 200)
	joao.mustStatus(joao.reservationCmd(b.id, rj, "cancel", nil), 200)
	if r := joao.reserve(b.id, premio); r.code() != "loyalty_program_disabled" {
		t.Fatalf("nova reserva com programa desligado: %s", r.raw)
	}
	if l := joao.loyalty(b.id); num(l["available"]) != 10 {
		t.Fatalf("saldo preservado: %v", l)
	}
	// Prêmio inativado: reservas novas recusadas (ao religar), as existentes seguem.
	b.owner.setProgram(b.id, true)
	res := joao.mustStatus(joao.reserve(b.id, premio), 201).data()
	cur := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/rewards"), nil), 200).body["data"].([]any)[0].(map[string]any)
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/rewards/"+premio["id"].(string)+"/deactivate"),
		map[string]any{"expected_version": num(cur["version"])}), 200)
	if r := maria.reserve(b.id, premio); r.code() != "reward_inactive" && r.code() != "version_conflict" {
		t.Fatalf("prêmio inativo: %s", r.raw)
	}
	b.betoB.mustStatus(b.betoB.reservationCmd(b.id, res, "deliver", nil), 200)
	e.explainable(t)
}

// T-LG-03 / T-RW-06 — exclusão de conta: reserva cancelada (libera unidade), saldo encerrado sem
// dinheiro, nome tirado da reserva; nada disso se repete.
func TestAccountDeletionClosesLoyalty(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	maria := e.verifiedCustomer("maria@exemplo.com")
	e.earn(b, maria, b.corte, b.barba)
	premio := b.owner.newReward(b.id, "Pomada", 10, ptr(1))
	maria.mustStatus(maria.reserve(b.id, premio), 201)
	maria.reauth()
	maria.mustStatus(maria.requestDeletion(), 202)
	acc := accountIDOf(t, e, "maria@exemplo.com")
	k := ledgerKinds(t, e, acc)
	if k["hold_release"] != 1 || k["account_closure"] != 1 {
		t.Fatalf("encerramento: %v", k)
	}
	var balance, held int64
	_ = e.pool.QueryRow(context.Background(), `SELECT balance, held FROM loyalty_account WHERE account_id = $1`, acc).Scan(&balance, &held)
	if balance != 0 || held != 0 || e.count(t, `SELECT units_available FROM reward WHERE id = $1`, premio["id"]) != 1 {
		t.Fatalf("saldo %d retido %d", balance, held)
	}
	if e.count(t, `SELECT count(*) FROM reward_reservation WHERE account_id = $1 AND holder_name <> 'Cliente'`, acc) != 0 {
		t.Fatal("nome ficou na reserva")
	}
	e.explainable(t)
}
