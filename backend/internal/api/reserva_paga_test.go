// Cenários da reserva paga (Fila 2.0, parte 2, ADR-0039): ver docs/cenarios-aceite.md, seção
// "Reserva com Pix" (C-BK-*).
package api_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/api"
)

// paidShop: loja da agenda ("os dois", margem 5) cobrando a reserva no modo e valor dados.
func (e *env) paidShop(t *testing.T, mode string, cents int64, window int, late string) agendaShop {
	t.Helper()
	s := e.agendaShop(t, "hybrid", 5)
	own := s.owner
	st := own.mustStatus(own.do("GET", estPath(s.id, "/settings"), nil), 200).data()["agenda"].(map[string]any)
	if st["charge_available"] != true {
		t.Fatalf("cobrança da reserva deveria estar disponível no teste: %v", st)
	}
	own.mustStatus(own.cmd("PATCH", estPath(s.id, "/settings/agenda"), map[string]any{"expected_version": num(st["version"]),
		"booking_payment_mode": mode, "booking_payment_cents": cents, "cancel_window_hours": window, "late_cancel_policy": late}), 200)
	return s
}

func (b *browser) appt(est uuid.UUID, id any) map[string]any {
	b.e.t.Helper()
	return b.mustStatus(b.do("GET", estPath(est, "/appointments/"+id.(string)), nil), 200).data()
}

func (b *browser) bookingPix(est uuid.UUID, appt map[string]any, key string) resp {
	b.e.t.Helper()
	return b.do("POST", estPath(est, "/appointments/"+appt["id"].(string)+"/pix-charges"), nil, "Idempotency-Key", key)
}

func booking(appt map[string]any) map[string]any {
	b, _ := appt["payment"].(map[string]any)["booking"].(map[string]any)
	return b
}

func cents(m any) int64 { return num(m.(map[string]any)["amount_cents"]) }

// payBooking: o provedor confirma o Pix da reserva por webhook assinado.
func (e *env) payBooking(chargeID any) resp {
	e.t.Helper()
	remote := e.remote(chargeID)
	e.pix.Set(remote.ID, "PAID", 0)
	return e.signed(e.pix.Webhook("evt_"+uuid.NewString(), "transparent.completed", e.remote(chargeID)))
}

// agendaTick roda uma rodada do worker da agenda (retenções vencidas, faltas).
func (e *env) agendaTick() int {
	e.t.Helper()
	n, err := e.api.Agenda.Tick(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// serveAppointment registra a chegada, chama, inicia e conclui (todos os itens feitos) a reserva.
func (s agendaShop) serveAppointment(t *testing.T, appt map[string]any, op string, outcome func(string) string, extra map[string]any) map[string]any {
	t.Helper()
	b := s.betoB
	a := b.appt(s.id, appt["id"])
	if a["arrived_at"] == nil {
		a = b.mustStatus(b.apptCmd(s.id, a, "arrival", nil), 200).data()
	}
	called := b.mustStatus(b.apptCmd(s.id, a, "call", nil), 200).data()
	part := called["participation_id"].(string)
	p := b.mustStatus(b.do("GET", estPath(s.id, "/participations/"+part), nil), 200).data()
	visit := b.mustStatus(b.partCmd(s.id, part, "start", map[string]any{"expected_version": num(p["version"])}), 200)
	r := b.mustStatus(b.closeVisit(s.id, visit, op, outcome, extra), 200)
	_, o := visitOf(r)
	return b.order(s.id, o["id"])
}

// C-BK-01 — sinal: cotação no servidor, horário retido, Pix confirmado só pelo provedor, sinal
// abatido na comanda (um crédito), aviso repetido sem efeito duplo, caixa sem dinheiro novo.
func TestReservaComSinalDePontaAPonta(t *testing.T) {
	e := newEnv(t)
	s := e.paidShop(t, "deposit", 1500, 12, "refund")
	c := e.verifiedCustomer("cli@exemplo.com")
	q := c.mustStatus(c.do("GET", "/v1/public/establishments/"+s.id.String()+"/availability?date="+e.today()+"&items="+s.corte25, nil), 200).data()
	pay := q["payment"].(map[string]any)
	if pay["mode"] != "deposit" || cents(pay["due_now"]) != 1500 || cents(pay["due_at_store"]) != 2500 || cents(pay["estimated_total"]) != 4000 {
		t.Fatalf("cotação: %v", pay)
	}
	appt := c.mustStatus(c.book(s.id, s.beto, e.at("11:00"), s.corte25), 201).data()
	bk := booking(appt)
	if appt["status"] != "held" || appt["hold_expires_at"] == nil || bk == nil || bk["status"] != "awaiting_payment" || cents(bk["amount"]) != 1500 {
		t.Fatalf("reserva paga deveria ficar retida: %v", appt)
	}
	// Retido: o horário não é oferecido a outra pessoa.
	if contains(c.slots(s.id, s.beto, e.today(), s.corte25), "11:00") {
		t.Fatal("horário retido continua oferecido")
	}
	// Pix: mesma chave → mesma tentativa, uma única chamada ao provedor.
	key := newIdemKey()
	ch := c.mustStatus(c.bookingPix(s.id, appt, key), 201).data()
	again := c.mustStatus(c.bookingPix(s.id, appt, key), 201).data()
	if ch["id"] != again["id"] || e.pix.Creates() != 1 || ch["br_code"] == nil || cents(ch["amount"]) != 1500 {
		t.Fatalf("Pix da reserva: %v / %v (criações %d)", ch, again, e.pix.Creates())
	}
	// O QR vence antes do fim da retenção.
	qrEnd, _ := time.Parse(time.RFC3339, ch["expires_at"].(string))
	holdEnd, _ := time.Parse(time.RFC3339, appt["hold_expires_at"].(string))
	if !qrEnd.Before(holdEnd) {
		t.Fatalf("QR (%v) deveria vencer antes da retenção (%v)", qrEnd, holdEnd)
	}
	// Consultar não confirma nada; só o provedor.
	if a := c.appt(s.id, appt["id"]); a["status"] != "held" {
		t.Fatalf("consulta mudou a reserva: %v", a["status"])
	}
	remote := e.remote(ch["id"])
	if remote.Metadata["booking_id"] == "" || remote.Metadata["order_id"] != "" {
		t.Fatalf("metadados do Pix da reserva: %v", remote.Metadata)
	}
	e.payBooking(ch["id"])
	a := c.appt(s.id, appt["id"])
	if a["status"] != "confirmed" || booking(a)["status"] != "paid" {
		t.Fatalf("pagamento confirmado deveria confirmar a reserva: %v", a)
	}
	// Aviso repetido (mesmo conteúdo, outro id de evento): nada muda.
	e.signed(e.pix.Webhook("evt_"+uuid.NewString(), "transparent.completed", e.remote(ch["id"])))
	if n := e.count(t, `SELECT count(*) FROM receipt WHERE booking_payment_id IS NOT NULL`); n != 1 {
		t.Fatalf("recebimentos da reserva: %d", n)
	}
	// Atendimento: o sinal é abatido uma vez; na barbearia ficam R$ 25.
	e.advanceTo("10:55")
	o := s.serveAppointment(t, appt, "complete", func(string) string { return "performed" }, nil)
	if total(o) != 4000 || due(o) != 2500 || amountOf(o, "credit") != 1500 || o["status"] != "awaiting_payment" {
		t.Fatalf("comanda com sinal: %v", o["payment"])
	}
	if n := e.count(t, `SELECT count(*) FROM booking_credit WHERE order_id = $1 AND amount_cents = 1500`, o["id"]); n != 1 {
		t.Fatalf("crédito do sinal: %d", n)
	}
	// Restante em dinheiro, conferido → paga (principal 2500 + crédito 1500 = 4000).
	o = s.betoB.mustStatus(s.betoB.receive(s.id, o, map[string]any{"method": "cash", "amount_cents": 2500, "cash_tendered_cents": 3000}), 201).data()
	if o["status"] != "settled" && o["status"] != "awaiting_confirmation" {
		rec := receiptsOf(o)
		o = s.betoB.mustStatus(s.betoB.confer(s.id, o, rec[len(rec)-1]["id"]), 200).data()
	}
	o = s.betoB.order(s.id, o["id"])
	if o["status"] != "settled" {
		t.Fatalf("comanda deveria estar paga: %v", o["status"])
	}
	if a := c.appt(s.id, appt["id"]); a["status"] != "completed" || booking(a)["outcome"] != "applied" || cents(booking(a)["credit_applied"]) != 1500 {
		t.Fatalf("reserva concluída: %v", a)
	}
}

// C-BK-02 — Pix pago depois do prazo: a retenção venceu e o horário voltou; o pagamento tardio é
// registrado como excesso, abre pendência e a devolução integral é pedida ao provedor. Nunca toma o
// horário de quem reservou depois.
func TestReservaPixTardioViraDevolucao(t *testing.T) {
	e := newEnv(t)
	s := e.paidShop(t, "deposit", 1500, 12, "refund")
	c := e.verifiedCustomer("cli@exemplo.com")
	appt := c.mustStatus(c.book(s.id, s.beto, e.at("12:00"), s.corte25), 201).data()
	ch := c.mustStatus(c.bookingPix(s.id, appt, newIdemKey()), 201).data()
	holdEnd, _ := time.Parse(time.RFC3339, appt["hold_expires_at"].(string))
	// No instante exato do prazo a retenção já venceu.
	e.clock.Advance(holdEnd.Sub(e.clock.Now()))
	if n := e.agendaTick(); n < 1 {
		t.Fatal("retenção vencida não foi solta")
	}
	if a := c.appt(s.id, appt["id"]); a["status"] != "expired" || booking(a)["status"] != "expired" {
		t.Fatalf("reserva deveria expirar: %v", a)
	}
	other := e.verifiedCustomer("outra@exemplo.com")
	if !contains(other.slots(s.id, s.beto, e.today(), s.corte25), "12:00") {
		t.Fatal("horário não voltou a ficar livre")
	}
	theirs := other.mustStatus(other.book(s.id, s.beto, e.at("12:00"), s.corte25), 201).data()
	// O Pix vencido da primeira pessoa é pago agora (tardio).
	e.payBooking(ch["id"])
	if a := c.appt(s.id, appt["id"]); a["status"] != "expired" {
		t.Fatalf("pagamento tardio reabriu a reserva: %v", a["status"])
	}
	if a := other.appt(s.id, theirs["id"]); a["status"] != "held" {
		t.Fatalf("a reserva de quem chegou depois mudou: %v", a["status"])
	}
	if n := e.count(t, `SELECT count(*) FROM receipt r JOIN booking_payment b ON b.id = r.booking_payment_id
		WHERE b.appointment_id = $1 AND r.classification = 'excess'`, appt["id"]); n != 1 {
		t.Fatalf("tardio deveria virar excesso: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM financial_issue WHERE kind = 'booking_late_payment' AND status = 'open'`); n != 1 {
		t.Fatalf("pendência do tardio: %d", n)
	}
	// O worker de pagamento pede a devolução ao provedor (integral) e confirma pelo provedor.
	e.reconcile()
	e.reconcile()
	if e.pix.Refunds() != 1 {
		t.Fatalf("devolução ao provedor: %d", e.pix.Refunds())
	}
	if a := c.appt(s.id, appt["id"]); booking(a)["refund"] == nil || booking(a)["refund"].(map[string]any)["status"] != "confirmed" {
		t.Fatalf("devolução do tardio: %v", booking(a))
	}
	// Repetir o aviso de pagamento não pede outra devolução.
	e.signed(e.pix.Webhook("evt_"+uuid.NewString(), "transparent.completed", e.remote(ch["id"])))
	e.reconcile()
	if e.pix.Refunds() != 1 || e.count(t, `SELECT count(*) FROM refund WHERE booking_payment_id IS NOT NULL`) != 1 {
		t.Fatal("aviso repetido gerou outra devolução")
	}
}

// C-BK-03 — política de cancelamento: dentro da janela devolve; loja cancela devolve; fora da
// janela com "reter" exige confirmação do cliente e mantém o valor; "solicitada" não é "devolvida".
func TestReservaCancelamentoSegueAPolitica(t *testing.T) {
	e := newEnv(t)
	s := e.paidShop(t, "deposit", 2000, 2, "retain")
	c := e.verifiedCustomer("cli@exemplo.com")
	paid := func(at string) map[string]any {
		t.Helper()
		a := c.mustStatus(c.book(s.id, s.beto, e.at(at), s.corte25), 201).data()
		ch := c.mustStatus(c.bookingPix(s.id, a, newIdemKey()), 201).data()
		e.payBooking(ch["id"])
		a = c.appt(s.id, a["id"])
		if a["status"] != "confirmed" {
			t.Fatalf("reserva paga: %v", a["status"])
		}
		return a
	}
	// Dentro da janela (faltam > 2 h): devolução integral pedida ao provedor.
	a1 := paid("13:00")
	a1 = c.mustStatus(c.apptCmd(s.id, a1, "cancel", nil), 200).data()
	if a1["status"] != "cancelled" || booking(a1)["outcome"] != "refund_due" || booking(a1)["refund"].(map[string]any)["status"] != "requested" {
		t.Fatalf("cancelamento na janela: %v", booking(a1))
	}
	e.reconcile()
	e.reconcile()
	if r := booking(c.appt(s.id, a1["id"]))["refund"].(map[string]any); r["status"] != "confirmed" || e.pix.Refunds() != 1 {
		t.Fatalf("devolução confirmada pelo provedor: %v (%d)", r, e.pix.Refunds())
	}
	// Fora da janela com "reter": o cliente precisa confirmar; depois, o valor fica com a loja.
	a2 := paid("10:30")
	e.advanceTo("09:00")
	if r := c.apptCmd(s.id, a2, "cancel", nil); r.status != 409 || r.code() != "cancel_retains_payment" {
		t.Fatalf("cancelamento tardio sem confirmação: %d %s", r.status, r.code())
	}
	a2 = c.mustStatus(c.apptCmd(s.id, a2, "cancel", map[string]any{"accept_retention": true}), 200).data()
	if booking(a2)["outcome"] != "kept" || booking(a2)["refund"] != nil {
		t.Fatalf("retenção pela política: %v", booking(a2))
	}
	// A loja cancela (mesmo em cima da hora): devolução sempre.
	a3 := paid("10:00")
	a3 = s.owner.mustStatus(s.owner.apptCmd(s.id, a3, "cancel", map[string]any{"reason": "barbeiro doente"}), 200).data()
	if booking(a3)["outcome"] != "refund_due" {
		t.Fatalf("loja cancelou: %v", booking(a3))
	}
	if e.count(t, `SELECT count(*) FROM refund WHERE booking_payment_id IS NOT NULL`) != 2 {
		t.Fatal("devoluções: deveriam ser duas (janela e loja)")
	}
}

// C-BK-04 — sinal igual ao valor: a comanda fecha paga sem recebimento de R$ 0 nem conferência.
// Taxa de reserva: cobrada além do atendimento e nunca vira crédito.
func TestReservaSinalIntegralETaxa(t *testing.T) {
	e := newEnv(t)
	s := e.paidShop(t, "deposit", 9000, 12, "refund") // sinal configurado acima do corte: limitado a R$ 40
	c := e.verifiedCustomer("cli@exemplo.com")
	a := c.mustStatus(c.book(s.id, s.beto, e.at("10:00"), s.corte25), 201).data()
	if cents(booking(a)["amount"]) != 4000 || cents(a["payment"].(map[string]any)["due_at_store"]) != 0 {
		t.Fatalf("sinal limitado ao valor: %v", a["payment"])
	}
	ch := c.mustStatus(c.bookingPix(s.id, a, newIdemKey()), 201).data()
	e.payBooking(ch["id"])
	e.advanceTo("09:55")
	o := s.serveAppointment(t, a, "complete", func(string) string { return "performed" }, nil)
	if o["status"] != "settled" || due(o) != 0 || len(receiptsOf(o)) != 0 {
		t.Fatalf("sinal integral deveria quitar a comanda: %v %v", o["status"], o["payment"])
	}

	e = newEnv(t) // outra instalação: a loja de teste usa sempre o mesmo endereço
	f := e.paidShop(t, "fee", 1000, 12, "refund")
	d := e.verifiedCustomer("taxa@exemplo.com")
	q := d.mustStatus(d.do("GET", "/v1/public/establishments/"+f.id.String()+"/availability?date="+e.today()+"&items="+f.corte25, nil), 200).data()
	if p := q["payment"].(map[string]any); p["mode"] != "fee" || cents(p["due_now"]) != 1000 || cents(p["estimated_total"]) != 5000 {
		t.Fatalf("cotação da taxa: %v", p)
	}
	b := d.mustStatus(d.book(f.id, f.beto, e.at("11:00"), f.corte25), 201).data()
	ch = d.mustStatus(d.bookingPix(f.id, b, newIdemKey()), 201).data()
	e.payBooking(ch["id"])
	e.advanceTo("10:55")
	o = f.serveAppointment(t, b, "complete", func(string) string { return "performed" }, nil)
	if due(o) != 4000 || amountOf(o, "credit") != 0 {
		t.Fatalf("taxa não abate o atendimento: %v", o["payment"])
	}
	if bk := booking(d.appt(f.id, b["id"])); bk["outcome"] != "kept" {
		t.Fatalf("taxa fica com a loja: %v", bk)
	}
}

// C-BK-05 — interrompido com valor final abaixo do sinal: crédito = total, sobra em devolução
// parcial que o dono confirma (o provedor só estorna o integral), com a pendência resolvida junto.
func TestReservaSobraDoSinalDevolucaoParcial(t *testing.T) {
	e := newEnv(t)
	s := e.paidShop(t, "deposit", 5000, 12, "refund")
	c := e.verifiedCustomer("cli@exemplo.com")
	a := c.mustStatus(c.book(s.id, s.beto, e.at("10:00"), s.corte25, s.barba30), 201).data()
	ch := c.mustStatus(c.bookingPix(s.id, a, newIdemKey()), 201).data()
	e.payBooking(ch["id"])
	e.advanceTo("09:55")
	o := s.serveAppointment(t, a, "interrupt", func(n string) string {
		if n == "Corte 25" {
			return "fully_performed"
		}
		return "not_started"
	}, map[string]any{"reason": "cliente precisou sair"})
	review := []map[string]any{}
	for _, it := range o["items"].([]any) {
		m := it.(map[string]any)
		out := "not_started"
		if m["name"] == "Corte 25" {
			out = "fully_performed"
		}
		review = append(review, map[string]any{"item_id": m["id"], "outcome": out})
	}
	o = s.owner.mustStatus(s.owner.cmd("POST", orderPath(s.id, o["id"])+"/close-interruption", map[string]any{
		"expected_version": num(o["version"]), "review_note": "só o corte", "items": review}), 200).data()
	if total(o) != 4000 || amountOf(o, "credit") != 4000 || o["status"] != "settled" {
		t.Fatalf("crédito limitado ao total: %v %v", o["status"], o["payment"])
	}
	issues := s.owner.mustStatus(s.owner.do("GET", estPath(s.id, "/financial-issues?status=open"), nil), 200).list()
	var refundID string
	for _, it := range issues {
		m := it.(map[string]any)
		if m["kind"] == "booking_refund_due" && cents(m["amount"]) == 1000 && m["appointment_id"] == a["id"] {
			refundID, _ = m["booking_refund_id"].(string)
		}
	}
	if refundID == "" {
		t.Fatalf("pendência da sobra do sinal: %v", issues)
	}
	if e.pix.Refunds() != 0 {
		t.Fatal("devolução parcial não vai ao provedor")
	}
	path := estPath(s.id, "/booking-refunds/"+refundID+"/confirm")
	if r := s.owner.cmd("POST", path, map[string]any{"evidence_note": "Pix de R$ 10 feito pelo app do banco"}); r.code() != "reauthentication_required" {
		t.Fatalf("sem reautenticação: %d %s", r.status, r.code())
	}
	s.owner.reauth()
	bk := s.owner.mustStatus(s.owner.cmd("POST", path, map[string]any{"evidence_note": "Pix de R$ 10 feito pelo app do banco"}), 200).data()
	if r := bk["refund"].(map[string]any); r["status"] != "confirmed" || r["partial"] != true || cents(r["amount"]) != 1000 {
		t.Fatalf("devolução parcial confirmada: %v", bk)
	}
	if e.count(t, `SELECT count(*) FROM financial_issue WHERE kind = 'booking_refund_due' AND status = 'open'`) != 0 {
		t.Fatal("pendência deveria sair junto")
	}
}

// C-BK-06 — concorrência: aviso do provedor e consulta do worker ao mesmo tempo registram UM
// recebimento; duas pessoas no mesmo horário pago: uma retém, a outra recebe slot_unavailable.
func TestReservaPagaConcorrencia(t *testing.T) {
	e := newEnv(t)
	s := e.paidShop(t, "deposit", 1500, 12, "refund")
	var wg sync.WaitGroup
	results := make([]resp, 6)
	people := make([]*browser, 6)
	for i := range people {
		people[i] = e.verifiedCustomer("p" + string(rune('a'+i)) + "@exemplo.com")
	}
	for i := range people {
		wg.Go(func() { results[i] = people[i].book(s.id, s.beto, e.at("15:00"), s.corte25) })
	}
	wg.Wait()
	won, lost := 0, 0
	var winner *browser
	var appt map[string]any
	for i, r := range results {
		switch {
		case r.status == 201:
			won++
			winner, appt = people[i], r.data()
		case r.status == 409 && r.code() == "slot_unavailable":
			lost++
		default:
			t.Fatalf("resposta inesperada: %d %s", r.status, r.raw)
		}
	}
	if won != 1 || lost != 5 {
		t.Fatalf("disputa do horário pago: %d venceu, %d perderam", won, lost)
	}
	ch := winner.mustStatus(winner.bookingPix(s.id, appt, newIdemKey()), 201).data()
	e.pix.Set(e.remote(ch["id"]).ID, "PAID", 0)
	body := e.pix.Webhook("evt_"+uuid.NewString(), "transparent.completed", e.remote(ch["id"]))
	var wg2 sync.WaitGroup
	wg2.Go(func() { e.signed(body) })
	wg2.Go(func() { e.signed(body) })
	wg2.Go(func() {
		if _, err := e.api.Payments.ReconcileOnce(context.Background()); err != nil {
			t.Error(err)
		}
	})
	wg2.Wait()
	e.reconcile()
	if n := e.count(t, `SELECT count(*) FROM receipt WHERE booking_payment_id IS NOT NULL`); n != 1 {
		t.Fatalf("recebimentos com aviso e consulta juntos: %d", n)
	}
	if a := winner.appt(s.id, appt["id"]); a["status"] != "confirmed" {
		t.Fatalf("reserva: %v", a["status"])
	}
}

// C-BK-07 — sem liberação ou sem Pix: a loja não consegue ligar a cobrança; reserva de balcão é
// sempre gratuita; horário em cima da hora não pode ser cobrado.
func TestReservaCobrancaSoComLiberacao(t *testing.T) {
	e := newEnv(t)
	s := e.paidShop(t, "deposit", 1500, 12, "refund")
	r := s.owner.mustStatus(s.owner.cmd("POST", estPath(s.id, "/appointments"), map[string]any{"professional_id": s.beto.String(),
		"starts_at": e.at("16:00").UTC().Format(time.RFC3339), "items": []map[string]any{{"service_id": s.corte25}},
		"counter_customer": map[string]any{"nick": "Seu Zé"}}), 201).data()
	if r["status"] != "confirmed" || booking(r) != nil {
		t.Fatalf("balcão deveria ser gratuito: %v", r)
	}
	st0 := s.owner.mustStatus(s.owner.do("GET", estPath(s.id, "/settings"), nil), 200).data()["agenda"].(map[string]any)
	s.owner.mustStatus(s.owner.cmd("PATCH", estPath(s.id, "/settings/agenda"), map[string]any{"expected_version": num(st0["version"]),
		"min_lead_minutes": 0}), 200)
	c := e.verifiedCustomer("cli@exemplo.com")
	// Retenção de 18 min (QR de 15 + margem): às 09:00 um horário às 09:15 não dá tempo de pagar.
	if r := c.book(s.id, s.beto, e.at("09:15"), s.corte25); r.status != 409 || r.code() != "booking_too_close" {
		t.Fatalf("horário sem tempo para pagar: %d %s", r.status, r.code())
	}
	// Instalação sem a liberação: nenhuma loja liga a cobrança.
	e2 := newEnvDeps(t, func(d *api.Deps) { d.Config.Ops.AgendaCharge = false })
	s2 := e2.agendaShop(t, "hybrid", 0)
	st := s2.owner.mustStatus(s2.owner.do("GET", estPath(s2.id, "/settings"), nil), 200).data()["agenda"].(map[string]any)
	if st["charge_available"] != false {
		t.Fatalf("cobrança indisponível sem a liberação: %v", st)
	}
	if r := s2.owner.cmd("PATCH", estPath(s2.id, "/settings/agenda"), map[string]any{"expected_version": num(st["version"]),
		"booking_payment_mode": "deposit", "booking_payment_cents": 1500}); r.status != 409 || r.code() != "booking_payment_unavailable" {
		t.Fatalf("ligar cobrança sem liberação: %d %s", r.status, r.code())
	}
}

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

// lastCode entrega os e-mails e devolve o código de 6 dígitos do último e-mail com o assunto.
func (e *env) lastCode(to, subject string) string {
	e.t.Helper()
	e.deliver()
	for i := len(e.inbox) - 1; i >= 0; i-- {
		m := e.inbox[i]
		if strings.EqualFold(m.To, to) && strings.Contains(m.Subject, subject) {
			if mm := sixDigits.FindStringSubmatch(m.Body); mm != nil {
				return mm[1]
			}
		}
	}
	e.t.Fatalf("nenhum código %q para %s", subject, to)
	return ""
}

func (e *env) inboxHas(to, subject string) bool {
	e.t.Helper()
	e.deliver()
	for _, m := range e.inbox {
		if strings.EqualFold(m.To, to) && strings.Contains(m.Subject, subject) {
			return true
		}
	}
	return false
}

// C-BK-08 — avisos e recuperação: o visitante confirma o e-mail por código, recebe a confirmação
// e a remarcação; em outro aparelho pede o link (resposta igual para e-mail desconhecido), troca o
// token uma única vez e vê a reserva. Aviso velho (antes da remarcação) não é enviado.
func TestReservaAvisosERecuperacaoPorEmail(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "hybrid", 5)
	ana := e.visitorIn(s.id, "Ana")
	const addr = "ana@exemplo.com"
	contact := estPath(s.id, "/visitor-contact")
	v := ana.mustStatus(ana.cmd("POST", contact, map[string]any{"email": addr}), 202).data()
	if v["verified"] != false || v["email_masked"] != "an***@exemplo.com" {
		t.Fatalf("contato pendente: %v", v)
	}
	code := e.lastCode(addr, "Código")
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if r := ana.cmd("POST", contact+"/verify", map[string]any{"code": wrong}); r.code() != "contact_code_invalid" {
		t.Fatalf("código errado: %d %s", r.status, r.raw)
	}
	if v := ana.mustStatus(ana.cmd("POST", contact+"/verify", map[string]any{"code": code}), 200).data(); v["verified"] != true {
		t.Fatalf("contato verificado: %v", v)
	}
	appt := ana.mustStatus(ana.book(s.id, s.beto, e.at("13:00"), s.corte25), 201).data()
	appt = ana.mustStatus(ana.apptCmd(s.id, appt, "reschedule", map[string]any{"starts_at": e.at("14:00").UTC().Format(time.RFC3339)}), 200).data()
	e.agendaTick()
	if e.inboxHas(addr, "Reserva confirmada") {
		t.Fatal("confirmação da revisão anterior não deveria sair depois da remarcação")
	}
	if !e.inboxHas(addr, "Reserva remarcada") {
		t.Fatal("aviso de remarcação não enviado")
	}
	// Outro aparelho: pedir o link (e-mail desconhecido recebe a mesma resposta, sem e-mail).
	other := e.browser()
	access := "/v1/public/establishments/" + s.id.String() + "/appointment-access"
	other.mustStatus(other.cmd("POST", access, map[string]any{"email": "ninguem@exemplo.com"}), 202)
	if e.mailCount("ninguem@exemplo.com") != 0 {
		t.Fatal("e-mail desconhecido recebeu link")
	}
	other.mustStatus(other.cmd("POST", access, map[string]any{"email": addr}), 202)
	token := e.lastToken(addr, "Acesso")
	other.mustStatus(other.cmd("POST", access+"/exchange", map[string]any{"token": token}), 200)
	if a := other.appt(s.id, appt["id"]); a["status"] != "confirmed" || a["local_time"] != "14:00" {
		t.Fatalf("reserva no outro aparelho: %v", a)
	}
	third := e.browser()
	if r := third.cmd("POST", access+"/exchange", map[string]any{"token": token}); r.status != 401 || r.code() != "access_link_invalid" {
		t.Fatalf("link reutilizado: %d %s", r.status, r.code())
	}
	// Conta: cancelamento gera aviso; visitante sem e-mail verificado: aviso pulado, nunca falha.
	c := e.verifiedCustomer("cli@exemplo.com")
	ca := c.mustStatus(c.book(s.id, s.caio, e.at("15:00"), s.corte25), 201).data()
	c.mustStatus(c.apptCmd(s.id, ca, "cancel", nil), 200)
	bia := e.visitorIn(s.id, "Bia")
	bia.mustStatus(bia.book(s.id, s.caio, e.at("16:00"), s.corte25), 201)
	e.agendaTick()
	if !e.inboxHas("cli@exemplo.com", "Reserva cancelada") {
		t.Fatal("aviso de cancelamento não enviado")
	}
	if e.count(t, `SELECT count(*) FROM appointment_notice WHERE status = 'skipped' AND skip_reason = 'sem e-mail verificado'`) < 1 {
		t.Fatal("aviso sem contato deveria ser pulado")
	}
	if e.count(t, `SELECT count(*) FROM appointment_notice WHERE status = 'pending' AND due_at <= $1`, e.clock.Now()) != 0 {
		t.Fatal("avisos vencidos ficaram pendentes")
	}
}
