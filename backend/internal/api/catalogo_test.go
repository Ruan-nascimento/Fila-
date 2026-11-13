package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"fila/internal/contrato"
)

func (b *browser) createService(est uuid.UUID, body map[string]any) map[string]any {
	b.e.t.Helper()
	return b.mustStatus(b.cmd("POST", estPath(est, "/services"), body), 201).data()
}

// C-EST-02 — catálogo valida preço, duração e pontos; o público não vê dados internos.
func TestServiceValidationAndPublicCatalog(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	caio := e.professional(loja.id, "Caio")
	valid := func() map[string]any {
		return map[string]any{"name": "Corte", "price_cents": 4500, "duration_minutes": 30, "points": 10}
	}
	bad := []struct {
		field string
		raw   string
	}{
		{"/price_cents", `{"name":"Corte","price_cents":0,"duration_minutes":30,"points":10}`},
		{"/price_cents", `{"name":"Corte","price_cents":45.5,"duration_minutes":30,"points":10}`},
		{"/price_cents", `{"name":"Corte","price_cents":"4500","duration_minutes":30,"points":10}`},
		{"/price_cents", `{"name":"Corte","price_cents":10000001,"duration_minutes":30,"points":10}`},
		{"/duration_minutes", `{"name":"Corte","price_cents":4500,"duration_minutes":0,"points":10}`},
		{"/points", `{"name":"Corte","price_cents":4500,"duration_minutes":30,"points":-1}`},
		{"/discount", `{"name":"Corte","price_cents":4500,"duration_minutes":30,"points":10,"discount":500}`},
		{"/name", `{"name":"","price_cents":4500,"duration_minutes":30,"points":10}`},
		{"/points", `{"name":"Corte","price_cents":4500,"duration_minutes":30}`},
	}
	for _, c := range bad {
		r := loja.owner.do("POST", estPath(loja.id, "/services"), c.raw, "Idempotency-Key", newIdemKey())
		if r.status != 400 || r.code() != "validation_failed" || !strings.Contains(string(r.raw), `"`+c.field+`"`) {
			t.Errorf("%s: %d %s", c.raw, r.status, r.raw)
		}
	}
	body := valid()
	body["professional_ids"] = []string{beto.String(), caio.String()}
	created := loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/services"), body), 201)
	var svc struct{ Data contrato.Service }
	strictDecode(t, created.raw, &svc)
	if svc.Data.Price.AmountCents != 4500 || svc.Data.Price.Currency != "BRL" || svc.Data.Points != 10 || svc.Data.Status != "active" || len(svc.Data.ProfessionalIds) != 2 {
		t.Fatalf("serviço: %+v", svc.Data)
	}
	// Caio desativado some das habilitações públicas (mas não da equipe).
	loja.owner.reauth()
	loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/professionals/"+caio.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "férias longas"}), 200)

	pub := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments/"+loja.id.String()+"/services", nil), 200)
	var public struct{ Data []contrato.PublicService }
	strictDecode(t, pub.raw, &public)
	if len(public.Data) != 1 || public.Data[0].Price.AmountCents != 4500 || public.Data[0].DurationMinutes != 30 ||
		len(public.Data[0].ProfessionalIds) != 1 || public.Data[0].ProfessionalIds[0].String() != beto.String() {
		t.Fatalf("catálogo público: %+v", public.Data)
	}
	for _, leak := range []string{"points", "version", "status", "establishment"} {
		if strings.Contains(string(pub.raw), leak) {
			t.Errorf("catálogo público expõe %q: %s", leak, pub.raw)
		}
	}
	staff := loja.owner.mustStatus(loja.owner.do("GET", estPath(loja.id, "/services"), nil), 200)
	if !strings.Contains(string(staff.raw), caio.String()) {
		t.Fatal("a equipe continua vendo a habilitação do desativado")
	}
}

// C-EST-06 — duas abas alteram o preço ao mesmo tempo: uma vence, a outra recebe o atual.
func TestConcurrentServiceUpdate(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	svc := loja.owner.createService(loja.id, map[string]any{"name": "Corte", "price_cents": 4500, "duration_minutes": 30, "points": 10})
	id := svc["id"].(string)
	var wg sync.WaitGroup
	type out struct {
		status int
		code   string
		price  float64
	}
	res := make([]out, 2)
	for i, price := range []int{5000, 5500} {
		wg.Go(func() {
			r := loja.owner.cmd("PATCH", estPath(loja.id, "/services/"+id), map[string]any{"expected_version": 1, "price_cents": price})
			o := out{status: r.status, code: r.code()}
			if r.status == 409 {
				o.price = r.body["current"].(map[string]any)["price"].(map[string]any)["amount_cents"].(float64)
			}
			res[i] = o
		})
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, o := range res {
		switch {
		case o.status == 200:
			wins++
		case o.status == 409 && o.code == "version_conflict" && (o.price == 5000 || o.price == 5500):
			conflicts++
		default:
			t.Fatalf("resultado inesperado: %+v", res)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("esperava 1 vitória e 1 conflito: %+v", res)
	}
	// Histórico do catálogo: versão 1 (4500) preservada e versão 2 com o novo preço.
	rows, err := e.pool.Query(context.Background(), `SELECT version, price_cents FROM service_revision WHERE service_id = $1 ORDER BY version`, id)
	if err != nil {
		t.Fatal(err)
	}
	var got [][2]int64
	for rows.Next() {
		var v, p int64
		_ = rows.Scan(&v, &p)
		got = append(got, [2]int64{v, p})
	}
	rows.Close()
	if len(got) != 2 || got[0] != [2]int64{1, 4500} || got[1][0] != 2 {
		t.Fatalf("revisões: %v", got)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE service_revision SET price_cents = 1 WHERE service_id = $1`, id); err == nil {
		t.Fatal("revisões devem ser imutáveis")
	}
}

// Serviço desativado: some do catálogo público, continua para a equipe e no histórico.
func TestDeactivatedServiceIsPreserved(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	svc := loja.owner.createService(loja.id, map[string]any{"name": "Barba", "price_cents": 3000, "duration_minutes": 20, "points": 5})
	id := svc["id"].(string)
	r := loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/services/"+id+"/deactivate"), map[string]any{"expected_version": 1}), 200)
	if r.data()["status"] != "inactive" || num(r.data()["version"]) != 2 {
		t.Fatalf("desativar: %s", r.raw)
	}
	if r := loja.owner.cmd("POST", estPath(loja.id, "/services/"+id+"/deactivate"), map[string]any{"expected_version": 2}); r.code() != "invalid_transition" {
		t.Fatalf("desativar duas vezes: %s", r.raw)
	}
	pub := e.browser().do("GET", "/v1/public/establishments/"+loja.id.String()+"/services", nil)
	if strings.Contains(string(pub.raw), id) {
		t.Fatal("serviço inativo no catálogo público")
	}
	if staff := loja.owner.do("GET", estPath(loja.id, "/services"), nil); !strings.Contains(string(staff.raw), id) {
		t.Fatal("a equipe deve continuar vendo o serviço inativo")
	}
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM service WHERE id = $1`, id); err == nil {
		t.Fatal("o banco deveria recusar apagar serviço")
	}
	loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/services/"+id+"/reactivate"), map[string]any{"expected_version": 2}), 200)
	pub = e.browser().do("GET", "/v1/public/establishments/"+loja.id.String()+"/services", nil)
	if !strings.Contains(string(pub.raw), id) {
		t.Fatal("serviço reativado deveria voltar ao catálogo")
	}
}

// Profissional incompatível (de outra loja) e consultas cruzadas no catálogo.
func TestCatalogCrossEstablishment(t *testing.T) {
	e := newEnv(t)
	lojaA := e.shop("loja-a", "ana@exemplo.com")
	lojaB := e.shop("loja-b", "bia@exemplo.com")
	betoA := e.professional(lojaA.id, "Beto")
	caioB := e.professional(lojaB.id, "Caio")
	r := lojaA.owner.cmd("POST", estPath(lojaA.id, "/services"),
		map[string]any{"name": "Corte", "price_cents": 4500, "duration_minutes": 30, "points": 0, "professional_ids": []string{caioB.String()}})
	if r.status != 400 || !strings.Contains(string(r.raw), `"/professional_ids/0"`) {
		t.Fatalf("profissional de outra loja na criação: %d %s", r.status, r.raw)
	}
	svcA := lojaA.owner.createService(lojaA.id, map[string]any{"name": "Corte", "price_cents": 4500, "duration_minutes": 30, "points": 0})
	idA := svcA["id"].(string)
	if r := lojaA.owner.cmd("PUT", estPath(lojaA.id, "/services/"+idA+"/professionals"),
		map[string]any{"expected_version": 1, "professional_ids": []string{betoA.String(), caioB.String()}}); r.status != 404 {
		t.Fatalf("habilitar profissional de outra loja: %d %s", r.status, r.raw)
	}
	if r := lojaA.owner.cmd("PUT", estPath(lojaA.id, "/services/"+idA+"/professionals"),
		map[string]any{"expected_version": 1, "professional_ids": []string{betoA.String(), betoA.String()}}); r.status != 400 {
		t.Fatalf("ids repetidos: %d", r.status)
	}
	ok := lojaA.owner.mustStatus(lojaA.owner.cmd("PUT", estPath(lojaA.id, "/services/"+idA+"/professionals"),
		map[string]any{"expected_version": 1, "professional_ids": []string{betoA.String()}}), 200)
	if num(ok.data()["version"]) != 2 {
		t.Fatalf("habilitações: %s", ok.raw)
	}
	// A dona da loja B não vê nem altera o serviço da loja A, nem pela rota da própria loja.
	if r := lojaB.owner.cmd("PATCH", estPath(lojaB.id, "/services/"+idA), map[string]any{"expected_version": 2, "price_cents": 1}); r.status != 404 {
		t.Fatalf("alterar serviço de outra loja: %d", r.status)
	}
	if r := lojaB.owner.do("GET", estPath(lojaA.id, "/services"), nil); r.status != 404 {
		t.Fatalf("listar serviços de outra loja: %d", r.status)
	}
	if list := lojaB.owner.do("GET", estPath(lojaB.id, "/services"), nil); strings.Contains(string(list.raw), idA) {
		t.Fatal("catálogo da loja B mostra serviço da loja A")
	}
	if r := e.browser().do("GET", "/v1/public/establishments/"+uuid.NewString()+"/services", nil); r.status != 404 {
		t.Fatalf("catálogo de loja inexistente: %d", r.status)
	}
}

// Papel e preço nunca vêm de quem não é dono; profissional só lê o catálogo.
func TestCatalogAuthorization(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	betoB := e.staffMember(loja, beto, "beto@exemplo.com")
	svc := loja.owner.createService(loja.id, map[string]any{"name": "Corte", "price_cents": 4500, "duration_minutes": 30, "points": 0})
	id := svc["id"].(string)
	betoB.mustStatus(betoB.do("GET", estPath(loja.id, "/services"), nil), 200)
	if r := betoB.cmd("POST", estPath(loja.id, "/services"), map[string]any{"name": "X", "price_cents": 1, "duration_minutes": 1, "points": 0}); r.status != 403 {
		t.Fatalf("profissional criando serviço: %d", r.status)
	}
	if r := betoB.cmd("PATCH", estPath(loja.id, "/services/"+id), map[string]any{"expected_version": 1, "price_cents": 1}); r.status != 403 {
		t.Fatalf("profissional mudando preço: %d", r.status)
	}
	cliente := e.verifiedCustomer("cliente@exemplo.com")
	if r := cliente.do("GET", estPath(loja.id, "/services"), nil); r.status != 404 {
		t.Fatalf("cliente lendo catálogo interno: %d", r.status)
	}
	if r := e.browser().do("GET", estPath(loja.id, "/services"), nil); r.status != 401 {
		t.Fatalf("sem sessão: %d", r.status)
	}
	// O formato de preço do contrato para escrita é só price_cents; "price" (objeto) é recusado.
	if r := loja.owner.cmd("PATCH", estPath(loja.id, "/services/"+id), map[string]any{"expected_version": 1, "price": map[string]any{"amount_cents": 1, "currency": "BRL"}}); r.status != 400 {
		t.Fatalf("campo price no corpo: %d", r.status)
	}
	var price int64
	_ = e.pool.QueryRow(context.Background(), `SELECT price_cents FROM service WHERE id = $1`, id).Scan(&price)
	if price != 4500 {
		t.Fatalf("preço alterado indevidamente: %d", price)
	}
}
