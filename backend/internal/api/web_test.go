package api_test

import (
	"strings"
	"testing"
)

// Perfil: a própria conta muda o nome exibido; valida; repetição devolve o mesmo; sem sessão 401.
func TestUpdateMyProfile(t *testing.T) {
	e := newEnv(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	for name, body := range map[string]map[string]any{
		"vazio":       {"name": "   "},
		"longo":       {"name": strings.Repeat("a", 81)},
		"campo extra": {"name": "Maria", "email": "outra@exemplo.com"},
		"sem nome":    {},
	} {
		if r := maria.cmd("PATCH", "/v1/me", body); r.status != 400 {
			t.Fatalf("%s: %d %s", name, r.status, r.raw)
		}
	}
	key := newIdemKey()
	r := maria.mustStatus(maria.do("PATCH", "/v1/me", map[string]any{"name": "Maria Souza"}, "Idempotency-Key", key), 200)
	if r.data()["name"] != "Maria Souza" || r.data()["email"] != "maria@exemplo.com" {
		t.Fatalf("perfil: %s", r.raw)
	}
	if again := maria.do("PATCH", "/v1/me", map[string]any{"name": "Maria Souza"}, "Idempotency-Key", key); again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("repetição")
	}
	if s := maria.session(); s["account"].(map[string]any)["name"] != "Maria Souza" {
		t.Fatalf("sessão com nome antigo: %v", s["account"])
	}
	anon := e.browser()
	if r := anon.cmd("PATCH", "/v1/me", map[string]any{"name": "X"}); r.status != 401 {
		t.Fatalf("sem sessão: %d", r.status)
	}
}

// Auditoria: só o dono da loja; só eventos da loja; sem e-mail nem nome; paginada.
func TestListAuditEvents(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	outra := e.shop("outra-loja", "bia@exemplo.com")
	maria := e.verifiedCustomer("maria@exemplo.com")
	p, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p["id"].(string))
	path := estPath(b.id, "/audit-events")
	if r := b.betoB.do("GET", path, nil); r.status != 403 {
		t.Fatalf("profissional: %d", r.status)
	}
	if r := maria.do("GET", path, nil); r.status != 403 && r.status != 404 {
		t.Fatalf("cliente: %d", r.status)
	}
	if r := outra.owner.do("GET", path, nil); r.status != 404 && r.status != 403 {
		t.Fatalf("dono de outra loja: %d", r.status)
	}
	r := b.owner.mustStatus(b.owner.do("GET", path+"?limit=3", nil), 200)
	list := r.body["data"].([]any)
	if len(list) != 3 || r.body["page"].(map[string]any)["has_more"] != true {
		t.Fatalf("página: %s", r.raw)
	}
	raw := string(r.raw)
	if strings.Contains(raw, "maria@exemplo.com") || strings.Contains(raw, "Pessoa Teste") || strings.Contains(raw, "bia@") {
		t.Fatal("dado pessoal na auditoria")
	}
	next := r.body["page"].(map[string]any)["next_cursor"].(string)
	r2 := b.owner.mustStatus(b.owner.do("GET", path+"?limit=3&cursor="+next, nil), 200)
	first := r2.body["data"].([]any)[0].(map[string]any)
	if first["id"] == list[2].(map[string]any)["id"] {
		t.Fatal("cursor repetiu evento")
	}
	if r := b.owner.do("GET", path+"?cursor=lixo", nil); r.status != 400 {
		t.Fatalf("cursor inválido: %d", r.status)
	}
}
