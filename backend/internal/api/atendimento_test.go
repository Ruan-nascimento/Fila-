package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/api"
	"fila/internal/contrato"
	"fila/internal/platform/database"
)

// ---------------------------------------------------------------------------------------------
// Ajudantes da etapa 05

// startServing chama o próximo da fila (deve ser part) e inicia o atendimento; devolve a visita.
func (b *browser) startServing(est, queue uuid.UUID, part string) resp {
	b.e.t.Helper()
	p, _ := visitOf(b.mustStatus(b.callNext(est, queue), 200))
	if p["id"] != part {
		b.e.t.Fatalf("chamou %v, esperado %s", p["id"], part)
	}
	return b.mustStatus(b.partCmd(est, part, "start", map[string]any{"expected_version": num(p["version"])}), 200)
}

// outcomes monta o resultado de todos os itens não retirados da comanda.
func outcomes(order map[string]any, outcome func(name string) string) []map[string]any {
	var out []map[string]any
	for _, it := range order["items"].([]any) {
		m := it.(map[string]any)
		if m["status"] == "removed" {
			continue
		}
		o := map[string]any{"item_id": m["id"], "outcome": outcome(m["name"].(string))}
		if o["outcome"] == "not_performed" {
			o["reason"] = "cliente desistiu deste serviço"
		}
		out = append(out, o)
	}
	return out
}

// closeVisit conclui (complete) ou interrompe (interrupt) a visita em atendimento.
func (b *browser) closeVisit(est uuid.UUID, visit resp, op string, outcome func(string) string, extra map[string]any) resp {
	b.e.t.Helper()
	p, o := b.currentVisit(est, visit)
	body := map[string]any{"expected_version": num(p["version"]), "order_expected_version": num(o["version"]), "items": outcomes(o, outcome)}
	for k, v := range extra {
		body[k] = v
	}
	return b.partCmd(est, p["id"].(string), op, body)
}

// currentVisit relê a participação e a comanda (versões atuais).
func (b *browser) currentVisit(est uuid.UUID, visit resp) (map[string]any, map[string]any) {
	b.e.t.Helper()
	p, _ := visitOf(visit)
	part := b.mustStatus(b.do("GET", estPath(est, "/participations/"+p["id"].(string)), nil), 200).data()
	order := b.mustStatus(b.do("GET", estPath(est, "/orders/"+part["order_id"].(string)), nil), 200).data()
	return part, order
}

// serve chama, inicia e conclui com todos os itens realizados.
func (b *browser) serve(est, queue uuid.UUID, part string) resp {
	b.e.t.Helper()
	v := b.startServing(est, queue, part)
	return b.mustStatus(b.closeVisit(est, v, "complete", func(string) string { return "performed" }, nil), 200)
}

func orderPath(est uuid.UUID, order any) string { return estPath(est, "/orders/"+order.(string)) }

func (b *browser) order(est uuid.UUID, order any) map[string]any {
	b.e.t.Helper()
	return b.mustStatus(b.do("GET", orderPath(est, order), nil), 200).data()
}

func itemByName(order map[string]any, name string) map[string]any {
	for _, it := range order["items"].([]any) {
		m := it.(map[string]any)
		if m["name"] == name && m["status"] != "removed" {
			return m
		}
	}
	return nil
}

func total(order map[string]any) int64 { return num(order["total"].(map[string]any)["amount_cents"]) }

func due(order map[string]any) int64 {
	return num(order["payment"].(map[string]any)["amounts"].(map[string]any)["due"].(map[string]any)["amount_cents"])
}

// explained confere que o total é a soma, item a item, dos itens cobrados (valor fechado) ou
// dos itens não retirados (comanda aberta).
func explained(t *testing.T, order map[string]any) {
	t.Helper()
	var sum int64
	frozen := order["total_frozen_at"] != nil
	for _, it := range order["items"].([]any) {
		m := it.(map[string]any)
		price := num(m["unit_price"].(map[string]any)["amount_cents"])
		if (frozen && m["charged"] == true) || (!frozen && m["status"] != "removed") {
			sum += price
		}
		if m["charged"] == true && m["status"] != "performed" {
			t.Errorf("item cobrado sem estar realizado: %v", m)
		}
	}
	if sum != total(order) {
		t.Fatalf("total %d não é a soma dos itens (%d): %v", total(order), sum, order["items"])
	}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---------------------------------------------------------------------------------------------

// C-CMD-03 — edição enquanto aguarda; preço adulterado; estimativa recalculada.
func TestEditItemsWhileWaiting(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	joao := e.visitorIn(b.id, "João")
	pj, _ := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	if num(pj["estimated_wait"].(map[string]any)["minutes"]) != 30 {
		t.Fatalf("espera de João antes: %v", pj["estimated_wait"])
	}
	items := orderPath(b.id, om["id"]) + "/items"
	// Preço, total ou item livre enviados pelo navegador são recusados (R-CMD-02).
	for _, extra := range []map[string]any{{"total": 1}, {"price_cents": 1}, {"items": []any{map[string]any{"name": "Corte", "unit_price": 1}}}} {
		body := map[string]any{"expected_version": 1, "service_ids": []string{b.corte}}
		for k, v := range extra {
			body[k] = v
		}
		if r := maria.cmd("PUT", items, body); r.status != 400 || r.code() != "validation_failed" {
			t.Fatalf("corpo adulterado %v: %d %s", extra, r.status, r.raw)
		}
	}
	if r := maria.cmd("PUT", items, map[string]any{"expected_version": 1, "service_ids": []string{}}); r.status != 400 {
		t.Fatalf("lista vazia: %d %s", r.status, r.raw)
	}
	if r := maria.cmd("PUT", items, map[string]any{"expected_version": 1, "service_ids": []string{b.corte, b.corte}}); r.status != 400 || !strings.Contains(string(r.raw), `"duplicate"`) {
		t.Fatalf("serviço repetido: %d %s", r.status, r.raw)
	}
	r := maria.mustStatus(maria.cmd("PUT", items, map[string]any{"expected_version": 1, "service_ids": []string{b.corte, b.barba}}), 200)
	var order struct{ Data contrato.Order }
	strictDecode(t, r.raw, &order)
	o := r.data()
	if total(o) != 7500 || num(o["version"]) != 2 || itemByName(o, "Corte")["id"] != itemByName(om, "Corte")["id"] {
		t.Fatalf("troca para Corte + Barba: %s", r.raw)
	}
	explained(t, o)
	if p, _ := maria.currentVisit(b.id, resp{body: map[string]any{"data": map[string]any{"participation": pm}}}); p["version"] != pm["version"] {
		t.Fatalf("editar a comanda não muda a participação: %v", p)
	}
	jv := joao.mustStatus(joao.do("GET", estPath(b.id, "/my-visit"), nil), 200).data()["participation"].(map[string]any)
	if num(jv["estimated_wait"].(map[string]any)["minutes"]) != 50 {
		t.Fatalf("estimativa de João não foi recalculada: %v", jv["estimated_wait"])
	}
	// Retirar a Barba devolve o total; o item retirado continua no histórico como "removed".
	r = maria.mustStatus(maria.cmd("PUT", items, map[string]any{"expected_version": 2, "service_ids": []string{b.corte}}), 200)
	if total(r.data()) != 4500 || len(r.data()["items"].([]any)) != 2 || itemByName(r.data(), "Barba") != nil {
		t.Fatalf("retirada: %s", r.raw)
	}
	// Equipe edita com motivo (T-O-03); sem motivo, 400.
	if r := b.betoB.cmd("PUT", items, map[string]any{"expected_version": 3, "service_ids": []string{b.corte, b.barba}}); r.status != 400 {
		t.Fatalf("equipe sem motivo: %d %s", r.status, r.raw)
	}
	// Chamada: o cliente não edita mais diretamente (409 order_not_editable).
	b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
	r = maria.cmd("PUT", items, map[string]any{"expected_version": 3, "service_ids": []string{b.corte, b.barba}})
	if r.status != 409 || r.code() != "order_not_editable" || r.body["current"] == nil {
		t.Fatalf("edição após a chamada: %d %s", r.status, r.raw)
	}
}

// C-CMD-06 — edição concorrente: uma vence, a outra recebe 409 com a comanda atual.
func TestConcurrentOrderEdit(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	_, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	items := orderPath(b.id, om["id"]) + "/items"
	pool := [][]string{{b.corte}, {b.barba}, {b.corte, b.barba}}
	for round := range 6 {
		cur := maria.order(b.id, om["id"])
		version := num(cur["version"])
		// Os dois pedem conjuntos diferentes do atual (senão um deles nada mudaria).
		var sets [][]string
		for _, set := range pool {
			same := len(set) == 0
			n := 0
			for _, it := range cur["items"].([]any) {
				if m := it.(map[string]any); m["status"] != "removed" {
					n++
				}
			}
			if n == len(set) {
				same = true
				for _, id := range set {
					found := false
					for _, it := range cur["items"].([]any) {
						if m := it.(map[string]any); m["status"] != "removed" && m["service_id"] == id {
							found = true
						}
					}
					same = same && found
				}
			}
			if !same {
				sets = append(sets, set)
			}
		}
		if round%2 == 1 {
			sets[0], sets[1] = sets[1], sets[0]
		}
		res := make([]resp, 2)
		var wg sync.WaitGroup
		wg.Go(func() {
			res[0] = maria.cmd("PUT", items, map[string]any{"expected_version": version, "service_ids": sets[0]})
		})
		wg.Go(func() {
			res[1] = b.owner.cmd("PUT", items, map[string]any{"expected_version": version, "service_ids": sets[1], "reason": "cliente pediu no balcão"})
		})
		wg.Wait()
		ok, conflicts := 0, 0
		for _, r := range res {
			switch {
			case r.status == 200:
				ok++
			case r.status == 409 && r.code() == "version_conflict" && num(r.body["current"].(map[string]any)["version"]) == version+1:
				conflicts++
			default:
				t.Fatalf("rodada %d: %d %s", round, r.status, r.raw)
			}
		}
		if ok != 1 || conflicts != 1 {
			t.Fatalf("rodada %d: %d vitórias e %d conflitos", round, ok, conflicts)
		}
		explained(t, maria.order(b.id, om["id"]))
	}
}

// C-CMD-02 — catálogo muda depois: itens aceitos mantêm nome e preço; inativo não entra.
func TestCatalogChangesDoNotRewriteOrder(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	_, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	b.owner.mustStatus(b.owner.cmd("PATCH", estPath(b.id, "/services/"+b.corte), map[string]any{"expected_version": 1, "price_cents": 5000, "name": "Corte novo"}), 200)
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/services/"+b.barba+"/deactivate"), map[string]any{"expected_version": 1}), 200)
	o := maria.order(b.id, om["id"])
	corte := itemByName(o, "Corte")
	if corte == nil || num(corte["unit_price"].(map[string]any)["amount_cents"]) != 4500 || total(o) != 4500 {
		t.Fatalf("item aceito mudou com o catálogo: %v", o["items"])
	}
	items := orderPath(b.id, om["id"]) + "/items"
	if r := maria.cmd("PUT", items, map[string]any{"expected_version": 1, "service_ids": []string{b.corte, b.barba}}); r.status != 409 || r.code() != "service_inactive" {
		t.Fatalf("incluir serviço inativo: %d %s", r.status, r.raw)
	}
	// Serviço de outra loja: 400 (não existe aqui).
	outra := e.shop("outra-loja", "bia@exemplo.com")
	alheio := outra.owner.createService(outra.id, map[string]any{"name": "Corte", "price_cents": 1, "duration_minutes": 1, "points": 0})["id"].(string)
	if r := maria.cmd("PUT", items, map[string]any{"expected_version": 1, "service_ids": []string{b.corte, alheio}}); r.status != 400 {
		t.Fatalf("serviço de outra loja: %d %s", r.status, r.raw)
	}
	// Reativada, a Barba entra pelo preço atual; o Corte aceito continua 4500.
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/services/"+b.barba+"/reactivate"), map[string]any{"expected_version": 2}), 200)
	b.owner.mustStatus(b.owner.cmd("PATCH", estPath(b.id, "/services/"+b.barba), map[string]any{"expected_version": 3, "price_cents": 3500}), 200)
	o = maria.mustStatus(maria.cmd("PUT", items, map[string]any{"expected_version": 1, "service_ids": []string{b.corte, b.barba}}), 200).data()
	if total(o) != 8000 || itemByName(o, "Corte")["name"] != "Corte" {
		t.Fatalf("snapshot: %v", o["items"])
	}
	// Uma alteração direta no banco que deixasse o total inexplicável é recusada no COMMIT.
	_, err := e.pool.Exec(context.Background(), `UPDATE service_order SET total_cents = total_cents + 1 WHERE id = $1`, om["id"])
	if err == nil || !strings.Contains(err.Error(), "23514") {
		t.Fatalf("total incoerente aceito pelo banco: %v", err)
	}
}

// C-CMD-04/05 — solicitação após a chamada; uma pendente; aprovação/recusa; item realizado.
func TestChangeRequests(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	op := orderPath(b.id, om["id"])
	if r := maria.cmd("POST", op+"/change-requests", map[string]any{"expected_version": 1, "add_service_ids": []string{b.barba}}); r.code() != "invalid_transition" {
		t.Fatalf("solicitação enquanto aguarda: %d %s", r.status, r.raw)
	}
	visit := b.betoB.startServing(b.id, b.filaBeto, pm["id"].(string))
	r := maria.mustStatus(maria.cmd("POST", op+"/change-requests", map[string]any{"expected_version": 1, "add_service_ids": []string{b.barba}}), 201)
	o := r.data()
	cr := o["pending_change_request"].(map[string]any)
	if total(o) != 4500 || cr["status"] != "pending" || num(o["version"]) != 2 ||
		num(cr["add"].([]any)[0].(map[string]any)["unit_price"].(map[string]any)["amount_cents"]) != 3000 {
		t.Fatalf("solicitação criada: %s", r.raw)
	}
	if r := maria.cmd("POST", op+"/change-requests", map[string]any{"expected_version": 2, "remove_item_ids": []any{itemByName(o, "Corte")["id"]}}); r.code() != "change_request_pending" {
		t.Fatalf("segunda solicitação: %d %s", r.status, r.raw)
	}
	// Com solicitação pendente: equipe não edita direto e não conclui (C-ATD-02).
	if r := b.betoB.cmd("PUT", op+"/items", map[string]any{"expected_version": 2, "service_ids": []string{b.corte}, "reason": "ajuste"}); r.code() != "change_request_pending" {
		t.Fatalf("edição com pendente: %d %s", r.status, r.raw)
	}
	if r := b.betoB.closeVisit(b.id, visit, "complete", func(string) string { return "performed" }, nil); r.status != 409 || r.code() != "change_request_pending" ||
		r.body["current"].(map[string]any)["participation"] == nil {
		t.Fatalf("concluir com pendente: %d %s", r.status, r.raw)
	}
	// A equipe vê o aviso na fila.
	if q := b.betoB.mustStatus(b.betoB.do("GET", estPath(b.id, "/queues"), nil), 200); !strings.Contains(string(q.raw), `"has_pending_change_request":true`) {
		t.Fatalf("fila sem aviso de solicitação: %s", q.raw)
	}
	crPath := op + "/change-requests/" + cr["id"].(string)
	if r := maria.cmd("POST", crPath+"/approve", map[string]any{"expected_version": 2}); r.status != 403 {
		t.Fatalf("cliente aprovando: %d %s", r.status, r.raw)
	}
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	if r := caio.cmd("POST", crPath+"/approve", map[string]any{"expected_version": 2}); r.status != 404 {
		t.Fatalf("outro profissional aprovando: %d %s", r.status, r.raw)
	}
	if r := b.betoB.cmd("POST", crPath+"/approve", map[string]any{"expected_version": 1}); r.code() != "version_conflict" {
		t.Fatalf("versão velha: %d %s", r.status, r.raw)
	}
	r = b.betoB.mustStatus(b.betoB.cmd("POST", crPath+"/approve", map[string]any{"expected_version": 2}), 200)
	o = r.data()
	crs := o["change_requests"].([]any)
	last := crs[len(crs)-1].(map[string]any)
	if total(o) != 7500 || o["pending_change_request"] != nil || last["status"] != "approved" || last["decided_by_role"] != "professional" || last["decided_at"] == nil {
		t.Fatalf("aprovação: %s", r.raw)
	}
	explained(t, o)
	var cnt int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE action = 'T-CR-02' AND target_id = $1 AND to_state = 'open@3'`, om["id"]).Scan(&cnt)
	if cnt != 1 {
		t.Fatal("auditoria da aprovação sem versão")
	}
	// Recusa exige motivo e deixa a comanda como estava.
	r = maria.mustStatus(maria.cmd("POST", op+"/change-requests", map[string]any{"expected_version": 3, "remove_item_ids": []any{itemByName(o, "Barba")["id"]}}), 201)
	crPath = op + "/change-requests/" + r.data()["pending_change_request"].(map[string]any)["id"].(string)
	if r := b.betoB.cmd("POST", crPath+"/reject", map[string]any{"expected_version": 4}); r.status != 400 {
		t.Fatalf("recusa sem motivo: %d %s", r.status, r.raw)
	}
	r = b.betoB.mustStatus(b.betoB.cmd("POST", crPath+"/reject", map[string]any{"expected_version": 4, "reason": "a barba já foi começada"}), 200)
	if total(r.data()) != 7500 || itemByName(r.data(), "Barba") == nil {
		t.Fatalf("recusa mudou a comanda: %s", r.raw)
	}
	if r := b.betoB.cmd("POST", crPath+"/reject", map[string]any{"expected_version": 5, "reason": "de novo"}); r.code() != "invalid_transition" {
		t.Fatalf("decidir duas vezes: %d %s", r.status, r.raw)
	}
	// C-CMD-05 — item marcado como realizado não sai pelo cliente.
	corteID := itemByName(r.data(), "Corte")["id"].(string)
	r = b.betoB.mustStatus(b.betoB.cmd("POST", op+"/items/"+corteID+"/mark-performed", map[string]any{"expected_version": 5}), 200)
	if itemByName(r.data(), "Corte")["status"] != "performed" {
		t.Fatalf("marcar realizado: %s", r.raw)
	}
	if r := maria.cmd("POST", op+"/change-requests", map[string]any{"expected_version": 6, "remove_item_ids": []string{corteID}}); r.code() != "item_already_performed" {
		t.Fatalf("remover item realizado: %d %s", r.status, r.raw)
	}
	if r := b.owner.cmd("PUT", op+"/items", map[string]any{"expected_version": 6, "service_ids": []string{b.barba}, "reason": "engano"}); r.code() != "item_already_performed" {
		t.Fatalf("equipe retirando item realizado: %d %s", r.status, r.raw)
	}
	maria.mustStatus(maria.cmd("POST", op+"/change-requests", map[string]any{"expected_version": 6, "remove_item_ids": []any{itemByName(r.data(), "Barba")["id"]}}), 201)
	// Eventos da solicitação foram para a outbox (visita, profissional e dona), na mesma transação.
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE type = 'change_request.updated' AND resource_id = $1`, strings.TrimPrefix(crPath, op+"/change-requests/")); n != 6 {
		t.Fatalf("eventos da solicitação recusada: %d", n)
	}
}

// C-ATD-01 — concluir libera a cadeira enquanto o pagamento fica pendente.
func TestCompleteFreesChair(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	joao := e.visitorIn(b.id, "João")
	pj, _ := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	visit := b.betoB.startServing(b.id, b.filaBeto, pm["id"].(string))
	// Validações: motivo obrigatório em "não realizado"; nenhum realizado = interrupção.
	p, o := b.betoB.currentVisit(b.id, visit)
	noReason := outcomes(o, func(string) string { return "performed" })
	noReason[0]["outcome"] = "not_performed"
	if r := b.betoB.partCmd(b.id, p["id"].(string), "complete", map[string]any{"expected_version": num(p["version"]), "order_expected_version": num(o["version"]), "items": noReason}); r.status != 400 {
		t.Fatalf("não realizado sem motivo: %d %s", r.status, r.raw)
	}
	if r := b.betoB.closeVisit(b.id, visit, "complete", func(string) string { return "not_performed" }, nil); r.code() != "performed_item_required" {
		t.Fatalf("nenhum realizado: %d %s", r.status, r.raw)
	}
	partial := outcomes(o, func(string) string { return "performed" })[:1]
	if r := b.betoB.partCmd(b.id, p["id"].(string), "complete", map[string]any{"expected_version": num(p["version"]), "order_expected_version": num(o["version"]), "items": partial}); r.status != 400 {
		t.Fatalf("faltou o resultado de um item: %d %s", r.status, r.raw)
	}
	if r := b.betoB.partCmd(b.id, p["id"].(string), "complete", map[string]any{"expected_version": num(p["version"]), "order_expected_version": num(o["version"]) + 1, "items": outcomes(o, func(string) string { return "performed" })}); r.code() != "version_conflict" {
		t.Fatalf("versão velha da comanda: %d %s", r.status, r.raw)
	}
	if r := maria.closeVisit(b.id, visit, "complete", func(string) string { return "performed" }, nil); r.status != 404 && r.status != 403 {
		t.Fatalf("cliente concluindo: %d %s", r.status, r.raw)
	}
	r := b.betoB.mustStatus(b.betoB.closeVisit(b.id, visit, "complete", func(string) string { return "performed" }, nil), 200)
	var v struct{ Data contrato.Visit }
	strictDecode(t, r.raw, &v)
	pc, oc := visitOf(r)
	if pc["status"] != "completed" || oc["status"] != "awaiting_payment" || total(oc) != 7500 || due(oc) != 7500 || oc["total_frozen_at"] == nil {
		t.Fatalf("conclusão: %s", r.raw)
	}
	explained(t, oc)
	// Cadeira livre já: Beto chama João sem esperar o pagamento de Maria.
	cj, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	if cj["id"] != pj["id"] {
		t.Fatalf("próximo chamado: %v", cj["id"])
	}
	// Maria ainda vê a comanda a pagar; não entra de novo enquanto ela estiver pendente.
	mv := maria.mustStatus(maria.do("GET", estPath(b.id, "/my-visit"), nil), 200).data()
	if mv["order"].(map[string]any)["status"] != "awaiting_payment" {
		t.Fatalf("visita atual depois de concluir: %v", mv)
	}
	if r := maria.enter(b.id, b.filaCaio, b.corte); r.code() != "pending_order_exists" {
		t.Fatalf("entrada com comanda a pagar: %d %s", r.status, r.raw)
	}
	// Valor fechado: ninguém edita direto.
	if r := b.owner.cmd("PUT", orderPath(b.id, om["id"])+"/items", map[string]any{"expected_version": num(oc["version"]), "service_ids": []string{b.corte}, "reason": "engano"}); r.code() != "order_not_editable" {
		t.Fatalf("edição depois de fechado: %d %s", r.status, r.raw)
	}
	// Conclusão com um item não realizado: só o realizado é cobrado (João pediu Barba na cadeira).
	vj := b.betoB.mustStatus(b.betoB.partCmd(b.id, pj["id"].(string), "start", map[string]any{"expected_version": num(cj["version"])}), 200)
	_, oj := visitOf(vj)
	b.betoB.mustStatus(b.betoB.cmd("PUT", orderPath(b.id, oj["id"])+"/items", map[string]any{"expected_version": num(oj["version"]),
		"service_ids": []string{b.corte, b.barba}, "reason": "cliente pediu na cadeira"}), 200)
	r = b.betoB.mustStatus(b.betoB.closeVisit(b.id, vj, "complete", func(n string) string {
		if n == "Barba" {
			return "not_performed"
		}
		return "performed"
	}, nil), 200)
	_, oj = visitOf(r)
	if total(oj) != 4500 || itemByName(oj, "Barba")["charged"] != false || itemByName(oj, "Barba")["outcome_reason"] == nil {
		t.Fatalf("conclusão parcial: %s", r.raw)
	}
	explained(t, oj)
}

// C-ATD-02/03 — interrupção com valor positivo; revisão só pelo dono; sem pontos.
// Cenários: C-ATD-03.
func TestInterruptionPositive(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	visit := b.betoB.startServing(b.id, b.filaBeto, pm["id"].(string))
	// Solicitação pendente é anulada pela interrupção (T-CR-04).
	maria.mustStatus(maria.cmd("POST", orderPath(b.id, om["id"])+"/change-requests", map[string]any{"expected_version": 1, "remove_item_ids": []any{itemByName(om, "Barba")["id"]}}), 201)
	if r := b.betoB.closeVisit(b.id, visit, "interrupt", func(string) string { return "fully_performed" }, nil); r.status != 400 {
		t.Fatalf("interromper sem motivo: %d %s", r.status, r.raw)
	}
	r := b.betoB.mustStatus(b.betoB.closeVisit(b.id, visit, "interrupt", func(n string) string {
		if n == "Corte" {
			return "fully_performed"
		}
		return "partially_performed"
	}, map[string]any{"reason": "cliente precisou sair"}), 200)
	pi, oi := visitOf(r)
	if pi["status"] != "interrupted" || pi["end_reason"] != "cliente precisou sair" || oi["status"] != "pending_review" || due(oi) != 0 ||
		oi["pending_change_request"] != nil || itemByName(oi, "Barba")["reported_outcome"] != "partially_performed" {
		t.Fatalf("interrupção: %s", r.raw)
	}
	if crs := oi["change_requests"].([]any); crs[0].(map[string]any)["status"] != "voided" {
		t.Fatalf("solicitação não anulada: %v", crs)
	}
	// Cadeira livre.
	joao := e.visitorIn(b.id, "João")
	joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201)
	b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)
	// Painel da dona: comandas em revisão.
	l := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/orders?status=pending_review"), nil), 200)
	if list := l.body["data"].([]any); len(list) != 1 || list[0].(map[string]any)["id"] != om["id"] {
		t.Fatalf("lista de revisão: %s", l.raw)
	}
	op := orderPath(b.id, om["id"])
	review := func(outcome func(string) string) []map[string]any {
		var out []map[string]any
		for _, it := range oi["items"].([]any) {
			m := it.(map[string]any)
			out = append(out, map[string]any{"item_id": m["id"], "outcome": outcome(m["name"].(string))})
		}
		return out
	}
	byName := func(n string) string {
		if n == "Corte" {
			return "fully_performed"
		}
		return "partially_performed"
	}
	if r := b.betoB.cmd("POST", op+"/close-interruption", map[string]any{"expected_version": num(oi["version"]), "review_note": "conferido", "items": review(byName)}); r.status != 403 {
		t.Fatalf("profissional revisando: %d %s", r.status, r.raw)
	}
	if r := b.owner.cmd("POST", op+"/close-interruption", map[string]any{"expected_version": num(oi["version"]), "items": review(byName)}); r.status != 400 {
		t.Fatalf("revisão sem nota: %d %s", r.status, r.raw)
	}
	r = b.owner.mustStatus(b.owner.cmd("POST", op+"/close-interruption", map[string]any{"expected_version": num(oi["version"]), "review_note": "Corte inteiro, barba pela metade", "items": review(byName)}), 200)
	o := r.data()
	if o["status"] != "awaiting_payment" || total(o) != 4500 || due(o) != 4500 || itemByName(o, "Barba") != nil && itemByName(o, "Barba")["charged"] == true ||
		o["review_note"] != "Corte inteiro, barba pela metade" || o["interrupted_at"] == nil {
		t.Fatalf("revisão positiva: %s", r.raw)
	}
	explained(t, o)
	if st, _ := e.partStatus(t, pm["id"].(string)); st != "interrupted" {
		t.Fatalf("participação deveria continuar interrompida: %s", st)
	}
	if r := b.owner.cmd("POST", op+"/close-interruption", map[string]any{"expected_version": num(o["version"]), "review_note": "de novo", "items": review(byName)}); r.code() != "invalid_transition" {
		t.Fatalf("revisar duas vezes: %d %s", r.status, r.raw)
	}
}

// C-ATD-04 — interrupção com valor zero: encerra sem cobrança e sem pagamento fictício.
func TestInterruptionZero(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	lia := e.visitorIn(b.id, "Lia")
	pl, ol := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	visit := b.betoB.startServing(b.id, b.filaBeto, pl["id"].(string))
	r := b.betoB.mustStatus(b.betoB.closeVisit(b.id, visit, "interrupt", func(string) string { return "not_started" }, map[string]any{"reason": "máquina quebrou"}), 200)
	_, oi := visitOf(r)
	var visitClosed *string
	_ = e.pool.QueryRow(context.Background(), `SELECT visit_closed_at::text FROM visitor WHERE id = (SELECT visitor_id FROM participation WHERE id = $1)`, pl["id"]).Scan(&visitClosed)
	if visitClosed != nil {
		t.Fatal("visita do visitante encerrada antes da revisão")
	}
	items := []map[string]any{{"item_id": itemByName(ol, "Corte")["id"], "outcome": "not_started"}}
	r = b.owner.mustStatus(b.owner.cmd("POST", orderPath(b.id, ol["id"])+"/close-interruption",
		map[string]any{"expected_version": num(oi["version"]), "review_note": "nada foi feito", "items": items}), 200)
	o := r.data()
	if o["status"] != "closed_no_charge" || o["closure_reason"] != "interruption_zero_total" || total(o) != 0 || due(o) != 0 || o["closed_at"] == nil {
		t.Fatalf("revisão zero: %s", r.raw)
	}
	if len(o["payment"].(map[string]any)["receipts"].([]any)) != 0 {
		t.Fatal("recebimento fictício")
	}
	explained(t, o)
	_ = e.pool.QueryRow(context.Background(), `SELECT visit_closed_at::text FROM visitor WHERE id = (SELECT visitor_id FROM participation WHERE id = $1)`, pl["id"]).Scan(&visitClosed)
	if visitClosed == nil {
		t.Fatal("visita do visitante deveria terminar (credencial vale mais 24 h)")
	}
	// Sem pendência: Lia pode entrar de novo.
	lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201)
}

type fakeCharges struct {
	mu    sync.Mutex
	state string
}

func (f *fakeCharges) set(s string) { f.mu.Lock(); f.state = s; f.mu.Unlock() }

func (f *fakeCharges) ChargeState(context.Context, database.Querier, uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

// C-ATD-05 — correção depois de fechado: só dono, reautenticado, por item, auditada e
// bloqueada com cobrança Pix ativa ou desconhecida (simulada; Pix chega na etapa 08).
func TestAdjustClosedOrder(t *testing.T) {
	charges := &fakeCharges{}
	e := newEnvDeps(t, func(d *api.Deps) { d.Charges = charges })
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	_, oc := visitOf(b.betoB.serve(b.id, b.filaBeto, pm["id"].(string)))
	op := orderPath(b.id, om["id"]) + "/adjustments"
	body := map[string]any{"expected_version": num(oc["version"]), "reason": "barba não foi feita", "uncharge_item_ids": []any{itemByName(oc, "Barba")["id"]}}
	if r := b.betoB.cmd("POST", op, body); r.status != 403 {
		t.Fatalf("profissional corrigindo: %d %s", r.status, r.raw)
	}
	if r := maria.cmd("POST", op, body); r.status != 403 && r.status != 404 {
		t.Fatalf("cliente corrigindo: %d %s", r.status, r.raw)
	}
	if r := b.owner.cmd("POST", op, map[string]any{"expected_version": num(oc["version"]), "reason": "valor livre", "total": 1}); r.status != 400 {
		t.Fatalf("valor livre: %d %s", r.status, r.raw)
	}
	e.clock.Advance(11 * time.Minute) // a confirmação de senha feita no preparo já venceu
	if r := b.owner.cmd("POST", op, body); r.status != 403 || r.code() != "reauthentication_required" {
		t.Fatalf("sem reautenticação: %d %s", r.status, r.raw)
	}
	b.owner.reauth()
	charges.set("active")
	if r := b.owner.cmd("POST", op, body); r.code() != "pix_charge_active" {
		t.Fatalf("Pix ativo: %d %s", r.status, r.raw)
	}
	charges.set("unknown")
	if r := b.owner.cmd("POST", op, body); r.code() != "pix_charge_outcome_unknown" {
		t.Fatalf("Pix desconhecido: %d %s", r.status, r.raw)
	}
	charges.set("")
	r := b.owner.mustStatus(b.owner.cmd("POST", op, body), 200)
	o := r.data()
	adj := o["adjustments"].([]any)
	if total(o) != 4500 || due(o) != 4500 || o["status"] != "awaiting_payment" || len(adj) != 1 ||
		num(adj[0].(map[string]any)["previous_total"].(map[string]any)["amount_cents"]) != 7500 || itemByName(o, "Barba")["charged"] != false {
		t.Fatalf("correção: %s", r.raw)
	}
	explained(t, o)
	var reason, to string
	_ = e.pool.QueryRow(context.Background(), `SELECT reason, to_state FROM audit_event WHERE action = 'T-O-11' AND target_id = $1`, om["id"]).Scan(&reason, &to)
	if reason != "barba não foi feita" || !strings.HasPrefix(to, "awaiting_payment@") {
		t.Fatalf("auditoria da correção: %q %q", reason, to)
	}
	if r := b.owner.cmd("POST", op, map[string]any{"expected_version": num(o["version"]), "reason": "repetido", "uncharge_item_ids": []any{itemByName(o, "Barba")["id"]}}); r.status != 400 {
		t.Fatalf("retirar cobrança de item não cobrado: %d %s", r.status, r.raw)
	}
	// Incluir serviço feito e não registrado: preço atual do catálogo vira snapshot.
	hidratacao := b.owner.createService(b.id, map[string]any{"name": "Hidratação", "price_cents": 2000, "duration_minutes": 15, "points": 0,
		"professional_ids": []string{b.beto.String()}})["id"].(string)
	o = b.owner.mustStatus(b.owner.cmd("POST", op, map[string]any{"expected_version": num(o["version"]), "reason": "fez hidratação e esqueceu", "add_performed_service_ids": []string{hidratacao}}), 200).data()
	if total(o) != 6500 || itemByName(o, "Hidratação")["charged"] != true {
		t.Fatalf("inclusão: %v", o)
	}
	explained(t, o)
	// Tudo retirado: encerra sem cobrança, com motivo.
	var ids []any
	for _, n := range []string{"Corte", "Hidratação"} {
		ids = append(ids, itemByName(o, n)["id"])
	}
	o = b.owner.mustStatus(b.owner.cmd("POST", op, map[string]any{"expected_version": num(o["version"]), "reason": "atendimento cortesia", "uncharge_item_ids": ids}), 200).data()
	if o["status"] != "closed_no_charge" || o["closure_reason"] != "adjustment_zero_total" || total(o) != 0 {
		t.Fatalf("correção para zero: %v", o)
	}
	if r := b.owner.cmd("POST", op, map[string]any{"expected_version": num(o["version"]), "reason": "depois de encerrar", "add_performed_service_ids": []string{b.corte}}); r.code() != "invalid_transition" {
		t.Fatalf("corrigir comanda encerrada: %d %s", r.status, r.raw)
	}
}

// Leitura e lista de comandas: quem vê o quê (R-PERM-*), filtro e paginação por cursor.
// Cenários: C-SEG-02, C-SEG-03, C-SEG-04.
func TestOrderAccessAndList(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	_, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	lia := e.visitorIn(b.id, "Lia")
	_, ol := visitOf(lia.mustStatus(lia.enter(b.id, b.filaCaio, b.corte), 201))
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	outro := e.verifiedCustomer("outro@exemplo.com")
	for _, c := range []struct {
		who  *browser
		want int
	}{{maria, 200}, {b.betoB, 200}, {b.owner, 200}, {outro, 404}, {caio, 404}, {lia, 404}, {e.browser(), 401}} {
		if r := c.who.do("GET", orderPath(b.id, om["id"]), nil); r.status != c.want {
			t.Errorf("GET comanda: %d, esperado %d", r.status, c.want)
		}
	}
	// Outra loja: a comanda não existe lá.
	outra := e.shop("outra-loja", "bia@exemplo.com")
	if r := outra.owner.do("GET", orderPath(outra.id, om["id"]), nil); r.status != 404 {
		t.Fatalf("dona de outra loja: %d", r.status)
	}
	if r := lia.cmd("PUT", orderPath(b.id, om["id"])+"/items", map[string]any{"expected_version": 1, "service_ids": []string{b.corte}}); r.status != 404 {
		t.Fatalf("visitante alterando comanda alheia: %d", r.status)
	}
	if r := caio.cmd("PUT", orderPath(b.id, om["id"])+"/items", map[string]any{"expected_version": 1, "service_ids": []string{b.corte}, "reason": "teste"}); r.status != 404 {
		t.Fatalf("Caio alterando comanda de Beto (C-CMD-07): %d", r.status)
	}
	// Equipe altera com motivo e a auditoria registra papel, transição, versão e motivo, sem nick.
	b.betoB.mustStatus(b.betoB.cmd("PUT", orderPath(b.id, om["id"])+"/items", map[string]any{"expected_version": 1, "service_ids": []string{b.corte, b.barba}, "reason": "cliente pediu na cadeira"}), 200)
	var kind, from, to, reason string
	_ = e.pool.QueryRow(context.Background(), `SELECT actor_kind, from_state, to_state, reason FROM audit_event WHERE action = 'T-O-03' AND target_id = $1`, om["id"]).Scan(&kind, &from, &to, &reason)
	if kind != "professional" || from != "open@1" || to != "open@2" || reason != "cliente pediu na cadeira" {
		t.Fatalf("auditoria T-O-03: %s %s %s %s", kind, from, to, reason)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_event WHERE reason LIKE '%Pessoa%' OR reason LIKE '%@%'`); n != 0 {
		t.Fatal("auditoria com dado pessoal")
	}
	// Listas.
	list := func(who *browser, query string) resp {
		return who.mustStatus(who.do("GET", estPath(b.id, "/orders"+query), nil), 200)
	}
	all := list(b.owner, "")
	var page struct {
		Data []contrato.Order
		Page contrato.PageInfo
	}
	strictDecode(t, all.raw, &page)
	if len(page.Data) != 2 || page.Page.HasMore {
		t.Fatalf("lista da dona: %s", all.raw)
	}
	if l := list(b.betoB, "").body["data"].([]any); len(l) != 1 || l[0].(map[string]any)["id"] != om["id"] {
		t.Fatalf("lista de Beto: %v", l)
	}
	if l := list(caio, "").body["data"].([]any); len(l) != 1 || l[0].(map[string]any)["id"] != ol["id"] {
		t.Fatalf("lista de Caio: %v", l)
	}
	first := list(b.owner, "?limit=1")
	next := first.body["page"].(map[string]any)["next_cursor"].(string)
	second := list(b.owner, "?limit=1&cursor="+next)
	if first.body["data"].([]any)[0].(map[string]any)["id"] == second.body["data"].([]any)[0].(map[string]any)["id"] || second.body["page"].(map[string]any)["has_more"] != false {
		t.Fatalf("paginação: %s | %s", first.raw, second.raw)
	}
	if l := list(b.owner, "?status=awaiting_payment,closed_no_charge").body["data"].([]any); len(l) != 0 {
		t.Fatalf("filtro de estado: %v", l)
	}
	for _, q := range []string{"?status=pago", "?limit=0", "?limit=101", "?cursor=@@"} {
		if r := b.owner.do("GET", estPath(b.id, "/orders"+q), nil); r.status != 400 {
			t.Errorf("consulta %s: %d", q, r.status)
		}
	}
	if r := maria.do("GET", estPath(b.id, "/orders"), nil); r.status != 404 && r.status != 403 {
		t.Fatalf("cliente listando comandas: %d", r.status)
	}
}

// Outbox: eventos gravados na mesma transação, um por audiência, com a visão projetada; um
// comando que falha não deixa evento (C-FILA-10: desistência anula a solicitação pendente).
func TestOrderEventsInOutbox(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	type ev struct {
		audience, typ string
		prof          *uuid.UUID
		account       *uuid.UUID
		version       int64
		payload       []byte
	}
	read := func(resource any) []ev {
		rows, err := e.pool.Query(context.Background(), `
			SELECT audience, type, professional_id, account_id, resource_version, payload FROM outbox WHERE resource_id = $1 ORDER BY id`, resource)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []ev
		for rows.Next() {
			var x ev
			if err := rows.Scan(&x.audience, &x.typ, &x.prof, &x.account, &x.version, &x.payload); err != nil {
				t.Fatal(err)
			}
			out = append(out, x)
		}
		return out
	}
	// Entrada: comanda (3 audiências) e fila (pública + equipe).
	if evs := read(om["id"]); len(evs) != 3 || evs[0].audience != "visit" || evs[0].account == nil || evs[1].prof == nil || *evs[1].prof != b.beto || evs[2].audience != "owner" {
		t.Fatalf("eventos da comanda criada: %+v", evs)
	}
	queueEvs := read(b.filaBeto)
	if len(queueEvs) != 6 { // abrir a fila (preparo) + entrada, 3 audiências cada
		t.Fatalf("eventos da fila: %d", len(queueEvs))
	}
	for _, x := range queueEvs[3:] {
		if x.audience == "public" && (strings.Contains(string(x.payload), "Pessoa") || strings.Contains(string(x.payload), "Corte") || x.typ != "queue.public.updated") {
			t.Fatalf("evento público com dado privado: %s", x.payload)
		}
		if x.audience != "public" && !strings.Contains(string(x.payload), "Pessoa") {
			t.Fatalf("evento da equipe sem nick: %s", x.payload)
		}
	}
	before := e.count(t, `SELECT count(*) FROM outbox`)
	// Comando recusado (versão velha) não grava evento.
	if r := maria.cmd("PUT", orderPath(b.id, om["id"])+"/items", map[string]any{"expected_version": 9, "service_ids": []string{b.barba}}); r.code() != "version_conflict" {
		t.Fatalf("versão velha: %d %s", r.status, r.raw)
	}
	if after := e.count(t, `SELECT count(*) FROM outbox`); after != before {
		t.Fatalf("comando recusado gravou %d eventos", after-before)
	}
	// Edição: comanda versão 2 + fila com versão maior que a anterior.
	maria.mustStatus(maria.cmd("PUT", orderPath(b.id, om["id"])+"/items", map[string]any{"expected_version": 1, "service_ids": []string{b.corte, b.barba}}), 200)
	evs := read(om["id"])
	var data struct{ Version int64 }
	_ = json.Unmarshal(evs[len(evs)-1].payload, &data)
	if evs[len(evs)-1].version != 2 || data.Version != 2 {
		t.Fatalf("versão do evento da comanda: %+v", evs[len(evs)-1])
	}
	if q := read(b.filaBeto); q[len(q)-1].version <= queueEvs[len(queueEvs)-1].version {
		t.Fatal("versão do evento de fila não cresceu")
	}
	// Chamada + solicitação + desistência: solicitação anulada, comanda encerrada, eventos gravados.
	called, _ := visitOf(b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200))
	cr := maria.mustStatus(maria.cmd("POST", orderPath(b.id, om["id"])+"/change-requests", map[string]any{"expected_version": 2, "add_service_ids": []string{}, "remove_item_ids": []any{itemByName(maria.order(b.id, om["id"]), "Barba")["id"]}}), 201).
		data()["pending_change_request"].(map[string]any)
	r := maria.mustStatus(maria.partCmd(b.id, pm["id"].(string), "cancel", map[string]any{"expected_version": num(called["version"])}), 200)
	_, oc := visitOf(r)
	if oc["status"] != "closed_no_charge" || oc["change_requests"].([]any)[0].(map[string]any)["status"] != "voided" || total(oc) != 0 || oc["total_frozen_at"] == nil {
		t.Fatalf("desistência com solicitação: %s", r.raw)
	}
	crEvs := read(cr["id"])
	if len(crEvs) != 6 || !strings.Contains(string(crEvs[5].payload), `"voided"`) {
		t.Fatalf("eventos da solicitação anulada: %d", len(crEvs))
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE type = 'participation.updated' AND resource_id = $1 AND audience = 'visit' AND payload ? 'identity_kind'`, pm["id"]); n != 0 {
		t.Fatal("evento da visita com campo só da equipe")
	}
}
