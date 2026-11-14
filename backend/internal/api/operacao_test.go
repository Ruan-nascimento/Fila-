package api_test

import (
	"context"
	"crypto/rand"
	"net/http/httptest"
	"testing"

	"fila/internal/api"
	"fila/internal/platform/config"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
)

// withKeys sobe OUTRA instância da API no mesmo banco com outra chave do servidor (simula a troca
// de FILA_SECRET_KEY com reinício). Os navegadores existentes podem ser apontados para ela.
func (e *env) withKeys(t *testing.T, master []byte, previous ...[]byte) *env {
	t.Helper()
	cfg := *e.deps.Config
	cfg.Security.SecretKey = config.Secret(master)
	cfg.Security.PreviousSecretKeys = nil
	for _, p := range previous {
		cfg.Security.PreviousSecretKeys = append(cfg.Security.PreviousSecretKeys, config.Secret(p))
	}
	deps := e.deps
	deps.Config = &cfg
	a, err := api.New(deps, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(httpserver.Chain(a.Handler(), deps.Log))
	ctx, stop := context.WithCancel(context.Background())
	go a.Run(ctx)
	t.Cleanup(func() { a.Stream.Shutdown(); srv.Close(); stop() })
	ring, _ := keys.New(master, previous...)
	out := &env{t: t, pool: e.pool, clock: e.clock, api: a, srv: srv, ring: ring, master: master, logs: e.logs, deps: deps, pix: e.pix}
	return out
}

// Troca da chave do servidor (rotação de segredo, ADR-0026): com a chave anterior configurada,
// o que já foi emitido continua valendo (código do visitante, protocolo de exclusão, CSRF da
// página aberta); sem ela, não. O que é novo usa só a chave nova.
func TestSecretKeyRotationKeepsIssuedCredentials(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	vis := e.browser()
	_, code := vis.createVisitor(b.id, "Zé")
	maria := e.verifiedCustomer("maria@exemplo.com")
	maria.reauth()
	protocol := maria.mustStatus(maria.requestDeletion(), 202).data()["protocol"].(string)
	clara := e.verifiedCustomer("clara@exemplo.com") // página aberta antes da troca (token CSRF antigo)

	nova := make([]byte, 32)
	_, _ = rand.Read(nova)
	sem := e.withKeys(t, nova) // esqueceram a chave anterior
	if r := sem.browser().do("POST", visitorPath(b.id, "/recover"), map[string]any{"recovery_code": code}); r.status != 401 {
		t.Fatalf("sem a chave anterior o código antigo não deveria valer: %d", r.status)
	}
	if r := sem.deletionStatus(protocol); r.status != 404 {
		t.Fatalf("sem a chave anterior o protocolo antigo não deveria ser achado: %d %s", r.status, r.raw)
	}

	com := e.withKeys(t, nova, e.master)
	if r := com.deletionStatus(protocol); r.status != 200 || r.data()["status"] != "processing" {
		t.Fatalf("protocolo emitido antes da troca: %d %s", r.status, r.raw)
	}
	clara.e = com
	if r := clara.cmd("PATCH", "/v1/me", map[string]any{"name": "Clara Souza"}); r.status != 200 {
		t.Fatalf("CSRF emitido antes da troca deveria valer: %d %s", r.status, r.raw)
	}
	outro := com.browser()
	r := outro.mustStatus(outro.do("POST", visitorPath(b.id, "/recover"), map[string]any{"recovery_code": code}), 200)
	novoCodigo := r.data()["recovery_code"].(string)
	// O código novo é gravado só com a chave nova: funciona mesmo sem a anterior.
	if r := sem.browser().do("POST", visitorPath(b.id, "/recover"), map[string]any{"recovery_code": novoCodigo}); r.status != 200 {
		t.Fatalf("código emitido depois da troca deveria valer só com a chave nova: %d", r.status)
	}
	// Nova sessão depois de reler: o token é o da chave nova.
	if tok := clara.session()["csrf_token"]; tok == "" {
		t.Fatal("sessão sem token")
	}
}
