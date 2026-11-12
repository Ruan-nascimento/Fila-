package api_test

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// C-ID-01 — Cadastro só ativa após verificar o e-mail.
func TestRegisterVerifyAndLogin(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	r := b.mustStatus(b.do("POST", "/v1/auth/register", map[string]any{"name": "Maria", "email": "Maria@Exemplo.com", "password": goodPassword}), 202)
	if r.data()["message"] == "" {
		t.Fatal("resposta genérica sem mensagem")
	}
	r = b.login("maria@exemplo.com", goodPassword, "customer")
	if r.status != 403 || r.code() != "email_not_verified" || b.cookie("__Host-fila_session") != nil {
		t.Fatalf("login antes de verificar: %d %s (cookie=%v)", r.status, r.raw, b.cookie("__Host-fila_session"))
	}
	tok := e.lastToken("Maria@Exemplo.com", "Confirme")
	b.mustStatus(b.do("POST", "/v1/auth/email-verification/confirm", map[string]any{"token": tok}), 204)
	r = b.do("POST", "/v1/auth/email-verification/confirm", map[string]any{"token": tok})
	if r.status != 409 || r.code() != "token_invalid" {
		t.Fatalf("token reutilizado: %d %s", r.status, r.raw)
	}
	b.mustStatus(b.login("MARIA@exemplo.com", goodPassword, "customer"), 200)
	s := b.session()
	acc, _ := s["account"].(map[string]any)
	sess, _ := s["session"].(map[string]any)
	if acc["status"] != "active" || acc["email"] != "Maria@Exemplo.com" || sess["kind"] != "customer" || b.csrf == "" {
		t.Fatalf("sessão inesperada: %v", s)
	}
}

func TestVerificationTokenExpiresIn24Hours(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	b.mustStatus(b.do("POST", "/v1/auth/register", map[string]any{"name": "Ana", "email": "ana@exemplo.com", "password": goodPassword}), 202)
	tok := e.lastToken("ana@exemplo.com", "Confirme")
	e.clock.Advance(24*time.Hour + time.Second)
	r := b.do("POST", "/v1/auth/email-verification/confirm", map[string]any{"token": tok})
	if r.status != 409 || r.code() != "token_invalid" {
		t.Fatalf("token vencido: %d %s", r.status, r.raw)
	}
	// Reenvio gera novo link válido e invalida o antigo.
	b.mustStatus(b.do("POST", "/v1/auth/email-verification/resend", map[string]any{"email": "ana@exemplo.com"}), 202)
	tok2 := e.lastToken("ana@exemplo.com", "Confirme")
	if tok2 == tok {
		t.Fatal("reenvio deveria gerar token novo")
	}
	b.mustStatus(b.do("POST", "/v1/auth/email-verification/confirm", map[string]any{"token": tok2}), 204)
}

// C-ID-02 — Respostas não revelam se o e-mail existe.
func TestPublicResponsesDoNotRevealAccounts(t *testing.T) {
	e := newEnv(t)
	e.verifiedCustomer("existe@exemplo.com")
	b := e.browser()
	type call struct{ path, field string }
	for _, c := range []call{{"/v1/auth/register", ""}, {"/v1/auth/email-verification/resend", ""}, {"/v1/auth/password-reset/request", ""}} {
		body := func(email string) map[string]any {
			if c.path == "/v1/auth/register" {
				return map[string]any{"name": "X", "email": email, "password": goodPassword}
			}
			return map[string]any{"email": email}
		}
		r1 := b.do("POST", c.path, body("existe@exemplo.com"))
		r2 := b.do("POST", c.path, body("nao-existe@exemplo.com"))
		if r1.status != 202 || r2.status != 202 || string(r1.raw) != string(r2.raw) {
			t.Errorf("%s revela existência: %d %s / %d %s", c.path, r1.status, r1.raw, r2.status, r2.raw)
		}
	}
	// Login: mesma resposta para senha errada e conta inexistente.
	r1 := b.login("existe@exemplo.com", "senha-errada-1", "customer")
	r2 := b.login("nao-existe@exemplo.com", "senha-errada-1", "customer")
	if r1.status != 401 || r2.status != 401 || r1.code() != r2.code() || r1.body["detail"] != r2.body["detail"] {
		t.Errorf("login revela existência: %s / %s", r1.raw, r2.raw)
	}
}

// C-ID-03 — Redefinição: uso único, 30 minutos, revoga todas as sessões; banco guarda só hash.
func TestPasswordResetRevokesAllSessions(t *testing.T) {
	e := newEnv(t)
	celular := e.verifiedCustomer("maria@exemplo.com")
	computador := e.browser()
	computador.mustStatus(computador.login("maria@exemplo.com", goodPassword, "customer"), 200)

	anon := e.browser()
	anon.mustStatus(anon.do("POST", "/v1/auth/password-reset/request", map[string]any{"email": "maria@exemplo.com"}), 202)
	old := e.lastToken("maria@exemplo.com", "Redefinição")
	e.clock.Advance(31 * time.Minute)
	r := anon.do("POST", "/v1/auth/password-reset/confirm", map[string]any{"token": old, "new_password": "nova-senha-456"}) // gitleaks:allow
	if r.status != 409 || r.code() != "token_invalid" {
		t.Fatalf("link com 31 min: %d %s", r.status, r.raw)
	}
	celular.session() // mantém a sessão usada
	anon.mustStatus(anon.do("POST", "/v1/auth/password-reset/request", map[string]any{"email": "maria@exemplo.com"}), 202)
	tok := e.lastToken("maria@exemplo.com", "Redefinição")
	e.clock.Advance(5 * time.Minute)
	anon.mustStatus(anon.do("POST", "/v1/auth/password-reset/confirm", map[string]any{"token": tok, "new_password": "nova-senha-456"}), 204) // gitleaks:allow

	for name, b := range map[string]*browser{"celular": celular, "computador": computador} {
		if r := b.do("POST", "/v1/auth/logout", nil); r.status != 401 {
			t.Errorf("%s: sessão deveria estar revogada, logout = %d %s", name, r.status, r.raw)
		}
	}
	r = anon.do("POST", "/v1/auth/password-reset/confirm", map[string]any{"token": tok, "new_password": "outra-senha-789"}) // gitleaks:allow
	if r.status != 409 || r.code() != "token_invalid" {
		t.Fatalf("link reutilizado: %d %s", r.status, r.raw)
	}
	if r := anon.login("maria@exemplo.com", goodPassword, "customer"); r.status != 401 {
		t.Fatalf("senha antiga ainda funciona: %d", r.status)
	}
	anon.mustStatus(anon.login("maria@exemplo.com", "nova-senha-456", "customer"), 200)

	// Só o hash do token fica no banco.
	sum := sha256.Sum256([]byte(tok))
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM account_token WHERE token_hash = $1 AND consumed_at IS NOT NULL`, sum[:]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hash do token não encontrado (n=%d, err=%v)", n, err)
	}
	assertNotInDatabase(t, e, tok)
}

// Dois usos simultâneos do mesmo link: só um vence.
func TestPasswordResetTokenConcurrentUse(t *testing.T) {
	e := newEnv(t)
	e.verifiedCustomer("ana@exemplo.com")
	b := e.browser()
	b.mustStatus(b.do("POST", "/v1/auth/password-reset/request", map[string]any{"email": "ana@exemplo.com"}), 202)
	tok := e.lastToken("ana@exemplo.com", "Redefinição")
	var wg sync.WaitGroup
	results := make([]int, 6)
	for i := range results {
		wg.Go(func() {
			results[i] = e.browser().do("POST", "/v1/auth/password-reset/confirm", map[string]any{"token": tok, "new_password": "concorrente-" + strings.Repeat("x", i+1)}).status // gitleaks:allow
		})
	}
	wg.Wait()
	ok := 0
	for _, s := range results {
		if s == 204 {
			ok++
		} else if s != 409 {
			t.Fatalf("status inesperado %d", s)
		}
	}
	if ok != 1 {
		t.Fatalf("esperava exatamente 1 sucesso, veio %d (%v)", ok, results)
	}
}

// C-ID-04 — Cadastro público não vira dono; dono inicial por processo controlado.
func TestPublicSignupNeverGrantsStaffAccess(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	r := b.do("POST", "/v1/auth/register", map[string]any{"name": "João", "email": "joao@exemplo.com", "password": goodPassword, "role": "owner"})
	if r.status != 400 || r.code() != "validation_failed" || !strings.Contains(string(r.raw), `"unknown_field"`) {
		t.Fatalf("campo role deveria ser recusado: %d %s", r.status, r.raw)
	}
	e.verifiedCustomer("joao@exemplo.com")
	r = b.login("joao@exemplo.com", goodPassword, "staff")
	if r.status != 403 || r.code() != "forbidden" {
		t.Fatalf("staff sem vínculo: %d %s", r.status, r.raw)
	}

	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	s := loja.owner.session()
	ms, _ := s["memberships"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["role"] != "owner" || s["session"].(map[string]any)["kind"] != "staff" {
		t.Fatalf("dona deveria ter vínculo de dono: %v", s)
	}
}

// C-ID-05 — Convite de profissional: uso único e 24 horas; exige reautenticação.
func TestProfessionalInvite(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	caio := e.professional(loja.id, "Caio")
	path := func(p string) string {
		return "/v1/establishments/" + loja.id.String() + "/professionals/" + p + "/invites"
	}

	r := loja.owner.do("POST", path(beto.String()), map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", newIdemKey())
	if r.status != 403 || r.code() != "reauthentication_required" {
		t.Fatalf("sem reautenticação: %d %s", r.status, r.raw)
	}
	loja.owner.reauth()
	e.clock.Advance(2 * time.Minute)
	key := newIdemKey()
	r = loja.owner.mustStatus(loja.owner.do("POST", path(beto.String()), map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", key), 201)
	if r.data()["status"] != "pending" || r.data()["professional_id"] != beto.String() {
		t.Fatalf("convite: %s", r.raw)
	}
	if strings.Contains(string(r.raw), "token") {
		t.Fatal("token do convite não pode aparecer na resposta")
	}
	// Repetição com a mesma chave: mesma resposta, nenhum convite novo.
	again := loja.owner.mustStatus(loja.owner.do("POST", path(beto.String()), map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", key), 201)
	if again.header.Get("Idempotent-Replayed") != "true" || again.data()["id"] != r.data()["id"] || e.mailCount("beto@exemplo.com") != 1 {
		t.Fatalf("repetição criou outro convite: %s", again.raw)
	}

	tok := e.lastToken("beto@exemplo.com", "Convite")
	anon := e.browser()
	anon.mustStatus(anon.do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Beto", "password": goodPassword}), 204)
	reused := e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Outro", "password": goodPassword})
	if reused.status != 409 || reused.code() != "invite_invalid" {
		t.Fatalf("convite reutilizado: %d %s", reused.status, reused.raw)
	}
	betoB := e.browser()
	betoB.mustStatus(betoB.login("beto@exemplo.com", goodPassword, "staff"), 200)
	ms := betoB.session()["memberships"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["role"] != "professional" || ms[0].(map[string]any)["professional_id"] != beto.String() {
		t.Fatalf("vínculo de profissional: %v", ms)
	}
	// Profissional já com acesso não recebe novo convite.
	r = loja.owner.do("POST", path(beto.String()), map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", newIdemKey())
	if r.status != 409 || r.code() != "invalid_transition" {
		t.Fatalf("convite para quem já tem acesso: %d %s", r.status, r.raw)
	}

	// Convite com 25 horas: mesma mensagem de convite usado.
	loja.owner.mustStatus(loja.owner.do("POST", path(caio.String()), map[string]any{"email": "caio@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	tokCaio := e.lastToken("caio@exemplo.com", "Convite")
	e.clock.Advance(25 * time.Hour)
	r2 := e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tokCaio, "name": "Caio", "password": goodPassword})
	if r2.status != 409 || r2.code() != "invite_invalid" || r2.body["detail"] != reused.body["detail"] {
		t.Fatalf("convite vencido: %d %s", r2.status, r2.raw)
	}
}

func TestInviteRevokeAndReinvite(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	caio := e.professional(loja.id, "Caio")
	loja.owner.reauth()
	base := "/v1/establishments/" + loja.id.String()
	first := loja.owner.mustStatus(loja.owner.do("POST", base+"/professionals/"+caio.String()+"/invites", map[string]any{"email": "caio@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	tok1 := e.lastToken("caio@exemplo.com", "Convite")
	// Novo convite revoga o anterior.
	loja.owner.mustStatus(loja.owner.do("POST", base+"/professionals/"+caio.String()+"/invites", map[string]any{"email": "caio@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	if r := e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok1, "name": "Caio", "password": goodPassword}); r.code() != "invite_invalid" {
		t.Fatalf("convite substituído ainda vale: %s", r.raw)
	}
	tok2 := e.lastToken("caio@exemplo.com", "Convite")
	firstID := first.data()["id"].(string)
	r := loja.owner.do("POST", base+"/invites/"+firstID+"/revoke", nil, "Idempotency-Key", newIdemKey())
	if r.status != 409 || r.code() != "invalid_transition" || r.body["current"].(map[string]any)["status"] != "revoked" {
		t.Fatalf("revogar convite já revogado: %d %s", r.status, r.raw)
	}
	var secondID string
	_ = e.pool.QueryRow(context.Background(), `SELECT id::text FROM invite WHERE professional_id = $1 AND status = 'pending'`, caio).Scan(&secondID)
	r = loja.owner.mustStatus(loja.owner.do("POST", base+"/invites/"+secondID+"/revoke", nil, "Idempotency-Key", newIdemKey()), 200)
	if r.data()["status"] != "revoked" {
		t.Fatalf("revogação: %s", r.raw)
	}
	if r := e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok2, "name": "Caio", "password": goodPassword}); r.code() != "invite_invalid" {
		t.Fatalf("convite revogado ainda vale: %s", r.raw)
	}
}

// Aceite com conta existente exige a senha dela; conta pendente criada por terceiro é assumida
// por quem prova o e-mail (o convite chegou a ele).
func TestInviteWithExistingAccounts(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	caio := e.professional(loja.id, "Caio")
	loja.owner.reauth()
	base := "/v1/establishments/" + loja.id.String() + "/professionals/"

	e.verifiedCustomer("beto@exemplo.com")
	loja.owner.mustStatus(loja.owner.do("POST", base+beto.String()+"/invites", map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	tok := e.lastToken("beto@exemplo.com", "Convite")
	r := e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "password": "senha-errada-999"}) // gitleaks:allow
	if r.status != 401 || r.code() != "invalid_credentials" {
		t.Fatalf("senha errada da conta existente: %d %s", r.status, r.raw)
	}
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "password": goodPassword}), 204)

	// Alguém cadastrou o e-mail do Caio sem conseguir verificar.
	intruso := e.browser()
	intruso.mustStatus(intruso.do("POST", "/v1/auth/register", map[string]any{"name": "Intruso", "email": "caio@exemplo.com", "password": "senha-do-intruso"}), 202) // gitleaks:allow
	loja.owner.mustStatus(loja.owner.do("POST", base+caio.String()+"/invites", map[string]any{"email": "caio@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	tok = e.lastToken("caio@exemplo.com", "Convite")
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Caio", "password": "senha-do-caio-1"}), 204) // gitleaks:allow
	if r := intruso.login("caio@exemplo.com", "senha-do-intruso", "customer"); r.status != 401 {
		t.Fatalf("senha do intruso não pode valer: %d", r.status)
	}
	caioB := e.browser()
	caioB.mustStatus(caioB.login("caio@exemplo.com", "senha-do-caio-1", "staff"), 200)
}

// Dois aceites simultâneos do mesmo convite: só um vínculo.
func TestInviteConcurrentAccept(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	loja.owner.reauth()
	loja.owner.mustStatus(loja.owner.do("POST", "/v1/establishments/"+loja.id.String()+"/professionals/"+beto.String()+"/invites",
		map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	tok := e.lastToken("beto@exemplo.com", "Convite")
	var wg sync.WaitGroup
	statuses := make([]int, 5)
	for i := range statuses {
		wg.Go(func() {
			statuses[i] = e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Beto", "password": goodPassword}).status
		})
	}
	wg.Wait()
	var ok, memberships int
	for _, s := range statuses {
		if s == 204 {
			ok++
		}
	}
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM staff_membership WHERE professional_id = $1`, beto).Scan(&memberships)
	if ok != 1 || memberships != 1 {
		t.Fatalf("aceites=%d vínculos=%d (%v)", ok, memberships, statuses)
	}
}

// C-ID-06 — Expiração de sessões de cliente e de equipe (relógio controlável).
func TestSessionExpiry(t *testing.T) {
	e := newEnv(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	e.clock.Advance(6 * 24 * time.Hour)
	if maria.session()["account"] == nil {
		t.Fatal("6 dias sem uso: sessão deveria continuar")
	}
	e.clock.Advance(7*24*time.Hour + time.Second)
	if maria.session()["account"] != nil {
		t.Fatal("7 dias sem uso: sessão deveria expirar")
	}
	if r := maria.do("POST", "/v1/auth/logout", nil); r.status != 401 {
		t.Fatalf("sessão expirada: %d", r.status)
	}

	diaria := e.browser()
	diaria.mustStatus(diaria.login("maria@exemplo.com", goodPassword, "customer"), 200)
	for range 29 {
		e.clock.Advance(24 * time.Hour)
		if diaria.session()["account"] == nil {
			t.Fatal("uso diário: sessão não deveria expirar antes de 30 dias")
		}
	}
	e.clock.Advance(24*time.Hour + time.Second)
	if diaria.session()["account"] != nil {
		t.Fatal("30 dias desde o login: sessão deveria expirar mesmo com uso diário")
	}

	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	for range 11 {
		e.clock.Advance(time.Hour)
		if loja.owner.session()["account"] == nil {
			t.Fatal("equipe: sessão não deveria expirar antes de 12 horas")
		}
	}
	e.clock.Advance(time.Hour)
	if loja.owner.session()["account"] != nil {
		t.Fatal("equipe: sessão deveria expirar 12 horas após o login, mesmo em uso")
	}
}

// C-ID-07 — Logout e troca de senha revogam no servidor.
func TestLogoutAndPasswordChangeRevoke(t *testing.T) {
	e := newEnv(t)
	celular := e.verifiedCustomer("maria@exemplo.com")
	computador := e.browser()
	computador.mustStatus(computador.login("maria@exemplo.com", goodPassword, "customer"), 200)
	tablet := e.browser()
	tablet.mustStatus(tablet.login("maria@exemplo.com", goodPassword, "customer"), 200)

	stolen := computador.cookie("__Host-fila_session")
	r := computador.mustStatus(computador.do("POST", "/v1/auth/logout", nil), 204)
	if !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout deveria apagar o cookie: %q", r.header.Get("Set-Cookie"))
	}
	// Cookie antigo reenviado manualmente.
	replay := e.browser()
	u := mustURL(e.srv.URL)
	replay.client.Jar.SetCookies(u, []*http.Cookie{{Name: stolen.Name, Value: stolen.Value, Path: "/", Secure: true}})
	if replay.session()["account"] != nil {
		t.Fatal("cookie revogado ainda autentica")
	}

	r = celular.do("POST", "/v1/auth/password/change", map[string]any{"current_password": "errada-123456", "new_password": "nova-senha-456"}) // gitleaks:allow
	if r.status != 401 || r.code() != "invalid_credentials" {
		t.Fatalf("troca com senha atual errada: %d %s", r.status, r.raw)
	}
	celular.mustStatus(celular.do("POST", "/v1/auth/password/change", map[string]any{"current_password": goodPassword, "new_password": "nova-senha-456"}), 204) // gitleaks:allow
	if celular.session()["account"] == nil {
		t.Fatal("a sessão que trocou a senha deve continuar")
	}
	if tablet.session()["account"] != nil {
		t.Fatal("as outras sessões devem ser revogadas")
	}
}

// C-ID-08 — Ações sensíveis exigem reautenticação recente (10 minutos).
func TestReauthenticationWindow(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	p1, p2 := e.professional(loja.id, "Beto"), e.professional(loja.id, "Caio")
	invite := func(p string) resp {
		return loja.owner.do("POST", "/v1/establishments/"+loja.id.String()+"/professionals/"+p+"/invites",
			map[string]any{"email": p[:8] + "@exemplo.com"}, "Idempotency-Key", newIdemKey())
	}
	e.clock.Advance(time.Hour)
	if r := invite(p1.String()); r.code() != "reauthentication_required" {
		t.Fatalf("sem reautenticar: %s", r.raw)
	}
	r := loja.owner.do("POST", "/v1/auth/reauthenticate", map[string]any{"password": "errada-000000"}) // gitleaks:allow
	if r.status != 401 {
		t.Fatalf("reautenticação com senha errada: %d", r.status)
	}
	r = loja.owner.mustStatus(loja.owner.do("POST", "/v1/auth/reauthenticate", map[string]any{"password": goodPassword}), 200)
	if r.data()["session"].(map[string]any)["reauth_valid_until"] == nil {
		t.Fatalf("reauth_valid_until ausente: %s", r.raw)
	}
	e.clock.Advance(9 * time.Minute)
	if r := invite(p1.String()); r.status != 201 {
		t.Fatalf("dentro de 10 min: %d %s", r.status, r.raw)
	}
	e.clock.Advance(2 * time.Minute)
	if r := invite(p2.String()); r.code() != "reauthentication_required" {
		t.Fatalf("11 minutos depois: %s", r.raw)
	}
}

// C-ID-09 — Força bruta limitada por conta, não só por IP.
func TestBruteForceLimitedPerAccount(t *testing.T) {
	e := newEnv(t)
	e.verifiedCustomer("maria@exemplo.com")
	e.verifiedCustomer("lia@exemplo.com")
	for i := range 10 {
		b := e.browser() // IP diferente a cada tentativa
		if r := b.login("maria@exemplo.com", "tentativa-errada-"+strings.Repeat("x", i), "customer"); r.status != 401 {
			t.Fatalf("tentativa %d: %d", i, r.status)
		}
	}
	b := e.browser()
	r := b.login("maria@exemplo.com", goodPassword, "customer")
	if r.status != 429 || r.code() != "rate_limited" || r.header.Get("Retry-After") == "" {
		t.Fatalf("limite por conta não aplicado (mesmo com a senha certa e outro IP): %d %s", r.status, r.raw)
	}
	b.mustStatus(b.login("lia@exemplo.com", goodPassword, "customer"), 200)
	e.clock.Advance(15 * time.Minute)
	e.browser().mustStatus(e.browser().login("maria@exemplo.com", goodPassword, "customer"), 200)
	if strings.Contains(e.logs.String(), "tentativa-errada") || strings.Contains(e.logs.String(), goodPassword) {
		t.Fatal("senha tentada apareceu no log")
	}
}

// Limite por IP: muitas contas diferentes a partir do mesmo endereço.
func TestBruteForceLimitedPerIP(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	for i := range 50 {
		b.login("pessoa"+strings.Repeat("a", i%5)+string(rune('a'+i%26))+"@exemplo.com", "qualquer-senha", "customer")
	}
	if r := b.login("outra@exemplo.com", "qualquer-senha", "customer"); r.status != 429 {
		t.Fatalf("limite por IP não aplicado: %d", r.status)
	}
}

// C-SEG-09 — CSRF e origem; cookies Secure/HttpOnly/SameSite=Lax com prefixo __Host-.
func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	c := maria.cookie("__Host-fila_session")
	if c == nil {
		t.Fatal("cookie __Host-fila_session ausente")
	}
	login := e.browser()
	lr := login.do("POST", "/v1/auth/login", map[string]any{"email": "maria@exemplo.com", "password": goodPassword, "context": "customer"})
	sc := lr.header.Get("Set-Cookie")
	for _, attr := range []string{"__Host-fila_session=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(sc, attr) {
			t.Errorf("Set-Cookie sem %s: %q", attr, sc)
		}
	}
	if strings.Contains(sc, "Domain=") {
		t.Error("cookie __Host- não pode ter Domain")
	}

	// Outra origem com o cookie da Maria.
	maria.origin = "https://site-malicioso.exemplo"
	if r := maria.do("POST", "/v1/auth/logout", nil); r.status != 403 || r.code() != "origin_not_allowed" {
		t.Fatalf("outra origem: %d %s", r.status, r.raw)
	}
	maria.origin = ""
	if r := maria.do("POST", "/v1/auth/logout", nil); r.status != 403 || r.code() != "origin_not_allowed" {
		t.Fatalf("sem Origin: %d %s", r.status, r.raw)
	}
	maria.origin = origin
	csrf := maria.csrf
	maria.csrf = ""
	if r := maria.do("POST", "/v1/auth/logout", nil); r.status != 403 || r.code() != "csrf_failed" {
		t.Fatalf("sem CSRF: %d %s", r.status, r.raw)
	}
	maria.csrf = "token-falso"
	if r := maria.do("POST", "/v1/auth/logout", nil); r.status != 403 || r.code() != "csrf_failed" {
		t.Fatalf("CSRF falso: %d %s", r.status, r.raw)
	}
	// Token de outra sessão também não serve.
	maria.csrf = login.csrf
	if r := maria.do("POST", "/v1/auth/logout", nil); r.code() != "csrf_failed" {
		t.Fatalf("CSRF de outra sessão: %s", r.raw)
	}
	maria.csrf = csrf
	maria.mustStatus(maria.do("POST", "/v1/auth/logout", nil), 204)
}

// C-SEG-05 / C-SEG-01 — papel vem da sessão; escalada e acesso a outra loja são negados.
func TestRoleEscalationAndCrossEstablishment(t *testing.T) {
	e := newEnv(t)
	lojaA := e.shop("loja-a", "ana@exemplo.com")
	lojaB := e.shop("loja-b", "bia@exemplo.com")
	betoA := e.professional(lojaA.id, "Beto")
	caioB := e.professional(lojaB.id, "Caio")

	// Beto vira profissional da loja A.
	lojaA.owner.reauth()
	lojaA.owner.mustStatus(lojaA.owner.do("POST", "/v1/establishments/"+lojaA.id.String()+"/professionals/"+betoA.String()+"/invites",
		map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	tok := e.lastToken("beto@exemplo.com", "Convite")
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Beto", "password": goodPassword}), 204)

	invitePath := func(est, prof string) string {
		return "/v1/establishments/" + est + "/professionals/" + prof + "/invites"
	}
	body := map[string]any{"email": "x@exemplo.com"}

	// Sem sessão → 401.
	anon := e.browser()
	if r := anon.do("POST", invitePath(lojaA.id.String(), betoA.String()), body, "Idempotency-Key", newIdemKey()); r.status != 401 {
		t.Fatalf("sem sessão: %d %s", r.status, r.raw)
	}
	// Cliente sem vínculo → 404 (não revela a loja).
	cliente := e.verifiedCustomer("cliente@exemplo.com")
	if r := cliente.do("POST", invitePath(lojaA.id.String(), betoA.String()), body, "Idempotency-Key", newIdemKey()); r.status != 404 {
		t.Fatalf("cliente: %d %s", r.status, r.raw)
	}
	// Profissional (staff) da loja → 403.
	beto := e.browser()
	beto.mustStatus(beto.login("beto@exemplo.com", goodPassword, "staff"), 200)
	beto.reauth()
	if r := beto.do("POST", invitePath(lojaA.id.String(), betoA.String()), body, "Idempotency-Key", newIdemKey()); r.status != 403 || r.code() != "forbidden" {
		t.Fatalf("profissional: %d %s", r.status, r.raw)
	}
	// Dona logada como cliente → 403 (vínculo só vale com sessão staff).
	anaCliente := e.browser()
	anaCliente.mustStatus(anaCliente.login("ana@exemplo.com", goodPassword, "customer"), 200)
	anaCliente.reauth()
	if r := anaCliente.do("POST", invitePath(lojaA.id.String(), betoA.String()), body, "Idempotency-Key", newIdemKey()); r.status != 403 {
		t.Fatalf("dona em sessão de cliente: %d %s", r.status, r.raw)
	}
	// Dona da loja A na loja B, e com profissional da B pela rota da A → 404.
	if r := lojaA.owner.do("POST", invitePath(lojaB.id.String(), caioB.String()), body, "Idempotency-Key", newIdemKey()); r.status != 404 {
		t.Fatalf("outra loja: %d %s", r.status, r.raw)
	}
	if r := lojaA.owner.do("POST", invitePath(lojaA.id.String(), caioB.String()), body, "Idempotency-Key", newIdemKey()); r.status != 404 {
		t.Fatalf("profissional de outra loja pela rota própria: %d %s", r.status, r.raw)
	}
	// Convite da loja B revogado pela rota da loja A → 404.
	lojaB.owner.reauth()
	inv := lojaB.owner.mustStatus(lojaB.owner.do("POST", invitePath(lojaB.id.String(), caioB.String()), map[string]any{"email": "caio@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201)
	if r := lojaA.owner.do("POST", "/v1/establishments/"+lojaA.id.String()+"/invites/"+inv.data()["id"].(string)+"/revoke", nil, "Idempotency-Key", newIdemKey()); r.status != 404 {
		t.Fatalf("revogar convite de outra loja: %d %s", r.status, r.raw)
	}
	// Campos de papel no corpo são recusados.
	if r := lojaA.owner.do("POST", invitePath(lojaA.id.String(), betoA.String()), map[string]any{"email": "x@exemplo.com", "role": "owner"}, "Idempotency-Key", newIdemKey()); r.status != 400 {
		t.Fatalf("role no corpo: %d %s", r.status, r.raw)
	}
	if r := beto.do("POST", "/v1/auth/login", map[string]any{"email": "beto@exemplo.com", "password": goodPassword, "context": "owner"}); r.status != 400 {
		t.Fatalf("contexto inventado: %d %s", r.status, r.raw)
	}
}

func mustURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}
