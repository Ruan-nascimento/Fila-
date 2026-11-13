package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/api"
	"fila/internal/estabelecimento"
	"fila/internal/identidade"
	"fila/internal/platform/clock"
	"fila/internal/platform/config"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
	"fila/internal/platform/logging"
	"fila/internal/platform/mail"
	"fila/internal/platform/testdb"
)

const origin = "https://fila.teste"

// lightParams deixa o Argon2id rápido nos testes de fluxo; os parâmetros reais são testados
// no pacote identidade.
var lightParams = identidade.PasswordParams{MemoryKiB: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	clock *clock.Fake
	api   *api.API
	srv   *httptest.Server
	ring  *keys.Ring
	logs  *syncBuffer
	inbox []mail.Message
	// ipSeq numera os navegadores simulados; atômico porque testes de concorrência criam
	// navegadores em várias goroutines ao mesmo tempo.
	ipSeq atomic.Int64
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newEnv(t *testing.T) *env { t.Helper(); return newEnvWith(t, nil) }

// newEnvWith permite simular participações nas filas (o módulo fila chega na etapa 04).
func newEnvWith(t *testing.T, occ estabelecimento.Occupancy) *env {
	t.Helper()
	pool := testdb.Migrated(t)
	master := make([]byte, 32)
	_, _ = rand.Read(master)
	cfg := &config.Config{Security: config.SecurityConfig{
		SecretKey:         config.Secret(master),
		AllowedOrigins:    []string{origin},
		PublicBaseURL:     origin,
		CookieSecure:      true,
		TrustForwardedFor: true, // permite simular IPs diferentes com X-Forwarded-For
	}}
	logs := &syncBuffer{}
	log := logging.New(logs, slog.LevelDebug, "json")
	clk := clock.NewFake(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC))
	a, err := api.New(api.Deps{Config: cfg, Pool: pool, Log: log, Clock: clk, PasswordParams: lightParams, Occupancy: occ}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(httpserver.Chain(a.Handler(), log))
	t.Cleanup(srv.Close)
	ring, _ := keys.New(master)
	return &env{t: t, pool: pool, clock: clk, api: a, srv: srv, ring: ring, logs: logs}
}

// browser simula um navegador: cookies próprios, origem do site e token CSRF em memória.
type browser struct {
	e      *env
	client *http.Client
	ip     string
	csrf   string
	origin string
}

func (e *env) browser() *browser {
	jar, _ := cookiejar.New(nil)
	c := e.srv.Client()
	client := &http.Client{Transport: c.Transport, Jar: jar, Timeout: 30 * time.Second}
	n := e.ipSeq.Add(1)
	return &browser{e: e, client: client, ip: fmt.Sprintf("203.0.%d.%d", n/250, n%250+1), origin: origin}
}

type resp struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

func (r resp) code() string {
	c, _ := r.body["code"].(string)
	return c
}

func (r resp) data() map[string]any {
	d, _ := r.body["data"].(map[string]any)
	return d
}

func (b *browser) do(method, path string, body any, headers ...string) resp {
	b.e.t.Helper()
	var rd io.Reader
	if body != nil {
		switch v := body.(type) {
		case string:
			rd = strings.NewReader(v)
		default:
			raw, _ := json.Marshal(v)
			rd = bytes.NewReader(raw)
		}
	}
	req, err := http.NewRequest(method, b.e.srv.URL+path, rd)
	if err != nil {
		b.e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.origin != "" {
		req.Header.Set("Origin", b.origin)
	}
	if b.csrf != "" {
		req.Header.Set("X-CSRF-Token", b.csrf)
	}
	req.Header.Set("X-Forwarded-For", b.ip)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: raw}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// session lê GET /v1/session e guarda o token CSRF (como o frontend faz).
func (b *browser) session() map[string]any {
	b.e.t.Helper()
	r := b.do("GET", "/v1/session", nil)
	if r.status != 200 {
		b.e.t.Fatalf("GET /v1/session = %d %s", r.status, r.raw)
	}
	d := r.data()
	if tok, ok := d["csrf_token"].(string); ok {
		b.csrf = tok
	} else {
		b.csrf = ""
	}
	return d
}

func (b *browser) cookie(name string) *http.Cookie {
	u, _ := url.Parse(b.e.srv.URL)
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (b *browser) mustStatus(r resp, want int) resp {
	b.e.t.Helper()
	if r.status != want {
		b.e.t.Fatalf("status %d, esperado %d: %s", r.status, want, r.raw)
	}
	return r
}

// deliver entrega a caixa de saída para a caixa de entrada do teste.
func (e *env) deliver() {
	e.t.Helper()
	d := &mail.Dispatcher{Pool: e.pool, Ring: e.ring, Sender: senderFunc(func(m mail.Message) { e.inbox = append(e.inbox, m) }),
		Clock: e.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxAttempts: 3, BatchSize: 100}
	for {
		n, err := d.RunOnce(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

type senderFunc func(mail.Message)

func (f senderFunc) Send(_ context.Context, _ uuid.UUID, m mail.Message) error {
	f(m)
	return nil
}

var tokenInLink = regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`)

// lastToken entrega os e-mails e devolve o token do último e-mail para o endereço com o assunto.
func (e *env) lastToken(to, subject string) string {
	e.t.Helper()
	e.deliver()
	for i := len(e.inbox) - 1; i >= 0; i-- {
		m := e.inbox[i]
		if strings.EqualFold(m.To, to) && strings.Contains(m.Subject, subject) {
			if mm := tokenInLink.FindStringSubmatch(m.Body); mm != nil {
				return mm[1]
			}
		}
	}
	e.t.Fatalf("nenhum e-mail %q para %s com link", subject, to)
	return ""
}

func (e *env) mailCount(to string) int {
	e.deliver()
	n := 0
	for _, m := range e.inbox {
		if strings.EqualFold(m.To, to) {
			n++
		}
	}
	return n
}

const goodPassword = "senha-forte-123" // senha fictícia de teste; gitleaks:allow

// verifiedCustomer cadastra, verifica e devolve um navegador logado como cliente.
func (e *env) verifiedCustomer(email string) *browser {
	e.t.Helper()
	b := e.browser()
	b.mustStatus(b.do("POST", "/v1/auth/register", map[string]any{"name": "Pessoa Teste", "email": email, "password": goodPassword}), 202)
	tok := e.lastToken(email, "Confirme")
	b.mustStatus(b.do("POST", "/v1/auth/email-verification/confirm", map[string]any{"token": tok}), 204)
	b.login(email, goodPassword, "customer")
	return b
}

func (b *browser) login(email, password, kind string) resp {
	b.e.t.Helper()
	r := b.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": password, "context": kind})
	if r.status == 200 {
		b.session()
	}
	return r
}

// fixture de loja: dono por bootstrap + aceite; profissionais criados direto no banco
// (o cadastro de profissionais pela interface é da etapa 03).
type shop struct {
	id    uuid.UUID
	owner *browser
	email string
}

func (e *env) shop(slug, ownerEmail string) shop {
	e.t.Helper()
	res, err := e.api.Identity.Bootstrap(context.Background(), identidade.BootstrapInput{
		Name: "Loja " + slug, Slug: slug, Timezone: "America/Sao_Paulo", Email: ownerEmail})
	if err != nil {
		e.t.Fatal(err)
	}
	tok := e.lastToken(ownerEmail, "Convite")
	b := e.browser()
	b.mustStatus(b.do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Dona " + slug, "password": goodPassword}), 204)
	b.mustStatus(b.login(ownerEmail, goodPassword, "staff"), 200)
	return shop{id: res.EstablishmentID, owner: b, email: ownerEmail}
}

func (e *env) professional(estID uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	ctx := context.Background()
	err := e.pool.QueryRow(ctx, `
		INSERT INTO professional (establishment_id, display_name, queue_letter, created_at, updated_at)
		VALUES ($1, $2, chr(65 + (SELECT count(*)::int FROM professional WHERE establishment_id = $1)), $3, $3)
		RETURNING id`, estID, name, e.clock.Now()).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO queue (establishment_id, professional_id, created_at, updated_at)
		VALUES ($1, $2, $3, $3)`, estID, id, e.clock.Now()); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (b *browser) reauth() {
	b.e.t.Helper()
	b.mustStatus(b.do("POST", "/v1/auth/reauthenticate", map[string]any{"password": goodPassword}), 200)
}

var idemSeq int

func newIdemKey() string {
	idemSeq++
	return fmt.Sprintf("chave-teste-%08d", idemSeq)
}

// cmd envia um comando com Idempotency-Key nova (uma intenção = uma chave).
func (b *browser) cmd(method, path string, body any) resp {
	b.e.t.Helper()
	return b.do(method, path, body, "Idempotency-Key", newIdemKey())
}

// fakeOccupancy simula participações por fila e por profissional (etapa 04 trará as reais).
type fakeOccupancy struct {
	mu      sync.Mutex
	pending map[uuid.UUID]int
	active  map[uuid.UUID]int
}

func newFakeOccupancy() *fakeOccupancy {
	return &fakeOccupancy{pending: map[uuid.UUID]int{}, active: map[uuid.UUID]int{}}
}

func (f *fakeOccupancy) set(queueID, profID uuid.UUID, pending, active int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[queueID], f.active[profID] = pending, active
}

func (f *fakeOccupancy) PendingInQueue(_ context.Context, _ database.Querier, id uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending[id], nil
}

func (f *fakeOccupancy) ActiveForProfessional(_ context.Context, _ database.Querier, id uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active[id], nil
}

// staffMember convida e ativa o acesso de um profissional e devolve o navegador logado como equipe.
func (e *env) staffMember(loja shop, profID uuid.UUID, email string) *browser {
	e.t.Helper()
	loja.owner.reauth()
	loja.owner.mustStatus(loja.owner.cmd("POST", "/v1/establishments/"+loja.id.String()+"/professionals/"+profID.String()+"/invites",
		map[string]any{"email": email}), 201)
	tok := e.lastToken(email, "Convite")
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Pro", "password": goodPassword}), 204)
	b := e.browser()
	b.mustStatus(b.login(email, goodPassword, "staff"), 200)
	return b
}

func (e *env) queueOf(profID uuid.UUID) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM queue WHERE professional_id = $1`, profID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}
