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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/agenda"
	"fila/internal/analitico"
	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
	"fila/internal/exclusao"
	"fila/internal/fidelidade"
	"fila/internal/fila"
	"fila/internal/fluxo"
	"fila/internal/identidade"
	"fila/internal/midia"
	"fila/internal/pagamento"
	"fila/internal/painel"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
	"fila/internal/platform/objstore"
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
	// Charges (só testes) substitui a consulta de cobranças Pix ativas de uma comanda.
	// nil = módulo pagamento.
	Charges atendimento.ChargeGuard
	// PixProvider (só testes) substitui o provedor Pix. nil = conforme a configuração
	// (FILA_PIX_PROVIDER: desligado ou AbacatePay).
	PixProvider pagamento.Provider
	// StreamLimits troca os limites do SSE (testes usam tempos curtos). Zero = padrão.
	StreamLimits fluxo.Limits
	// SnapshotHook (só testes): ver fluxo.Deps.
	SnapshotHook func(stage string)
	// Benefits (só testes) substitui o encerramento de pontos e reservas na exclusão. nil = fidelidade.
	Benefits exclusao.Benefits
	// AnalyticsStream (só testes) troca os tempos do fluxo do Dashboard. Zero = padrão.
	AnalyticsStream analitico.StreamDeps
	// DeletionFault (só testes) simula queda no meio da anonimização.
	DeletionFault func(step string) error
	// MediaStore (só testes) substitui o armazenamento de imagens. nil = conforme a configuração
	// (FILA_MEDIA_STORAGE: desligado ou pasta persistente).
	MediaStore midia.Storage
	// GoogleHTTPClient (só testes) fala com um provedor OpenID de teste.
	GoogleHTTPClient *http.Client
}

// MediaStore escolhe o armazenamento de imagens: override (testes), pasta persistente quando
// configurada, ou nil (envio de fotos desligado; as telas usam iniciais).
func MediaStore(c config.MediaConfig, override midia.Storage) midia.Storage {
	switch {
	case override != nil:
		return override
	case c.Storage == config.MediaS3:
		return midia.NewS3Store(ObjectClient(c.S3, c.S3.Bucket), c.S3.Prefix)
	case c.Enabled():
		return midia.FileStore{Root: c.Dir}
	}
	return nil
}

// ObjectClient cria o cliente do armazenamento de objetos (R2) para um bucket.
func ObjectClient(c config.S3Config, bucket string) *objstore.Client {
	return &objstore.Client{Endpoint: c.Endpoint, Bucket: bucket, Region: c.Region,
		AccessKey: c.AccessKey, SecretKey: c.SecretKey.Reveal()}
}

// API é a aplicação montada.
type API struct {
	Identity *identidade.Service
	Hub      *outbox.Hub
	Stream   *fluxo.Service
	Deletion *exclusao.Service
	Payments *pagamento.Service
	Loyalty  *fidelidade.Service
	Media    *midia.Service
	Agenda   *agenda.Service
	routes   []hs.Route
	mux      *http.ServeMux
	handler  http.Handler
}

// New monta a API. register permite acrescentar rotas extras (ex.: saúde) ao mesmo mux.
func New(d Deps, register func(mux *http.ServeMux)) (*API, error) {
	ring, err := keys.NewRotating(d.Config.Security.KeyMaterial())
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
			Google: identidade.GoogleSettings{ClientID: d.Config.Google.ClientID, ClientSecret: d.Config.Google.ClientSecret.Reveal(),
				RedirectURL: d.Config.Google.RedirectURL, Issuer: d.Config.Google.Issuer, HTTPClient: d.GoogleHTTPClient},
		},
	})
	media := midia.New(midia.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Store: MediaStore(d.Config.Media, d.MediaStore),
		Auth: ident, AccountOf: ident.AccountOf, Limiter: ratelimit.New(d.Pool, d.Clock, ring),
		PerMinute: d.Config.Media.UploadsPerMin, PerDay: d.Config.Media.UploadsPerDay})
	ident.SetMedia(media)

	agendaOn := d.Config.Ops.AgendaEnabled
	fl := fila.New(fila.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident, Customers: ident,
		Limiter: ratelimit.New(d.Pool, d.Clock, ring), TrustForwardedFor: d.Config.Security.TrustForwardedFor,
		VisitStarted: ident.VisitStarted, VisitClosed: ident.VisitClosed, Media: media, AgendaEnabled: agendaOn})
	// Agenda (ADR-0038): regras de cadeira para a fila e reservas. Sempre ligada às visitas (protege
	// reservas já existentes); FILA_AGENDA só decide se lojas podem ADERIR e receber reservas novas.
	ag := agenda.New(agenda.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident, Customers: ident,
		Limiter: ratelimit.New(d.Pool, d.Clock, ring), Visits: fl, Enabled: agendaOn, CounterVisitor: ident.CounterVisitor,
		TrustForwardedFor: d.Config.Security.TrustForwardedFor})
	fl.Agenda = ag
	ident.SetAgendaEnabled(agendaOn)
	ident.SetActiveVisit(fl.VisitorBusy)
	var occ estabelecimento.Occupancy = fl
	if d.Occupancy != nil {
		occ = d.Occupancy // testes da etapa 03 simulam ocupação
	}
	est := estabelecimento.New(estabelecimento.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident,
		Occupancy: occ, Activity: fl, PendingInvites: ident.PendingInvites, AccessRevoked: ident.AccessRevoked,
		PublicBaseURL: d.Config.Security.PublicBaseURL, Media: media, AvatarOf: identidade.AvatarAssetOf,
		AgendaEnabled: agendaOn, AgendaImpact: agenda.FutureAppointments, FutureAppointmentsOf: agenda.FutureAppointmentsOf})
	cat := catalogo.New(catalogo.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident})
	atd := atendimento.New(atendimento.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident, Customers: ident,
		Visits: fl, Charges: d.Charges, VisitClosed: ident.VisitClosed})
	loyalty := fidelidade.New(fidelidade.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident, Customers: ident})
	pay := pagamento.New(pagamento.Deps{Loyalty: loyalty, Pool: d.Pool, Clock: d.Clock, Log: d.Log, Orders: atd,
		Provider: PixProvider(d.Config.Payments, d.PixProvider), ExpiresIn: d.Config.Payments.ExpiresIn,
		Timeout: d.Config.Payments.Timeout, VisitClosed: ident.VisitClosed, Auth: ident,
		WebhookSecret: d.Config.Payments.WebhookSecret, Pendencies: painel.Record})
	if d.Charges == nil {
		atd.Charges = pay // correção de comanda fechada respeita o Pix ativo (R-CMD-12)
	}
	// Pix só é oferecido com a integração ligada (configurações da loja e página pública).
	pixOperational := func() bool { return pay.Provider != nil }
	est.PixOperational = pixOperational
	ident.SetPixOperational(pixOperational)

	hub := outbox.NewHub(d.Pool, d.Log)
	stream := fluxo.New(fluxo.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Hub: hub, Auth: ident, Customers: ident,
		Refresh: ident.Refresh, Ended: ident.EndedReasonOf, Sources: fl, Limits: streamLimits(d), TrustForwardedFor: d.Config.Security.TrustForwardedFor,
		SnapshotHook: d.SnapshotHook, Pendencies: func(ctx context.Context, q database.Querier, estID uuid.UUID) (any, error) {
			return painel.Count(ctx, q, estID)
		}, Loyalty: loyalty.Snapshot})
	panel := &painel.Service{Pool: d.Pool, Auth: ident}
	analyticsStream := d.AnalyticsStream
	analyticsStream.Hub = hub
	analytics := analitico.New(analitico.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Auth: ident, Stream: analyticsStream})

	// Exclusão de conta encerra pontos e reservas (T-LG-03, T-RW-06); testes podem substituir.
	var benefits exclusao.Benefits = loyalty
	if d.Benefits != nil {
		benefits = d.Benefits
	}
	deletion := exclusao.New(exclusao.Deps{Pool: d.Pool, Clock: d.Clock, Log: d.Log, Ring: ring, Auth: ident,
		Limiter: ratelimit.New(d.Pool, d.Clock, ring), TrustForwardedFor: d.Config.Security.TrustForwardedFor,
		Identity: ident, Queue: fl, Benefits: benefits, Fault: d.DeletionFault})

	a := &API{Identity: ident, Hub: hub, Stream: stream, Deletion: deletion, Payments: pay, Loyalty: loyalty, Media: media, Agenda: ag,
		mux: http.NewServeMux()}
	a.routes = append(a.routes, ident.Routes()...)
	a.routes = append(a.routes, est.Routes()...)
	a.routes = append(a.routes, cat.Routes()...)
	a.routes = append(a.routes, fl.Routes()...)
	a.routes = append(a.routes, ag.Routes()...)
	a.routes = append(a.routes, atd.Routes()...)
	a.routes = append(a.routes, pay.Routes()...)
	a.routes = append(a.routes, panel.Routes()...)
	a.routes = append(a.routes, loyalty.Routes()...)
	a.routes = append(a.routes, (&auditoria.Service{Pool: d.Pool, Auth: ident}).Routes()...)
	a.routes = append(a.routes, stream.Routes()...)
	a.routes = append(a.routes, analytics.Routes()...)
	a.routes = append(a.routes, deletion.Routes()...)
	a.routes = append(a.routes, media.Routes()...)
	hs.Mount(a.mux, d.Log, a.routes)
	if register != nil {
		register(a.mux)
	}
	a.mux.HandleFunc("/", hs.NotFound)
	// Ordem: origem (recusa site de fora) → sessão + CSRF → rota.
	a.handler = hs.RequireOrigin(d.Config.Security.AllowedOrigins, ident.Middleware(a.mux))
	return a, nil
}

// streamLimits aplica os tetos de conexão da configuração (FILA_SSE_*) aos limites do SSE.
func streamLimits(d Deps) fluxo.Limits {
	l := d.StreamLimits
	if l.PerIP == 0 {
		l.PerIP = d.Config.Ops.SSEPerIP
	}
	if l.MaxTotal == 0 {
		l.MaxTotal = d.Config.Ops.SSEMaxConnections
	}
	return l
}

// PixProvider escolhe o provedor Pix: override (testes), AbacatePay quando configurado, ou nil
// (Pix desligado: só dinheiro e cartão). A validação da configuração (chave de teste fora de
// produção, produção só depois de validada) está em config.
func PixProvider(c config.PaymentsConfig, override pagamento.Provider) pagamento.Provider {
	switch {
	case override != nil:
		return override
	case c.Pix == config.PixAbacatePay:
		return pagamento.NewAbacatePay(c)
	}
	return nil
}

// Run mantém o distribuidor de eventos (LISTEN no banco) até ctx terminar. Sem ele o SSE só
// entrega snapshot e replay, não eventos ao vivo.
func (a *API) Run(ctx context.Context) { a.Hub.Run(ctx) }

// Handler devolve o http.Handler completo (sem os middlewares de plataforma de httpserver.Chain).
func (a *API) Handler() http.Handler { return a.handler }

// Routes lista as operações do contrato implementadas (usado pelo teste de contrato).
func (a *API) Routes() []hs.Route { return a.routes }
