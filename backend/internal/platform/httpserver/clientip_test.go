package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const proxySecret = "segredo-do-proxy-de-borda-com-32-caracteres-ou-mais"

func seen(t *testing.T, p ClientIPPolicy, mutate func(*http.Request)) (ip string, leaked bool) {
	t.Helper()
	h := ResolveClientIP(p, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ip = ClientIP(r, p.TrustForwardedFor)
		leaked = r.Header.Get(HeaderProxyAuth) != "" || r.Header.Get(HeaderClientIP) != ""
	}))
	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.RemoteAddr = "10.0.0.5:4000" // balanceador interno da plataforma
	mutate(r)
	h.ServeHTTP(httptest.NewRecorder(), r)
	return ip, leaked
}

func TestClientIPTopologies(t *testing.T) {
	render := ClientIPPolicy{TrustForwardedFor: true}
	edge := ClientIPPolicy{TrustForwardedFor: true, ProxySecret: []byte(proxySecret)}

	// Via Vercel autenticada: vale o IP que a Vercel viu.
	ip, leaked := seen(t, edge, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "203.0.113.9, 76.76.21.1")
		r.Header.Set(HeaderProxyAuth, proxySecret)
		r.Header.Set(HeaderClientIP, "203.0.113.9")
	})
	if ip != "203.0.113.9" || leaked {
		t.Fatalf("proxy autenticado: ip=%s vazou=%v", ip, leaked)
	}
	// Clientes diferentes pela mesma Vercel não se misturam.
	ip2, _ := seen(t, edge, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "198.51.100.7, 76.76.21.1")
		r.Header.Set(HeaderProxyAuth, proxySecret)
		r.Header.Set(HeaderClientIP, "198.51.100.7")
	})
	if ip2 == ip {
		t.Fatal("clientes distintos agregados no mesmo IP")
	}
	// Acesso direto à API forjando os cabeçalhos sem o segredo: vale o IP que o balanceador viu.
	ip, leaked = seen(t, edge, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "1.2.3.4, 192.0.2.66")
		r.Header.Set(HeaderProxyAuth, "chute")
		r.Header.Set(HeaderClientIP, "1.2.3.4")
	})
	if ip != "192.0.2.66" || leaked {
		t.Fatalf("forjado: ip=%s vazou=%v", ip, leaked)
	}
	// Segredo certo com IP inválido: não aceita lixo.
	ip, _ = seen(t, edge, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "192.0.2.66")
		r.Header.Set(HeaderProxyAuth, proxySecret)
		r.Header.Set(HeaderClientIP, "nao-e-ip")
	})
	if ip != "192.0.2.66" {
		t.Fatalf("IP inválido do proxy: %s", ip)
	}
	// Sem proxy de borda configurado, os cabeçalhos do proxy são ignorados (e removidos).
	ip, leaked = seen(t, render, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "192.0.2.66")
		r.Header.Set(HeaderProxyAuth, proxySecret)
		r.Header.Set(HeaderClientIP, "1.2.3.4")
	})
	if ip != "192.0.2.66" || leaked {
		t.Fatalf("sem segredo configurado: ip=%s vazou=%v", ip, leaked)
	}
	// Sem confiar em XFF: conexão TCP.
	ip, _ = seen(t, ClientIPPolicy{}, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "1.2.3.4") })
	if ip != "10.0.0.5" {
		t.Fatalf("sem XFF confiável: %s", ip)
	}
}

func TestClientIPDiagnosticEndpoint(t *testing.T) {
	h := ResolveClientIP(ClientIPPolicy{TrustForwardedFor: true, ProxySecret: []byte(proxySecret)},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("não deveria chegar ao app") }))
	r := httptest.NewRequest(http.MethodGet, "/healthz/client-ip", nil)
	r.Header.Set("X-Forwarded-For", "192.0.2.66")
	r.Header.Set(HeaderProxyAuth, proxySecret)
	r.Header.Set(HeaderClientIP, "203.0.113.9")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(b), `"ip":"203.0.113.9"`) || !strings.Contains(string(b), `"proxy_autenticado":true`) ||
		strings.Contains(string(b), proxySecret) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("diagnóstico inesperado: %s", b)
	}
}
