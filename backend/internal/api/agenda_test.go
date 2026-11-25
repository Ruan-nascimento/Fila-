// Cenários da agenda (Fila 2.0, parte 1, ADR-0038): ver docs/cenarios-aceite.md, seção Agenda.
package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// O relógio de teste começa em 12/11/2026 12:00 UTC = 09:00 em São Paulo (quinta-feira).
var spLoc = func() *time.Location { l, _ := time.LoadLocation("America/Sao_Paulo"); return l }()

// at devolve o instante local "HH:MM" do dia do relógio (fuso da loja).
func (e *env) at(hhmm string) time.Time {
	d := e.clock.Now().In(spLoc)
	t, err := time.ParseInLocation("2006-01-02 15:04", d.Format("2006-01-02")+" "+hhmm, spLoc)
	if err != nil {
		panic(err)
	}
	return t
}

// advanceTo avança o relógio até o horário local "HH:MM" de hoje.
func (e *env) advanceTo(hhmm string) {
	e.clock.Advance(e.at(hhmm).Sub(e.clock.Now()))
}

// agendaShop: loja com Beto e Caio, corte (25 min, R$ 40), barba (30 min, R$ 30, só Beto) e um
// produto; jornada da loja 09:00–18:00 todos os dias; modo e margem conforme o teste.
type agendaShop struct {
	barbearia
	corte25, barba30, pomada string
}

func (e *env) agendaShop(t *testing.T, mode string, buffer int) agendaShop {
	t.Helper()
	b := e.barbearia(t)
	s := agendaShop{barbearia: b}
	own := b.owner
	s.corte25 = own.createService(b.id, map[string]any{"name": "Corte 25", "price_cents": 4000, "duration_minutes": 25, "points": 0,
		"professional_ids": []string{b.beto.String(), b.caio.String()}})["id"].(string)
	s.barba30 = own.createService(b.id, map[string]any{"name": "Barba 30", "price_cents": 3000, "duration_minutes": 30, "points": 0,
		"professional_ids": []string{b.beto.String()}})["id"].(string)
	s.pomada = own.mustStatus(own.cmd("POST", estPath(b.id, "/products"), map[string]any{"name": "Pomada", "price_cents": 2500}), 201).data()["id"].(string)
	shifts := []map[string]any{}
	for d := range 7 {
		shifts = append(shifts, map[string]any{"weekday": d, "start_minute": 9 * 60, "end_minute": 18 * 60, "modality": "both"})
	}
	own.mustStatus(own.cmd("PUT", estPath(b.id, "/agenda/hours"), map[string]any{
		"effective_from": e.clock.Now().In(spLoc).Format("2006-01-02"), "shifts": shifts}), 200)
	st := own.mustStatus(own.do("GET", estPath(b.id, "/settings"), nil), 200).data()["agenda"].(map[string]any)
	own.mustStatus(own.cmd("PATCH", estPath(b.id, "/settings/agenda"), map[string]any{"expected_version": num(st["version"]),
		"service_mode": mode, "buffer_minutes": buffer}), 200)
	return s
}

func (b *browser) book(est, prof uuid.UUID, start time.Time, items ...string) resp {
	b.e.t.Helper()
	lines := []map[string]any{}
	for _, it := range items {
		id, qty, _ := strings.Cut(it, ":")
		l := map[string]any{"service_id": id}
		if qty != "" {
			var n int
			fmt.Sscan(qty, &n)
			l["quantity"] = n
		}
		lines = append(lines, l)
	}
	return b.cmd("POST", estPath(est, "/appointments"), map[string]any{"professional_id": prof.String(),
		"starts_at": start.UTC().Format(time.RFC3339), "items": lines})
}

func (b *browser) apptCmd(est uuid.UUID, appt map[string]any, op string, body map[string]any) resp {
	b.e.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["expected_version"] = num(appt["version"])
	return b.cmd("POST", estPath(est, "/appointments/"+appt["id"].(string)+"/"+op), body)
}

func (b *browser) slots(est, prof uuid.UUID, day string, items ...string) []string {
	b.e.t.Helper()
	q := "?date=" + day + "&professional_id=" + prof.String()
	for _, it := range items {
		q += "&items=" + it
	}
	d := b.mustStatus(b.do("GET", "/v1/public/establishments/"+est.String()+"/availability"+q, nil), 200).data()
	out := []string{}
	for _, p := range d["professionals"].([]any) {
		for _, s := range p.(map[string]any)["slots"].([]any) {
			out = append(out, s.(map[string]any)["local_time"].(string))
		}
	}
	return out
}

// list devolve "data" quando é uma lista.
func (r resp) list() []any {
	l, _ := r.body["data"].([]any)
	return l
}

func (e *env) today() string { return e.clock.Now().In(spLoc).Format("2006-01-02") }

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// C-AG-01 — loja que não aderiu continua só com fila; a agenda nem aparece.
func TestAgendaLojaSemAdesaoContinuaFila(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	st := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/settings"), nil), 200).data()["agenda"].(map[string]any)
	if st["service_mode"] != "queue" || st["available"] != true {
		t.Fatalf("padrão deve ser fila (com a agenda liberada no servidor de teste): %v", st)
	}
	r := e.browser().do("GET", "/v1/public/establishments/"+b.id.String()+"/availability?date="+e.today()+"&items="+b.corte, nil)
	if r.status != 409 || r.code() != "schedule_disabled" {
		t.Fatalf("loja só de fila não oferece horários: %d %s", r.status, r.code())
	}
	c := e.verifiedCustomer("cli@exemplo.com")
	c.mustStatus(c.enter(b.id, b.filaBeto, b.corte), 201)
	// Sem jornada, sem reservas: chamar segue a regra 1.0.
	b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
}

// C-AG-02 — modo "com horário marcado": reserva de ponta a ponta, mesma cadeira e mesma comanda.
func TestAgendaModoHorarioMarcadoDePontaAPonta(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 5)
	c := e.verifiedCustomer("cli@exemplo.com")
	// Fila fechada à entrada no modo agenda.
	if r := c.enter(s.id, s.filaBeto, s.corte25); r.status != 409 || r.code() != "queue_entry_schedule_only" {
		t.Fatalf("entrada na fila deveria fechar no modo agenda: %d %s", r.status, r.code())
	}
	// 3 cortes de 25 + produto + 5 de limpeza = 80 min; grade de 15; antecedência 30 (agora 09:00).
	got := c.slots(s.id, s.beto, e.today(), s.corte25+":3", s.pomada)
	if got[0] != "09:30" || contains(got, "16:45") || got[len(got)-1] != "16:40" && got[len(got)-1] != "16:30" {
		t.Fatalf("horários inesperados: %v", got)
	}
	appt := c.mustStatus(c.book(s.id, s.beto, e.at("15:00"), s.corte25+":3", s.pomada), 201).data()
	if num(appt["price"].(map[string]any)["amount_cents"]) != 3*4000+2500 || num(appt["service_minutes"]) != 75 || num(appt["buffer_minutes"]) != 5 {
		t.Fatalf("preço/duração congelados errados: %v", appt)
	}
	if appt["status"] != "confirmed" || appt["local_time"] != "15:00" {
		t.Fatalf("reserva: %v", appt)
	}
	// O horário saiu da disponibilidade (e quem invadiria também).
	if after := c.slots(s.id, s.beto, e.today(), s.corte25); contains(after, "15:00") || contains(after, "14:45") || !contains(after, "14:30") {
		t.Fatalf("disponibilidade não descontou a reserva: %v", after)
	}
	// Chamar cedo demais (sem presença) → 409; registrar chegada fora da janela → 409.
	b := s.betoB
	if r := b.apptCmd(s.id, appt, "call", nil); r.status != 409 || r.code() != "appointment_too_early" {
		t.Fatalf("chamada cedo: %d %s", r.status, r.code())
	}
	e.advanceTo("14:50")
	appt = b.mustStatus(b.apptCmd(s.id, appt, "arrival", nil), 200).data()
	called := b.mustStatus(b.apptCmd(s.id, appt, "call", nil), 200).data()
	if called["status"] != "called" || called["participation_id"] == nil {
		t.Fatalf("chamada não abriu a visita: %v", called)
	}
	// Prazo = max(15:00, 14:50) + 10 = 15:10.
	if dl, _ := time.Parse(time.RFC3339, called["deadline_at"].(string)); !dl.Equal(e.at("15:10")) {
		t.Fatalf("prazo errado: %v", called["deadline_at"])
	}
	part := called["participation_id"].(string)
	p := b.mustStatus(b.do("GET", estPath(s.id, "/participations/"+part), nil), 200).data()
	o := b.mustStatus(b.do("GET", estPath(s.id, "/orders/"+p["order_id"].(string)), nil), 200).data()
	if p["status"] != "called" || p["origin"] != "appointment" || num(o["total"].(map[string]any)["amount_cents"]) != 3*4000+2500 {
		t.Fatalf("visita da reserva: %v %v", p, o)
	}
	// Iniciar pelo fluxo da visita: a reserva acompanha.
	b.mustStatus(b.partCmd(s.id, part, "start", map[string]any{"expected_version": num(p["version"])}), 200)
	cur := c.mustStatus(c.do("GET", estPath(s.id, "/appointments/"+appt["id"].(string)), nil), 200).data()
	if cur["status"] != "in_service" {
		t.Fatalf("reserva deveria estar em atendimento: %v", cur["status"])
	}
	// Uma cadeira: chamar outro durante o atendimento é recusado.
	if r := b.callNext(s.id, s.filaBeto); r.status != 409 {
		t.Fatalf("segunda chamada na mesma cadeira: %d", r.status)
	}
}

// C-AG-03 — duas pessoas disputam o último horário: uma vence, a outra recebe slot_unavailable com
// os horários atuais. Dois barbeiros livres atendem ao mesmo tempo.
func TestAgendaDisputaDoUltimoHorario(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	start := e.at("16:00")
	var wg sync.WaitGroup
	res := make([]resp, 6)
	for i := range res {
		c := e.verifiedCustomer(fmt.Sprintf("disputa%d@exemplo.com", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i] = c.book(s.id, s.beto, start, s.corte25)
		}()
	}
	wg.Wait()
	ok, lost := 0, 0
	for _, r := range res {
		switch {
		case r.status == 201:
			ok++
		case r.status == 409 && r.code() == "slot_unavailable":
			lost++
		default:
			t.Fatalf("resposta inesperada: %d %s", r.status, r.code())
		}
	}
	if ok != 1 || lost != 5 {
		t.Fatalf("esperava 1 vencedor e 5 conflitos, obtive %d e %d", ok, lost)
	}
	// Outro barbeiro no mesmo horário: livre.
	c := e.verifiedCustomer("caio-cliente@exemplo.com")
	c.mustStatus(c.book(s.id, s.caio, start, s.corte25), 201)
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM agenda_occupancy WHERE released_at IS NULL`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("ocupações ativas: %d %v", n, err)
	}
}

// C-AG-04 — o mesmo cliente não sobrepõe reservas (nem com outro barbeiro); limite de futuras;
// mesma chave = mesma reserva; mesma chave com outro corpo = 422.
func TestAgendaLimitesDoCliente(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	c := e.verifiedCustomer("cli@exemplo.com")
	body := map[string]any{"professional_id": s.beto.String(), "starts_at": e.at("10:00").UTC().Format(time.RFC3339),
		"items": []map[string]any{{"service_id": s.corte25}}}
	r1 := c.do("POST", estPath(s.id, "/appointments"), body, "Idempotency-Key", "chave-reserva-00001")
	r2 := c.do("POST", estPath(s.id, "/appointments"), body, "Idempotency-Key", "chave-reserva-00001")
	c.mustStatus(r1, 201)
	if r2.status != 201 || r2.data()["id"] != r1.data()["id"] {
		t.Fatalf("repetição deveria devolver a mesma reserva: %d", r2.status)
	}
	body["starts_at"] = e.at("11:00").UTC().Format(time.RFC3339)
	if r := c.do("POST", estPath(s.id, "/appointments"), body, "Idempotency-Key", "chave-reserva-00001"); r.status != 422 {
		t.Fatalf("mesma chave com outro corpo: %d", r.status)
	}
	if r := c.book(s.id, s.caio, e.at("10:15"), s.corte25); r.status != 409 || r.code() != "appointment_overlaps_own" {
		t.Fatalf("sobreposição com outro barbeiro: %d %s", r.status, r.code())
	}
	c.mustStatus(c.book(s.id, s.caio, e.at("11:00"), s.corte25), 201)
	c.mustStatus(c.book(s.id, s.caio, e.at("12:00"), s.corte25), 201)
	if r := c.book(s.id, s.caio, e.at("13:00"), s.corte25); r.status != 409 || r.code() != "appointment_limit_reached" {
		t.Fatalf("limite de 3 futuras: %d %s", r.status, r.code())
	}
	// Outro cliente não vê a reserva (404) e o calendário público não traz nomes.
	other := e.verifiedCustomer("outro@exemplo.com")
	if r := other.do("GET", estPath(s.id, "/appointments/"+r1.data()["id"].(string)), nil); r.status != 404 {
		t.Fatalf("reserva alheia: %d", r.status)
	}
	raw := string(e.browser().do("GET", "/v1/public/establishments/"+s.id.String()+"/availability?date="+e.today()+"&items="+s.corte25, nil).raw)
	if strings.Contains(raw, "Pessoa Teste") || strings.Contains(raw, "cli@exemplo.com") {
		t.Fatal("disponibilidade pública expôs dados do cliente")
	}
	// Minhas reservas.
	mine := c.mustStatus(c.do("GET", "/v1/my-appointments", nil), 200)
	if n := len(mine.list()); n != 3 {
		t.Fatalf("minhas reservas: %d", n)
	}
}

// C-AG-05 — exemplo de aceite do ADR: agora 14h20, reserva às 15h, tolerância 10, limpeza 5.
func TestAgendaEncaixeAntesDoHorarioMarcado(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "hybrid", 5)
	reserva := e.verifiedCustomer("reserva@exemplo.com")
	reserva.mustStatus(reserva.book(s.id, s.beto, e.at("15:00"), s.corte25), 201)
	e.advanceTo("14:20")
	b := s.betoB
	// Corte de 25: 14h20 + 10 + 25 + 5 = 15h → cabe.
	c1 := e.verifiedCustomer("c1@exemplo.com")
	p1, _ := visitOf(c1.mustStatus(c1.enter(s.id, s.filaBeto, s.corte25), 201))
	b.mustStatus(b.callNext(s.id, s.filaBeto), 200)
	// O dono encerra (cliente desistiu) para liberar a cadeira.
	cur := b.mustStatus(b.do("GET", estPath(s.id, "/participations/"+p1["id"].(string)), nil), 200).data()
	s.owner.mustStatus(s.owner.partCmd(s.id, p1["id"].(string), "cancel", map[string]any{"expected_version": num(cur["version"]), "reason": "teste"}), 200)
	// Barba de 30: precisa de 45 com a tolerância → recusa com o motivo; o cliente fica no lugar.
	c2 := e.verifiedCustomer("c2@exemplo.com")
	p2, _ := visitOf(c2.mustStatus(c2.cmd("POST", estPath(s.id, "/participations"), map[string]any{"queue_id": s.filaBeto.String(),
		"service_ids": []string{s.barba30}}), 201))
	r := b.callNext(s.id, s.filaBeto)
	if r.status != 409 || r.code() != "chair_reserved" || !strings.Contains(r.problem()["detail"].(string), "Próximo horário marcado às 15h") ||
		!strings.Contains(r.problem()["detail"].(string), "35 min com a limpeza") {
		t.Fatalf("chamada que invade a reserva: %d %s %v", r.status, r.code(), r.problem()["detail"])
	}
	if st, _ := e.partStatus(t, p2["id"].(string)); st != "waiting" {
		t.Fatalf("o cliente deveria continuar aguardando: %s", st)
	}
	// Presente e início imediato: 14h20 + 30 + 5 = 14h55 → cabe.
	b.mustStatus(b.cmd("POST", estPath(s.id, "/queues/"+s.filaBeto.String()+"/call-next"), map[string]any{"start_immediately": true}), 200)
	if st, _ := e.partStatus(t, p2["id"].(string)); st != "in_service" {
		t.Fatalf("chamar e iniciar: %s", st)
	}
	// Aumentar o serviço de quem está na cadeira de forma a invadir a reserva é recusado.
	cur2 := b.mustStatus(b.do("GET", estPath(s.id, "/participations/"+p2["id"].(string)), nil), 200).data()
	ord := b.mustStatus(b.do("GET", estPath(s.id, "/orders/"+cur2["order_id"].(string)), nil), 200).data()
	if r := b.cmd("PUT", estPath(s.id, "/orders/"+ord["id"].(string)+"/items"), map[string]any{"expected_version": num(ord["version"]),
		"reason": "cliente pediu", "items": []map[string]any{{"service_id": s.barba30}, {"service_id": s.corte25}}}); r.status != 409 || r.code() != "chair_reserved" {
		t.Fatalf("aumento que invade a reserva: %d %s", r.status, r.code())
	}
}

// C-AG-06 — 14h40: um corte de 25 não cabe nem com início imediato; "descer e chamar o próximo"
// passa pela mesma regra.
func TestAgendaMesmaRegraEmChamarEDescer(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "hybrid", 5)
	reserva := e.verifiedCustomer("reserva@exemplo.com")
	reserva.mustStatus(reserva.book(s.id, s.beto, e.at("15:00"), s.corte25), 201)
	e.advanceTo("14:00")
	b := s.betoB
	c1 := e.verifiedCustomer("c1@exemplo.com")
	p1, _ := visitOf(c1.mustStatus(c1.enter(s.id, s.filaBeto, s.corte25), 201))
	c2 := e.verifiedCustomer("c2@exemplo.com")
	c2.mustStatus(c2.enter(s.id, s.filaBeto, s.corte25), 201)
	// 14h00 + 10 + 25 + 5 = 14h40: o primeiro é chamado.
	b.mustStatus(b.callNext(s.id, s.filaBeto), 200)
	e.advanceTo("14:40")
	// Descer o chamado chamaria o próximo às 14h40 (14h40 + 40 > 15h): recusado, nada muda.
	cur := b.mustStatus(b.do("GET", estPath(s.id, "/participations/"+p1["id"].(string)), nil), 200).data()
	r := b.partCmd(s.id, p1["id"].(string), "move", map[string]any{"expected_version": num(cur["version"]), "direction": "down"})
	if r.status != 409 || r.code() != "chair_reserved" {
		t.Fatalf("descer e chamar o próximo: %d %s", r.status, r.code())
	}
	if st, _ := e.partStatus(t, p1["id"].(string)); st != "called" {
		t.Fatalf("o chamado deveria continuar chamado: %s", st)
	}
}

// C-AG-07 — falta só com chamada válida e sem presença; prazo pela chamada efetiva; loja que não
// chama vira "atraso da loja", não falta; o worker é idempotente.
func TestAgendaFaltaAutomaticaEAtrasoDaLoja(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	ctx := context.Background()
	c1 := e.verifiedCustomer("c1@exemplo.com")
	a1 := c1.mustStatus(c1.book(s.id, s.beto, e.at("15:00"), s.corte25), 201).data()
	c2 := e.verifiedCustomer("c2@exemplo.com")
	a2 := c2.mustStatus(c2.book(s.id, s.caio, e.at("15:00"), s.corte25), 201).data()
	b := s.betoB
	// Loja chama tarde: 15h12 → prazo 15h22.
	e.advanceTo("15:12")
	a1 = b.mustStatus(b.apptCmd(s.id, a1, "call", nil), 200).data()
	if dl, _ := time.Parse(time.RFC3339, a1["deadline_at"].(string)); !dl.Equal(e.at("15:22")) {
		t.Fatalf("prazo pela chamada efetiva: %v", a1["deadline_at"])
	}
	e.advanceTo("15:21")
	if _, err := e.api.Agenda.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.partStatus(t, a1["participation_id"].(string)); st != "called" {
		t.Fatalf("antes do prazo não há falta: %s", st)
	}
	// Caio nunca chamou a2: tolerância passou → atraso da loja, não falta.
	cur2 := c2.mustStatus(c2.do("GET", estPath(s.id, "/appointments/"+a2["id"].(string)), nil), 200).data()
	if cur2["status"] != "confirmed" || cur2["store_late"] != true {
		t.Fatalf("atraso da loja: %v %v", cur2["status"], cur2["store_late"])
	}
	e.advanceTo("15:22") // no instante exato do prazo, já venceu
	for range 2 {
		if _, err := e.api.Agenda.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cur1 := c1.mustStatus(c1.do("GET", estPath(s.id, "/appointments/"+a1["id"].(string)), nil), 200).data()
	if cur1["status"] != "no_show" {
		t.Fatalf("falta automática: %v", cur1["status"])
	}
	if st, _ := e.partStatus(t, a1["participation_id"].(string)); st != "no_show" {
		t.Fatalf("visita deveria estar ausente: %s", st)
	}
	var events int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM appointment_event WHERE appointment_id = $1 AND to_status = 'no_show'`, a1["id"]).Scan(&events)
	if events != 1 {
		t.Fatalf("falta registrada %d vezes", events)
	}
}

// C-AG-08 — presença confirmada impede a falta automática.
func TestAgendaPresencaImpedeFalta(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	c := e.verifiedCustomer("c1@exemplo.com")
	a := c.mustStatus(c.book(s.id, s.beto, e.at("15:00"), s.corte25), 201).data()
	b := s.betoB
	e.advanceTo("14:50")
	a = c.mustStatus(c.apptCmd(s.id, a, "arrival-signal", nil), 200).data()
	if a["arrival_signal_at"] == nil || a["arrived_at"] != nil {
		t.Fatalf("\"Cheguei\" é só aviso: %v", a)
	}
	a = b.mustStatus(b.apptCmd(s.id, a, "arrival", nil), 200).data()
	a = b.mustStatus(b.apptCmd(s.id, a, "call", nil), 200).data()
	e.advanceTo("16:00")
	if _, err := e.api.Agenda.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.partStatus(t, a["participation_id"].(string)); st != "called" {
		t.Fatalf("cliente presente não perde a vez: %s", st)
	}
}

// C-AG-09 — remarcar e cancelar: destino disputado preserva a origem; cancelar libera o horário.
func TestAgendaRemarcarECancelar(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	c1 := e.verifiedCustomer("c1@exemplo.com")
	a1 := c1.mustStatus(c1.book(s.id, s.beto, e.at("10:00"), s.corte25), 201).data()
	c2 := e.verifiedCustomer("c2@exemplo.com")
	c2.mustStatus(c2.book(s.id, s.beto, e.at("11:00"), s.corte25), 201)
	r := c1.apptCmd(s.id, a1, "reschedule", map[string]any{"starts_at": e.at("11:00").UTC().Format(time.RFC3339)})
	if r.status != 409 || r.code() != "slot_unavailable" {
		t.Fatalf("remarcar para horário ocupado: %d %s", r.status, r.code())
	}
	cur := c1.mustStatus(c1.do("GET", estPath(s.id, "/appointments/"+a1["id"].(string)), nil), 200).data()
	if cur["local_time"] != "10:00" || cur["status"] != "confirmed" {
		t.Fatalf("origem deveria continuar: %v", cur)
	}
	moved := c1.mustStatus(c1.apptCmd(s.id, cur, "reschedule", map[string]any{"starts_at": e.at("12:00").UTC().Format(time.RFC3339),
		"professional_id": s.caio.String()}), 200).data()
	if moved["local_time"] != "12:00" || num(moved["revision"]) != 2 || moved["professional"].(map[string]any)["id"] != s.caio.String() {
		t.Fatalf("remarcação: %v", moved)
	}
	if !contains(c1.slots(s.id, s.beto, e.today(), s.corte25), "10:00") {
		t.Fatal("o horário antigo deveria voltar a ficar livre")
	}
	moved = c1.mustStatus(c1.apptCmd(s.id, moved, "cancel", nil), 200).data()
	if moved["status"] != "cancelled" || moved["cancelled_by"] != "customer" {
		t.Fatalf("cancelamento: %v", moved)
	}
	if !contains(c1.slots(s.id, s.caio, e.today(), s.corte25), "12:00") {
		t.Fatal("cancelar deveria liberar o horário")
	}
	// Equipe cancela só com motivo.
	a3 := c1.mustStatus(c1.book(s.id, s.beto, e.at("13:00"), s.corte25), 201).data()
	if r := s.betoB.apptCmd(s.id, a3, "cancel", nil); r.status != 400 {
		t.Fatalf("cancelamento da equipe sem motivo: %d", r.status)
	}
	a3 = s.betoB.mustStatus(s.betoB.apptCmd(s.id, a3, "cancel", map[string]any{"reason": "barbeiro doente"}), 200).data()
	if a3["cancelled_by"] != "store" {
		t.Fatalf("cancelado pela loja: %v", a3["cancelled_by"])
	}
}

// C-AG-10 — mudança de jornada ou bloqueio que invade reserva confirmada exige remanejar antes;
// turno do profissional fica dentro da loja; modo "os dois" recusa entrada sem vaga até o fechamento.
func TestAgendaMudancasEmProducao(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "hybrid", 0)
	c := e.verifiedCustomer("c1@exemplo.com")
	c.mustStatus(c.book(s.id, s.beto, e.at("16:00"), s.corte25), 201)
	own := s.owner
	short := []map[string]any{}
	for d := range 7 {
		short = append(short, map[string]any{"weekday": d, "start_minute": 9 * 60, "end_minute": 15 * 60, "modality": "both"})
	}
	if r := own.cmd("PUT", estPath(s.id, "/agenda/hours"), map[string]any{"effective_from": e.today(), "shifts": short}); r.status != 409 ||
		r.code() != "schedule_conflicts_appointments" {
		t.Fatalf("jornada que deixa reserva de fora: %d %s", r.status, r.code())
	}
	if r := own.cmd("POST", estPath(s.id, "/agenda/exceptions"), map[string]any{"professional_id": s.beto.String(), "date": e.today(),
		"start_minute": 15 * 60, "end_minute": 17 * 60, "reason": "consulta médica"}); r.status != 409 || r.code() != "exception_conflicts_appointments" {
		t.Fatalf("bloqueio sobre reserva: %d %s", r.status, r.code())
	}
	if r := own.cmd("PUT", estPath(s.id, "/agenda/hours"), map[string]any{"effective_from": e.today(), "professional_id": s.caio.String(),
		"shifts": []map[string]any{{"weekday": 4, "start_minute": 8 * 60, "end_minute": 12 * 60, "modality": "both"}}}); r.status != 400 {
		t.Fatalf("turno do profissional fora da loja: %d", r.status)
	}
	// Bloqueio de feriado (loja inteira) amanhã e profissional sem permissão de bloquear.
	tomorrow := e.clock.Now().In(spLoc).AddDate(0, 0, 1).Format("2006-01-02")
	own.mustStatus(own.cmd("POST", estPath(s.id, "/agenda/exceptions"), map[string]any{"date": tomorrow, "reason": "feriado"}), 201)
	if got := c.slots(s.id, s.beto, tomorrow, s.corte25); len(got) != 0 {
		t.Fatalf("feriado não deveria ter horários: %v", got)
	}
	if r := s.betoB.cmd("POST", estPath(s.id, "/agenda/exceptions"), map[string]any{"professional_id": s.beto.String(), "date": tomorrow,
		"reason": "folga"}); r.status != 403 {
		t.Fatalf("profissional sem permissão de bloquear: %d", r.status)
	}
	// Desativar Beto com reserva futura: recusado (remanejar antes), nada muda.
	own.reauth()
	if r := own.cmd("POST", estPath(s.id, "/professionals/"+s.beto.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "saiu da loja"}); r.status != 409 ||
		r.code() != "professional_has_appointments" {
		t.Fatalf("desativar com reserva futura: %d %s", r.status, r.code())
	}
	// Fim do dia: 17h45, corte de 25 não termina até as 18h → sem vaga na fila de Beto.
	e.advanceTo("17:45")
	c2 := e.verifiedCustomer("c2@exemplo.com")
	if r := c2.enter(s.id, s.filaBeto, s.corte25); r.status != 409 || r.code() != "queue_no_capacity_today" {
		t.Fatalf("entrada sem vaga até o fechamento: %d %s", r.status, r.code())
	}
}

// C-AG-11 — a restrição de exclusão do banco decide sozinha, mesmo sem a trava da aplicação: duas
// conexões reais gravam ocupações sobrepostas do mesmo profissional; só a primeira vale.
// Intervalo semiaberto: [10:00, 10:30) e [10:30, 11:00) coexistem; ocupação liberada não bloqueia.
func TestAgendaExclusaoNoBancoComDuasConexoes(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	ctx := context.Background()
	c := e.verifiedCustomer("cli@exemplo.com")
	a := c.mustStatus(c.book(s.id, s.beto, e.at("10:00"), s.corte25), 201).data()
	// Uma exceção real para servir de dona das ocupações "block" do teste.
	ex := s.owner.mustStatus(s.owner.cmd("POST", estPath(s.id, "/agenda/exceptions"), map[string]any{"professional_id": s.caio.String(),
		"date": e.today(), "start_minute": 17 * 60, "end_minute": 18 * 60, "reason": "teste"}), 201).data()
	insert := `INSERT INTO agenda_occupancy (establishment_id, professional_id, kind, exception_id, period)
		VALUES ($1, $2, 'block', $3, tstzrange($4, $5, '[)'))`
	c1, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Release()
	c2, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Release()
	// O índice único (exceção, profissional) é de outra regra: cada tentativa usa o profissional certo.
	tx1, _ := c1.Begin(ctx)
	tx2, _ := c2.Begin(ctx)
	if _, err := tx1.Exec(ctx, insert, s.id, s.beto, ex["id"], e.at("10:30"), e.at("11:00")); err != nil {
		t.Fatalf("semiaberto: [10:30, 11:00) deveria caber depois de [10:00, 10:25): %v", err)
	}
	done := make(chan error, 1)
	go func() { // a segunda conexão (move a reserva para cima do bloqueio) espera a primeira e perde
		_, err := tx2.Exec(ctx, `UPDATE agenda_occupancy SET period = tstzrange($2, $3, '[)') WHERE appointment_id = $1`,
			a["id"], e.at("10:40"), e.at("11:05"))
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-done
	_ = tx2.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "agenda_occupancy_no_overlap") {
		t.Fatalf("a segunda conexão deveria violar a exclusão: %v", err)
	}
	// Liberada, não bloqueia mais.
	if _, err := e.pool.Exec(ctx, `UPDATE agenda_occupancy SET released_at = now() WHERE exception_id = $1 AND professional_id = $2`, ex["id"], s.beto); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE agenda_occupancy SET period = tstzrange($2, $3, '[)') WHERE appointment_id = $1`,
		a["id"], e.at("10:40"), e.at("11:05")); err != nil {
		t.Fatalf("ocupação liberada não deveria bloquear: %v", err)
	}
}

// C-AG-14 — reserva de balcão: o dono marca para quem não tem o app; o cliente ganha a própria
// identidade recuperável (código mostrado uma única vez) e acompanha a reserva com ele.
func TestAgendaReservaDeBalcao(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	body := map[string]any{"professional_id": s.beto.String(), "starts_at": e.at("11:00").UTC().Format(time.RFC3339),
		"items": []map[string]any{{"service_id": s.corte25, "quantity": 2}}, "counter_customer": map[string]any{"nick": "Seu Jorge"}}
	r1 := s.owner.do("POST", estPath(s.id, "/appointments"), body, "Idempotency-Key", "chave-balcao-000001")
	a := s.owner.mustStatus(r1, 201).data()
	code, _ := a["counter_recovery_code"].(string)
	if code == "" || a["origin"] != "counter" || a["nick"] != "Seu Jorge" || num(a["service_minutes"]) != 50 {
		t.Fatalf("reserva de balcão: %v", a)
	}
	// Repetição: a mesma reserva, sem o código (ele aparece uma única vez).
	r2 := s.owner.do("POST", estPath(s.id, "/appointments"), body, "Idempotency-Key", "chave-balcao-000001")
	if r2.status != 201 || r2.data()["id"] != a["id"] || r2.data()["counter_recovery_code"] != nil {
		t.Fatalf("repetição do balcão: %d %v", r2.status, r2.data()["counter_recovery_code"])
	}
	// Cliente e profissional não fazem reserva de balcão.
	if r := s.betoB.cmd("POST", estPath(s.id, "/appointments"), body); r.status != 403 {
		t.Fatalf("profissional no balcão: %d", r.status)
	}
	// O cliente entra com o código em outro aparelho e vê a própria reserva.
	cli := e.browser()
	cli.mustStatus(cli.do("POST", estPath(s.id, "/visitor-sessions/recover"), map[string]any{"recovery_code": code}), 200)
	mine := cli.mustStatus(cli.do("GET", "/v1/my-appointments", nil), 200).list()
	if len(mine) != 1 || mine[0].(map[string]any)["appointment"].(map[string]any)["id"] != a["id"] {
		t.Fatalf("o cliente do balcão deveria ver a reserva: %v", mine)
	}
}
