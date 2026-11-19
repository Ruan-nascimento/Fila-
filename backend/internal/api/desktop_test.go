package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
)

// Programa desktop: ativação, vínculo à sessão de dono e prova DPoP (ADR-0027).

// pcDono simula o componente nativo: chave P-256 própria, sem cookies.
type pcDono struct {
	e     *env
	key   *ecdsa.PrivateKey
	b     *browser
	token string
	// skew desloca o iat (relógio do PC errado).
	skew time.Duration
	last string // última prova enviada (para testar reutilização)
}

func (e *env) pc() *pcDono {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	return &pcDono{e: e, key: k, b: e.browser()}
}

func (p *pcDono) proof(method, path, token string) string {
	p.e.t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: p.key},
		(&jose.SignerOptions{EmbedJWK: true}).WithType("dpop+jwt"))
	if err != nil {
		p.e.t.Fatal(err)
	}
	jti := make([]byte, 16)
	_, _ = rand.Read(jti)
	claims := map[string]any{"jti": hex.EncodeToString(jti), "htm": method, "htu": origin + strings.SplitN(path, "?", 2)[0],
		"iat": p.e.clock.Now().Add(p.skew).Unix()}
	if token != "" {
		sum := sha256.Sum256([]byte(token))
		claims["ath"] = base64.RawURLEncoding.EncodeToString(sum[:])
	}
	raw, _ := json.Marshal(claims)
	jws, err := signer.Sign(raw)
	if err != nil {
		p.e.t.Fatal(err)
	}
	out, err := jws.CompactSerialize()
	if err != nil {
		p.e.t.Fatal(err)
	}
	return out
}

func (p *pcDono) call(method, path string, body any) resp {
	p.e.t.Helper()
	p.last = p.proof(method, path, p.token)
	h := []string{"DPoP", p.last}
	if p.token != "" {
		h = append(h, "Authorization", "DPoP "+p.token)
	}
	return p.b.do(method, path, body, h...)
}

func (p *pcDono) callWithProof(method, path, proof string) resp {
	return p.b.do(method, path, nil, "DPoP", proof, "Authorization", "DPoP "+p.token)
}

// activate emite o código como o fila-admin e ativa a instalação.
func (p *pcDono) activate(email, label string) {
	p.e.t.Helper()
	act, err := p.e.api.Identity.IssueDesktopActivation(context.Background(), email, label)
	if err != nil {
		p.e.t.Fatal(err)
	}
	p.b.mustStatus(p.call("POST", "/v1/desktop/installations/activate", map[string]any{"code": act.Code}), 201)
}

// link: a gestão (cookie de dono) pede o ticket; o nativo troca pelo token.
func (p *pcDono) link(owner *browser) resp {
	p.e.t.Helper()
	owner.session()
	t := owner.mustStatus(owner.do("POST", "/v1/desktop/link-tickets", nil), 201).data()["ticket"].(string)
	r := p.call("POST", "/v1/desktop/bindings", map[string]any{"ticket": t})
	if r.status == 201 {
		p.token = r.data()["access_token"].(string)
	}
	return r
}

func analyticsPath(est uuid.UUID, rest string) string {
	return "/v1/desktop/analytics/establishments/" + est.String() + rest
}

func TestDesktopActivationAndProof(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	day := "from=2026-11-12&to=2026-11-12"
	sum := analyticsPath(loja.id, "/summary?"+day)

	// Dono no navegador comum: gestão normal, mas o analítico é recusado (sem prova de instalação).
	loja.owner.mustStatus(loja.owner.pendencias(loja.id), 200)
	if r := loja.owner.do("GET", sum, nil); r.status != 401 || r.code() != "desktop_proof_invalid" {
		t.Fatalf("navegador comum no analítico: %d %s", r.status, r.raw)
	}
	// Cabeçalhos "de desktop" falsos também não bastam.
	if r := loja.owner.do("GET", sum+"&desktop=true", nil, "User-Agent", "Tauri", "DPoP", "x", "Authorization", "DPoP "+strings.Repeat("a", 43)); r.status != 401 {
		t.Fatalf("cabeçalhos fixos: %d %s", r.status, r.raw)
	}

	pc := e.pc()
	// Sem ativação: ticket válido não vira acesso.
	if r := pc.link(loja.owner); r.status != 403 || r.code() != "desktop_installation_invalid" {
		t.Fatalf("instalação não ativada: %d %s", r.status, r.raw)
	}
	act, err := e.api.Identity.IssueDesktopActivation(context.Background(), loja.email, "PC do balcão")
	if err != nil {
		t.Fatal(err)
	}
	pc.b.mustStatus(pc.call("POST", "/v1/desktop/installations/activate", map[string]any{"code": act.Code}), 201)
	if r := e.pc().call("POST", "/v1/desktop/installations/activate", map[string]any{"code": act.Code}); r.code() != "desktop_activation_invalid" {
		t.Fatalf("código reutilizado: %d %s", r.status, r.raw)
	}
	pc.b.mustStatus(pc.link(loja.owner), 201)
	r := pc.b.mustStatus(pc.call("GET", sum, nil), 200)
	if r.body["meta"].(map[string]any)["timezone"] != "America/Sao_Paulo" {
		t.Fatalf("meta: %s", r.raw)
	}
	ctxView := pc.b.mustStatus(pc.call("GET", "/v1/desktop/context", nil), 200).data()
	if ctxView["installation"].(map[string]any)["label"] != "PC do balcão" {
		t.Fatalf("contexto: %v", ctxView)
	}
	// Prova reutilizada, prova de outro caminho, prova vencida e chave de outra instalação.
	if r := pc.callWithProof("GET", sum, pc.last); r.code() != "desktop_proof_invalid" {
		t.Fatalf("prova reutilizada: %d %s", r.status, r.raw)
	}
	if r := pc.callWithProof("GET", sum, pc.proof("GET", "/v1/desktop/context", pc.token)); r.code() != "desktop_proof_invalid" {
		t.Fatalf("prova de outro caminho: %d %s", r.status, r.raw)
	}
	pc.skew = -10 * time.Minute
	if r := pc.call("GET", sum, nil); r.code() != "desktop_proof_invalid" {
		t.Fatalf("prova vencida: %d %s", r.status, r.raw)
	}
	pc.skew = 0
	ladrao := e.pc()
	ladrao.token = pc.token // token roubado sem a chave
	if r := ladrao.call("GET", sum, nil); r.code() != "desktop_proof_invalid" {
		t.Fatalf("token roubado: %d %s", r.status, r.raw)
	}
	pc.b.mustStatus(pc.call("GET", sum, nil), 200)

	// Instalação revogada: consultas seguintes recusadas.
	if err := e.api.Identity.RevokeDesktopInstallation(context.Background(), uuid.MustParse(ctxView["installation"].(map[string]any)["id"].(string))); err != nil {
		t.Fatal(err)
	}
	if r := pc.call("GET", sum, nil); r.status != 401 {
		t.Fatalf("instalação revogada: %d %s", r.status, r.raw)
	}
}

// Web confirma a troca: o desktop perde inclusive os gráficos; desktop confirma a troca: a web
// perde a gestão. Logout encerra o vínculo. A página local e a gestão usam a MESMA sessão.
func TestDesktopFollowsOwnerSession(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("loja-a", "ana@exemplo.com")
	sum := analyticsPath(loja.id, "/summary?from=2026-11-12&to=2026-11-12")
	pc := e.pc()
	pc.activate(loja.email, "PC")
	janela := loja.owner // a gestão aberta dentro da janela do programa (mesmo cookie)
	pc.b.mustStatus(pc.link(janela), 201)
	pc.b.mustStatus(pc.call("GET", sum, nil), 200)
	janela.mustStatus(janela.pendencias(loja.id), 200) // alternar não gera conflito
	pc.b.mustStatus(pc.call("GET", sum, nil), 200)

	// Outro navegador assume: o conflito mostra que a sessão atual é o desktop autorizado.
	web := e.browser()
	challenge, other := conflictOf(t, web.do("POST", "/v1/auth/login", map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"}))
	if other["channel"] != "desktop_authorized" || other["installation_name"] != "PC" {
		t.Fatalf("contexto da outra sessão: %v", other)
	}
	web.mustStatus(web.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": challenge}), 200)
	if r := pc.call("GET", sum, nil); r.status != 401 || r.code() != "owner_session_replaced" {
		t.Fatalf("desktop depois da troca: %d %s", r.status, r.raw)
	}
	web.session()
	web.mustStatus(web.pendencias(loja.id), 200)

	// Instalação autorizada sem sessão de dono: não há como obter ticket.
	if r := janela.do("POST", "/v1/desktop/link-tickets", nil); r.status != 401 {
		t.Fatalf("ticket sem sessão: %d %s", r.status, r.raw)
	}
	// Desktop retoma (nova entrada na janela), web perde.
	janela2 := e.browser()
	c2, _ := conflictOf(t, janela2.do("POST", "/v1/auth/login", map[string]any{"email": loja.email, "password": goodPassword, "context": "staff"}))
	janela2.mustStatus(janela2.do("POST", "/v1/auth/owner-session/takeover", map[string]any{"challenge": c2}), 200)
	pc.b.mustStatus(pc.link(janela2), 201)
	pc.b.mustStatus(pc.call("GET", sum, nil), 200)
	if r := web.pendencias(loja.id); r.status != 401 {
		t.Fatalf("web depois da troca: %d", r.status)
	}
	// Ticket de uso único.
	janela2.session()
	tk := janela2.mustStatus(janela2.do("POST", "/v1/desktop/link-tickets", nil), 201).data()["ticket"].(string)
	pc.b.mustStatus(pc.call("POST", "/v1/desktop/bindings", map[string]any{"ticket": tk}), 201)
	if r := pc.call("POST", "/v1/desktop/bindings", map[string]any{"ticket": tk}); r.code() != "desktop_link_invalid" {
		t.Fatalf("ticket reutilizado: %d %s", r.status, r.raw)
	}
	// Logout na gestão: o Dashboard perde o acesso.
	janela2.mustStatus(janela2.do("POST", "/v1/auth/logout", nil), 204)
	if r := pc.call("GET", sum, nil); r.status != 401 {
		t.Fatalf("depois do logout: %d", r.status)
	}
}

// Dono de A e profissional de B: o Dashboard mostra só A; o id de B (na URL ou no filtro) não
// devolve dados de B. Instalação de outra conta não usa ticket desta.
func TestDesktopStoreScope(t *testing.T) {
	e := newEnv(t)
	a := e.barbearia(t) // Ana dona de A
	bShop := e.shop("loja-b", "bia@exemplo.com")
	profB := e.professional(bShop.id, "Ana na B")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO staff_membership (establishment_id, account_id, role, professional_id, created_at, updated_at)
		SELECT $1, id, 'professional', $2, now(), now() FROM account WHERE email_normalized = 'ana@exemplo.com'`, bShop.id, profB); err != nil {
		t.Fatal(err)
	}
	pc := e.pc()
	pc.activate(a.email, "PC da Ana")
	pc.b.mustStatus(pc.link(a.owner), 201)
	ctxView := pc.b.mustStatus(pc.call("GET", "/v1/desktop/context", nil), 200).data()
	ests := ctxView["establishments"].([]any)
	if len(ests) != 1 || ests[0].(map[string]any)["id"] != a.id.String() {
		t.Fatalf("lojas do Dashboard: %v", ests)
	}
	q := "?from=2026-11-12&to=2026-11-12"
	if r := pc.call("GET", analyticsPath(bShop.id, "/summary"+q), nil); r.status != 404 {
		t.Fatalf("loja B (só profissional): %d %s", r.status, r.raw)
	}
	if r := pc.call("GET", analyticsPath(bShop.id, "/records/receipts"+q), nil); r.status != 404 {
		t.Fatalf("registros da loja B: %d", r.status)
	}
	// Profissional de B como filtro na loja A: nenhum dado (o filtro só restringe dentro de A).
	r := pc.b.mustStatus(pc.call("GET", analyticsPath(a.id, "/breakdowns/professionals"+q+"&professional_id="+profB.String()), nil), 200)
	if rows := r.data()["rows"].([]any); len(rows) != 0 {
		t.Fatalf("filtro com profissional de outra loja devolveu: %v", rows)
	}
	// Instalação de outra conta não troca ticket da Ana.
	outro := e.pc()
	outro.activate("bia@exemplo.com", "PC da Bia")
	a.owner.session()
	tk := a.owner.mustStatus(a.owner.do("POST", "/v1/desktop/link-tickets", nil), 201).data()["ticket"].(string)
	if r := outro.call("POST", "/v1/desktop/bindings", map[string]any{"ticket": tk}); r.code() != "desktop_link_invalid" {
		t.Fatalf("ticket de outra conta: %d %s", r.status, r.raw)
	}
}
