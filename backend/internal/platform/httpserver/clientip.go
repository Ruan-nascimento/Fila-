package httpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// Cabeçalhos do proxy de borda confiável (frontend/middleware.ts na Vercel). O proxy prova que é
// ele com um segredo compartilhado (FILA_PROXY_SHARED_SECRET) e informa o IP do cliente que ELE
// observou. Quem chama a API direto (sem o segredo) nunca consegue escolher o próprio IP.
const (
	HeaderProxyAuth = "X-Fila-Proxy-Auth"
	HeaderClientIP  = "X-Fila-Client-Ip"
)

const clientIPKey ctxKey = 3

// ClientIPPolicy define em quem confiar para descobrir o IP do cliente.
//
// Topologia da V1: navegador → Vercel (proxy de borda) → balanceador do Render → API.
//   - TrustForwardedFor: o balanceador da plataforma ACRESCENTA o endereço que viu ao fim de
//     X-Forwarded-For; o último item é, então, quem conectou ao balanceador (não forjável).
//     Sem proxy de borda autenticado, esse endereço é o do próprio cliente (acesso direto) ou o da
//     Vercel (todos os clientes juntos).
//   - ProxySecret: quando o pedido traz o segredo correto, vale X-Fila-Client-Ip (o IP que a Vercel
//     viu). Sem o segredo, esses cabeçalhos são ignorados.
type ClientIPPolicy struct {
	TrustForwardedFor bool
	ProxySecret       []byte
}

// ResolveClientIP calcula o IP do cliente uma única vez, guarda no contexto e REMOVE os
// cabeçalhos do proxy (o segredo nunca chega aos handlers nem a log algum).
// Também responde GET /healthz/client-ip: devolve ao chamador o próprio IP como a API o enxerga
// (diagnóstico da cadeia de proxies; não revela nada além do IP de quem pergunta).
func ResolveClientIP(p ClientIPPolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, authenticated := p.resolve(r)
		r.Header.Del(HeaderProxyAuth)
		r.Header.Del(HeaderClientIP)
		if authenticated {
			// Só o IP informado pelo proxy autenticado vai para o contexto; sem ele, cada ponto que
			// limita por IP continua decidindo pela própria configuração (comportamento anterior).
			r = r.WithContext(context.WithValue(r.Context(), clientIPKey, ip))
		}
		// Com o proxy configurado, a API de negócio (/v1) só atende pedidos vindos dele: acesso
		// direto ao endereço da plataforma pularia o IP real e juntaria limites por IP. /healthz
		// continua aberto para a checagem de saúde do Render.
		if len(p.ProxySecret) > 0 && !authenticated && strings.HasPrefix(r.URL.Path, "/v1/") {
			writeProblem(w, r, &Error{Status: http.StatusForbidden, Code: "proxy_required", Title: "Acesso não permitido",
				Detail: "Use o endereço do site."})
			return
		}
		if r.URL.Path == "/healthz/client-ip" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"ip": ip, "proxy_autenticado": authenticated})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p ClientIPPolicy) resolve(r *http.Request) (string, bool) {
	if len(p.ProxySecret) > 0 {
		if got := r.Header.Get(HeaderProxyAuth); got != "" &&
			subtle.ConstantTimeCompare([]byte(got), p.ProxySecret) == 1 {
			if ip := net.ParseIP(strings.TrimSpace(r.Header.Get(HeaderClientIP))); ip != nil {
				return ip.String(), true
			}
		}
	}
	return clientIPFromPeer(r, p.TrustForwardedFor), false
}

// clientIPFromPeer: último item de X-Forwarded-For (acrescentado pelo balanceador da plataforma)
// ou o endereço da conexão.
func clientIPFromPeer(r *http.Request, trustForwardedFor bool) string {
	if trustForwardedFor {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			parts := strings.Split(xff[len(xff)-1], ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
