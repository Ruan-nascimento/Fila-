package api_test

// Revisão final (etapa 13): varredura de autorização em TODAS as rotas do contrato e testes de
// privacidade e limites que faltavam na rastreabilidade (docs/rastreabilidade.md).

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	hs "fila/internal/platform/httpserver"
)

var pathParam = regexp.MustCompile(`\{([a-zA-Z]+)\}`)

// grantFor informa se a rota dá acesso a este tipo de ator (qualquer escopo).
func grantFor(rt hs.Route, actor string) (bool, []string) {
	var scopes []string
	for _, g := range rt.Grants {
		if g.Actor == actor {
			scopes = append(scopes, g.Scope)
		}
	}
	return len(scopes) > 0, scopes
}

// Cenários: C-SEG-02, C-SEG-03, C-SEG-04, C-SEG-12.
// Varredura de autorização: cada rota do contrato é chamada por cinco intrusos com ids REAIS de
// recursos da loja A. Onde o contrato não dá acesso àquele ator e escopo, a resposta precisa ser
// 401/403/404 (nunca 2xx, 409, 422 ou 5xx). No fim, nada mudou nos recursos da loja A.
func TestAuthorizationSweepAllRoutes(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	b.owner.setProgram(b.id, true)
	reward := b.owner.newReward(b.id, "Pomada", 10, nil)

	// Recursos reais da loja A: visita, comanda paga por Pix e conferida, reserva, convite.
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, pm["id"].(string))
	order := maria.order(b.id, om["id"])
	ch := maria.mustStatus(maria.createPix(b.id, order, newIdemKey()), 201).data()
	e.pix.Set(e.remote(ch["id"]).ID, "PAID", 0)
	e.reconcile()
	order = b.betoB.order(b.id, om["id"])
	rec := receiptsOf(order)[0]
	order = b.betoB.mustStatus(b.betoB.confer(b.id, order, rec["id"]), 200).data()
	res := maria.mustStatus(maria.reserve(b.id, reward), 201).data()
	b.owner.reauth()
	duda := e.professional(b.id, "Duda") // convite pendente (Beto já tem acesso)
	inv := b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/professionals/"+duda.String()+"/invites"),
		map[string]any{"email": "outro-beto@exemplo.com"}), 201).data()
	items := order["items"].([]any)

	ids := map[string]string{
		"establishmentId": b.id.String(), "queueId": b.filaBeto.String(), "participationId": pm["id"].(string),
		"orderId": om["id"].(string), "professionalId": b.beto.String(), "serviceId": b.corte, "rewardId": reward["id"].(string),
		"reservationId": res["id"].(string), "receiptId": rec["id"].(string), "chargeId": ch["id"].(string),
		"inviteId": inv["id"].(string), "itemId": items[0].(map[string]any)["id"].(string), "slug": "barbearia-teste",
		// Sem recurso real deste tipo no cenário: id aleatório (a recusa ainda é exigida).
		"issueId": uuid.NewString(), "refundId": uuid.NewString(), "changeRequestId": uuid.NewString(),
	}

	outra := e.shop("outra-loja", "olga@exemplo.com")
	joao := e.verifiedCustomer("joao@exemplo.com")
	ze := e.visitorIn(b.id, "Zé")
	anon := e.browser()

	type intruso struct {
		nome    string
		b       *browser
		account bool   // é uma conta (pode agir como cliente de si mesma)
		actor   string // papel do ator nesta loja
		estID   string // establishmentId usado na URL
	}
	params := func(rt hs.Route) []string {
		var out []string
		for _, m := range pathParam.FindAllStringSubmatch(rt.Path, -1) {
			out = append(out, m[1])
		}
		return out
	}
	objeto := func(rt hs.Route) bool { // a rota aponta um recurso específico (além da loja)
		for _, p := range params(rt) {
			if p != "establishmentId" {
				return true
			}
		}
		return false
	}
	// acesso devolve: "negado" (precisa recusar), "proprio" (só os dados do próprio chamador ou
	// uma lista filtrada: não pode conter recurso alheio) ou "livre" (público ou permitido).
	acesso := func(in intruso, rt hs.Route) string {
		if ok, _ := grantFor(rt, "public"); ok {
			return "livre"
		}
		if in.account {
			if ok, sc := grantFor(rt, "customer"); ok && (slices.Contains(sc, "self") || slices.Contains(sc, "visit_owner")) {
				if !objeto(rt) {
					return "proprio"
				}
			}
			if ok, sc := grantFor(rt, "owner"); ok && slices.Contains(sc, "self") {
				return "proprio"
			}
		}
		switch in.actor {
		case "visitor":
			if ok, _ := grantFor(rt, "visitor"); ok && !objeto(rt) {
				return "proprio"
			}
		case "professional":
			if ok, sc := grantFor(rt, "professional"); ok {
				if slices.Contains(sc, "establishment_member") || slices.Contains(sc, "self") {
					return "livre"
				}
				if !objeto(rt) {
					return "proprio"
				}
			}
		case "owner":
			if in.estID != b.id.String() {
				// Dona da loja B com o id da própria loja: listas são dela; objetos da loja A, não.
				if ok, _ := grantFor(rt, "owner"); ok && !objeto(rt) {
					return "livre"
				}
			}
		}
		return "negado"
	}
	alheios := []string{pm["id"].(string), om["id"].(string), res["id"].(string), rec["id"].(string), ch["id"].(string),
		b.filaBeto.String(), "maria@exemplo.com"}
	versao := map[string]string{"queueId": "queue", "professionalId": "professional", "serviceId": "service",
		"rewardId": "reward", "reservationId": "reward_reservation", "participationId": "participation", "orderId": "service_order"}
	// corpoValido monta um corpo que passa na validação, com a versão REAL do recurso alvo: assim
	// uma resposta 400 vira prova (a recusa precisa vir da autorização, não da validação).
	corpoValido := func(rt hs.Route) map[string]any {
		body := map[string]any{}
		ps := params(rt)
		alvo := ps[len(ps)-1]
		if tb, ok := versao[alvo]; ok {
			var v int64
			if err := e.pool.QueryRow(t.Context(), "SELECT version FROM "+tb+" WHERE id = $1", ids[alvo]).Scan(&v); err == nil {
				body["expected_version"] = v
			}
		}
		switch rt.Operation {
		case "updateProfessional":
			body["display_name"] = "Invasor"
		case "setServiceProfessionals":
			body["professional_ids"] = []string{}
		case "revertRewardDelivery":
			body["reason"] = "teste de autorização"
		case "resolveFinancialIssue":
			body = map[string]any{"resolution_note": "teste de autorização"}
		case "signalAvailability":
			body = map[string]any{"unavailable": true}
		}
		return body
	}
	intrusos := []intruso{
		{"anônimo", anon, false, "public", b.id.String()},
		{"dona de outra loja (loja A na URL)", outra.owner, true, "owner", b.id.String()},
		{"dona de outra loja (objeto da loja A sob a loja B)", outra.owner, true, "owner", outra.id.String()},
		{"cliente sem relação", joao, true, "customer", b.id.String()},
		{"visitante sem relação", ze, false, "visitor", b.id.String()},
		{"outro profissional da mesma loja", caio, true, "professional", b.id.String()},
	}

	before := snapshotA(t, e, om["id"].(string), pm["id"].(string), res["id"].(string))
	checked, filtered, provenAfter400 := 0, 0, 0
	primeira := map[int]int{} // status da primeira tentativa (corpo vazio)
	porAtor := map[string]int{}
	var inconclusive []string
	call := func(in intruso, rt hs.Route, path string, body map[string]any) resp {
		if rt.Method == http.MethodGet {
			if strings.HasSuffix(path, "/v1/me/history") {
				path += "?establishment_id=" + b.id.String()
			}
			return in.b.do(rt.Method, path, nil)
		}
		in.b.session() // token CSRF atual, como o navegador
		return in.b.cmd(rt.Method, path, body)
	}
	for _, in := range intrusos {
		for _, rt := range e.api.Routes() {
			modo := acesso(in, rt)
			// "livre" não é o alvo; comando sobre a PRÓPRIA conta (sair, trocar senha, excluir) mudaria
			// o próprio intruso e não testa acesso alheio: só leituras próprias são conferidas.
			if modo == "livre" || (modo == "proprio" && rt.Method != http.MethodGet) {
				continue
			}
			path := pathParam.ReplaceAllStringFunc(rt.Path, func(m string) string {
				name := m[1 : len(m)-1]
				if name == "establishmentId" {
					return in.estID
				}
				return ids[name]
			})
			r := call(in, rt, path, map[string]any{})
			if modo == "proprio" {
				filtered++
				if r.status >= 500 {
					t.Errorf("%s em %s %s: %d", in.nome, rt.Method, rt.Path, r.status)
				}
				for _, x := range alheios {
					if r.status < 300 && bytes.Contains(r.raw, []byte(x)) {
						t.Errorf("VAZAMENTO: %s em %s %s recebeu %q, que não é dele: %s", in.nome, rt.Method, rt.Path, x, truncate(r.raw))
					}
				}
				continue
			}
			checked++
			primeira[r.status]++
			porAtor[in.nome+" "+fmt.Sprint(r.status)]++
			if r.status == 400 && rt.Method != http.MethodGet {
				r = call(in, rt, path, corpoValido(rt))
				if r.status != 400 {
					provenAfter400++
				}
			}
			switch {
			case r.status == 401 || r.status == 403 || r.status == 404:
			case r.status == 400:
				inconclusive = append(inconclusive, fmt.Sprintf("%s â†’ %s %s: %s", in.nome, rt.Method, rt.Path, truncate(r.raw)))
			default:
				t.Errorf("FALHA DE AUTORIZAÇÃO: %s em %s %s (%s) respondeu %d: %s", in.nome, rt.Method, rt.Path,
					rt.Operation, r.status, truncate(r.raw))
			}
		}
	}
	// A varredura só vale se cada intruso continuou com a própria sessão até o fim.
	for _, in := range intrusos[1:] {
		sess := in.b.session()
		if in.account && sess["account"] == nil {
			t.Fatalf("%s perdeu a sessão durante a varredura: as recusas dele não provam nada", in.nome)
		}
		if !in.account && len(sess["visitors"].([]any)) == 0 {
			t.Fatalf("%s perdeu a sessão de visitante durante a varredura", in.nome)
		}
	}
	if primeira[401] > 0 && primeira[403]+primeira[404] == 0 {
		t.Fatal("só 401: os intrusos logados não foram testados de verdade")
	}
	t.Logf("varredura: %d chamadas negadas por contrato (%d provadas com corpo válido depois de um 400), %d inconclusivas; %d chamadas a dados próprios/listas filtradas sem vazamento",
		checked, provenAfter400, len(inconclusive), filtered)
	t.Logf("status da primeira tentativa (corpo vazio): %v", primeira)
	for k, v := range porAtor {
		t.Logf("  %s: %d", k, v)
	}
	for _, x := range inconclusive {
		t.Errorf("inconclusiva (400 mesmo com corpo válido): %s", x)
	}
	if after := snapshotA(t, e, om["id"].(string), pm["id"].(string), res["id"].(string)); after != before {
		t.Fatalf("a varredura mudou recursos da loja A:\nantes  %s\ndepois %s", before, after)
	}
	if checked < 150 {
		t.Fatalf("varredura pequena demais (%d): rotas ou atores faltando", checked)
	}
}

// snapshotA resume o estado dos recursos da loja A (versões, estados e contagens).
func snapshotA(t *testing.T, e *env, order, part, res string) string {
	var ov, pv, rv int64
	var os, ps, rs string
	if err := e.pool.QueryRow(t.Context(), `SELECT version, status FROM service_order WHERE id = $1`, order).Scan(&ov, &os); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(t.Context(), `SELECT version, status FROM participation WHERE id = $1`, part).Scan(&pv, &ps); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(t.Context(), `SELECT version, status FROM reward_reservation WHERE id = $1`, res).Scan(&rv, &rs); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("comanda %s v%d · participação %s v%d · reserva %s v%d · recebimentos %d · estornos %d · convites %d · profissionais %d",
		os, ov, ps, pv, rs, rv, e.count(t, `SELECT count(*) FROM receipt`), e.count(t, `SELECT count(*) FROM refund`),
		e.count(t, `SELECT count(*) FROM invite`), e.count(t, `SELECT count(*) FROM professional`))
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

// Cenários: C-FILA-08.
// A fila pública (resposta e eventos SSE do canal public) não leva nick, nome, e-mail, serviços,
// valores nem ids de pessoa, comanda ou participação; pedir canais da equipe sem sessão é negado.
func TestPublicQueueLeaksNothing(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	e.publish()
	pub := e.browser()
	s := pub.stream(b.id, "public,owner,professional,visit", "")
	snap := s.expect("stream.snapshot")
	if d := snap.data()["denied_channels"]; fmt.Sprint(d) != "[owner professional visit]" {
		t.Fatalf("canais sem sessão deveriam ser negados: %v", d)
	}
	maria := e.verifiedCustomer("maria-souza@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	lia := e.visitorIn(b.id, "Liazinha")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	e.publish()
	ev := s.expect("queue.public.updated")
	r := pub.mustStatus(pub.do("GET", "/v1/public/establishments/"+b.id.String()+"/queues", nil), 200)
	acct := accountIDOf(t, e, "maria-souza@exemplo.com")
	for _, raw := range [][]byte{r.raw, []byte(ev.raw), []byte(snap.raw)} {
		for _, proibido := range []string{"Pessoa Teste", "Liazinha", "maria-souza", "@exemplo.com", "Corte", "Barba", "7500", "4500",
			pm["id"].(string), pl["id"].(string), om["id"].(string), acct.String()} {
			if bytes.Contains(raw, []byte(proibido)) {
				t.Errorf("fila pública vazou %q: %s", proibido, truncate(raw))
			}
		}
	}
	if !strings.Contains(string(r.raw), "A00") {
		t.Fatalf("a fila pública deveria mostrar as senhas: %s", r.raw)
	}
}

// Cenários: C-SEG-10.
// Corpo grande demais e campo de texto enorme são recusados sem processar.
func TestBodyAndFieldLimits(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	big := `{"email":"a@exemplo.com","password":"` + strings.Repeat("x", 1<<20) + `"}`
	if r := e.browser().do("POST", "/v1/auth/login", big); r.status != 413 || r.code() != "payload_too_large" {
		t.Fatalf("corpo de 1 MiB: %d %s", r.status, truncate(r.raw))
	}
	maria := e.verifiedCustomer("maria@exemplo.com")
	pm, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	r := b.owner.cmd("POST", estPath(b.id, "/participations/"+pm["id"].(string)+"/cancel"),
		map[string]any{"expected_version": 1, "reason": strings.Repeat("motivo ", 1500)})
	if r.status != 400 && r.status != 413 {
		t.Fatalf("motivo com 10 mil caracteres: %d %s", r.status, truncate(r.raw))
	}
	if st, _ := e.partStatus(t, pm["id"].(string)); st != "waiting" {
		t.Fatal("nada deveria ter sido processado")
	}
}

// Cenários: C-EXC-01, C-EXC-03.
// Exclusão completa: depois de concluída, o nome, o e-mail e o nick da pessoa não aparecem em
// NENHUMA coluna de texto, JSON ou binária de NENHUMA tabela (perfil, fila, reservas, eventos,
// respostas repetíveis, auditoria, caixa de saída).
func TestDeletionLeavesNoPersonalDataAnywhere(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	b.owner.setProgram(b.id, true)
	reward := b.owner.newReward(b.id, "Pomada", 10, nil)
	z := e.browser()
	z.mustStatus(z.do("POST", "/v1/auth/register", map[string]any{"name": "Zuleica Quintanilha",
		"email": "zuleica.quintanilha@exemplo.com", "password": goodPassword}), 202)
	z.mustStatus(z.do("POST", "/v1/auth/email-verification/confirm",
		map[string]any{"token": e.lastToken("zuleica.quintanilha@exemplo.com", "Confirme")}), 204)
	z.mustStatus(z.login("zuleica.quintanilha@exemplo.com", goodPassword, "customer"), 200)
	// Resposta repetível com nome e e-mail (idempotency_record) e nick na fila.
	z.mustStatus(z.cmd("PATCH", "/v1/me", map[string]any{"name": "Zuleica Q. Quintanilha"}), 200)
	pz, oz := visitOf(z.mustStatus(z.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, pz["id"].(string))
	order := b.betoB.order(b.id, oz["id"])
	b.betoB.mustStatus(b.betoB.receive(b.id, order, map[string]any{"method": "cash", "amount_cents": due(order),
		"cash_tendered_cents": due(order)}), 201)
	z.mustStatus(z.reserve(b.id, reward), 201) // reserva guarda o nome para a equipe entregar
	e.publish()
	zid := accountIDOf(t, e, "zuleica.quintanilha@exemplo.com")
	z.reauth()
	z.mustStatus(z.requestDeletion(), 202)
	if n := e.processDeletions(); n != 1 {
		t.Fatalf("exclusão deveria concluir: %d", n)
	}
	e.deliver() // o e-mail do protocolo sai e o conteúdo é apagado da caixa de saída
	e.publish()
	if n := e.count(t, `SELECT count(*) FROM idempotency_record WHERE actor_key = $1`, "conta:"+zid.String()); n != 0 {
		t.Fatalf("respostas guardadas da conta excluída continuam: %d", n)
	}

	rows, err := e.pool.Query(t.Context(), `
		SELECT table_name, column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND data_type IN ('text', 'character varying', 'jsonb', 'json', 'bytea')
		ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatal(err)
	}
	type col struct{ table, column, kind string }
	var cols []col
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.table, &c.column, &c.kind); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	for _, c := range cols {
		expr := `lower(` + c.column + `::text)`
		if c.kind == "bytea" {
			expr = `lower(encode(` + c.column + `, 'escape'))`
		}
		for _, needle := range []string{"zuleica", "quintanilha"} {
			var n int
			sql := `SELECT count(*) FROM ` + c.table + ` WHERE ` + expr + ` LIKE '%' || $1 || '%'`
			if err := e.pool.QueryRow(t.Context(), sql, needle).Scan(&n); err != nil {
				t.Fatalf("%s.%s: %v", c.table, c.column, err)
			}
			if n > 0 {
				t.Errorf("DADO PESSOAL depois da exclusão: %q em %s.%s (%d linha(s))", needle, c.table, c.column, n)
				if c.table == "idempotency_record" || c.table == "event_log" {
					keyCols := "actor_key || ' ' || operation"
					if c.table == "event_log" {
						keyCols = "audience || ' ' || type || ' ' || resource_type"
					}
					rs, _ := e.pool.Query(t.Context(), `SELECT `+keyCols+` FROM `+c.table+` WHERE `+expr+` LIKE '%' || $1 || '%'`, needle)
					for rs.Next() {
						var k string
						_ = rs.Scan(&k)
						t.Logf("    %s: %s", c.table, k)
					}
					rs.Close()
				}
			}
		}
	}
	t.Logf("%d colunas de texto/JSON/binárias conferidas", len(cols))
}

// Cenários: C-SEG-07.
// Textos maliciosos são só dados: SQL em nick, slug, protocolo e e-mail não muda o banco, e
// HTML no nick volta como texto (escapado só na tela, pelo React; a API devolve o texto).
func TestInjectionStringsAreData(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	antes := e.count(t, `SELECT count(*) FROM account`)
	sqlNick := `x'); DROP TABLE account;--`
	v := e.visitorIn(b.id, sqlNick)
	v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	html := `<img src=x onerror=alert(1)>`
	h := e.visitorIn(b.id, html)
	h.mustStatus(h.enter(b.id, b.filaBeto, b.corte), 201)
	q := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/queues"), nil), 200)
	if !strings.Contains(string(q.raw), `x'); DROP TABLE account;--`) || !strings.Contains(string(q.raw), `\u003cimg src=x onerror=alert(1)\u003e`) {
		t.Fatalf("os nicks deveriam voltar literalmente (JSON escapado): %s", truncate(q.raw))
	}
	for _, path := range []string{"/v1/public/slugs/x'%20OR%20'1'%3D'1", "/v1/public/slugs/barbearia-teste'--"} {
		if r := e.browser().do("GET", path, nil); r.status != 404 && r.status != 400 {
			t.Fatalf("%s: %d %s", path, r.status, truncate(r.raw))
		}
	}
	if r := e.browser().do("POST", "/v1/deletion-requests/status", map[string]any{"protocol": "' OR 1=1 --"}); r.status != 404 && r.status != 400 {
		t.Fatalf("protocolo com SQL: %d", r.status)
	}
	if r := e.browser().do("POST", "/v1/auth/login", map[string]any{"email": "' OR '1'='1", "password": "' OR '1'='1", "context": "customer"}); r.status == 200 {
		t.Fatal("login com SQL entrou")
	}
	if e.count(t, `SELECT count(*) FROM account`) != antes {
		t.Fatal("tabela de contas mudou")
	}
}
