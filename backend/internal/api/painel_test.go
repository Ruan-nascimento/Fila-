package api_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"fila/internal/contrato"
)

// C-CLI-01 — diretório de barbearias: distância, abertas primeiro, busca sem acento, paginação.
func TestPublicDirectory(t *testing.T) {
	e := newEnv(t)
	// Centro de SP (Sé), Paulista (~2,5 km), Campinas (~85 km) e uma sem localização.
	type loja struct {
		slug, nome, end string
		lat, lng        float64
		local, aberta   bool
	}
	lojas := []loja{
		{"campinas", "Barbearia Campinas", "Av. Norte Sul, Campinas", -22.9056, -47.0608, true, true},
		{"paulista", "Salão Paulista", "Av. Paulista, 1000", -23.5614, -46.6559, true, false},
		{"centro", "Barbearia do Zé", "Praça da Sé", -23.5505, -46.6333, true, true},
		{"sem-local", "Corte Álvaro", "Rua sem mapa", 0, 0, false, false},
	}
	ids := map[string]uuid.UUID{}
	owners := map[string]*browser{}
	for i, l := range lojas {
		s := e.shop(l.slug, l.slug+"@exemplo.com")
		ids[l.slug], owners[l.slug] = s.id, s.owner
		body := map[string]any{"expected_version": 1, "name": l.nome, "public_address": l.end}
		if l.local {
			body["location"] = map[string]any{"latitude": l.lat, "longitude": l.lng}
		}
		r := s.owner.mustStatus(s.owner.cmd("PATCH", estPath(s.id, ""), body), 200)
		if l.local && r.data()["location"] == nil {
			t.Fatalf("loja %d sem localização na resposta: %s", i, r.raw)
		}
		if l.aberta {
			pro := e.professional(s.id, "Beto")
			s.owner.mustStatus(s.owner.cmd("POST", estPath(s.id, "/queues/"+e.queueOf(pro).String()+"/open"), map[string]any{"expected_version": 1}), 200)
		}
	}
	list := func(q url.Values) ([]contrato.DirectoryEstablishment, *string, resp) {
		t.Helper()
		r := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments?"+q.Encode(), nil), 200)
		var out struct {
			Data []contrato.DirectoryEstablishment
			Page contrato.PageInfo
		}
		strictDecode(t, r.raw, &out)
		return out.Data, out.Page.NextCursor, r
	}
	slugs := func(l []contrato.DirectoryEstablishment) string {
		var s []string
		for _, x := range l {
			s = append(s, x.Slug)
		}
		return strings.Join(s, ",")
	}

	// Perto da Sé: centro, paulista, campinas; a sem localização no fim.
	got, _, _ := list(url.Values{"lat": {"-23.5505"}, "lng": {"-46.6333"}})
	if slugs(got) != "centro,paulista,campinas,sem-local" {
		t.Fatalf("ordem por distância: %s", slugs(got))
	}
	if d := got[1].DistanceMeters; d == nil || *d < 2000 || *d > 3000 {
		t.Fatalf("distância Sé–Paulista: %v", d)
	}
	if got[3].DistanceMeters != nil || got[0].OpenQueues != 1 || got[0].ShortestWaitMinutes == nil {
		t.Fatalf("resumo: %+v", got)
	}
	// Sem GPS: abertas primeiro, depois nome.
	got, _, _ = list(url.Values{})
	if slugs(got) != "campinas,centro,sem-local,paulista" {
		t.Fatalf("abertas primeiro: %s", slugs(got))
	}
	got, _, _ = list(url.Values{"sort": {"name"}})
	// Barbearia Campinas, Barbearia do Zé, Corte Álvaro, Salão Paulista.
	if slugs(got) != "campinas,centro,sem-local,paulista" {
		t.Fatalf("A–Z: %s", slugs(got))
	}
	// Busca sem acento e sem maiúsculas, no nome e no endereço; curinga do LIKE é literal.
	if got, _, _ = list(url.Values{"q": {"alvaro"}}); slugs(got) != "sem-local" {
		t.Fatalf("busca sem acento: %s", slugs(got))
	}
	if got, _, _ = list(url.Values{"q": {"PAULISTA"}}); slugs(got) != "paulista" {
		t.Fatalf("busca no endereço: %s", slugs(got))
	}
	if got, _, _ = list(url.Values{"q": {"%"}}); len(got) != 0 {
		t.Fatalf("curinga deveria ser literal: %s", slugs(got))
	}
	// Paginação: 2 + 2, sem repetir; cursor de outra busca é recusado.
	p1, next, _ := list(url.Values{"limit": {"2"}})
	if len(p1) != 2 || next == nil {
		t.Fatalf("primeira página: %d %v", len(p1), next)
	}
	p2, next2, _ := list(url.Values{"limit": {"2"}, "cursor": {*next}})
	if len(p2) != 2 || next2 != nil || slugs(append(p1, p2...)) != "campinas,centro,sem-local,paulista" {
		t.Fatalf("segunda página: %s %v", slugs(p2), next2)
	}
	for name, q := range map[string]string{
		"cursor de outra busca": "limit=2&q=x&cursor=" + url.QueryEscape(*next),
		"lat sem lng":           "lat=1",
		"distância sem posição": "sort=distance",
		"latitude fora":         "lat=91&lng=0",
		"limite":                "limit=0",
	} {
		if r := e.browser().do("GET", "/v1/public/establishments?"+q, nil); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
	}
	// Remover a localização (null): a loja vai para o fim da ordem por distância.
	o := owners["centro"]
	o.mustStatus(o.cmd("PATCH", estPath(ids["centro"], ""), map[string]any{"expected_version": 2, "location": nil}), 200)
	got, _, _ = list(url.Values{"lat": {"-23.5505"}, "lng": {"-46.6333"}})
	if slugs(got) != "paulista,campinas,centro,sem-local" {
		t.Fatalf("sem localização vai para o fim: %s", slugs(got))
	}
	if r := o.cmd("PATCH", estPath(ids["centro"], ""), map[string]any{"expected_version": 3, "location": map[string]any{"latitude": 95, "longitude": 0}}); r.status != 400 {
		t.Fatalf("latitude inválida: %d", r.status)
	}
}

// C-CLI-02 — painel do cliente: as filas em que está, em lojas diferentes (conta e visitante).
func TestMyVisits(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	outra := e.shop("outra-barbearia", "dani@exemplo.com")
	pro := e.professional(outra.id, "Caio")
	q := e.queueOf(pro)
	outra.owner.mustStatus(outra.owner.cmd("POST", estPath(outra.id, "/queues/"+q.String()+"/open"), map[string]any{"expected_version": 1}), 200)
	corte := outra.owner.createService(outra.id, map[string]any{"name": "Corte", "price_cents": 4000, "duration_minutes": 30, "points": 0,
		"professional_ids": []string{pro.String()}})["id"].(string)

	if r := e.browser().do("GET", "/v1/my-visits", nil); r.status != 401 {
		t.Fatalf("sem identidade: %d", r.status)
	}
	// Visitante: o mesmo navegador entra nas duas lojas como visitante.
	v := e.browser()
	v.createVisitor(b.id, "Lia")
	v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	v.createVisitor(outra.id, "Lia")
	v.mustStatus(v.enter(outra.id, q, corte), 201)
	b.betoB.mustStatus(b.betoB.callNext(b.id, b.filaBeto), 200)

	r := v.mustStatus(v.do("GET", "/v1/my-visits", nil), 200)
	var out struct{ Data []contrato.MyVisit }
	strictDecode(t, r.raw, &out)
	if len(out.Data) != 2 {
		t.Fatalf("visitas: %s", r.raw)
	}
	// Chamada primeiro.
	if out.Data[0].Establishment.Id != b.id || string(out.Data[0].Visit.Participation.Status) != "called" ||
		out.Data[0].ProfessionalName != "Beto" || out.Data[1].Establishment.Slug != "outra-barbearia" {
		t.Fatalf("ordem/dados: %s", r.raw)
	}
	// Outro navegador não vê nada disso.
	maria := e.verifiedCustomer("maria@exemplo.com")
	r = maria.mustStatus(maria.do("GET", "/v1/my-visits", nil), 200)
	if !strings.Contains(string(r.raw), `"data":[]`) {
		t.Fatalf("outra pessoa vendo visitas: %s", r.raw)
	}
	// Conta: entra numa loja e aparece.
	maria.mustStatus(maria.enter(outra.id, q, corte), 201)
	r = maria.mustStatus(maria.do("GET", "/v1/my-visits", nil), 200)
	var m struct{ Data []map[string]any }
	_ = json.Unmarshal(r.raw, &m)
	if len(m.Data) != 1 {
		t.Fatalf("conta: %s", r.raw)
	}
}
