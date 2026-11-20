package identidade

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
	"fila/internal/platform/ratelimit"
	"fila/internal/platform/validate"
)

// Login com Google — exclusivo da jornada de cliente (ADR-0028).
//
// OpenID Connect com Authorization Code no backend e PKCE S256 (go-oidc + x/oauth2, bibliotecas
// mantidas; nenhum validador de JWT escrito à mão):
//   - POST /v1/auth/google/start (Origin + CSRF como qualquer comando) cria uma transação de uso
//     único com state, nonce e verificador PKCE, presa a este navegador (cookie de correlação
//     HttpOnly), à intenção (login ou vínculo), ao retorno validado e à loja resolvida;
//   - GET /v1/auth/google/callback consome a transação ANTES de trocar o código, troca o código
//     no servidor (o secret nunca sai do backend), valida assinatura, emissor, audiência,
//     expiração, nonce e azp, e redireciona na hora para uma rota interna limpa.
//
// A chave da identidade é provedor + emissor + sub (o e-mail muda; o sub não). Nunca há vínculo
// automático por e-mail igual. Sessão aberta pelo Google é SEMPRE de cliente (o banco recusa
// sessão de equipe que não seja por senha): nada administrativo é desbloqueado por ela.

// GoogleSettings vem da configuração (FILA_GOOGLE_*). Vazio = desligado.
type GoogleSettings struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Issuer       string
	// HTTPClient (opcional, testes) fala com o provedor. nil = cliente com tempo-limite de 10 s.
	HTTPClient *http.Client
}

func (g GoogleSettings) enabled() bool {
	return g.ClientID != "" && g.ClientSecret != "" && g.RedirectURL != ""
}

const (
	oidcTxTTL         = 10 * time.Minute
	oidcCookieAge     = 15 * time.Minute
	oidcMaxPerBrowser = 5
	providerGoogle    = "google"
)

var (
	ruleGoogleStartIP = ratelimit.Rule{Name: "google_inicio_ip", Limit: 30, Window: 15 * time.Minute}
	ruleReauthCode    = ratelimit.Rule{Name: "codigo_reauth_conta", Limit: 5, Window: 15 * time.Minute}
	ruleReauthMail    = ratelimit.Rule{Name: "codigo_reauth_envio", Limit: 3, Window: 15 * time.Minute}
)

// Destinos de retorno aceitos depois do Google: lista fechada de rotas internas (nunca URL
// absoluta, "//", barra invertida, controle ou codificação ambígua — nada disso casa aqui).
var returnPattern = regexp.MustCompile(`^/(conta|loja/[a-z0-9](?:[a-z0-9-]{1,48}[a-z0-9])(?:/(?:historico|pontos))?)$`)

// SafeReturn valida o destino de volta no servidor.
func SafeReturn(v string) (string, bool) {
	if v == "" || len(v) > 300 || !returnPattern.MatchString(v) {
		return "", false
	}
	return v, true
}

type googleClient struct {
	mu       sync.Mutex
	provider *oidc.Provider
}

func (s *Service) googleHTTPClient() *http.Client {
	if s.cfg.Google.HTTPClient != nil {
		return s.cfg.Google.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// provider descobre o emissor (discovery + JWKS oficiais) sob demanda e guarda o resultado; uma
// falha não fica guardada (o próximo pedido tenta de novo).
func (s *Service) provider(ctx context.Context) (*oidc.Provider, error) {
	s.google.mu.Lock()
	defer s.google.mu.Unlock()
	if s.google.provider != nil {
		return s.google.provider, nil
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, s.googleHTTPClient()), 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, s.cfg.Google.Issuer)
	if err != nil {
		return nil, err
	}
	s.google.provider = p
	return p, nil
}

func (s *Service) oauthConfig(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.cfg.Google.ClientID,
		ClientSecret: s.cfg.Google.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  s.cfg.Google.RedirectURL,
		// Escopos mínimos: sem Drive, Gmail, acesso offline ou refresh token.
		Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}
}

var errGoogleUnavailable = &httpserver.Error{Status: 503, Code: "google_unavailable", Title: "Google indisponível",
	Detail: "Entrar com o Google não está disponível agora. Use e-mail e senha ou entre sem conta."}

func (s *Service) oidcCookieName() string {
	if s.cfg.CookieSecure {
		return "__Host-fila_oidc"
	}
	return "fila_oidc"
}

// ---------------------------------------------------------------------------------------------
// startGoogleAuth

type googleStartRequest struct {
	Intent            *string `json:"intent"`
	EstablishmentSlug *string `json:"establishment_slug"`
	ReturnTo          *string `json:"return_to"`
}

type googleStartView struct {
	AuthorizationURL string `json:"authorization_url"`
}

func (s *Service) startGoogleAuth(w http.ResponseWriter, r *http.Request) error {
	var req googleStartRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !s.cfg.Google.enabled() {
		return errGoogleUnavailable
	}
	var v validate.Collector
	v.Required("/intent", req.Intent == nil)
	if req.Intent != nil && *req.Intent != "login" && *req.Intent != "link" {
		v.Add(&httpserver.FieldError{Field: "/intent", Code: httpserver.FieldNotAllowed, Message: "use login ou link"})
	}
	returnTo := ""
	if req.ReturnTo != nil {
		rt, ok := SafeReturn(*req.ReturnTo)
		if !ok {
			v.Add(&httpserver.FieldError{Field: "/return_to", Code: httpserver.FieldNotAllowed, Message: "destino de volta não permitido"})
		}
		returnTo = rt
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.allow(ctx, lim(ruleGoogleStartIP, s.clientIP(r))); err != nil {
		return err
	}
	intent := *req.Intent
	var estID *uuid.UUID
	if req.EstablishmentSlug != nil {
		// Loja inexistente ou encerrada: não inicia o Google.
		if !slugPattern.MatchString(*req.EstablishmentSlug) {
			return httpserver.ErrNotFound()
		}
		e, err := estabelecimento.PublicBySlug(ctx, s.pool, *req.EstablishmentSlug)
		if errors.Is(err, estabelecimento.ErrNotFound) {
			return httpserver.ErrNotFound()
		}
		if err != nil {
			return err
		}
		estID = &e.ID
		base := "/loja/" + e.Slug
		if returnTo == "" {
			returnTo = base
		} else if returnTo != base && !strings.HasPrefix(returnTo, base+"/") {
			return httpserver.ErrValidation(httpserver.FieldError{Field: "/return_to", Code: httpserver.FieldNotAllowed,
				Message: "o destino precisa ser a mesma loja"})
		}
	}
	var accountID, sessionID *uuid.UUID
	if intent == "link" {
		// Vincular exige sessão válida e reautenticação recente; o Google prova a outra ponta.
		sess, err := requireAccount(r)
		if err != nil {
			return err
		}
		if err := s.requireReauth(sess); err != nil {
			return err
		}
		var linked bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM external_identity WHERE account_id = $1 AND provider = 'google')`,
			sess.AccountID).Scan(&linked); err != nil {
			return err
		}
		if linked {
			return errIdentityLinked
		}
		accountID, sessionID = &sess.AccountID, &sess.ID
		returnTo = "/conta"
	}
	p, err := s.provider(ctx)
	if err != nil {
		s.log.Warn("descoberta do provedor Google falhou", "error", err.Error())
		return errGoogleUnavailable
	}
	// Cookie de correlação: o mesmo navegador pode ter várias transações (várias abas), cada uma
	// com state próprio; o cookie prova que o callback volta ao navegador que iniciou.
	browser := ""
	if c, err := r.Cookie(s.oidcCookieName()); err == nil && len(c.Value) == 43 {
		browser = c.Value
	} else {
		browser, _ = newToken()
	}
	state, stateHash := newToken()
	nonce, nonceHash := newToken()
	verifier := oauth2.GenerateVerifier()
	sealed, err := s.ring.Seal(keys.PurposeOIDC, []byte(verifier), stateHash)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	var ret *string
	if returnTo != "" {
		ret = &returnTo
	}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		bh := hashToken(browser)
		// Limite por navegador: as transações mais antigas deixam de valer.
		if _, err := tx.Exec(ctx, `UPDATE oidc_transaction SET consumed_at = $3 WHERE id IN (
			SELECT id FROM oidc_transaction WHERE browser_hash = $1 AND consumed_at IS NULL AND expires_at > $3
			ORDER BY created_at DESC OFFSET $2)`, bh, oidcMaxPerBrowser-1, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO oidc_transaction (state_hash, browser_hash, nonce_hash, verifier_sealed, intent,
			return_to, establishment_id, account_id, session_id, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			stateHash, bh, nonceHash, sealed, intent, ret, estID, accountID, sessionID, now, now.Add(oidcTxTTL))
		return err
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: s.oidcCookieName(), Value: browser, Path: "/", MaxAge: int(oidcCookieAge.Seconds()),
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	authURL := s.oauthConfig(p).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("prompt", "select_account"))
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[googleStartView]{Data: googleStartView{AuthorizationURL: authURL}})
	return nil
}

var errIdentityLinked = &httpserver.Error{Status: 409, Code: "identity_already_linked", Title: "Google já conectado",
	Detail: "Esta conta já tem um Google conectado."}

// ---------------------------------------------------------------------------------------------
// googleCallback

type oidcTx struct {
	ID        uuid.UUID
	Intent    string
	Sealed    []byte
	NonceHash []byte
	Return    *string
	AccountID *uuid.UUID
	SessionID *uuid.UUID
}

// googleClaims são as declarações usadas do id_token (já verificado pela biblioteca).
type googleClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	HostedDomain  string `json:"hd"`
	Name          string `json:"name"`
	AZP           string `json:"azp"`
}

// Resultados do callback (vão na query da rota interna; nunca detalhes técnicos).
const (
	gOK            = "ok"
	gCancelled     = "cancelado"
	gFailed        = "falhou"
	gAccountExists = "conta_existente"
	gVerifyEmail   = "verificar_email"
	gLinked        = "conectado"
	gAlreadyLinked = "ja_vinculado"
	gUnavailable   = "indisponivel"
)

// redirectInternal responde 303 para uma rota interna (sem o código nem o state na barra).
func redirectInternal(w http.ResponseWriter, path string, params url.Values) {
	target := path
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Service) googleCallback(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	fail := func(intent, status string, ret *string) error {
		params := url.Values{"google": {status}}
		if intent == "link" {
			redirectInternal(w, "/conta", params)
			return nil
		}
		if ret != nil {
			params.Set("voltar", *ret)
		}
		redirectInternal(w, "/entrar", params)
		return nil
	}
	if !s.cfg.Google.enabled() {
		return fail("login", gUnavailable, nil)
	}
	state := q.Get("state")
	if len(state) != 43 {
		return fail("login", gFailed, nil)
	}
	ctx := r.Context()
	now := s.clock.Now()
	c, err := r.Cookie(s.oidcCookieName())
	if err != nil || len(c.Value) != 43 {
		return fail("login", gFailed, nil)
	}
	// Consome a transação ANTES de qualquer outra coisa: state reutilizado, vencido, de outro
	// navegador ou callback repetido não passam daqui.
	var tx *oidcTx
	err = database.InTx(ctx, s.pool, func(dbtx pgx.Tx) error {
		var t oidcTx
		var browserHash []byte
		var expires time.Time
		var consumed *time.Time
		err := dbtx.QueryRow(ctx, `SELECT id, intent, verifier_sealed, nonce_hash, return_to, account_id, session_id,
			browser_hash, expires_at, consumed_at FROM oidc_transaction WHERE state_hash = $1 FOR UPDATE`, hashToken(state)).
			Scan(&t.ID, &t.Intent, &t.Sealed, &t.NonceHash, &t.Return, &t.AccountID, &t.SessionID, &browserHash, &expires, &consumed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if consumed != nil || !now.Before(expires) || subtle.ConstantTimeCompare(browserHash, hashToken(c.Value)) != 1 {
			return nil
		}
		if _, err := dbtx.Exec(ctx, `UPDATE oidc_transaction SET consumed_at = $2 WHERE id = $1`, t.ID, now); err != nil {
			return err
		}
		tx = &t
		return nil
	})
	if err != nil {
		return err
	}
	if tx == nil {
		return fail("login", gFailed, nil)
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return fail(tx.Intent, gCancelled, tx.Return)
		}
		return fail(tx.Intent, gFailed, tx.Return)
	}
	code := q.Get("code")
	if code == "" || len(code) > 2048 {
		return fail(tx.Intent, gFailed, tx.Return)
	}
	verifier, err := s.ring.Open(keys.PurposeOIDC, tx.Sealed, hashToken(state))
	if err != nil {
		return fail(tx.Intent, gFailed, tx.Return)
	}
	claims, issuer, subject, err := s.exchange(ctx, code, string(verifier), tx.NonceHash)
	if err != nil {
		// Sem código, token, state ou nonce no log: só a categoria do erro.
		s.log.Warn("login com Google recusado", "request_id", httpserver.RequestIDFrom(ctx), "motivo", err.Error())
		return fail(tx.Intent, gFailed, tx.Return)
	}
	if tx.Intent == "link" {
		status := s.linkGoogle(ctx, tx, claims, issuer, subject)
		redirectInternal(w, "/conta", url.Values{"google": {status}})
		return nil
	}
	status, token, err := s.loginGoogle(ctx, claims, issuer, subject)
	if err != nil {
		return err
	}
	if status != gOK {
		return fail("login", status, tx.Return)
	}
	s.setCookie(w, s.sessionCookieName(), token, CustomerAbsolute)
	dest := "/conta"
	if tx.Return != nil {
		dest = *tx.Return
	}
	redirectInternal(w, dest, nil)
	return nil
}

// Motivos internos de recusa (só categorias: nunca o conteúdo).
var (
	errOIDCExchange = errors.New("troca do código recusada pelo provedor")
	errOIDCToken    = errors.New("id_token inválido")
	errOIDCNonce    = errors.New("nonce não confere")
	errOIDCAzp      = errors.New("azp não confere")
)

// exchange troca o código (com o verificador PKCE) e valida o id_token. Os tokens do Google não
// são guardados: depois da verificação, só ficam emissor, sub e e-mail exibido ao titular.
func (s *Service) exchange(ctx context.Context, code, verifier string, nonceHash []byte) (googleClaims, string, string, error) {
	var gc googleClaims
	p, err := s.provider(ctx)
	if err != nil {
		return gc, "", "", errOIDCExchange
	}
	cctx, cancel := context.WithTimeout(context.WithValue(ctx, oauth2.HTTPClient, s.googleHTTPClient()), 10*time.Second)
	defer cancel()
	tok, err := s.oauthConfig(p).Exchange(cctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return gc, "", "", errOIDCExchange
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return gc, "", "", errOIDCToken
	}
	// Assinatura (JWKS oficial), emissor, audiência (client ID) e expiração.
	idt, err := p.VerifierContext(oidc.ClientContext(cctx, s.googleHTTPClient()), &oidc.Config{ClientID: s.cfg.Google.ClientID}).Verify(cctx, raw)
	if err != nil {
		return gc, "", "", errOIDCToken
	}
	if idt.Nonce == "" || subtle.ConstantTimeCompare(hashToken(idt.Nonce), nonceHash) != 1 {
		return gc, "", "", errOIDCNonce
	}
	if err := idt.Claims(&gc); err != nil {
		return gc, "", "", errOIDCToken
	}
	if gc.AZP != "" && gc.AZP != s.cfg.Google.ClientID {
		return gc, "", "", errOIDCAzp
	}
	if idt.Subject == "" || len(idt.Subject) > 255 {
		return gc, "", "", errOIDCToken
	}
	return gc, idt.Issuer, idt.Subject, nil
}

// emailAuthoritative: o Google só é autoridade do e-mail quando ele foi verificado E é Gmail ou
// de um domínio Google Workspace (claim hd). Nos demais casos a verificação local é exigida.
func emailAuthoritative(c googleClaims, emailNorm string) bool {
	return c.EmailVerified && (c.HostedDomain != "" || strings.HasSuffix(emailNorm, "@gmail.com"))
}

// loginGoogle entra (ou cria a conta de CLIENTE) a partir da identidade verificada. Devolve o
// token da sessão nova quando o acesso foi liberado.
func (s *Service) loginGoogle(ctx context.Context, c googleClaims, issuer, subject string) (string, string, error) {
	ctx = database.WithoutTxGuard(ctx)
	now := s.clock.Now()
	prev := PrincipalFrom(ctx)
	for attempt := 0; attempt < 2; attempt++ {
		status, token, err := s.loginGoogleOnce(ctx, prev, c, issuer, subject, now)
		if errors.Is(err, errConcurrentSignup) {
			continue // dois callbacks simultâneos da mesma identidade: o outro criou; agora é login
		}
		return status, token, err
	}
	return gFailed, "", nil
}

func (s *Service) loginGoogleOnce(ctx context.Context, prev *Principal, c googleClaims, issuer, subject string, now time.Time) (string, string, error) {
	status, token := gOK, ""
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var accountID uuid.UUID
		var accStatus string
		err := tx.QueryRow(ctx, `SELECT a.id, a.status FROM external_identity i JOIN account a ON a.id = i.account_id
			WHERE i.provider = 'google' AND i.issuer = $1 AND i.subject = $2 FOR UPDATE OF i`, issuer, subject).Scan(&accountID, &accStatus)
		switch {
		case err == nil:
			// Identidade já vinculada: mesma conta, mesmo que o e-mail do Google tenha mudado. O
			// e-mail local, a verificação e as permissões não mudam por isso.
			switch accStatus {
			case "active":
			case "pending_verification":
				status = gVerifyEmail
				return nil
			default:
				status = gFailed
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE external_identity SET last_login_at = $3, email = coalesce($4, email)
				WHERE provider = 'google' AND issuer = $1 AND subject = $2`, issuer, subject, now, emailOrNil(c.Email)); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
			email, emailNorm, fe := validate.Email("/email", c.Email)
			if fe != nil {
				status = gFailed
				return nil
			}
			existing, err := findAccount(ctx, tx, emailNorm, true)
			if err != nil {
				return err
			}
			if existing != nil {
				// Nunca vincula por e-mail igual: a pessoa entra pelo método que já tem e conecta o
				// Google depois, em Minha conta (com reautenticação).
				status = gAccountExists
				return nil
			}
			name := strings.TrimSpace(c.Name)
			if name == "" {
				name, _, _ = strings.Cut(email, "@")
			}
			if utf8.RuneCountInString(name) > 80 {
				name = string([]rune(name)[:80])
			}
			auth := emailAuthoritative(c, emailNorm)
			accStatus = "pending_verification"
			var verified *time.Time
			if auth {
				accStatus, verified = "active", &now
			}
			// Só identidade de cliente: nenhum vínculo de equipe nasce aqui. Sem senha (NULL).
			err = tx.QueryRow(ctx, `INSERT INTO account (name, email, email_normalized, password_hash, status, email_verified_at,
				password_changed_at, created_at, updated_at) VALUES ($1, $2, $3, NULL, $4, $5, $6, $6, $6) RETURNING id`,
				name, email, emailNorm, accStatus, verified, now).Scan(&accountID)
			if database.IsUniqueViolation(err, "account_email_uq") {
				status = gAccountExists
				return nil
			}
			if err != nil {
				return fmt.Errorf("criar conta pelo Google: %w", err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO external_identity (account_id, provider, issuer, subject, email, created_at, last_login_at)
				VALUES ($1, 'google', $2, $3, $4, $5, $5)`, accountID, issuer, subject, email, now)
			if database.IsUniqueViolation(err, "external_identity_subject_uq") {
				return errConcurrentSignup
			}
			if err != nil {
				return err
			}
			if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
				ActorID: auditoria.Ptr(accountID), Action: "T-AC-05", TargetType: "account", TargetID: accountID,
				ToState: accStatus, Reason: "conta de cliente criada pelo Google", RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
				return err
			}
			if !auth {
				// O Google não é autoridade deste e-mail: confirmação local antes de liberar.
				tok, err := issueAccountToken(ctx, tx, accountID, "email_verification", VerificationTTL, now)
				if err != nil {
					return err
				}
				if err := s.mailVerification(ctx, tx, email, name, tok, now); err != nil {
					return err
				}
				status = gVerifyEmail
				return nil
			}
		default:
			return err
		}
		// Sessão nova de cliente (rotação: a sessão anterior deste navegador é encerrada).
		if prev.Session != nil {
			if _, err := tx.Exec(ctx, `UPDATE account_session SET revoked_at = $2, revoke_reason = 'replaced'
				WHERE id = $1 AND revoked_at IS NULL`, prev.Session.ID, now); err != nil {
				return err
			}
			if err := revokeBindings(ctx, tx, prev.Session.ID, now); err != nil {
				return err
			}
			if err := notifyRevocation(ctx, tx); err != nil {
				return err
			}
		}
		var tokenHash []byte
		token, tokenHash = newToken()
		secs := int32(CustomerIdle.Seconds())
		var sid uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO account_session (account_id, token_hash, kind, auth_method, created_at, last_used_at,
			absolute_expires_at, idle_timeout_seconds) VALUES ($1, $2, 'customer', 'google', $3, $3, $4, $5) RETURNING id`,
			accountID, tokenHash, now, now.Add(CustomerAbsolute), secs).Scan(&sid); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(accountID), Action: "T-SS-08", TargetType: "session", TargetID: sid,
			ToState: "active", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return "", "", err
	}
	if status != gOK {
		token = ""
	}
	return status, token, nil
}

func emailOrNil(e string) *string {
	if _, _, fe := validate.Email("/email", e); fe != nil {
		return nil
	}
	return &e
}

// linkGoogle conecta o Google à conta da MESMA sessão que iniciou (com reautenticação recente).
func (s *Service) linkGoogle(ctx context.Context, tx *oidcTx, c googleClaims, issuer, subject string) string {
	p := PrincipalFrom(ctx)
	if p.Session == nil || tx.SessionID == nil || p.Session.ID != *tx.SessionID || p.Session.AccountID != *tx.AccountID {
		return gFailed
	}
	now := s.clock.Now()
	status := gLinked
	err := database.InTx(ctx, s.pool, func(dbtx pgx.Tx) error {
		var owner uuid.UUID
		err := dbtx.QueryRow(ctx, `SELECT account_id FROM external_identity WHERE provider = 'google' AND issuer = $1 AND subject = $2`,
			issuer, subject).Scan(&owner)
		if err == nil {
			// Já pertence a outra conta (ou já é desta): nada muda.
			status = gAlreadyLinked
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var email *string
		if _, _, fe := validate.Email("/email", c.Email); fe == nil {
			email = &c.Email
		}
		_, err = dbtx.Exec(ctx, `INSERT INTO external_identity (account_id, provider, issuer, subject, email, created_at)
			VALUES ($1, 'google', $2, $3, $4, $5)`, *tx.AccountID, issuer, subject, email, now)
		if database.IsUniqueViolation(err, "") {
			status = gAlreadyLinked
			return nil
		}
		if err != nil {
			return err
		}
		return auditoria.Record(ctx, dbtx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: tx.AccountID, Action: "T-AC-06", TargetType: "account", TargetID: *tx.AccountID,
			FromState: "active", ToState: "active", Reason: "Google conectado", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		s.log.Error("vínculo do Google falhou", "request_id", httpserver.RequestIDFrom(ctx), "error", database.Describe(err))
		return gFailed
	}
	return status
}

// ---------------------------------------------------------------------------------------------
// unlinkGoogle

var errLastAccessMethod = &httpserver.Error{Status: 409, Code: "last_access_method", Title: "Único jeito de entrar",
	Detail: "O Google é o único jeito de entrar nesta conta. Desconectá-lo deixaria a conta sem acesso."}

func (s *Service) unlinkGoogle(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	if err := s.requireReauth(sess); err != nil {
		return err
	}
	ctx := r.Context()
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var hasPassword bool
		if err := tx.QueryRow(ctx, `SELECT password_hash IS NOT NULL FROM account WHERE id = $1 FOR UPDATE`, sess.AccountID).
			Scan(&hasPassword); err != nil {
			return err
		}
		if !hasPassword {
			return errLastAccessMethod
		}
		tag, err := tx.Exec(ctx, `DELETE FROM external_identity WHERE account_id = $1 AND provider = 'google'`, sess.AccountID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil // já desconectado: repetir não muda nada
		}
		// Sessões abertas pelo Google deixam de valer: o método foi retirado.
		if _, err := tx.Exec(ctx, `UPDATE account_session SET revoked_at = $2, revoke_reason = 'logout'
			WHERE account_id = $1 AND auth_method = 'google' AND revoked_at IS NULL AND id <> $3`, sess.AccountID, now, sess.ID); err != nil {
			return err
		}
		if err := notifyRevocation(ctx, tx); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(sess.AccountID), Action: "T-AC-07", TargetType: "account", TargetID: sess.AccountID,
			FromState: "active", ToState: "active", Reason: "Google desconectado", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------------------------------------
// Reautenticação por código no e-mail (conta sem senha). Escolher uma conta no Google não prova
// presença recente (o Google não garante prompt=login nem auth_time); por isso o código.

var errReauthCodeInvalid = &httpserver.Error{Status: 401, Code: "reauth_code_invalid", Title: "Código inválido ou vencido",
	Detail: "Confira o código do e-mail ou peça outro."}

func (s *Service) reauthCodeHash(sessionID uuid.UUID, code string) []byte {
	return s.ring.MAC(keys.PurposeReauthCode, sessionID[:], []byte(code))
}

func (s *Service) requestReauthCode(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var hasPassword bool
	if err := s.pool.QueryRow(ctx, `SELECT password_hash IS NOT NULL FROM account WHERE id = $1`, sess.AccountID).Scan(&hasPassword); err != nil {
		return err
	}
	if hasPassword {
		return &httpserver.Error{Status: 409, Code: "password_available", Title: "Use sua senha",
			Detail: "Esta conta tem senha: confirme sua identidade digitando a senha."}
	}
	if err := s.allow(ctx, lim(ruleReauthMail, sess.AccountID.String())); err != nil {
		return err
	}
	code, err := newNumericCode(8)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE account_token SET revoked_at = $2
			WHERE account_id = $1 AND purpose = 'reauth_code' AND consumed_at IS NULL AND revoked_at IS NULL`, sess.AccountID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_token (account_id, purpose, token_hash, session_id, created_at, expires_at)
			VALUES ($1, 'reauth_code', $2, $3, $4, $5)`, sess.AccountID, s.reauthCodeHash(sess.ID, code), sess.ID, now, now.Add(ReauthWindow)); err != nil {
			return err
		}
		return s.mailReauthCode(ctx, tx, sess.AccountEmail, code, now)
	})
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusAccepted, httpserver.Data[messageView]{Data: messageView{
		Message: "Enviamos um código para o seu e-mail. Ele vale por 10 minutos."}})
	return nil
}

// checkReauthCode consome o código desta sessão (tentativas limitadas por conta).
func (s *Service) checkReauthCode(ctx context.Context, sess *AccountSession, code string) error {
	l := lim(ruleReauthCode, sess.AccountID.String())
	if err := s.checkBlocked(ctx, l); err != nil {
		return err
	}
	now := s.clock.Now()
	ok := false
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE account_token SET consumed_at = $4
			WHERE token_hash = $1 AND account_id = $2 AND session_id = $3 AND purpose = 'reauth_code'
			  AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > $4`,
			s.reauthCodeHash(sess.ID, code), sess.AccountID, sess.ID, now)
		ok = err == nil && tag.RowsAffected() == 1
		return err
	})
	if err != nil {
		return err
	}
	if !ok {
		if err := s.hit(ctx, l); err != nil {
			return err
		}
		return errReauthCodeInvalid
	}
	return nil
}

var codePattern = regexp.MustCompile(`^[0-9]{8}$`)

// PruneOIDC apaga transações do Google vencidas há mais de uma hora (limpeza do worker).
func PruneOIDC(ctx context.Context, q database.Querier, now time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM oidc_transaction WHERE expires_at < $1`, now.Add(-time.Hour))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
