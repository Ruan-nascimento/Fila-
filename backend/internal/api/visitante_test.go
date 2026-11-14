package api_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/contrato"
)

var codeFormat = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`)

func visitorPath(est uuid.UUID, suffix string) string {
	return "/v1/establishments/" + est.String() + "/visitor-sessions" + suffix
}

// createVisitor cria o visitante e devolve (id do visitante, código).
func (b *browser) createVisitor(est uuid.UUID, nick string) (string, string) {
	b.e.t.Helper()
	r := b.mustStatus(b.do("POST", visitorPath(est, ""), map[string]any{"nick": nick}), 201)
	b.session()
	d := r.data()
	return d["visitor"].(map[string]any)["id"].(string), d["recovery_code"].(string)
}

func visitorIDs(s map[string]any) []string {
	var out []string
	for _, v := range s["visitors"].([]any) {
		out = append(out, v.(map[string]any)["id"].(string))
	}
	return out
}

// C-VIS-01 — visitante entra com nick e recebe código uma vez; banco guarda só HMAC.
// Cenários: C-SEG-07.
func TestVisitorSessionAndRecoveryCode(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	lia := e.browser()
	r := lia.mustStatus(lia.do("POST", visitorPath(loja.id, ""), map[string]any{"nick": "<img src=x onerror=alert(1)>"}), 201)
	if r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("resposta com código precisa de no-store: %q", r.header.Get("Cache-Control"))
	}
	var created struct {
		Data contrato.VisitorSessionCreated
	}
	strictDecode(t, r.raw, &created)
	code := created.Data.RecoveryCode
	if !codeFormat.MatchString(code) || created.Data.Visitor.Nick != "<img src=x onerror=alert(1)>" {
		t.Fatalf("visitante criado: %+v", created.Data)
	}
	if !created.Data.RecoveryCodeValidUntil.Equal(e.clock.Now().Add(30 * 24 * time.Hour)) {
		t.Fatalf("código deve valer 30 dias: %v", created.Data.RecoveryCodeValidUntil)
	}
	sc := r.header.Get("Set-Cookie")
	for _, attr := range []string{"__Host-fila_visitor=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(sc, attr) {
			t.Errorf("Set-Cookie do visitante sem %s: %q", attr, sc)
		}
	}
	s := lia.session()
	if ids := visitorIDs(s); len(ids) != 1 || ids[0] != created.Data.Visitor.Id.String() || s["account"] != nil || lia.csrf == "" {
		t.Fatalf("sessão do visitante: %v", s)
	}
	norm := strings.ReplaceAll(code, "-", "")
	assertNotInDatabase(t, e, code)
	assertNotInDatabase(t, e, norm)
	if strings.Contains(e.logs.String(), norm[:8]) {
		t.Fatal("código apareceu no log")
	}

	for _, nick := range []string{strings.Repeat("a", 31), "ab\x00c", "  ", "abc\u202edef"} {
		if r := e.browser().do("POST", visitorPath(loja.id, ""), map[string]any{"nick": nick}); r.status != 400 || r.code() != "validation_failed" {
			t.Errorf("nick %q: %d %s", nick, r.status, r.raw)
		}
	}
	if r := e.browser().do("POST", visitorPath(uuid.New(), ""), map[string]any{"nick": "Lia"}); r.status != 404 {
		t.Fatalf("loja inexistente: %d", r.status)
	}
	if r := e.browser().do("POST", visitorPath(loja.id, ""), map[string]any{"nick": "Lia", "account_id": uuid.NewString()}); r.status != 400 {
		t.Fatalf("campo extra: %d", r.status)
	}
}

// C-VIS-02 — prazos da credencial: 7 dias sem uso, 30 dias, 24 h após encerramento; sair do aparelho.
func TestVisitorCredentialExpiry(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")

	inativa := e.browser()
	inativa.createVisitor(loja.id, "Inativa")
	e.clock.Advance(7*24*time.Hour + time.Second)
	if len(visitorIDs(inativa.session())) != 0 {
		t.Fatal("8 dias sem uso: credencial deveria ter expirado")
	}

	assidua := e.browser()
	vid, _ := assidua.createVisitor(loja.id, "Assídua")
	for range 4 {
		e.clock.Advance(6 * 24 * time.Hour)
		if len(visitorIDs(assidua.session())) != 1 {
			t.Fatal("uso a cada 6 dias: credencial deveria continuar")
		}
	}
	e.clock.Advance(6*24*time.Hour + time.Second) // 30 dias desde a criação
	if len(visitorIDs(assidua.session())) != 0 {
		t.Fatal("30 dias desde a criação: credencial deveria expirar")
	}
	var exists int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM visitor WHERE id = $1`, vid).Scan(&exists)
	if exists != 1 {
		t.Fatal("expirar a credencial não pode apagar o visitante (a visita fica para a equipe)")
	}

	// Comanda encerrada: vale até 24 h depois.
	paga := e.browser()
	pid, _ := paga.createVisitor(loja.id, "Paga")
	closeVisit(t, e, pid, e.clock.Now().Add(-23*time.Hour))
	if len(visitorIDs(paga.session())) != 1 {
		t.Fatal("23 h após o encerramento: ainda deveria ver a visita")
	}
	e.clock.Advance(time.Hour + time.Second)
	if len(visitorIDs(paga.session())) != 0 {
		t.Fatal("24 h após o encerramento: credencial deveria deixar de valer")
	}

	// "Sair deste aparelho": revoga a credencial; o visitante continua existindo.
	sair := e.browser()
	sid, _ := sair.createVisitor(loja.id, "Sair")
	sair.mustStatus(sair.do("POST", "/v1/visitor-sessions/current/end", nil), 204)
	sair.session()
	if len(visitorIDs(sair.session())) != 0 {
		t.Fatal("credencial encerrada ainda vale")
	}
	if r := sair.do("POST", "/v1/visitor-sessions/current/end", nil); r.status != 401 {
		t.Fatalf("encerrar sem credencial: %d", r.status)
	}
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM visitor WHERE id = $1`, sid).Scan(&exists)
	if exists != 1 {
		t.Fatal("encerrar a credencial não cancela nem apaga o visitante")
	}
}

func closeVisit(t *testing.T, e *env, visitorID string, at time.Time) {
	t.Helper()
	// O encerramento da comanda é das etapas 05/09; aqui simulamos o resultado no banco.
	if _, err := e.pool.Exec(context.Background(), `UPDATE visitor SET visit_closed_at = $2 WHERE id = $1`, visitorID, at); err != nil {
		t.Fatal(err)
	}
}

// C-VIS-04 e C-VIS-05 — recuperação em outro aparelho troca o código e revoga a anterior.
func TestVisitorRecovery(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	outra := e.shop("outra-loja", "bia@exemplo.com")
	celular := e.browser()
	vid, k1 := celular.createVisitor(loja.id, "Lia")

	outroAparelho := e.browser()
	// Código digitado em minúsculas e sem hífens também vale.
	typed := strings.ToLower(strings.ReplaceAll(k1, "-", " "))
	r := outroAparelho.mustStatus(outroAparelho.do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": typed}), 200)
	if r.header.Get("Cache-Control") != "no-store" {
		t.Fatal("resposta com código novo precisa de no-store")
	}
	var rec struct {
		Data contrato.VisitorSessionCreated
	}
	strictDecode(t, r.raw, &rec)
	k2 := rec.Data.RecoveryCode
	if rec.Data.Visitor.Id.String() != vid || k2 == k1 || !codeFormat.MatchString(k2) {
		t.Fatalf("recuperação: %+v (k1=%s)", rec.Data, k1)
	}
	if ids := visitorIDs(outroAparelho.session()); len(ids) != 1 || ids[0] != vid {
		t.Fatalf("novo aparelho deveria ver a visita: %v", ids)
	}
	if len(visitorIDs(celular.session())) != 0 {
		t.Fatal("credencial antiga deveria ser revogada")
	}
	// Replay do código usado.
	replay := e.browser().do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": k1})
	if replay.status != 401 || replay.code() != "recovery_code_invalid" {
		t.Fatalf("código reutilizado: %d %s", replay.status, replay.raw)
	}
	// Código certo na rota de outra loja: mesma resposta de código errado.
	if r := e.browser().do("POST", visitorPath(outra.id, "/recover"), map[string]any{"recovery_code": k2}); r.status != 401 || r.body["detail"] != replay.body["detail"] {
		t.Fatalf("código em outra loja: %d %s", r.status, r.raw)
	}
	// Visita encerrada há 25 h: código não vale (mesma mensagem).
	closeVisit(t, e, vid, e.clock.Now().Add(-25*time.Hour))
	if r := e.browser().do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": k2}); r.status != 401 || r.body["detail"] != replay.body["detail"] {
		t.Fatalf("visita encerrada há 25 h: %d %s", r.status, r.raw)
	}
	if r := e.browser().do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": "curto"}); r.status != 400 {
		t.Fatalf("código fora do formato: %d", r.status)
	}
}

// Código usado ao mesmo tempo em vários aparelhos: só um recupera.
func TestVisitorRecoveryConcurrent(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	_, code := e.browser().createVisitor(loja.id, "Lia")
	var wg sync.WaitGroup
	statuses := make([]int, 6)
	for i := range statuses {
		wg.Go(func() {
			statuses[i] = e.browser().do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": code}).status
		})
	}
	wg.Wait()
	ok := 0
	for _, s := range statuses {
		if s == 200 {
			ok++
		} else if s != 401 {
			t.Fatalf("status inesperado %d", s)
		}
	}
	var active int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM visitor_credential WHERE revoked_at IS NULL`).Scan(&active)
	if ok != 1 || active != 1 {
		t.Fatalf("recuperações=%d credenciais ativas=%d (%v)", ok, active, statuses)
	}
}

// Tentativas de código limitadas por loja+IP (R-CONTA-11, R-VIS-04).
func TestVisitorRecoveryBruteForceLimited(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	_, code := e.browser().createVisitor(loja.id, "Lia")
	atacante := e.browser()
	for range 10 {
		atacante.do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": "AAAA-AAAA-AAAA-AAAA"})
	}
	r := atacante.do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": code})
	if r.status != 429 || r.header.Get("Retry-After") == "" {
		t.Fatalf("limite de tentativas: %d %s", r.status, r.raw)
	}
	// Outra origem (IP) continua podendo usar o código certo.
	e.browser().mustStatus(e.browser().do("POST", visitorPath(loja.id, "/recover"), map[string]any{"recovery_code": code}), 200)
}

// C-VIS-06 — nick não autentica: mesmo nick gera outra identidade, sem acesso à primeira.
func TestNickDoesNotAuthenticate(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	liaID, _ := e.browser().createVisitor(loja.id, "Lia")
	outra := e.browser()
	outraID, _ := outra.createVisitor(loja.id, "Lia")
	if ids := visitorIDs(outra.session()); liaID == outraID || len(ids) != 1 || ids[0] != outraID {
		t.Fatalf("mesmo nick não pode dar acesso à visita de outra pessoa: %v", ids)
	}
}

// C-VIS-08 — limites de abuso por navegador e por origem (não identifica pessoas).
func TestVisitorCreationRateLimited(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	b := e.browser()
	for i := range 5 {
		b.mustStatus(b.do("POST", visitorPath(loja.id, ""), map[string]any{"nick": "N" + strings.Repeat("x", i)}), 201)
		b.session()
	}
	if r := b.do("POST", visitorPath(loja.id, ""), map[string]any{"nick": "Sexto"}); r.status != 429 {
		t.Fatalf("6º visitante no mesmo navegador: %d %s", r.status, r.raw)
	}
	// Muitos navegadores novos a partir do mesmo IP também são limitados.
	ip := "198.51.100.7"
	got429 := false
	for range 21 {
		nb := e.browser()
		nb.ip = ip
		if r := nb.do("POST", visitorPath(loja.id, ""), map[string]any{"nick": "Anon"}); r.status == 429 {
			got429 = true
		}
	}
	if !got429 {
		t.Fatal("limite por loja+IP não aplicado")
	}
}

// Conta e visitante no mesmo navegador ficam separados: não existe associação.
func TestVisitorAndAccountStaySeparate(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	b := e.browser()
	vid, _ := b.createVisitor(loja.id, "Lia")
	b.mustStatus(b.do("POST", "/v1/auth/register", map[string]any{"name": "Lia", "email": "lia@exemplo.com", "password": goodPassword}), 202)
	tok := e.lastToken("lia@exemplo.com", "Confirme")
	b.mustStatus(b.do("POST", "/v1/auth/email-verification/confirm", map[string]any{"token": tok}), 204)
	b.mustStatus(b.login("lia@exemplo.com", goodPassword, "customer"), 200)
	s := b.session()
	if s["account"] == nil || len(visitorIDs(s)) != 1 || visitorIDs(s)[0] != vid {
		t.Fatalf("conta e visitante deveriam coexistir separados: %v", s)
	}
	b.mustStatus(b.do("POST", "/v1/auth/logout", nil), 204)
	b.session()
	if len(visitorIDs(b.session())) != 1 {
		t.Fatal("logout da conta não encerra o visitante")
	}
	// Nenhuma tabela de visitante tem coluna de conta.
	var n int
	_ = e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name LIKE 'visitor%' AND column_name LIKE '%account%'`).Scan(&n)
	if n != 0 {
		t.Fatal("tabelas de visitante não podem ter vínculo com conta (R-VIS-09)")
	}
	// CSRF também vale para o visitante.
	b.csrf = ""
	if r := b.do("POST", "/v1/visitor-sessions/current/end", nil); r.status != 403 || r.code() != "csrf_failed" {
		t.Fatalf("encerrar visitante sem CSRF: %d %s", r.status, r.raw)
	}
}
