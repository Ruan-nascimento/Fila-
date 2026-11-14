// Package fluxo é o módulo de atualizações SSE (servidor → navegador). Comandos continuam HTTP;
// o fluxo só avisa mudanças já gravadas (event_log, ADR-0010/0021).
//
// Uma conexão por aba: snapshot autorizado + cursor do mesmo instante, depois eventos em ordem.
// Reconexão com cursor retoma sem perda; cursor vencido, ilegível ou de outra época recebe
// snapshot novo. A conexão é reautorizada a cada 30 s e a cada aviso de revogação.
//
// Fronteiras (ADR-0001): não tem tabelas próprias. Lê o event_log pela plataforma (outbox) e as
// visões pelos módulos donos (fila/estabelecimento), via a interface Sources.
package fluxo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/estabelecimento"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// Sources monta as visões do snapshot (implementado pelo módulo fila).
type Sources interface {
	PublicQueues(ctx context.Context, q database.Querier, estID uuid.UUID, now time.Time) (any, error)
	StaffQueues(ctx context.Context, q database.Querier, estID uuid.UUID, professionalID *uuid.UUID) (any, error)
	CurrentVisit(ctx context.Context, q database.Querier, estID uuid.UUID, account, visitor *acesso.Identity) (any, error)
}

// Limits são os limites da conexão (eventos-sse.md §8).
type Limits struct {
	Heartbeat     time.Duration // comentário ": ping"
	Revalidate    time.Duration // reconferência periódica da sessão
	MaxLifetime   time.Duration // depois disso o servidor encerra; o cliente volta com o cursor
	WriteTimeout  time.Duration // prazo de cada mensagem (cliente parado → conexão cai)
	Buffer        int           // eventos esperando envio; estourou → conexão encerrada
	PerCredential int           // conexões simultâneas por credencial (neste processo)
	PerIP         int           // conexões simultâneas por IP (neste processo); 0 = padrão
	MaxTotal      int           // conexões simultâneas no processo; 0 = padrão
}

// Padrões dos tetos por IP e por processo (FILA_SSE_MAX_PER_IP, FILA_SSE_MAX_CONNECTIONS).
// Por IP é generoso porque uma barbearia pode ter vários celulares atrás do mesmo Wi-Fi.
const (
	DefaultPerIP    = 100
	DefaultMaxTotal = 2000
)

// DefaultLimits são os valores iniciais do contrato.
var DefaultLimits = Limits{Heartbeat: 15 * time.Second, Revalidate: 30 * time.Second, MaxLifetime: 30 * time.Minute,
	WriteTimeout: 5 * time.Second, Buffer: 256, PerCredential: 6}

// Deps são as dependências do módulo.
type Deps struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Log       *slog.Logger
	Hub       *outbox.Hub
	Auth      acesso.Authorizer
	Customers acesso.Customers
	// Refresh relê a sessão dos cookies da requisição (módulo identidade).
	Refresh           func(r *http.Request) (*http.Request, error)
	Sources           Sources
	Limits            Limits
	TrustForwardedFor bool
	// SnapshotHook (só testes) roda em dois momentos: "registered" (depois do registro no Hub,
	// antes do snapshot) e "in_snapshot" (dentro da leitura do snapshot, depois de fixar a
	// posição). Prova que um evento publicado nesses intervalos não se perde.
	SnapshotHook func(stage string)
	// Loyalty lê o resumo de pontos da conta para o snapshot (canal account). nil = sem fidelidade.
	Loyalty func(ctx context.Context, q database.Querier, estID, accountID uuid.UUID) (any, error)
	// Pendencies lê os contadores do painel do dono para o snapshot (nil = sem painel).
	Pendencies func(ctx context.Context, q database.Querier, estID uuid.UUID) (any, error)
}

// Service atende GET /v1/stream.
type Service struct {
	Deps
	mu       sync.Mutex
	perCred  map[string]int
	perIP    map[string]int
	total    int
	shutdown chan struct{}
	once     sync.Once
}

// New cria o serviço.
func New(d Deps) *Service {
	if d.Limits.Heartbeat == 0 {
		perIP, total := d.Limits.PerIP, d.Limits.MaxTotal
		d.Limits = DefaultLimits
		d.Limits.PerIP, d.Limits.MaxTotal = perIP, total
	}
	if d.Limits.PerIP <= 0 {
		d.Limits.PerIP = DefaultPerIP
	}
	if d.Limits.MaxTotal <= 0 {
		d.Limits.MaxTotal = DefaultMaxTotal
	}
	return &Service{Deps: d, perCred: map[string]int{}, perIP: map[string]int{}, shutdown: make(chan struct{})}
}

// Active devolve quantas conexões SSE estão abertas neste processo (métrica).
func (s *Service) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// Shutdown encerra as conexões abertas (o servidor está parando); os clientes reconectam com o
// cursor em outro processo ou quando este voltar. Chamar via http.Server.RegisterOnShutdown.
func (s *Service) Shutdown() { s.once.Do(func() { close(s.shutdown) }) }

// Routes devolve a operação do contrato.
func (s *Service) Routes() []hs.Route {
	return []hs.Route{{Operation: "openEventStream", Method: http.MethodGet, Path: "/v1/stream",
		Grants: []hs.Grant{hs.GrantPublic, {Actor: "visitor", Scope: "visit_owner"}, {Actor: "customer", Scope: "self"},
			{Actor: "professional", Scope: "own_queue"}, hs.GrantOwner},
		Handler: s.openEventStream}}
}

var audiences = []string{outbox.Public, outbox.Visit, outbox.Account, outbox.Professional, outbox.Owner}

// grantsFor calcula, a partir da sessão (cookies), o que a conexão pode receber dentre os canais
// pedidos. Negação por padrão; o pedido nunca define papel ou identidade.
func (s *Service) grantsFor(r *http.Request, estID uuid.UUID, requested []string) (outbox.Grants, []string, string, error) {
	var g outbox.Grants
	account, visitor := s.Customers.CustomerIdentities(r, estID)
	actor, err := s.Auth.Member(r, estID)
	staff := err == nil
	if err != nil {
		var he *hs.Error
		if !errors.As(err, &he) || he.Status >= 500 {
			return g, nil, "", err
		}
	}
	var denied []string
	for _, ch := range requested {
		ok := true
		switch ch {
		case outbox.Public:
			g.Public = true
		case outbox.Visit:
			if account != nil {
				id := account.AccountID
				g.VisitAccount = &id
			}
			if visitor != nil {
				id := visitor.VisitorID
				g.VisitVisitor = &id
			}
			ok = account != nil || visitor != nil
		case outbox.Account:
			if ok = account != nil; ok {
				id := account.AccountID
				g.Account = &id
			}
		case outbox.Professional:
			if ok = staff && actor.ProfessionalID != nil; ok {
				id := *actor.ProfessionalID
				g.ProfessionalID = &id
			}
		case outbox.Owner:
			ok = staff && actor.IsOwner()
			g.Owner = ok
		}
		if !ok {
			denied = append(denied, ch)
		}
	}
	key := "ip:" + hs.ClientIP(r, s.TrustForwardedFor)
	switch {
	case account != nil:
		key = "conta:" + account.AccountID.String()
	case visitor != nil:
		key = "visitante:" + visitor.VisitorID.String()
	}
	return g, denied, key, nil
}

// acquire reserva uma vaga: por credencial e por IP (429, abuso de um cliente) e no processo
// (503: servidor cheio; o cliente tenta de novo e o balanceador pode mandar a outro processo).
func (s *Service) acquire(key, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total >= s.Limits.MaxTotal {
		return &hs.Error{Status: 503, Code: "service_unavailable", Title: "Servidor ocupado",
			Detail: "Muitas conexões abertas agora. A tela tenta de novo sozinha.", RetryAfter: 10 * time.Second}
	}
	// Sem credencial (só olhando a página da loja), a "credencial" seria o IP: várias pessoas no
	// mesmo Wi-Fi da barbearia contariam como uma. Para elas vale só o teto por IP.
	anonymous := strings.HasPrefix(key, "ip:")
	if (!anonymous && s.perCred[key] >= s.Limits.PerCredential) || s.perIP[ip] >= s.Limits.PerIP {
		return hs.ErrRateLimited(5 * time.Second)
	}
	s.perCred[key]++
	s.perIP[ip]++
	s.total++
	return nil
}

func (s *Service) release(key, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perCred[key]--; s.perCred[key] <= 0 {
		delete(s.perCred, key)
	}
	if s.perIP[ip]--; s.perIP[ip] <= 0 {
		delete(s.perIP, ip)
	}
	s.total--
}

// parseRequest valida establishment_id, channels e cursor (query ou Last-Event-ID).
func parseRequest(r *http.Request) (uuid.UUID, []string, string, error) {
	q := r.URL.Query()
	var fe []hs.FieldError
	estID, err := uuid.Parse(q.Get("establishment_id"))
	if err != nil {
		fe = append(fe, hs.FieldError{Field: "establishment_id", Code: hs.FieldInvalidFormat})
	}
	var channels []string
	for _, raw := range q["channels"] {
		for _, ch := range strings.Split(raw, ",") {
			switch {
			case !slices.Contains(audiences, ch):
				fe = append(fe, hs.FieldError{Field: "channels", Code: hs.FieldNotAllowed})
			case !slices.Contains(channels, ch):
				channels = append(channels, ch)
			}
		}
	}
	if len(channels) == 0 {
		fe = append(fe, hs.FieldError{Field: "channels", Code: hs.FieldRequired})
	}
	cursor := r.Header.Get("Last-Event-ID") // reconexão nativa do navegador
	if cursor == "" {
		cursor = q.Get("cursor")
	}
	if len(cursor) > 128 {
		fe = append(fe, hs.FieldError{Field: "cursor", Code: hs.FieldTooLong})
	}
	if len(fe) > 0 {
		return estID, nil, "", hs.ErrValidation(fe...)
	}
	return estID, channels, cursor, nil
}

// snapshotData segue StreamSnapshot.
type snapshotData struct {
	Reason          string   `json:"reason"`
	GrantedChannels []string `json:"granted_channels"`
	DeniedChannels  []string `json:"denied_channels"`
	PublicQueues    any      `json:"public_queues,omitempty"`
	StaffQueues     any      `json:"staff_queues,omitempty"`
	Visit           any      `json:"visit"`
	Pendencies      any      `json:"pendencies"`
	Loyalty         any      `json:"loyalty"`
}

type resource struct {
	Type    string    `json:"type"`
	ID      uuid.UUID `json:"id"`
	Version int64     `json:"version"`
}

// envelope segue EventEnvelope.
type envelope struct {
	SchemaVersion   int             `json:"schema_version"`
	EventID         uuid.UUID       `json:"event_id"`
	Type            string          `json:"type"`
	OccurredAt      time.Time       `json:"occurred_at"`
	EstablishmentID uuid.UUID       `json:"establishment_id"`
	Audience        string          `json:"audience"`
	Resource        resource        `json:"resource"`
	Data            json.RawMessage `json:"data"`
}

// mainAudience é a audiência registrada nas mensagens do próprio fluxo (snapshot, fim de
// sessão): a mais específica concedida à conexão.
func mainAudience(g outbox.Grants) string {
	ch := g.Channels()
	for _, a := range []string{outbox.Owner, outbox.Professional, outbox.Account, outbox.Visit} {
		if slices.Contains(ch, a) {
			return a
		}
	}
	return outbox.Public
}

func (s *Service) openEventStream(w http.ResponseWriter, r *http.Request) error {
	estID, requested, rawCursor, err := parseRequest(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := estabelecimento.ActiveByID(ctx, s.Pool, estID); errors.Is(err, estabelecimento.ErrNotFound) {
		return hs.ErrNotFound()
	} else if err != nil {
		return err
	}
	grants, denied, key, err := s.grantsFor(r, estID, requested)
	if err != nil {
		return err
	}
	ip := hs.ClientIP(r, s.TrustForwardedFor)
	if err := s.acquire(key, ip); err != nil {
		return err
	}
	defer s.release(key, ip)

	// 1. Registrar no Hub ANTES do snapshot: o que for publicado daqui em diante chega por sub.C.
	sub, err := s.Hub.Subscribe(ctx, estID, s.Limits.Buffer)
	if err != nil {
		return err
	}
	defer s.Hub.Unsubscribe(sub)
	if s.SnapshotHook != nil {
		s.SnapshotHook("registered")
	}
	// 2. Snapshot + posição do MESMO instante (REPEATABLE READ).
	var head outbox.Head
	var reason string
	var snap snapshotData
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		if head, err = outbox.ReadHead(ctx, tx); err != nil {
			return err
		}
		if s.SnapshotHook != nil {
			s.SnapshotHook("in_snapshot")
		}
		if reason = head.Resume(rawCursor); reason == "" {
			return nil // cursor válido: só os eventos depois dele
		}
		snap, err = s.snapshot(ctx, tx, r, estID, grants)
		return err
	})
	if err != nil {
		return err
	}
	after := head.Last
	if reason == "" {
		c, _ := outbox.DecodeCursor(rawCursor)
		after = c.Position
	}
	snap.Reason, snap.GrantedChannels, snap.DeniedChannels = reason, nonNil(grants.Channels()), nonNil(denied)

	c, err := s.start(w, r, estID, head.Epoch)
	if err != nil {
		return nil // conexão já começou a responder; nada a devolver como erro
	}
	if reason != "" {
		if err := c.control("stream.snapshot", grants, head.Last, snap); err != nil {
			return nil
		}
	}
	// 3. Replay do event_log depois da posição (filtrado por audiência no banco).
	for {
		list, err := outbox.ReadAfter(ctx, s.Pool, estID, after, grants, 500)
		if err != nil {
			s.Log.Warn("replay de eventos falhou", "error", database.Describe(err))
			return nil
		}
		for _, e := range list {
			if err := c.event(e); err != nil {
				return nil
			}
			after = e.Position
		}
		if len(list) < 500 {
			break
		}
	}
	// 4. Ao vivo.
	s.live(r, c, sub, estID, requested, grants, after)
	return nil
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// snapshot monta o estado autorizado (na transação do instante do cursor).
func (s *Service) snapshot(ctx context.Context, tx pgx.Tx, r *http.Request, estID uuid.UUID, g outbox.Grants) (snapshotData, error) {
	var d snapshotData
	var err error
	if g.Public {
		if d.PublicQueues, err = s.Sources.PublicQueues(ctx, tx, estID, s.Clock.Now()); err != nil {
			return d, err
		}
	}
	switch {
	case g.Owner:
		d.StaffQueues, err = s.Sources.StaffQueues(ctx, tx, estID, nil)
		if err == nil && s.Pendencies != nil {
			d.Pendencies, err = s.Pendencies(ctx, tx, estID)
		}
	case g.ProfessionalID != nil:
		d.StaffQueues, err = s.Sources.StaffQueues(ctx, tx, estID, g.ProfessionalID)
	}
	if err != nil {
		return d, err
	}
	if g.Account != nil && s.Loyalty != nil {
		if d.Loyalty, err = s.Loyalty(ctx, tx, estID, *g.Account); err != nil {
			return d, err
		}
	}
	if g.VisitAccount != nil || g.VisitVisitor != nil {
		account, visitor := s.Customers.CustomerIdentities(r, estID)
		if d.Visit, err = s.Sources.CurrentVisit(ctx, tx, estID, account, visitor); err != nil {
			return d, err
		}
	}
	return d, nil
}

// live entrega eventos do Hub, heartbeats e reconferências até a conexão acabar.
func (s *Service) live(r *http.Request, c *conn, sub *outbox.Subscription, estID uuid.UUID, requested []string, grants outbox.Grants, after int64) {
	ctx := r.Context()
	heartbeat := time.NewTicker(s.Limits.Heartbeat)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(s.Limits.Revalidate)
	defer revalidate.Stop()
	lifetime := time.NewTimer(s.Limits.MaxLifetime)
	defer lifetime.Stop()
	for {
		select {
		case <-ctx.Done():
			return // cliente foi embora (rede caiu, aba fechou)
		case <-s.shutdown:
			return
		case <-lifetime.C:
			return // o cliente reconecta com o cursor
		case <-sub.Overflow:
			s.Log.Info("conexão SSE encerrada: cliente lento (buffer cheio)")
			return
		case <-heartbeat.C:
			if c.comment("ping") != nil {
				return
			}
		case <-revalidate.C:
			if !s.stillAllowed(r, c, estID, requested, grants) {
				return
			}
		case <-sub.Revalidate:
			if !s.stillAllowed(r, c, estID, requested, grants) {
				return
			}
		case e := <-sub.C:
			if e.Position <= after {
				continue // já enviado pelo replay
			}
			after = e.Position
			if !grants.Allows(e) { // reconferido no envio, com as permissões atuais da conexão
				continue
			}
			if c.event(e) != nil {
				return
			}
		}
	}
}

// stillAllowed reconfere a sessão. Perdeu algo que tinha (logout, senha trocada, acesso de
// equipe encerrado, visitante recuperado em outro aparelho, expiração) → stream.session_ended.
func (s *Service) stillAllowed(r *http.Request, c *conn, estID uuid.UUID, requested []string, grants outbox.Grants) bool {
	if s.Refresh == nil {
		return true
	}
	fresh, err := s.Refresh(r)
	if err == nil {
		var now outbox.Grants
		if now, _, _, err = s.grantsFor(fresh, estID, requested); err == nil && now.Covers(grants) {
			return true
		}
	}
	if err != nil && r.Context().Err() == nil {
		s.Log.Warn("reconferência da sessão SSE falhou; conexão encerrada", "error", database.Describe(err))
	}
	_ = c.control("stream.session_ended", grants, 0, map[string]string{"reason": "session_revoked"})
	return false
}

// ---------------------------------------------------------------------------------------------
// Escrita SSE

type conn struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	est    uuid.UUID
	epoch  uuid.UUID
	id     uuid.UUID
	clock  clock.Clock
	limits Limits
	buf    bytes.Buffer
}

// start envia os cabeçalhos do fluxo. O prazo global de escrita/leitura do servidor HTTP não
// vale aqui: cada mensagem tem o próprio prazo (WriteTimeout) e a leitura não expira.
func (s *Service) start(w http.ResponseWriter, r *http.Request, estID, epoch uuid.UUID) (*conn, error) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no") // nginx: não segurar as mensagens
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	id, _ := uuid.NewV7()
	c := &conn{w: w, rc: rc, est: estID, epoch: epoch, id: id, clock: s.Clock, limits: s.Limits}
	w.WriteHeader(http.StatusOK)
	// retry: sugestão para a reconexão nativa; o cliente aplica o próprio backoff com jitter.
	return c, c.write("retry: 3000\n\n")
}

func (c *conn) write(s string) error {
	if err := c.rc.SetWriteDeadline(time.Now().Add(c.limits.WriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := c.w.Write([]byte(s)); err != nil {
		return err
	}
	return c.rc.Flush()
}

func (c *conn) comment(text string) error { return c.write(": " + text + "\n\n") }

func (c *conn) message(id, event string, env envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.buf.Reset()
	if id != "" {
		fmt.Fprintf(&c.buf, "id: %s\n", id)
	}
	fmt.Fprintf(&c.buf, "event: %s\ndata: %s\n\n", event, data)
	return c.write(c.buf.String())
}

func (c *conn) event(e outbox.Published) error {
	payload := e.Payload
	var compact bytes.Buffer
	if json.Compact(&compact, payload) == nil {
		payload = compact.Bytes() // uma linha só (o formato SSE separa mensagens por linha)
	}
	return c.message(outbox.Cursor{Epoch: c.epoch, Position: e.Position}.Encode(), e.Type, envelope{SchemaVersion: 1,
		EventID: e.EventID, Type: e.Type, OccurredAt: e.OccurredAt, EstablishmentID: e.EstablishmentID, Audience: e.Audience,
		Resource: resource{Type: e.ResourceType, ID: e.ResourceID, Version: e.ResourceVersion}, Data: payload})
}

// control envia uma mensagem do próprio fluxo (snapshot com o cursor do instante; fim de sessão
// sem cursor).
func (c *conn) control(event string, g outbox.Grants, position int64, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	id := ""
	if event == "stream.snapshot" {
		id = outbox.Cursor{Epoch: c.epoch, Position: position}.Encode()
	}
	eventID, _ := uuid.NewV7()
	return c.message(id, event, envelope{SchemaVersion: 1, EventID: eventID, Type: event, OccurredAt: c.clock.Now(),
		EstablishmentID: c.est, Audience: mainAudience(g), Resource: resource{Type: "stream", ID: c.id, Version: position}, Data: raw})
}
