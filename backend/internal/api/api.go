// Package api monta a API HTTP: liga os módulos de domínio às rotas do contrato e aplica as
// proteções comuns (origem, sessão/CSRF). É a "raiz de composição" usada por cmd/api e pelos
// testes de integração.
package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/identidade"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
	"fila/internal/platform/ratelimit"
)

// Deps são as dependências da API.
type Deps struct {
	Config *config.Config
	Pool   *pgxpool.Pool
	Log    *slog.Logger
	Clock  clock.Clock
	// PasswordParams permite parâmetros mais leves nos testes; zero = padrão de produção.
	PasswordParams identidade.PasswordParams
	// Occupancy substitui a ocupação real das filas (só em testes). nil = módulo fila.
	Occupancy estabelecimento.Occupancy
}

// API é a aplicação montada.
type API struct {
	Identity *identidade.Service
	routes   []hs.Route
	mux      *http.ServeMux
	handler  http.Handler
}

// New monta a API. register permite acrescentar rotas extras (ex.: saúde) ao mesmo mux.
func New(d Deps, register func(mux *http.ServeMux)) (*API, error) {
	ring, err := keys.New([]byte(d.Config.Security.SecretKey.Reveal()))
	if err != nil {
		return nil, fmt.Errorf("chave do servidor: %w", err)
	}
	params := d.PasswordParams
	if params == (identidade.PasswordParams{}) {
		params = identidade.DefaultPasswordParams
	}
	ident := identidade.New(identidade.Deps{
		Pool:   d.Pool,
		Clock:  d.Clock,
		Ring:   ring,
		Hasher: identidade.NewHasher(params, max(runtime.NumCPU(), 2)),
		Log:    d.Log,
		Config: identidade.Config{
			CookieSecure:      d.Config.Security.CookieSecure,
			PublicBaseURL:     d.Config.Security.PublicBaseURL,
			TrustForwardedFor: d.Config.Security.TrustForwardedFor,
		},
	})

	fl := fila.New(fila.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident, Customers: ident,
		Limiter: ratelimit.New(d.Pool, d.Clock, ring), TrustForwardedFor: d.Config.Security.TrustForwardedFor,
		VisitStarted: ident.VisitStarted, VisitClosed: ident.VisitClosed})
	ident.SetActiveVisit(fl.VisitorBusy)
	var occ estabelecimento.Occupancy = fl
	if d.Occupancy != nil {
		occ = d.Occupancy // testes da etapa 03 simulam ocupação
	}
	est := estabelecimento.New(estabelecimento.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident,
		Occupancy: occ, Activity: fl, PendingInvites: ident.PendingInvites, AccessRevoked: ident.AccessRevoked})
	cat := catalogo.New(catalogo.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident})

	a := &API{Identity: ident, mux: http.NewServeMux()}
	a.routes = append(a.routes, ident.Routes()...)
	a.routes = append(a.routes, est.Routes()...)
	a.routes = append(a.routes, cat.Routes()...)
	a.routes = append(a.routes, fl.Routes()...)
	hs.Mount(a.mux, d.Log, a.routes)
	if register != nil {
		register(a.mux)
	}
	a.mux.HandleFunc("/", hs.NotFound)
	// Ordem: origem (recusa site de fora) → sessão + CSRF → rota.
	a.handler = hs.RequireOrigin(d.Config.Security.AllowedOrigins, ident.Middleware(a.mux))
	return a, nil
}

// Handler devolve o http.Handler completo (sem os middlewares de plataforma de httpserver.Chain).
func (a *API) Handler() http.Handler { return a.handler }

// Routes lista as operações do contrato implementadas (usado pelo teste de contrato).
func (a *API) Routes() []hs.Route { return a.routes }
