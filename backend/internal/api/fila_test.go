package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/contrato"
)

// cenário: loja com Beto (fila A: Corte e Barba) e Caio (fila B: só Corte), filas abertas.
type barbearia struct {
	shop
	beto, caio         uuid.UUID
	filaBeto, filaCaio uuid.UUID
	corte, barba       string
	betoB              *browser
}

func (e *env) barbearia(t *testing.T) barbearia {
	t.Helper()
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	b := barbearia{shop: loja}
	b.beto = e.professional(loja.id, "Beto")
	b.caio = e.professional(loja.id, "Caio")
	b.filaBeto, b.filaCaio = e.queueOf(b.beto), e.queueOf(b.caio)
	b.corte = loja.owner.createService(loja.id, map[string]any{"name": "Corte", "price_cents": 4500, "duration_minutes": 30, "points": 10,
		"professional_ids": []string{b.beto.String(), b.caio.String()}})["id"].(string)
	b.barba = loja.owner.createService(loja.id, map[string]any{"name": "Barba", "price_cents": 3000, "duration_minutes": 20, "points": 5,
		"professional_ids": []string{b.beto.String()}})["id"].(string)
	for _, q := range []uuid.UUID{b.filaBeto, b.filaCaio} {
		loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/open"), map[string]any{"expected_version": 1}), 200)
	}
	b.betoB = e.staffMember(loja, b.beto, "beto@exemplo.com")
	return b
}

func (b *browser) enter(est, queue uuid.UUID, services ...string) resp {
	b.e.t.Helper()
	return b.cmd("POST", estPath(est, "/participations"), map[string]any{"queue_id": queue.String(), "service_ids": services})
}

func visitOf(r resp) (participation, order map[string]any) {
	d := r.data()
	p, _ := d["participation"].(map[string]any)
	o, _ := d["order"].(map[string]any)
	return p, o
}

func (b *browser) partCmd(est uuid.UUID, part string, op string, body map[string]any) resp {
	b.e.t.Helper()
	return b.cmd("POST", estPath(est, "/participations/"+part+"/"+op), body)
}

func (b *browser) callNext(est, queue uuid.UUID) resp {
	b.e.t.Helper()
	return b.cmd("POST", estPath(est, "/queues/"+queue.String()+"/call-next"), nil)
}

// visitorIn cria um visitante (navegador novo) na loja.
func (e *env) visitorIn(est uuid.UUID, nick string) *browser {
	b := e.browser()
	b.createVisitor(est, nick)
	return b
}

func (e *env) partStatus(t *testing.T, id string) (status, queue string) {
	t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT status, queue_id::text FROM participation WHERE id = $1`, id).Scan(&status, &queue); err != nil {
		t.Fatal(err)
	}
	return
}

// C-FILA-01 — entrada válida; preço calculado no servidor; fila pública sem dados privados.
func TestQueueEntry(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	// Campos de preço/total/papel no corpo são recusados (o navegador nunca define o total).
	for _, extra := range []string{"price_cents", "total", "account_id", "role", "position"} {
		r := maria.cmd("POST", estPath(b.id, "/participations"), map[string]any{"queue_id": b.filaBeto.String(), "service_ids": []string{b.corte}, extra: 1})
		if r.status != 400 || !strings.Contains(string(r.raw), `"unknown_field"`) {
			t.Errorf("campo %s: %d %s", extra, r.status, r.raw)
		}
	}
	r := maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201)
	var visit struct{ Data contrato.Visit }
	strictDecode(t, r.raw, &visit)
	p, o := visit.Data.Participation, visit.Data.Order
	if p.TicketCode != "A001" || p.Status != "waiting" || p.Position == nil || *p.Position != 1 || p.EstimatedWait == nil ||
		p.EstimatedWait.Kind != "estimate" || p.IdentityKind != nil || p.Nick != "Pessoa Teste" {
		t.Fatalf("participação: %+v", p)
	}
	if o.Status != "open" || o.Total.AmountCents != 7500 || len(o.Items) != 2 || o.Items[0].UnitPrice.AmountCents == 0 || o.Payment.Amounts.Due.AmountCents != 0 {
		t.Fatalf("comanda: %+v", o)
	}
	// Segunda pessoa: posição 2, espera estimada = 50 min (Corte 30 + Barba 20 de quem está à frente).
	joao := e.visitorIn(b.id, "João")
	r2 := joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201)
	p2, _ := visitOf(r2)
	if p2["ticket_code"] != "A002" || num(p2["position"]) != 2 || num(p2["estimated_wait"].(map[string]any)["minutes"]) != 50 {
		t.Fatalf("segunda entrada: %v", p2)
	}
	// Visita atual (cliente) e fila pública.
	cur := maria.mustStatus(maria.do("GET", estPath(b.id, "/my-visit"), nil), 200)
	if cur.body["cursor"] == nil || cur.data()["participation"].(map[string]any)["ticket_code"] != "A001" {
		t.Fatalf("visita atual: %s", cur.raw)
	}
	pub := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments/"+b.id.String()+"/queues", nil), 200)
	var pq struct {
		Data   []contrato.PublicQueue
		Cursor *string
	}
	strictDecode(t, pub.raw, &pq)
	if pq.Data[0].WaitingCount != 2 || len(pq.Data[0].Tickets) != 2 || pq.Data[0].EstimatedWait.Minutes != 80 || pq.Data[0].EstimatedWait.Kind != "estimate" {
		t.Fatalf("fila pública: %+v", pq.Data[0])
	}
	for _, leak := range []string{"Pessoa", "João", "Corte", "Barba", "4500", "amount", "nick", "@"} {
		if strings.Contains(string(pub.raw), leak) {
			t.Errorf("fila pública expõe %q", leak)
		}
	}
	// Equipe vê nick e serviços; visitante x conta só para a equipe.
	staff := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/queues"), nil), 200)
	if !strings.Contains(string(staff.raw), "João") || !strings.Contains(string(staff.raw), "Barba") {
		t.Fatalf("visão da equipe: %s", staff.raw)
	}
	gp := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/participations/"+p2["id"].(string)), nil), 200)
	if gp.data()["identity_kind"] != "visitor" {
		t.Fatalf("equipe deveria ver o tipo de identidade: %s", gp.raw)
	}
}

// C-FILA-02 — mesma identidade em duas abas: exatamente uma participação.
func TestSameIdentityTwoTabs(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	keys := []string{newIdemKey(), newIdemKey()}
	queues := []uuid.UUID{b.filaBeto, b.filaCaio}
	res := make([]resp, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			res[i] = maria.do("POST", estPath(b.id, "/participations"),
				map[string]any{"queue_id": queues[i].String(), "service_ids": []string{b.corte}}, "Idempotency-Key", keys[i])
		})
	}
	wg.Wait()
	win := -1
	for i, r := range res {
		switch {
		case r.status == 201:
			if win >= 0 {
				t.Fatal("duas participações criadas")
			}
			win = i
		case r.status == 409 && r.code() == "active_participation_exists" && r.body["current"] != nil:
		default:
			t.Fatalf("resultado inesperado: %d %s", r.status, r.raw)
		}
	}
	if win < 0 {
		t.Fatal("nenhuma entrada criada")
	}
	again := maria.do("POST", estPath(b.id, "/participations"),
		map[string]any{"queue_id": queues[win].String(), "service_ids": []string{b.corte}}, "Idempotency-Key", keys[win])
	if again.status != 201 || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("repetição da vencedora: %d %s", again.status, again.raw)
	}
	mismatch := maria.do("POST", estPath(b.id, "/participations"),
		map[string]any{"queue_id": queues[win].String(), "service_ids": []string{b.barba}}, "Idempotency-Key", keys[win])
	if mismatch.status != 422 || mismatch.code() != "idempotency_key_reused" {
		t.Fatalf("mesma chave, outro corpo: %d %s", mismatch.status, mismatch.raw)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM participation`).Scan(&n)
	if n != 1 {
		t.Fatalf("participações no banco = %d", n)
	}
}

// C-FILA-03 — entradas simultâneas enquanto a dona pausa: tudo ou nada; posições e senhas únicas.
func TestConcurrentEntriesWhilePausing(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	for i := range 3 {
		e.visitorIn(b.id, "Antes"+string(rune('A'+i))).mustStatus(e.browser().enter(b.id, b.filaBeto, b.corte), 401) // sem visitante não entra
	}
	for i := range 3 {
		v := e.visitorIn(b.id, "Antes"+string(rune('A'+i)))
		v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	}
	clientes := make([]*browser, 8)
	for i := range clientes {
		clientes[i] = e.visitorIn(b.id, "Cliente"+string(rune('A'+i)))
	}
	var wg sync.WaitGroup
	results := make([]resp, len(clientes))
	var pause resp
	for i := range clientes {
		wg.Go(func() { results[i] = clientes[i].enter(b.id, b.filaBeto, b.corte) })
	}
	wg.Go(func() {
		pause = b.owner.cmd("POST", estPath(b.id, "/queues/"+b.filaBeto.String()+"/pause"), map[string]any{"expected_version": 2})
	})
	wg.Wait()
	if pause.status != 200 {
		t.Fatalf("pausa: %d %s", pause.status, pause.raw)
	}
	for _, r := range results {
		if r.status != 201 && !(r.status == 409 && r.code() == "queue_not_open") {
			t.Fatalf("entrada pela metade ou erro inesperado: %d %s", r.status, r.raw)
		}
	}
	ctx := context.Background()
	var parts, orders, items, distinctPos, distinctTickets, maxPos int
	_ = e.pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT position), count(DISTINCT ticket_code), max(position) FROM participation WHERE queue_id = $1`, b.filaBeto).
		Scan(&parts, &distinctPos, &distinctTickets, &maxPos)
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM service_order`).Scan(&orders)
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM order_item`).Scan(&items)
	if parts != orders || items != parts || distinctPos != parts || distinctTickets != parts || maxPos != parts {
		t.Fatalf("participações=%d comandas=%d itens=%d posições=%d senhas=%d maior posição=%d", parts, orders, items, distinctPos, distinctTickets, maxPos)
	}
	// Depois da pausa ninguém entra.
	late := e.visitorIn(b.id, "Atrasado")
	if r := late.enter(b.id, b.filaBeto, b.corte); r.code() != "queue_not_open" {
		t.Fatalf("entrada com fila pausada: %s", r.raw)
	}
}

// C-FILA-04 — comanda pendente bloqueia nova entrada; aguardando só conferência não bloqueia.
func TestPendingOrderBlocksEntry(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	p, o0 := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	// Conclusão real (etapa 05): comanda aguardando pagamento com valor fechado.
	b.owner.serve(b.id, b.filaBeto, p["id"].(string))
	if r := maria.enter(b.id, b.filaBeto, b.corte); r.status != 409 || r.code() != "pending_order_exists" {
		t.Fatalf("comanda aguardando pagamento: %d %s", r.status, r.raw)
	}
	// Recebimento real em dinheiro (etapa 08): comanda paga libera nova entrada.
	om := maria.order(b.id, o0["id"])
	b.owner.mustStatus(b.owner.receive(b.id, om, map[string]any{"method": "cash", "amount_cents": due(om), "cash_tendered_cents": due(om)}), 201)
	p2, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))

	// Lia: Pix confirmado pelo provedor, só falta a conferência da equipe (awaiting_confirmation)
	// → pode entrar de novo.
	lia := e.verifiedCustomer("lia@exemplo.com")
	pl, ol := visitOf(lia.mustStatus(lia.enter(b.id, b.filaCaio, b.corte), 201))
	b.owner.serve(b.id, b.filaCaio, pl["id"].(string))
	ch := lia.mustStatus(lia.createPix(b.id, lia.order(b.id, ol["id"]), newIdemKey()), 201).data()
	e.pix.Set(e.pix.ByExternalID(ch["id"].(string)).ID, "PAID", 0)
	e.reconcile()
	if o := lia.order(b.id, ol["id"]); o["status"] != "awaiting_confirmation" {
		t.Fatalf("Pix pago: %v", o["status"])
	}
	lia.mustStatus(lia.enter(b.id, b.filaCaio, b.corte), 201)
	_ = p2
}

// C-FILA-05 — serviço inválido ou fila fechada.
func TestEntryValidation(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	outra := e.shop("outra-loja", "bia@exemplo.com")
	alheio := outra.owner.createService(outra.id, map[string]any{"name": "Corte", "price_cents": 1, "duration_minutes": 1, "points": 0})["id"].(string)
	maria := e.verifiedCustomer("maria@exemplo.com")
	if r := maria.enter(b.id, b.filaCaio, b.barba); r.status != 409 || r.code() != "service_not_eligible" {
		t.Fatalf("Barba com Caio: %d %s", r.status, r.raw)
	}
	if r := maria.enter(b.id, b.filaBeto); r.status != 400 {
		t.Fatalf("sem serviços: %d", r.status)
	}
	if r := maria.enter(b.id, b.filaBeto, b.corte, b.corte); r.status != 400 {
		t.Fatalf("serviço repetido: %d", r.status)
	}
	if r := maria.enter(b.id, b.filaBeto, alheio); r.status != 400 {
		t.Fatalf("serviço de outra loja: %d %s", r.status, r.raw)
	}
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/services/"+b.barba+"/deactivate"), map[string]any{"expected_version": 1}), 200)
	if r := maria.enter(b.id, b.filaBeto, b.barba); r.status != 409 || r.code() != "service_inactive" {
		t.Fatalf("serviço inativo: %d %s", r.status, r.raw)
	}
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/queues/"+b.filaCaio.String()+"/close"), map[string]any{"expected_version": 2}), 200)
	if r := maria.enter(b.id, b.filaCaio, b.corte); r.status != 409 || r.code() != "queue_not_open" {
		t.Fatalf("fila fechada: %d %s", r.status, r.raw)
	}
	if r := maria.enter(b.id, uuid.New(), b.corte); r.status != 404 {
		t.Fatalf("fila inexistente: %d", r.status)
	}
	if r := e.browser().enter(b.id, b.filaBeto, b.corte); r.status != 401 {
		t.Fatalf("sem identidade: %d", r.status)
	}
}

// C-FILA-06 e C-FILA-07 — FIFO determinístico; duas chamadas simultâneas não ocupam a cadeira 2×.
func TestFIFOAndDoubleCall(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	nomes := []string{"Maria", "João", "Lia"}
	ids := map[string]string{}
	clientes := map[string]*browser{}
	for _, n := range nomes {
		c := e.visitorIn(b.id, n)
		p, _ := visitOf(c.mustStatus(c.enter(b.id, b.filaBeto, b.corte), 201))
		ids[n], clientes[n] = p["id"].(string), c
	}
	var wg sync.WaitGroup
	res := make([]resp, 2)
	for i, who := range []*browser{b.betoB, b.owner} {
		wg.Go(func() { res[i] = who.callNext(b.id, b.filaBeto) })
	}
	wg.Wait()
	called := 0
	for _, r := range res {
		switch {
		case r.status == 200:
			called++
			if p, _ := visitOf(r); p["id"] != ids["Maria"] {
				t.Fatalf("FIFO: chamou %v em vez de Maria", p["nick"])
			}
		case r.status == 409 && r.code() == "professional_busy" && r.body["current"] != nil:
		default:
			t.Fatalf("chamada: %d %s", r.status, r.raw)
		}
	}
	if called != 1 {
		t.Fatalf("chamadas bem-sucedidas = %d", called)
	}
	if st, _ := e.partStatus(t, ids["João"]); st != "waiting" {
		t.Fatalf("João deveria continuar aguardando: %s", st)
	}
	// Ordem completa: Maria (desiste), João (ausente após 10 min), Lia.
	maria := clientes["Maria"]
	pm, _ := visitOf(maria.mustStatus(maria.do("GET", estPath(b.id, "/my-visit"), nil), 200))
	maria.mustStatus(maria.partCmd(b.id, ids["Maria"], "cancel", map[string]any{"expected_version": num(pm["version"])}), 200)
	r := b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
	if p, _ := visitOf(r); p["id"] != ids["João"] {
		t.Fatal("segunda chamada deveria ser João")
	}
	e.clock.Advance(10 * time.Minute)
	pj, _ := visitOf(r)
	b.betoB.mustStatus(b.betoB.partCmd(b.id, ids["João"], "no-show", map[string]any{"expected_version": num(pj["version"])}), 200)
	r = b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
	if p, _ := visitOf(r); p["id"] != ids["Lia"] {
		t.Fatal("terceira chamada deveria ser Lia")
	}
	// Com Lia chamada a cadeira está ocupada; fila vazia de espera → no_waiting depois.
	if r := b.betoB.callNext(b.id, b.filaBeto); r.code() != "professional_busy" {
		t.Fatalf("chamar com cadeira ocupada: %s", r.raw)
	}
	if r := b.betoB.callNext(b.id, b.filaCaio); r.status != 403 {
		t.Fatalf("Beto chamando a fila de Caio: %d", r.status)
	}
}

// C-FILA-09 — tolerância fixa de 10 minutos; ausência manual só depois; sem cancelamento automático.
func TestToleranceAndNoShow(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	lia := e.visitorIn(b.id, "Lia")
	lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201)
	called := b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
	p, _ := visitOf(called)
	ends := p["tolerance_ends_at"].(string)
	calledAt, _ := time.Parse(time.RFC3339Nano, p["called_at"].(string))
	endsAt, _ := time.Parse(time.RFC3339Nano, ends)
	if endsAt.Sub(calledAt) != 10*time.Minute {
		t.Fatalf("tolerância deveria ser 10 min: %v → %v", calledAt, endsAt)
	}
	e.clock.Advance(5 * time.Minute)
	cur, _ := visitOf(lia.mustStatus(lia.do("GET", estPath(b.id, "/my-visit"), nil), 200))
	if cur["tolerance_ends_at"] != ends {
		t.Fatal("recarregar a página não pode reiniciar o prazo")
	}
	e.clock.Advance(4*time.Minute + 59*time.Second)
	if r := b.betoB.partCmd(b.id, p["id"].(string), "no-show", map[string]any{"expected_version": num(p["version"])}); r.code() != "tolerance_not_elapsed" {
		t.Fatalf("ausência antes dos 10 min: %s", r.raw)
	}
	e.clock.Advance(21 * time.Minute) // 10:30 — nada acontece sozinho
	if st, _ := e.partStatus(t, p["id"].(string)); st != "called" {
		t.Fatalf("sem cancelamento automático: %s", st)
	}
	e.clock.Advance(time.Minute)
	started := b.betoB.mustStatus(b.betoB.partCmd(b.id, p["id"].(string), "start", map[string]any{"expected_version": num(p["version"])}), 200)
	if ps, _ := visitOf(started); ps["status"] != "in_service" || ps["started_at"] == nil {
		t.Fatalf("iniciar após a tolerância: %s", started.raw)
	}

	// João chamado na fila de Caio, ausente após 10 min: comanda encerrada sem cobrança; volta no fim.
	joao := e.visitorIn(b.id, "João")
	joao.mustStatus(joao.enter(b.id, b.filaCaio, b.corte), 201)
	ana := e.visitorIn(b.id, "Ana")
	ana.mustStatus(ana.enter(b.id, b.filaCaio, b.corte), 201)
	cj, _ := visitOf(b.owner.mustStatus(b.owner.callNext(b.id, b.filaCaio), 200))
	e.clock.Advance(10 * time.Minute)
	ns := b.owner.mustStatus(b.owner.partCmd(b.id, cj["id"].(string), "no-show", map[string]any{"expected_version": num(cj["version"])}), 200)
	pn, on := visitOf(ns)
	if pn["status"] != "no_show" || on["status"] != "closed_no_charge" || on["closure_reason"] != "no_show" {
		t.Fatalf("ausência: %s", ns.raw)
	}
	again, _ := visitOf(joao.mustStatus(joao.enter(b.id, b.filaCaio, b.corte), 201))
	if num(again["position"]) != 2 || again["ticket_code"] != "B003" {
		t.Fatalf("volta no fim da fila: %v", again)
	}
}

// C-FILA-10 — desistência e cancelamento pela dona.
func TestCancelParticipation(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	called, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	r := maria.mustStatus(maria.partCmd(b.id, pm["id"].(string), "cancel", map[string]any{"expected_version": num(called["version"])}), 200)
	pc, oc := visitOf(r)
	if pc["status"] != "cancelled" || oc["status"] != "closed_no_charge" || oc["closure_reason"] != "cancelled_by_customer" {
		t.Fatalf("desistência: %s", r.raw)
	}
	// Cadeira livre: dá para chamar o próximo.
	joao := e.visitorIn(b.id, "João")
	pj, _ := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	cj, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	sj, _ := visitOf(b.betoB.mustStatus(b.betoB.partCmd(b.id, pj["id"].(string), "start", map[string]any{"expected_version": num(cj["version"])}), 200))
	if r := joao.partCmd(b.id, pj["id"].(string), "cancel", map[string]any{"expected_version": num(sj["version"])}); r.status != 409 || r.code() != "invalid_transition" {
		t.Fatalf("desistir em atendimento: %d %s", r.status, r.raw)
	}
	lia := e.visitorIn(b.id, "Lia")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaCaio, b.corte), 201))
	if r := b.owner.partCmd(b.id, pl["id"].(string), "cancel", map[string]any{"expected_version": 1}); r.status != 400 {
		t.Fatalf("dona sem motivo: %d %s", r.status, r.raw)
	}
	if r := b.betoB.partCmd(b.id, pl["id"].(string), "cancel", map[string]any{"expected_version": 1, "reason": "teste"}); r.status != 403 {
		t.Fatalf("profissional cancelando: %d", r.status)
	}
	r = b.owner.mustStatus(b.owner.partCmd(b.id, pl["id"].(string), "cancel", map[string]any{"expected_version": 1, "reason": "Loja vai fechar mais cedo"}), 200)
	if _, o := visitOf(r); o["closure_reason"] != "cancelled_by_owner" {
		t.Fatalf("cancelamento pela dona: %s", r.raw)
	}
	var reason string
	_ = e.pool.QueryRow(context.Background(), `SELECT reason FROM audit_event WHERE action = 'T-P-05' AND target_id = $1`, pl["id"]).Scan(&reason)
	if reason != "Loja vai fechar mais cedo" {
		t.Fatalf("auditoria sem motivo: %q", reason)
	}
}

// C-FILA-11 — transferência válida e recusas.
func TestTransfer(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	for _, n := range []string{"X", "Y"} {
		v := e.visitorIn(b.id, n)
		v.mustStatus(v.enter(b.id, b.filaCaio, b.corte), 201)
	}
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	joao := e.visitorIn(b.id, "João")
	pj, _ := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.barba), 201))
	consent := map[string]any{"given": true, "method": "in_person_verbal", "note": "concordou no balcão"}
	body := func(v int64, target uuid.UUID) map[string]any {
		return map[string]any{"expected_version": v, "target_queue_id": target.String(), "reason": "Beto saiu mais cedo", "customer_consent": consent}
	}
	if r := b.betoB.partCmd(b.id, pm["id"].(string), "transfer", body(1, b.filaCaio)); r.status != 403 {
		t.Fatalf("profissional transferindo: %d", r.status)
	}
	if r := b.owner.partCmd(b.id, pm["id"].(string), "transfer", map[string]any{"expected_version": 1, "target_queue_id": b.filaCaio.String(), "reason": "x123"}); r.status != 400 {
		t.Fatalf("sem consentimento: %d", r.status)
	}
	if r := b.owner.partCmd(b.id, pm["id"].(string), "transfer", map[string]any{"expected_version": 1, "target_queue_id": b.filaCaio.String(), "reason": "x123",
		"customer_consent": map[string]any{"given": false, "method": "in_person_verbal"}}); r.status != 400 || r.code() != "transfer_consent_required" {
		t.Fatalf("consentimento negado: %d %s", r.status, r.raw)
	}
	if r := b.owner.partCmd(b.id, pj["id"].(string), "transfer", body(1, b.filaCaio)); r.status != 409 || r.code() != "transfer_target_incompatible" {
		t.Fatalf("João com Barba para Caio: %d %s", r.status, r.raw)
	}
	r := b.owner.mustStatus(b.owner.partCmd(b.id, pm["id"].(string), "transfer", body(1, b.filaCaio)), 200)
	var v struct{ Data contrato.Visit }
	strictDecode(t, r.raw, &v)
	p := v.Data.Participation
	if p.QueueId.String() != b.filaCaio.String() || *p.Position != 3 || p.TicketCode != "B003" || len(*p.Transfers) != 1 ||
		v.Data.Order.Total.AmountCents != 4500 || p.EstimatedWait.Minutes != 60 {
		t.Fatalf("transferência: %+v", p)
	}
	if r := b.owner.partCmd(b.id, pm["id"].(string), "transfer", body(1, b.filaBeto)); r.code() != "version_conflict" {
		t.Fatalf("versão antiga: %s", r.raw)
	}
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/queues/"+b.filaBeto.String()+"/pause"), map[string]any{"expected_version": 2}), 200)
	if r := b.owner.partCmd(b.id, pm["id"].(string), "transfer", body(2, b.filaBeto)); r.code() != "queue_not_open" {
		t.Fatalf("destino pausado: %s", r.raw)
	}
	var reason, method string
	_ = e.pool.QueryRow(context.Background(), `SELECT reason, consent_method FROM participation_transfer WHERE participation_id = $1`, pm["id"]).Scan(&reason, &method)
	if reason != "Beto saiu mais cedo" || method != "in_person_verbal" {
		t.Fatalf("registro da transferência: %q %q", reason, method)
	}
}

// C-FILA-12 — transferência disputando com chamada: nunca chamada por Beto E na fila de Caio.
func TestTransferVersusCall(t *testing.T) {
	for round := range 3 {
		e := newEnv(t)
		b := e.barbearia(t)
		maria := e.visitorIn(b.id, "Maria")
		pm, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
		joao := e.visitorIn(b.id, "João")
		joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201)
		var wg sync.WaitGroup
		var tr, call resp
		wg.Go(func() {
			tr = b.owner.partCmd(b.id, pm["id"].(string), "transfer", map[string]any{"expected_version": 1, "target_queue_id": b.filaCaio.String(),
				"reason": "equilibrar filas", "customer_consent": map[string]any{"given": true, "method": "in_person_verbal"}})
		})
		wg.Go(func() { call = b.betoB.callNext(b.id, b.filaBeto) })
		wg.Wait()
		status, queue := e.partStatus(t, pm["id"].(string))
		switch {
		case tr.status == 200:
			if status != "waiting" || queue != b.filaCaio.String() || call.status != 200 {
				t.Fatalf("rodada %d: transferida, mas estado %s/%s e chamada %d %s", round, status, queue, call.status, call.raw)
			}
			if p, _ := visitOf(call); p["nick"] != "João" {
				t.Fatalf("rodada %d: Beto deveria chamar João", round)
			}
		case tr.status == 409 && (tr.code() == "invalid_transition" || tr.code() == "version_conflict"):
			if status != "called" || queue != b.filaBeto.String() {
				t.Fatalf("rodada %d: chamada venceu, mas estado %s/%s", round, status, queue)
			}
		default:
			t.Fatalf("rodada %d: transferência %d %s", round, tr.status, tr.raw)
		}
	}
}

// C-FILA-13 — desistência disputando com início: só uma transição vence; a outra recebe current.
func TestCancelVersusStart(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	lia := e.visitorIn(b.id, "Lia")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	cl, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	v := num(cl["version"])
	var wg sync.WaitGroup
	var cancel, start resp
	wg.Go(func() { cancel = lia.partCmd(b.id, pl["id"].(string), "cancel", map[string]any{"expected_version": v}) })
	wg.Go(func() {
		start = b.betoB.partCmd(b.id, pl["id"].(string), "start", map[string]any{"expected_version": v})
	})
	wg.Wait()
	ok := 0
	for _, r := range []resp{cancel, start} {
		if r.status == 200 {
			ok++
		} else if r.status != 409 || r.body["current"] == nil {
			t.Fatalf("perdedor deveria receber 409 com current: %d %s", r.status, r.raw)
		}
	}
	if ok != 1 {
		t.Fatalf("vitórias = %d", ok)
	}
}

// Acesso cruzado: ninguém vê ou age sobre participação alheia.
func TestParticipationAccess(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	outra := e.shop("outra-loja", "bia@exemplo.com")
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	id := pm["id"].(string)
	joao := e.verifiedCustomer("joao@exemplo.com")
	lia := e.visitorIn(b.id, "Lia")
	caioB := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	for name, c := range map[string]struct {
		b    *browser
		path string
		want int
	}{
		"outro cliente":                   {joao, estPath(b.id, "/participations/"+id), 404},
		"outro visitante":                 {lia, estPath(b.id, "/participations/"+id), 404},
		"sem sessão":                      {e.browser(), estPath(b.id, "/participations/"+id), 401},
		"dona de outra loja":              {outra.owner, estPath(b.id, "/participations/"+id), 404},
		"pela rota de outra loja":         {outra.owner, estPath(outra.id, "/participations/"+id), 404},
		"profissional de outra fila":      {caioB, estPath(b.id, "/participations/"+id), 403},
		"profissional da fila":            {b.betoB, estPath(b.id, "/participations/"+id), 200},
		"dona da loja":                    {b.owner, estPath(b.id, "/participations/"+id), 200},
		"dona da visita":                  {maria, estPath(b.id, "/participations/"+id), 200},
		"visita atual de outro visitante": {lia, estPath(b.id, "/my-visit"), 200},
	} {
		r := c.b.do("GET", c.path, nil)
		if r.status != c.want {
			t.Errorf("%s: %d (esperado %d) %s", name, r.status, c.want, r.raw)
		}
		if name == "visita atual de outro visitante" && r.data() != nil {
			t.Errorf("Lia não pode ver a visita de Maria: %s", r.raw)
		}
	}
	if r := caioB.partCmd(b.id, id, "start", map[string]any{"expected_version": 1}); r.status != 403 {
		t.Errorf("Caio iniciando atendimento da fila de Beto: %d", r.status)
	}
	if r := lia.partCmd(b.id, id, "cancel", map[string]any{"expected_version": 1}); r.status != 404 {
		t.Errorf("visitante cancelando visita alheia: %d", r.status)
	}
}

// Visitante: visita ativa impede criar outro visitante; encerrar vale 24 h para o acesso.
func TestVisitorVisitLifecycle(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	lia := e.visitorIn(b.id, "Lia")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	if r := lia.do("POST", visitorPath(b.id, ""), map[string]any{"nick": "Outra"}); r.status != 409 || r.code() != "visitor_visit_in_progress" {
		t.Fatalf("novo visitante com visita ativa: %d %s", r.status, r.raw)
	}
	lia.mustStatus(lia.partCmd(b.id, pl["id"].(string), "cancel", map[string]any{"expected_version": 1}), 200)
	e.clock.Advance(23 * time.Hour)
	if cur := lia.mustStatus(lia.do("GET", estPath(b.id, "/my-visit"), nil), 200); cur.data() == nil {
		t.Fatal("visita encerrada há 23 h ainda aparece para o visitante")
	}
	e.clock.Advance(time.Hour + time.Second)
	if len(visitorIDs(lia.session())) != 0 {
		t.Fatal("credencial deveria terminar 24 h após o encerramento")
	}
}

// Fila real (etapa 04) nas regras da etapa 03: fechar e desativar respeitam quem está na fila.
func TestQueueRulesWithRealParticipations(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.visitorIn(b.id, "Maria")
	pm, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	if r := b.owner.cmd("POST", estPath(b.id, "/queues/"+b.filaBeto.String()+"/close"), map[string]any{"expected_version": 2}); r.code() != "queue_has_pending_participants" {
		t.Fatalf("fechar com aguardando: %s", r.raw)
	}
	cm, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	// Pausada: entradas e chamadas recusadas, mas iniciar quem já foi chamado funciona (C-EST-04).
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/queues/"+b.filaBeto.String()+"/pause"), map[string]any{"expected_version": 2}), 200)
	if r := e.visitorIn(b.id, "Nova").enter(b.id, b.filaBeto, b.corte); r.code() != "queue_not_open" {
		t.Fatalf("entrada com fila pausada: %s", r.raw)
	}
	b.betoB.mustStatus(b.betoB.partCmd(b.id, pm["id"].(string), "start", map[string]any{"expected_version": num(cm["version"])}), 200)
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/queues/"+b.filaBeto.String()+"/close"), map[string]any{"expected_version": 3}), 200)
	b.owner.reauth()
	if r := b.owner.cmd("POST", estPath(b.id, "/professionals/"+b.beto.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "teste"}); r.code() != "professional_has_active_participations" {
		t.Fatalf("desativar com atendimento em curso: %s", r.raw)
	}
}
