package httpserver

import (
	"log/slog"
	"net/http"
)

// Grant é um par ator/escopo autorizado (x-authz do contrato). Serve de documentação
// verificável: o teste de contrato compara as rotas registradas com o OpenAPI. A verificação
// em si acontece no handler, que nega tudo o que não estiver aqui (R-PERM-01).
type Grant struct {
	Actor string
	Scope string
}

// Route é uma operação do contrato implementada.
type Route struct {
	Operation string // operationId
	Method    string
	Path      string // igual ao OpenAPI, com {parametros}
	Grants    []Grant
	Handler   HandlerFunc
}

// Mount registra as rotas no mux.
func Mount(mux *http.ServeMux, log *slog.Logger, routes []Route) {
	for _, rt := range routes {
		mux.Handle(rt.Method+" "+rt.Path, Handle(log, rt.Handler))
	}
}

// Atalhos de autorização usados nas tabelas de rotas.
var (
	GrantPublic     = Grant{Actor: "public", Scope: "none"}
	GrantAnyAccount = []Grant{{"customer", "self"}, {"professional", "self"}, {"owner", "self"}}
	GrantOwner      = Grant{Actor: "owner", Scope: "establishment"}
)
