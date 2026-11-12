package identidade

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/keys"
	"fila/internal/platform/mail"
	"fila/internal/platform/ratelimit"
)

// Durações das regras R-CONTA-03/05/06/07/10 e R-VIS-02/03/04 (convencoes.md §10).
const (
	CustomerIdle       = 7 * 24 * time.Hour
	CustomerAbsolute   = 30 * 24 * time.Hour
	StaffAbsolute      = 12 * time.Hour
	ReauthWindow       = 10 * time.Minute
	VerificationTTL    = 24 * time.Hour
	PasswordResetTTL   = 30 * time.Minute
	InviteTTL          = 24 * time.Hour
	VisitorIdle        = 7 * 24 * time.Hour
	VisitorAbsolute    = 30 * 24 * time.Hour
	VisitorAfterClosed = 24 * time.Hour
	// touchEvery evita gravar "último uso" a cada requisição.
	touchEvery = time.Minute
)

// Tipos de sessão de conta (SessionKind).
const (
	KindCustomer = "customer"
	KindStaff    = "staff"
)

// Config são as opções de implantação do módulo.
type Config struct {
	CookieSecure      bool
	PublicBaseURL     string
	TrustForwardedFor bool
}

// Service concentra as regras de identidade, sessões e acesso.
type Service struct {
	pool    *pgxpool.Pool
	clock   clock.Clock
	ring    *keys.Ring
	limiter *ratelimit.Limiter
	outbox  *mail.Outbox
	hasher  *Hasher
	idem    *idempotency.Runner
	log     *slog.Logger
	cfg     Config
}

// Deps são as dependências do serviço.
type Deps struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Ring   *keys.Ring
	Hasher *Hasher
	Log    *slog.Logger
	Config Config
}

// New cria o serviço.
func New(d Deps) *Service {
	return &Service{
		pool:    d.Pool,
		clock:   d.Clock,
		ring:    d.Ring,
		limiter: ratelimit.New(d.Pool, d.Clock, d.Ring),
		outbox:  mail.NewOutbox(d.Ring),
		hasher:  d.Hasher,
		idem:    idempotency.New(d.Pool, d.Clock, d.Log),
		log:     d.Log,
		cfg:     d.Config,
	}
}

// ---------------------------------------------------------------------------------------------
// Cookies

// Nomes dos cookies. Com Secure (produção/HTTPS) usam o prefixo __Host-, que o navegador só
// aceita com Secure, Path=/ e sem Domain (cookie preso a este host). Em desenvolvimento com
// http:// o prefixo não é aceito pelos navegadores, então o nome muda (ver ADR-0017).
func (s *Service) sessionCookieName() string {
	if s.cfg.CookieSecure {
		return "__Host-fila_session"
	}
	return "fila_session"
}

func (s *Service) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
}

// ---------------------------------------------------------------------------------------------
// Principal: quem está fazendo o pedido, segundo os cookies (nunca o corpo da requisição).

// AccountSession é uma sessão de conta válida.
type AccountSession struct {
	ID               uuid.UUID
	AccountID        uuid.UUID
	Kind             string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	ReauthValidUntil *time.Time
	AccountName      string
	AccountEmail     string
	AccountStatus    string
	EmailNormalized  string
}

// VisitorCredential é uma credencial de visitante válida (uma por loja no navegador).
type VisitorCredential struct {
	ID              uuid.UUID
	VisitorID       uuid.UUID
	EstablishmentID uuid.UUID
	Nick            string
	ExpiresAt       time.Time
}

// Principal reúne as identidades válidas deste navegador.
type Principal struct {
	Session   *AccountSession
	BrowserID *uuid.UUID // navegador de visitante (cookie de visitante válido)
	Visitors  []VisitorCredential
}

// VisitorIn devolve a credencial de visitante da loja, se houver.
func (p *Principal) VisitorIn(establishmentID uuid.UUID) *VisitorCredential {
	for i := range p.Visitors {
		if p.Visitors[i].EstablishmentID == establishmentID {
			return &p.Visitors[i]
		}
	}
	return nil
}

type principalKey struct{}

// PrincipalFrom lê o principal resolvido pelo middleware (nunca nil dentro de /v1).
func PrincipalFrom(ctx context.Context) *Principal {
	if p, ok := ctx.Value(principalKey{}).(*Principal); ok {
		return p
	}
	return &Principal{}
}

// Middleware resolve as sessões dos cookies e, em métodos não seguros, exige o token CSRF
// quando há alguma sessão (convencoes.md §10). Deve rodar depois da validação de Origin.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r) // saúde e rotas fora da API não usam sessão
			return
		}
		p, err := s.resolve(r.Context(), r)
		if err != nil {
			httpserver.WriteError(w, r, s.log, err)
			return
		}
		if httpserver.IsUnsafeMethod(r.Method) && (p.Session != nil || p.BrowserID != nil) {
			want := s.csrfToken(p)
			got := r.Header.Get("X-CSRF-Token")
			if got == "" || !hmac.Equal([]byte(got), []byte(want)) {
				httpserver.WriteError(w, r, s.log, &httpserver.Error{Status: 403, Code: "csrf_failed",
					Title: "Pedido não confirmado", Detail: "Atualize a página e tente de novo."})
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// csrfToken é um HMAC das sessões presentes: muda quando a pessoa entra, sai ou troca de
// visitante, e não pode ser calculado por quem não tem a chave do servidor.
func (s *Service) csrfToken(p *Principal) string {
	if p.Session == nil && p.BrowserID == nil {
		return ""
	}
	var sess, browser []byte
	if p.Session != nil {
		sess = p.Session.ID[:]
	}
	if p.BrowserID != nil {
		browser = p.BrowserID[:]
	}
	return base64.RawURLEncoding.EncodeToString(s.ring.MAC(keys.PurposeCSRF, []byte("sessao"), sess, []byte("navegador"), browser))
}

func (s *Service) resolve(ctx context.Context, r *http.Request) (*Principal, error) {
	p := &Principal{}
	now := s.clock.Now()
	if c, err := r.Cookie(s.sessionCookieName()); err == nil && c.Value != "" && len(c.Value) <= 64 {
		sess, err := s.loadSession(ctx, s.pool, hashToken(c.Value), now)
		if err != nil {
			return nil, err
		}
		p.Session = sess
	}
	return p, nil
}

// loadSession devolve a sessão válida do token (ou nil) e renova o "último uso".
func (s *Service) loadSession(ctx context.Context, q database.Querier, tokenHash []byte, now time.Time) (*AccountSession, error) {
	var sess AccountSession
	var lastUsed, absolute time.Time
	var idle *int32
	err := q.QueryRow(ctx, `
		SELECT s.id, s.account_id, s.kind, s.created_at, s.last_used_at, s.absolute_expires_at,
		       s.idle_timeout_seconds, s.reauth_valid_until, a.name, a.email, a.status, a.email_normalized
		FROM account_session s JOIN account a ON a.id = s.account_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND a.status = 'active'`, tokenHash).
		Scan(&sess.ID, &sess.AccountID, &sess.Kind, &sess.CreatedAt, &lastUsed, &absolute, &idle,
			&sess.ReauthValidUntil, &sess.AccountName, &sess.AccountEmail, &sess.AccountStatus, &sess.EmailNormalized)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler sessão: %w", err)
	}
	// T-SS-05: expiração verificada a cada uso, com o relógio do servidor.
	if !now.Before(absolute) {
		return nil, nil
	}
	if idle != nil && !now.Before(lastUsed.Add(time.Duration(*idle)*time.Second)) {
		return nil, nil
	}
	if now.Sub(lastUsed) >= touchEvery {
		if _, err := q.Exec(ctx, `UPDATE account_session SET last_used_at = $2 WHERE id = $1 AND revoked_at IS NULL`, sess.ID, now); err != nil {
			return nil, fmt.Errorf("renovar sessão: %w", err)
		}
		lastUsed = now
	}
	sess.ExpiresAt = absolute
	if idle != nil {
		sess.ExpiresAt = minTime(absolute, lastUsed.Add(time.Duration(*idle)*time.Second))
	}
	return &sess, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
