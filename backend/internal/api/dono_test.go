package api_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/estabelecimento"
)

// Sessão única do dono (ADR-0027). Os testes usam PostgreSQL real.

func conflictOf(t *testing.T, r resp) (challenge string, other map[string]any) {
	t.Helper()
	if r.status != 409 || r.code() != "owner_session_active" {
		t.Fatalf("esperado 409 owner_session_active, veio %d %s", r.status, r.raw)
	}
	cur, _ := r.body["current"].(map[string]any)
	challenge, _ = cur["challenge"].(string)
	other, _ = cur["other_session"].(map[string]any)
	if challenge == "" || other == nil {
		t.Fatalf("conflito sem desafio: %s", r.raw)
	}
	return challenge, other
}

// pendencias é uma leitura protegida só de dono.
func (b *browser) pendencias(est uuid.UUID) resp {
	b.e.t.Helper()
	return b.do("GET", "/v1/establishments/"+est.String()+"/pendencies", nil)
}

// mutacao de dono (troca o nome da loja) como prova de "ainda tem poderes de dono".
func (b *browser) renameShop(est uuid.UUID, name string) resp {
	b.e.t.Helper()
	cur := b.do("GET", "/v1/establishments/"+est.String(), nil)
	v := num(cur.data()["version"])
	b.session()
	return b.cmd("PATCH", "/v1/establishments/"+est.String(), map[string]any{"name": name, "expected_version": v})
}

func (e *env) validOwnerSessions(t *testing.T, email string) int {
	t.Helper()
	return e.count(t, `SELECT count(*) FROM account_session s JOIN account a ON a.id = s.account_id
		WHERE a.email_normalized = '`+email+`' AND s.kind = 'staff' AND s.revoked_at IS NULL`)
}

// Web ativa → tentativa em outro aparelho → cancelar: a original continua; a nova não recebe nada.
func TestOwnerSessionConflictThenCancel(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	web := loja.owner
	pc := e.browser()
	r := pc.do("POST", "/v1/auth/login", map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"})
	challenge, other := conflictOf(t, r)
	if other["channel"] != "unknown" {
		t.Fatalf("canal sem prova deve ser unknown: %v", other)
	}
	if pc.cookie("__Host-fila_session") != nil {
		t.Fatal("tentativa em conflito recebeu cookie de sessão")
	}
	if pc.cookie("__Host-fila_owner_attempt") == nil {
		t.Fatal("tentativa sem cookie de tentativa")
	}
	if s := pc.session(); s["account"] != nil {
		t.Fatalf("tentativa em conflito ficou autenticada: %v", s)
	}
	if r := pc.pendencias(loja.id); r.status != 401 {
		t.Fatalf("tentativa leu dados da loja: %d", r.status)
	}
	pc.mustStatus(pc.do("POST", "/v1/auth/owner-session/cancel", map[string]any{"challenge": challenge}), 204)
	// Cancelado: o desafio não serve mais.
	if r := pc.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": challenge}); r.code() != "owner_session_challenge_invalid" {
		t.Fatalf("desafio cancelado ainda vale: %d %s", r.status, r.raw)
	}
	web.mustStatus(web.pendencias(loja.id), 200)
	web.mustStatus(web.renameShop(loja.id, "Loja A renomeada"), 200)
	if n := e.validOwnerSessions(t, "ana@exemplo.com"); n != 1 {
		t.Fatalf("sessões de dono válidas: %d", n)
	}
}

// Web ativa → confirmar no outro aparelho: o novo assume; a web perde leitura, mutação e SSE, e
// a mensagem explica o motivo. O logout atrasado da sessão antiga não derruba a nova.
func TestOwnerSessionTakeover(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	web := loja.owner
	events := web.stream(loja.id, "owner", "")
	defer events.close()
	events.expect("stream.snapshot")

	pc := e.browser()
	challenge, _ := conflictOf(t, pc.do("POST", "/v1/auth/login", map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"}))
	r := pc.mustStatus(pc.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": challenge}), 200)
	if r.data()["session"].(map[string]any)["owner_authority"] != true {
		t.Fatalf("nova sessão sem autoridade de dono: %s", r.raw)
	}
	pc.session()
	pc.mustStatus(pc.pendencias(loja.id), 200)

	if ended := events.expect("stream.session_ended"); ended.data()["reason"] != "owner_session_replaced" {
		t.Fatalf("SSE antigo: %s", ended.raw)
	}
	if r := web.pendencias(loja.id); r.status != 401 || r.code() != "owner_session_replaced" {
		t.Fatalf("web antiga ainda lê: %d %s", r.status, r.raw)
	}
	if r := web.do("PATCH", "/v1/establishments/"+loja.id.String(), map[string]any{"name": "x", "expected_version": 1}); r.status != 401 && r.status != 403 {
		t.Fatalf("web antiga ainda altera: %d %s", r.status, r.raw)
	}
	s := web.do("GET", "/v1/session", nil).data()
	if s["account"] != nil || s["ended_reason"] != "owner_session_replaced" {
		t.Fatalf("sessão antiga: %v", s)
	}
	// Logout tardio da sessão antiga: não encerra a nova.
	web.do("POST", "/v1/auth/logout", nil)
	pc.mustStatus(pc.pendencias(loja.id), 200)
	pc.mustStatus(pc.renameShop(loja.id, "Loja A no PC"), 200)
	if n := e.validOwnerSessions(t, "ana@exemplo.com"); n != 1 {
		t.Fatalf("sessões de dono válidas: %d", n)
	}
	if e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-SS-07' AND to_state = 'revoked'`) != 1 {
		t.Fatal("auditoria da transferência")
	}
	// Repetir a confirmação (resposta perdida) reentrega o acesso sem nova revogação.
	again := pc.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": challenge})
	if again.status != 200 {
		t.Fatalf("reconciliação da mesma tentativa: %d %s", again.status, again.raw)
	}
	pc.session()
	pc.mustStatus(pc.pendencias(loja.id), 200)
	if e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-SS-07' AND to_state = 'revoked'`) != 1 {
		t.Fatal("a repetição revogou de novo")
	}
}

// Dois logins simultâneos sem sessão prévia: no máximo uma sessão de dono depois do commit.
func TestOwnerConcurrentLogins(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	loja.owner.mustStatus(loja.owner.do("POST", "/v1/auth/logout", nil), 204)
	const n = 8
	browsers := make([]*browser, n)
	results := make([]resp, n)
	for i := range browsers {
		browsers[i] = e.browser()
	}
	var wg sync.WaitGroup
	for i := range browsers {
		wg.Go(func() {
			results[i] = browsers[i].do("POST", "/v1/auth/login", map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"})
		})
	}
	wg.Wait()
	ok, conflicts := 0, 0
	for _, r := range results {
		switch {
		case r.status == 200:
			ok++
		case r.status == 409 && r.code() == "owner_session_active":
			conflicts++
		default:
			t.Fatalf("resposta inesperada: %d %s", r.status, r.raw)
		}
	}
	if ok != 1 || conflicts != n-1 {
		t.Fatalf("vencedores %d, conflitos %d", ok, conflicts)
	}
	if v := e.validOwnerSessions(t, "ana@exemplo.com"); v != 1 {
		t.Fatalf("sessões de dono válidas: %d", v)
	}
}

// Dois aparelhos confirmam a troca contra a MESMA sessão: só um vence; o atrasado não derruba o
// vencedor. Um desafio emitido antes de outra troca fica desatualizado.
func TestOwnerTakeoverRaceAndStaleChallenge(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	b1, b2, b3 := e.browser(), e.browser(), e.browser()
	login := map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"}
	c1, _ := conflictOf(t, b1.do("POST", "/v1/auth/login", login))
	c2, _ := conflictOf(t, b2.do("POST", "/v1/auth/login", login))
	c3, _ := conflictOf(t, b3.do("POST", "/v1/auth/login", login))
	var r1, r2 resp
	var wg sync.WaitGroup
	wg.Go(func() { r1 = b1.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": c1}) })
	wg.Go(func() { r2 = b2.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": c2}) })
	wg.Wait()
	winner, loser := b1, r2
	if r2.status == 200 {
		winner, loser = b2, r1
	}
	if (r1.status == 200) == (r2.status == 200) {
		t.Fatalf("esperado exatamente um vencedor: %d / %d", r1.status, r2.status)
	}
	if loser.code() != "owner_session_challenge_invalid" {
		t.Fatalf("perdedor: %d %s", loser.status, loser.raw)
	}
	winner.session()
	winner.mustStatus(winner.pendencias(loja.id), 200)
	// b3 viu a vaga ANTES da troca: o aviso está desatualizado e não derruba o vencedor.
	if r := b3.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": c3}); r.code() != "owner_session_challenge_invalid" {
		t.Fatalf("desafio desatualizado: %d %s", r.status, r.raw)
	}
	winner.mustStatus(winner.pendencias(loja.id), 200)
	loja.owner.session()
	if r := loja.owner.pendencias(loja.id); r.status != 401 {
		t.Fatalf("sessão original: %d", r.status)
	}
	if v := e.validOwnerSessions(t, "ana@exemplo.com"); v != 1 {
		t.Fatalf("sessões de dono válidas: %d", v)
	}
}

// Mesmo cookie (abas, F5, novo login no mesmo navegador) não disputa a vaga; desafio vencido e
// desafio de outra tentativa (sem o cookie da tentativa) são recusados.
func TestOwnerSameCookieAndChallengeBinding(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	web := loja.owner
	for range 3 { // "várias abas" = mesmo cookie
		web.mustStatus(web.pendencias(loja.id), 200)
	}
	web.mustStatus(web.login(loja.email, goodPassword, "staff"), 200) // renovar no mesmo navegador
	web.mustStatus(web.pendencias(loja.id), 200)

	pc := e.browser()
	challenge, _ := conflictOf(t, pc.do("POST", "/v1/auth/login", map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"}))
	ladrao := e.browser() // tem o desafio, mas não o cookie da tentativa
	if r := ladrao.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": challenge}); r.code() != "owner_session_challenge_invalid" {
		t.Fatalf("desafio sem a tentativa: %d %s", r.status, r.raw)
	}
	e.clock.Advance(3 * time.Minute)
	if r := pc.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": challenge}); r.code() != "owner_session_challenge_invalid" {
		t.Fatalf("desafio vencido: %d %s", r.status, r.raw)
	}
	web.mustStatus(web.pendencias(loja.id), 200)
}

// Duas contas donas da mesma loja trabalham ao mesmo tempo; um dono alterna entre as próprias
// lojas sem abrir outra sessão. Conta dona de A e profissional de B não entra pela rota de
// profissional com uma segunda sessão.
func TestOwnerSlotIsPerAccount(t *testing.T) {
	e := newEnv(t)
	a := e.shop("loja-a", "ana@exemplo.com")
	b := e.shop("loja-b", "bia@exemplo.com")
	// Ana também é dona de B (vínculo direto no banco, como faria o processo controlado).
	var anaID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM account WHERE email_normalized = 'ana@exemplo.com'`).Scan(&anaID); err != nil {
		t.Fatal(err)
	}
	if _, err := estabelecimento.AddMembership(context.Background(), e.pool, b.id, anaID, estabelecimento.RoleOwner, nil, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	a.owner.mustStatus(a.owner.pendencias(a.id), 200)
	a.owner.mustStatus(a.owner.pendencias(b.id), 200) // troca de loja, mesma sessão
	b.owner.mustStatus(b.owner.pendencias(b.id), 200) // outra conta dona da mesma loja, ao mesmo tempo
	a.owner.mustStatus(a.owner.pendencias(a.id), 200)

	// Bia vira profissional de A: conta mista. Login de equipe em outro navegador → conflito.
	prof := e.professional(a.id, "Bia na A")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO staff_membership (establishment_id, account_id, role, professional_id, created_at, updated_at)
		SELECT $1, id, 'professional', $2, now(), now() FROM account WHERE email_normalized = 'bia@exemplo.com'`, a.id, prof); err != nil {
		t.Fatal(err)
	}
	outro := e.browser()
	conflictOf(t, outro.do("POST", "/v1/auth/login", map[string]any{"email": b.email, "password": goodPassword, "context": "staff"}))
	// Sessão de cliente não ocupa a vaga.
	cli := e.browser()
	cli.mustStatus(cli.login(b.email, goodPassword, "customer"), 200)
	b.owner.mustStatus(b.owner.pendencias(b.id), 200)
}

// Expiração, troca e redefinição de senha, vínculo removido e sessões anteriores à vaga.
func TestOwnerSlotLifecycle(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	// Troca de senha mantém a sessão atual (ocupante) e encerra as outras.
	loja.owner.mustStatus(loja.owner.do("POST", "/v1/auth/password/change", map[string]any{"current_password": goodPassword,
		"new_password": goodPassword + "-nova"}), 204)
	loja.owner.session()
	loja.owner.mustStatus(loja.owner.pendencias(loja.id), 200)
	// Sessão vencida (12 h) libera a vaga: novo login entra sem conflito.
	e.clock.Advance(13 * time.Hour)
	novo := e.browser()
	novo.mustStatus(novo.login(loja.email, goodPassword+"-nova", "staff"), 200)
	novo.mustStatus(novo.pendencias(loja.id), 200)

	// Sessão de equipe que NÃO ocupa a vaga (ex.: aberta antes de a conta virar dona) não exerce
	// poderes de dono: simulada tirando a sessão da vaga direto no banco.
	if _, err := e.pool.Exec(context.Background(), `UPDATE owner_session_slot SET session_id = NULL`); err != nil {
		t.Fatal(err)
	}
	if r := novo.pendencias(loja.id); r.status != 403 || r.code() != "owner_session_required" {
		t.Fatalf("sessão fora da vaga: %d %s", r.status, r.raw)
	}
	// Vínculo de dono removido: a sessão perde o acesso de dono na hora.
	novo.mustStatus(novo.login(loja.email, goodPassword+"-nova", "staff"), 200)
	novo.mustStatus(novo.pendencias(loja.id), 200)
	if _, err := e.pool.Exec(context.Background(), `UPDATE staff_membership SET status = 'inactive' WHERE establishment_id = $1 AND role = 'owner'`, loja.id); err != nil {
		t.Fatal(err)
	}
	if r := novo.pendencias(loja.id); r.status != 404 {
		t.Fatalf("sem vínculo de dono: %d", r.status)
	}
}

// Mutação da sessão antiga já em andamento × troca: a troca espera a mutação terminar (FOR SHARE ×
// FOR UPDATE na vaga); depois dela, a sessão antiga não efetiva mais nada.
func TestOwnerTakeoverWaitsForInFlightMutation(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	ctx := context.Background()
	// "Mutação em andamento": uma transação segurando a vaga em modo compartilhado, como a guarda.
	pc := e.browser()
	challenge, _ := conflictOf(t, pc.do("POST", "/v1/auth/login", map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"}))
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, `SELECT 1 FROM owner_session_slot o JOIN account a ON a.id = o.account_id
		WHERE a.email_normalized = 'ana@exemplo.com' FOR SHARE OF o`); err != nil {
		t.Fatal(err)
	}
	done := make(chan resp, 1)
	go func() {
		done <- pc.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": challenge})
	}()
	select {
	case r := <-done:
		t.Fatalf("a troca não esperou a mutação em andamento: %d", r.status)
	case <-time.After(400 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil { // a mutação "confirma" antes da troca
		t.Fatal(err)
	}
	r := <-done
	if r.status != 200 {
		t.Fatalf("troca: %d %s", r.status, r.raw)
	}
	if r := loja.owner.renameShop(loja.id, "depois da troca"); r.status != 401 {
		t.Fatalf("mutação da sessão antiga depois da troca: %d %s", r.status, r.raw)
	}
}
