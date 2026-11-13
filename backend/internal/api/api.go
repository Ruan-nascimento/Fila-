// Package api monta a API HTTP: liga os módulos de domínio às rotas do contrato e aplica as
// proteções comuns (origem, sessão/CSRF). É a "raiz de composição" usada por cmd/api e pelos
// testes de integração.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/atendimento"
	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/fluxo"
	"fila/internal/identidade"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
	"fila/internal/platform/outbox"
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
	// Charges informa cobranças Pix ativas/desconhecidas de uma comanda. As cobranças chegam na
	// etapa 08; até lá nil = nenhuma cobrança (os testes simulam uma para provar o bloqueio).
	Charges atendimento.ChargeGuard
	// StreamLimits troca os limites do SSE (testes usam tempos curtos). Zero = padrão.
	StreamLimits fluxo.Limits
	// SnapshotHook (só testes): ver fluxo.Deps.
	SnapshotHook func(stage string)
}

// API é a aplicação montada.
type API struct {
	Identity *identidade.Service
	Hub      *outbox.Hub
	Stream   *fluxo.Service
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
	atd := atendimento.New(atendimento.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident, Customers: ident,
		Visits: fl, Charges: d.Charges, VisitClosed: ident.VisitClosed})

	hub := outbox.NewHub(d.Pool, d.Log)
	stream := fluxo.New(fluxo.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Hub: hub, Auth: ident, Customers: ident,
		Refresh: ident.Refresh, Sources: fl, Limits: d.StreamLimits, TrustForwardedFor: d.Config.Security.TrustForwardedFor,
		SnapshotHook: d.SnapshotHook})

	a := &API{Identity: ident, Hub: hub, Stream: stream, mux: http.NewServeMux()}
	a.routes = append(a.routes, ident.Routes()...)
	a.routes = append(a.routes, est.Routes()...)
	a.routes = append(a.routes, cat.Routes()...)
	a.routes = append(a.routes, fl.Routes()...)
	a.routes = append(a.routes, atd.Routes()...)
	a.routes = append(a.routes, stream.Routes()...)
	hs.Mount(a.mux, d.Log, a.routes)
	if register != nil {
		register(a.mux)
	}
	a.mux.HandleFunc("/", hs.NotFound)
	// Ordem: origem (recusa site de fora) → sessão + CSRF → rota.
	a.handler = hs.RequireOrigin(d.Config.Security.AllowedOrigins, ident.Middleware(a.mux))
	return a, nil
}

// Run mantém o distribuidor de eventos (LISTEN no banco) até ctx terminar. Sem ele o SSE só
// entrega snapshot e replay, não eventos ao vivo.
func (a *API) Run(ctx context.Context) { a.Hub.Run(ctx) }

// Handler devolve o http.Handler completo (sem os middlewares de plataforma de httpserver.Chain).
func (a *API) Handler() http.Handler { return a.handler }

// Routes lista as operações do contrato implementadas (usado pelo teste de contrato).
func (a *API) Routes() []hs.Route { return a.routes }
