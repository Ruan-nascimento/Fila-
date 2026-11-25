// Cenários da parte 3 da Fila 2.0 (escala e recuperação): ver docs/cenarios-aceite.md (C-AG-15…).
package api_test

import (
	"testing"
	"time"
)

// C-AG-15 — worker parado: ao voltar, chamada vencida há horas NÃO vira falta em massa nem dispara
// aviso velho de chamada; a vencida há pouco segue a regra normal. A retenção vencida é solta.
func TestAgendaWorkerParadoNaoGeraFaltaEmMassa(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "hybrid", 0)
	ana := e.verifiedCustomer("cli-ana@exemplo.com")
	bia := e.verifiedCustomer("bia@exemplo.com")
	a1 := ana.mustStatus(ana.book(s.id, s.beto, e.at("10:00"), s.corte25), 201).data()
	a2 := bia.mustStatus(bia.book(s.id, s.caio, e.at("12:00"), s.corte25), 201).data()
	e.advanceTo("09:55")
	s.betoB.mustStatus(s.betoB.apptCmd(s.id, s.betoB.appt(s.id, a1["id"]), "call", nil), 200) // prazo 10:10
	// Worker fora do ar das 09:55 às 12:20.
	e.advanceTo("11:55")
	s.owner.mustStatus(s.owner.apptCmd(s.id, s.owner.appt(s.id, a2["id"]), "call", nil), 200) // prazo 12:10
	e.advanceTo("12:15")
	e.agendaTick()
	if st := ana.appt(s.id, a1["id"])["status"]; st != "called" {
		t.Fatalf("chamada vencida há 2 h virou %v (deveria ficar para a equipe decidir)", st)
	}
	if st := bia.appt(s.id, a2["id"])["status"]; st != "no_show" {
		t.Fatalf("chamada vencida há 5 min deveria virar falta: %v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM appointment_notice WHERE appointment_id = $1 AND kind = 'called' AND status = 'skipped'
		AND skip_reason = 'vencido'`, a1["id"]); n != 1 {
		t.Fatalf("aviso de chamada de 2 h atrás deveria ser pulado como vencido: %d", n)
	}
	if e.inboxHas("cli-ana@exemplo.com", "Chegou a sua vez") {
		t.Fatal("aviso velho de chamada foi enviado")
	}
}

// C-AG-16 — calendário mensal: uma consulta agregada diz quais dias têm vaga (folga e dia lotado
// aparecem sem vaga), com o mesmo resultado da consulta detalhada de cada dia.
func TestAgendaDiasComVaga(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	amanha := e.clock.Now().In(spLoc).AddDate(0, 0, 1).Format("2006-01-02")
	s.owner.mustStatus(s.owner.cmd("POST", estPath(s.id, "/agenda/exceptions"), map[string]any{"professional_id": s.beto.String(),
		"date": amanha, "reason": "folga"}), 201)
	c := e.browser()
	fim := e.clock.Now().In(spLoc).AddDate(0, 0, 13).Format("2006-01-02")
	q := "/v1/public/establishments/" + s.id.String() + "/availability/days?from=" + e.today() + "&to=" + fim +
		"&professional_id=" + s.beto.String() + "&items=" + s.corte25
	d := c.mustStatus(c.do("GET", q, nil), 200).data()
	days := d["days"].([]any)
	if len(days) != 14 {
		t.Fatalf("dias: %d", len(days))
	}
	for _, x := range days {
		m := x.(map[string]any)
		detalhe := c.slots(s.id, s.beto, m["date"].(string), s.corte25)
		if (len(detalhe) > 0) != m["has_slots"].(bool) {
			t.Fatalf("%s: agregado %v, detalhado %d horários", m["date"], m["has_slots"], len(detalhe))
		}
		if m["date"] == amanha && m["has_slots"] == true {
			t.Fatal("dia de folga aparece com vaga")
		}
	}
	if r := c.do("GET", "/v1/public/establishments/"+s.id.String()+"/availability/days?from="+e.today()+"&to=2027-12-31&professional_id="+
		s.beto.String()+"&items="+s.corte25, nil); r.status != 400 {
		t.Fatalf("intervalo longo demais: %d", r.status)
	}
}

// C-AG-17 — invalidação pública: reservar e remarcar emitem `availability.updated` para o público só
// com profissional e data (dia antigo e novo na remarcação); nunca o apelido de quem reservou.
func TestAgendaInvalidacaoPublica(t *testing.T) {
	e := newEnv(t)
	s := e.agendaShop(t, "schedule", 0)
	c := e.verifiedCustomer("zelia@exemplo.com")
	a := c.mustStatus(c.book(s.id, s.beto, e.at("10:00"), s.corte25), 201).data()
	amanha := e.at("10:00").AddDate(0, 0, 1)
	c.mustStatus(c.apptCmd(s.id, a, "reschedule", map[string]any{"starts_at": amanha.UTC().Format(time.RFC3339)}), 200)
	n := e.count(t, `SELECT count(*) FROM outbox WHERE establishment_id = $1 AND type = 'availability.updated' AND audience = 'public'`, s.id)
	if n < 3 {
		t.Fatalf("eventos de disponibilidade: %d (esperado reserva + dia antigo + dia novo)", n)
	}
	if e.count(t, `SELECT count(*) FROM outbox WHERE type = 'availability.updated' AND payload::text ILIKE '%zelia%'`) != 0 {
		t.Fatal("evento público com dado de quem reservou")
	}
	if e.count(t, `SELECT count(DISTINCT payload->>'date') FROM outbox WHERE establishment_id = $1 AND type = 'availability.updated'`, s.id) < 2 {
		t.Fatal("remarcação deveria invalidar o dia antigo e o novo")
	}
}
