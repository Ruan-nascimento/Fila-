package api_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func location(t *testing.T, r resp) *url.URL {
	t.Helper()
	if r.status != 303 {
		t.Fatalf("esperava 303, veio %d: %s", r.status, r.raw)
	}
	u, err := url.Parse(r.header.Get("Location"))
	if err != nil || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		t.Fatalf("Location não é rota interna: %q", r.header.Get("Location"))
	}
	return u
}

// Primeiro acesso com Google cria SÓ cliente, volta para a loja e a sessão nunca abre a gestão.
// Cenários: C-CFG-01.
func TestGoogleLoginCreatesCustomerAndReturnsToShop(t *testing.T) {
	e, g := newEnvFull(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	b := e.browser()
	r := b.googleLogin(g, map[string]any{"intent": "login", "establishment_slug": "barbearia-teste"}, "sub-maria", "maria@gmail.com", nil)
	if u := location(t, r); u.String() != "/loja/barbearia-teste" {
		t.Fatalf("destino: %s", u)
	}
	s := b.session()
	sess := s["session"].(map[string]any)
	if sess["kind"] != "customer" || sess["auth_method"] != "google" || len(s["memberships"].([]any)) != 0 {
		t.Fatalf("sessão do Google: %v", s)
	}
	if r := b.do("GET", "/v1/establishments/"+loja.id.String(), nil); r.status != 404 {
		t.Fatalf("cliente do Google acessou a gestão: %d", r.status)
	}
	me := b.mustStatus(b.do("GET", "/v1/me", nil), 200).data()
	if me["has_password"] != false || me["email_verified"] != true || me["google"].(map[string]any)["linked"] != true {
		t.Fatalf("perfil: %v", me)
	}
	// Segunda vez: mesma conta (sem duplicar).
	b2 := e.browser()
	location(t, b2.googleLogin(g, map[string]any{"intent": "login"}, "sub-maria", "maria@gmail.com", nil))
	if n := e.count(t, `SELECT count(*) FROM account WHERE email_normalized = 'maria@gmail.com'`); n != 1 {
		t.Fatalf("contas: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM staff_membership m JOIN account a ON a.id = m.account_id WHERE a.email_normalized = 'maria@gmail.com'`); n != 0 {
		t.Fatal("Google criou vínculo de equipe")
	}
	// Login por senha nunca funciona numa conta sem senha (mesma resposta de senha errada).
	if r := e.browser().login("maria@gmail.com", "qualquer-senha-123", "customer"); r.status != 401 || r.code() != "invalid_credentials" {
		t.Fatalf("senha em conta Google: %d %s", r.status, r.raw)
	}
	// O banco recusa sessão de equipe aberta pelo Google, mesmo que o código errasse.
	_, err := e.pool.Exec(context.Background(), `INSERT INTO account_session (account_id, token_hash, kind, auth_method, created_at,
		last_used_at, absolute_expires_at) SELECT id, sha256('x'::bytea), 'staff', 'google', now(), now(), now() + interval '1 hour'
		FROM account WHERE email_normalized = 'maria@gmail.com'`)
	if err == nil || !strings.Contains(err.Error(), "account_session_staff_password_ck") {
		t.Fatalf("sessão de equipe pelo Google aceita no banco: %v", err)
	}
}

// Dono com Google conectado: a sessão do Google é de cliente e não usa a API de gestão.
// Cenários: C-CFG-01, C-CFG-03.
func TestGoogleSessionOfOwnerNeverUnlocksManagement(t *testing.T) {
	e, g := newEnvFull(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	// Vincular exige reautenticação recente.
	if r := loja.owner.do("POST", "/v1/auth/google/start", map[string]any{"intent": "link"}); r.code() != "reauthentication_required" {
		t.Fatalf("vínculo sem reautenticação: %d %s", r.status, r.raw)
	}
	loja.owner.reauth()
	loja.owner.noRedirect()
	r := loja.owner.mustStatus(loja.owner.do("POST", "/v1/auth/google/start", map[string]any{"intent": "link"}), 200)
	state, code := g.authorize(r.data()["authorization_url"].(string), "sub-ana", "ana.google@gmail.com", nil, nil)
	cb := loja.owner.do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil)
	if u := location(t, cb); u.Path != "/conta" || u.Query().Get("google") != "conectado" {
		t.Fatalf("vínculo: %s", u)
	}
	// A sessão de equipe (senha) continua.
	loja.owner.mustStatus(loja.owner.do("GET", "/v1/establishments/"+loja.id.String(), nil), 200)
	// Entrar pelo Google com a mesma identidade: sessão de cliente, sem gestão, sem tocar a vaga do dono.
	c := e.browser()
	location(t, c.googleLogin(g, map[string]any{"intent": "login"}, "sub-ana", "ana.google@gmail.com", nil))
	if s := c.session()["session"].(map[string]any); s["kind"] != "customer" || s["owner_authority"] != false {
		t.Fatalf("sessão: %v", s)
	}
	if r := c.do("GET", "/v1/establishments/"+loja.id.String(), nil); r.status != 403 {
		t.Fatalf("Google abriu a gestão: %d %s", r.status, r.raw)
	}
	loja.owner.mustStatus(loja.owner.do("GET", "/v1/establishments/"+loja.id.String(), nil), 200)
	// A mesma identidade não pode ser conectada a outra conta.
	other := e.verifiedCustomer("outra@exemplo.com")
	other.reauth()
	other.noRedirect()
	r = other.mustStatus(other.do("POST", "/v1/auth/google/start", map[string]any{"intent": "link"}), 200)
	state, code = g.authorize(r.data()["authorization_url"].(string), "sub-ana", "ana.google@gmail.com", nil, nil)
	if u := location(t, other.do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil)); u.Query().Get("google") != "ja_vinculado" {
		t.Fatalf("vínculo duplicado: %s", u)
	}
	// Desconectar com senha disponível funciona (com reautenticação).
	loja.owner.reauth()
	loja.owner.mustStatus(loja.owner.do("POST", "/v1/auth/google/unlink", nil), 204)
	if n := e.count(t, `SELECT count(*) FROM external_identity WHERE subject = 'sub-ana'`); n != 0 {
		t.Fatal("identidade continuou vinculada")
	}
}

// State ausente, errado, reutilizado, de outro navegador, vencido; cancelamento; falhas do token.
// Cenários: C-CFG-02.
func TestGoogleCallbackRejections(t *testing.T) {
	e, g := newEnvFull(t)
	failed := func(name string, r resp, want string) {
		t.Helper()
		u := location(t, r)
		if u.Path != "/entrar" || u.Query().Get("google") != want {
			t.Errorf("%s: destino %s", name, u)
		}
		if r.header.Get("Set-Cookie") != "" && strings.Contains(r.header.Get("Set-Cookie"), "fila_session=") {
			t.Errorf("%s: criou sessão", name)
		}
	}
	b := e.browser().noRedirect()
	b.session()
	failed("sem state", b.do("GET", "/v1/auth/google/callback?code=x", nil), "falhou")
	failed("state errado", b.do("GET", "/v1/auth/google/callback?state="+strings.Repeat("A", 43)+"&code=x", nil), "falhou")

	start := func(b *browser) string {
		return b.mustStatus(b.do("POST", "/v1/auth/google/start", map[string]any{"intent": "login"}), 200).data()["authorization_url"].(string)
	}
	// Reutilizado: o segundo callback com o mesmo state não passa.
	state, code := g.authorize(start(b), "sub-1", "um@gmail.com", nil, nil)
	location(t, b.do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil))
	failed("reutilizado", b.do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil), "falhou")

	// Outro navegador (sem o cookie de correlação).
	b2 := e.browser().noRedirect()
	state, code = g.authorize(start(b2), "sub-2", "dois@gmail.com", nil, nil)
	failed("outro navegador", e.browser().noRedirect().do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil), "falhou")

	// Vencido (10 min).
	b3 := e.browser().noRedirect()
	state, code = g.authorize(start(b3), "sub-3", "tres@gmail.com", nil, nil)
	e.clock.Advance(11 * time.Minute)
	failed("vencido", b3.do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil), "falhou")

	// Cancelado no Google.
	b4 := e.browser().noRedirect()
	u, _ := url.Parse(start(b4))
	failed("cancelado", b4.do("GET", "/v1/auth/google/callback?state="+u.Query().Get("state")+"&error=access_denied", nil), "cancelado")

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := map[string]struct {
		extra  map[string]any
		tamper func(map[string]any) *rsa.PrivateKey
	}{
		"nonce errado":        {extra: map[string]any{"nonce": "outro"}},
		"assinatura inválida": {tamper: func(map[string]any) *rsa.PrivateKey { return other }},
		"emissor errado":      {extra: map[string]any{"iss": "https://falso.exemplo"}},
		"audiência errada":    {extra: map[string]any{"aud": "outro-cliente"}},
		"azp errado":          {extra: map[string]any{"azp": "outro-cliente"}},
		"vencido":             {extra: map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}},
	}
	for name, c := range cases {
		bx := e.browser().noRedirect()
		state, code := g.authorize(start(bx), "sub-"+name, "x@gmail.com", c.extra, c.tamper)
		failed(name, bx.do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil), "falhou")
	}
	if n := e.count(t, `SELECT count(*) FROM external_identity WHERE subject LIKE 'sub-%' AND subject <> 'sub-1'`); n != 0 {
		t.Fatalf("identidades criadas em callbacks recusados: %d", n)
	}
	// Nada de código, state ou token nos logs.
	logs := e.logs.String()
	for _, tok := range g.tokens {
		if strings.Contains(logs, tok[:40]) {
			t.Fatal("id_token no log")
		}
	}
	if strings.Contains(logs, state) || strings.Contains(logs, code) {
		t.Fatal("state/código no log")
	}
}

// Destinos de volta absolutos, ambíguos ou fora da lista são recusados no servidor.
// Cenários: C-CFG-02.
func TestGoogleStartRejectsUnsafeReturn(t *testing.T) {
	e, _ := newEnvFull(t)
	e.shop("barbearia-teste", "ana@exemplo.com")
	b := e.browser()
	for _, ret := range []string{"//evil.com", "https://evil.com", "/\\evil.com", "/loja/../conta", "/loja/barbearia-teste?x=1",
		"/gestao", "/conta%2F%2Fevil", "/loja/barbearia-teste\n", "javascript:alert(1)"} {
		if r := b.do("POST", "/v1/auth/google/start", map[string]any{"intent": "login", "return_to": ret}); r.status != 400 {
			t.Errorf("%q aceito: %d", ret, r.status)
		}
	}
	if r := b.do("POST", "/v1/auth/google/start", map[string]any{"intent": "login", "establishment_slug": "nao-existe"}); r.status != 404 {
		t.Fatalf("loja inexistente iniciou o Google: %d", r.status)
	}
	if r := b.do("POST", "/v1/auth/google/start", map[string]any{"intent": "login", "establishment_slug": "barbearia-teste",
		"return_to": "/loja/outra-loja"}); r.status != 400 {
		t.Fatalf("retorno de outra loja: %d", r.status)
	}
}

// E-mail igual a uma conta local não vincula; e-mail que o Google não controla exige confirmação
// local; mudança de e-mail no Google mantém a mesma conta (sub).
// Cenários: C-CFG-03.
func TestGoogleEmailRules(t *testing.T) {
	e, g := newEnvFull(t)
	e.verifiedCustomer("maria@gmail.com")
	r := e.browser().googleLogin(g, map[string]any{"intent": "login"}, "sub-m", "maria@gmail.com", nil)
	if u := location(t, r); u.Query().Get("google") != "conta_existente" {
		t.Fatalf("colisão: %s", u)
	}
	if n := e.count(t, `SELECT count(*) FROM external_identity`); n != 0 {
		t.Fatal("vínculo automático por e-mail")
	}
	// Domínio comum, sem hd: o Google não é autoridade → conta pendente + e-mail de confirmação.
	b := e.browser()
	if u := location(t, b.googleLogin(g, map[string]any{"intent": "login"}, "sub-z", "zeca@empresa.com.br", nil)); u.Query().Get("google") != "verificar_email" {
		t.Fatalf("não autoritativo: %s", u)
	}
	if b.session()["account"] != nil {
		t.Fatal("conta pendente recebeu sessão")
	}
	tok := e.lastToken("zeca@empresa.com.br", "Confirme")
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/email-verification/confirm", map[string]any{"token": tok}), 204)
	location(t, e.browser().googleLogin(g, map[string]any{"intent": "login"}, "sub-z", "zeca@empresa.com.br", nil))
	// Workspace (hd) é autoridade.
	location(t, e.browser().googleLogin(g, map[string]any{"intent": "login"}, "sub-w", "w@empresa.com.br", map[string]any{"hd": "empresa.com.br"}))
	if n := e.count(t, `SELECT count(*) FROM account WHERE email_normalized = 'w@empresa.com.br' AND status = 'active'`); n != 1 {
		t.Fatal("conta Workspace não ativa")
	}
	// O e-mail mudou no Google: mesma conta (sub), e-mail local intacto.
	c := e.browser()
	location(t, c.googleLogin(g, map[string]any{"intent": "login"}, "sub-z", "zeca.novo@gmail.com", nil))
	if c.session()["account"].(map[string]any)["email"] != "zeca@empresa.com.br" {
		t.Fatal("e-mail local mudou")
	}
}

// Callbacks simultâneos da mesma identidade nova criam UMA conta.
// Cenários: C-CFG-01.
func TestGoogleConcurrentCallbacks(t *testing.T) {
	e, g := newEnvFull(t)
	var wg sync.WaitGroup
	for range 4 {
		b := e.browser().noRedirect()
		b.session()
		r := b.mustStatus(b.do("POST", "/v1/auth/google/start", map[string]any{"intent": "login"}), 200)
		state, code := g.authorize(r.data()["authorization_url"].(string), "sub-c", "c@gmail.com", nil, nil)
		wg.Go(func() { b.do("GET", "/v1/auth/google/callback?state="+state+"&code="+code, nil) })
	}
	wg.Wait()
	if n := e.count(t, `SELECT count(*) FROM account WHERE email_normalized = 'c@gmail.com'`); n != 1 {
		t.Fatalf("contas: %d", n)
	}
}

// Conta só com Google: não troca senha, não recupera senha em silêncio, não desconecta o único
// método; reautentica por código no e-mail e consegue excluir a conta.
// Cenários: C-CFG-03.
func TestGoogleOnlyAccountReauthAndDeletion(t *testing.T) {
	e, g := newEnvFull(t)
	b := e.browser()
	location(t, b.googleLogin(g, map[string]any{"intent": "login"}, "sub-g", "g@gmail.com", nil))
	b.session()
	if r := b.do("POST", "/v1/auth/password/change", map[string]any{"current_password": "x", "new_password": "nova-senha-123"}); r.code() != "no_password" {
		t.Fatalf("troca de senha: %d %s", r.status, r.raw)
	}
	e.browser().do("POST", "/v1/auth/password-reset/request", map[string]any{"email": "g@gmail.com"})
	e.deliver()
	for _, m := range e.inbox {
		if m.To == "g@gmail.com" && strings.Contains(m.Body, "#token=") {
			t.Fatal("recuperação criou link de senha para conta Google")
		}
	}
	if r := b.do("POST", "/v1/auth/reauthenticate", map[string]any{"password": "chute"}); r.code() != "no_password" {
		t.Fatalf("senha em conta sem senha: %s", r.raw)
	}
	b.mustStatus(b.do("POST", "/v1/auth/reauthenticate/email-code", nil), 202)
	e.deliver()
	code := ""
	for _, m := range e.inbox {
		if m.To == "g@gmail.com" && m.Subject == "Código de confirmação" {
			for _, line := range strings.Split(m.Body, "\n") {
				if len(line) == 8 && strings.Trim(line, "0123456789") == "" {
					code = line
				}
			}
		}
	}
	if code == "" {
		t.Fatal("código não enviado")
	}
	if r := b.do("POST", "/v1/auth/reauthenticate", map[string]any{"email_code": "00000000"}); r.code() != "reauth_code_invalid" && code != "00000000" {
		t.Fatalf("código errado: %s", r.raw)
	}
	// O código vale só nesta sessão.
	other := e.browser()
	location(t, other.googleLogin(g, map[string]any{"intent": "login"}, "sub-g", "g@gmail.com", nil))
	other.session()
	if r := other.do("POST", "/v1/auth/reauthenticate", map[string]any{"email_code": code}); r.status != 401 {
		t.Fatalf("código de outra sessão aceito: %d", r.status)
	}
	b.mustStatus(b.do("POST", "/v1/auth/reauthenticate", map[string]any{"email_code": code}), 200)
	if r := b.do("POST", "/v1/auth/google/unlink", nil); r.code() != "last_access_method" {
		t.Fatalf("desconectou o único método: %s", r.raw)
	}
	b.mustStatus(b.cmd("POST", "/v1/me/deletion-request", map[string]any{"confirm_effects": true}), 202)
	if _, err := e.api.Deletion.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM external_identity`); n != 0 {
		t.Fatal("identidade Google ficou depois da exclusão")
	}
}

// Conta só com Google convidada para a equipe: o convite define a primeira senha; o Google
// continua sem dar acesso de equipe.
// Cenários: C-CFG-04.
func TestGoogleOnlyAccountAcceptsInviteWithPassword(t *testing.T) {
	e, g := newEnvFull(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	location(t, e.browser().googleLogin(g, map[string]any{"intent": "login"}, "sub-b", "beto@gmail.com", nil))
	loja.owner.reauth()
	loja.owner.mustStatus(loja.owner.cmd("POST", "/v1/establishments/"+loja.id.String()+"/professionals/"+beto.String()+"/invites",
		map[string]any{"email": "beto@gmail.com"}), 201)
	tok := e.lastToken("beto@gmail.com", "Convite")
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "password": "senha-do-beto-1"}), 204)
	staff := e.browser()
	staff.mustStatus(staff.login("beto@gmail.com", "senha-do-beto-1", "staff"), 200)
	c := e.browser()
	location(t, c.googleLogin(g, map[string]any{"intent": "login"}, "sub-b", "beto@gmail.com", nil))
	if c.session()["session"].(map[string]any)["kind"] != "customer" {
		t.Fatal("Google abriu sessão de equipe")
	}
}

// Cenários: C-CFG-08.
func TestGoogleDisabledKeepsPasswordLogin(t *testing.T) {
	e := newEnv(t)
	if r := e.browser().do("POST", "/v1/auth/google/start", map[string]any{"intent": "login"}); r.code() != "google_unavailable" {
		t.Fatalf("Google desligado: %d %s", r.status, r.raw)
	}
	cfg := e.browser().mustStatus(e.browser().do("GET", "/v1/public/config", nil), 200).data()
	if cfg["google_login"] != false || cfg["media_uploads"] != false || cfg["public_base_url"] != origin {
		t.Fatalf("config pública: %v", cfg)
	}
	e.verifiedCustomer("maria@exemplo.com") // senha continua
}
