package identidade

import (
	"context"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/validate"
)

// ---------------------------------------------------------------------------------------------
// getSession

func (s *Service) getSession(w http.ResponseWriter, r *http.Request) error {
	v, err := s.buildSessionView(r.Context(), s.pool, PrincipalFrom(r.Context()))
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[sessionView]{Data: v})
	return nil
}

// ---------------------------------------------------------------------------------------------
// login (T-SS-01)

type loginRequest struct {
	Email    *string `json:"email"`
	Password *string `json:"password"`
	Context  *string `json:"context"`
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/email", req.Email == nil)
	v.Required("/password", req.Password == nil || *req.Password == "")
	v.Required("/context", req.Context == nil)
	if err := v.Err(); err != nil {
		return err
	}
	_, emailNorm, fe := validate.Email("/email", *req.Email)
	v.Add(fe)
	if len([]rune(*req.Password)) > MaxPasswordLen {
		v.Add(&httpserver.FieldError{Field: "/password", Code: httpserver.FieldTooLong})
	}
	if *req.Context != KindCustomer && *req.Context != KindStaff {
		v.Add(&httpserver.FieldError{Field: "/context", Code: httpserver.FieldNotAllowed, Message: "use customer ou staff"})
	}
	if err := v.Err(); err != nil {
		return err
	}

	ctx := r.Context()
	limits := []limitCheck{lim(rulePasswordAccount, emailNorm), lim(rulePasswordIP, s.clientIP(r))}
	if err := s.checkBlocked(ctx, limits...); err != nil {
		return err
	}
	a, err := findAccount(ctx, s.pool, emailNorm, false)
	if err != nil {
		return err
	}
	if a == nil || (a.Status != "active" && a.Status != "pending_verification") {
		s.hasher.Burn(ctx, *req.Password) // mesmo custo de uma conta existente
		if err := s.hit(ctx, limits...); err != nil {
			return err
		}
		return errInvalidCredentials
	}
	ok, rehash, err := s.hasher.Verify(ctx, *req.Password, a.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		if err := s.hit(ctx, limits...); err != nil {
			return err
		}
		return errInvalidCredentials
	}
	if a.Status == "pending_verification" {
		return &httpserver.Error{Status: 403, Code: "email_not_verified", Title: "Confirme seu e-mail",
			Detail: "Abra o link enviado para o seu e-mail para ativar a conta. Você pode pedir um novo link."}
	}
	kind := *req.Context
	if kind == KindStaff {
		ms, err := estabelecimento.ActiveMemberships(ctx, s.pool, a.ID)
		if err != nil {
			return err
		}
		if len(ms) == 0 {
			return httpserver.ErrForbidden("Esta conta não tem acesso de equipe ativo. Entre como cliente.")
		}
	}

	now := s.clock.Now()
	var newHash string
	if rehash {
		if newHash, err = s.hasher.Hash(ctx, *req.Password); err != nil {
			return err
		}
	}
	prev := PrincipalFrom(ctx)
	token, tokenHash := newToken()
	var sess *AccountSession
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if newHash != "" {
			if _, err := tx.Exec(ctx, `UPDATE account SET password_hash = $2, updated_at = $3 WHERE id = $1`, a.ID, newHash, now); err != nil {
				return err
			}
		}
		// Um navegador tem uma sessão de conta por vez: a anterior é encerrada.
		if prev.Session != nil {
			if _, err := tx.Exec(ctx, `UPDATE account_session SET revoked_at = $2, revoke_reason = 'replaced'
				WHERE id = $1 AND revoked_at IS NULL`, prev.Session.ID, now); err != nil {
				return err
			}
			if err := notifyRevocation(ctx, tx); err != nil {
				return err
			}
		}
		absolute, idle := now.Add(StaffAbsolute), (*int32)(nil)
		if kind == KindCustomer {
			absolute = now.Add(CustomerAbsolute)
			secs := int32(CustomerIdle.Seconds())
			idle = &secs
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO account_session (account_id, token_hash, kind, created_at, last_used_at, absolute_expires_at, idle_timeout_seconds)
			VALUES ($1, $2, $3, $4, $4, $5, $6) RETURNING id`, a.ID, tokenHash, kind, now, absolute, idle).Scan(&id); err != nil {
			return err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(a.ID), Action: "T-SS-01", TargetType: "session", TargetID: id,
			ToState: "active", RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return err
		}
		sess, err = s.loadSession(ctx, tx, tokenHash, now)
		return err
	})
	if err != nil {
		return err
	}
	p := &Principal{Session: sess, BrowserID: prev.BrowserID, Visitors: prev.Visitors}
	view, err := s.buildSessionView(ctx, s.pool, p)
	if err != nil {
		return err
	}
	// O cookie dura até o limite absoluto; a inatividade é conferida no servidor a cada uso.
	cookieAge := StaffAbsolute
	if kind == KindCustomer {
		cookieAge = CustomerAbsolute
	}
	s.setCookie(w, s.sessionCookieName(), token, cookieAge)
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[sessionView]{Data: view})
	return nil
}

// ---------------------------------------------------------------------------------------------
// logout (T-SS-02)

func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE account_session SET revoked_at = $2, revoke_reason = 'logout'
			WHERE id = $1 AND revoked_at IS NULL`, sess.ID, now); err != nil {
			return err
		}
		if err := notifyRevocation(ctx, tx); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(sess.AccountID), Action: "T-SS-02", TargetType: "session", TargetID: sess.ID,
			FromState: "active", ToState: "revoked", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	s.clearCookie(w, s.sessionCookieName())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------------------------------------
// reauthenticate (T-SS-06)

type reauthRequest struct {
	Password *string `json:"password"`
}

func (s *Service) reauthenticate(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	var req reauthRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Password == nil || *req.Password == "" {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/password", Code: httpserver.FieldRequired})
	}
	if len([]rune(*req.Password)) > MaxPasswordLen {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/password", Code: httpserver.FieldTooLong})
	}
	ctx := r.Context()
	if err := s.checkPassword(ctx, r, sess.AccountID, sess.EmailNormalized, *req.Password); err != nil {
		return err
	}
	now := s.clock.Now()
	until := now.Add(ReauthWindow)
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE account_session SET reauth_valid_until = $2 WHERE id = $1 AND revoked_at IS NULL`,
			sess.ID, until); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(sess.AccountID), Action: "T-SS-06", TargetType: "session", TargetID: sess.ID,
			ToState: "reauthenticated", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	p := PrincipalFrom(ctx)
	updated := *p.Session
	updated.ReauthValidUntil = &until
	view, err := s.buildSessionView(ctx, s.pool, &Principal{Session: &updated, BrowserID: p.BrowserID, Visitors: p.Visitors})
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[sessionView]{Data: view})
	return nil
}

// ---------------------------------------------------------------------------------------------
// getPublicEstablishmentBySlug

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,48}[a-z0-9])$`)

func (s *Service) publicEstablishment(w http.ResponseWriter, r *http.Request) error {
	slug := r.PathValue("slug")
	if !slugPattern.MatchString(slug) {
		return httpserver.ErrNotFound()
	}
	e, err := estabelecimento.PublicBySlug(r.Context(), s.pool, slug)
	if err == estabelecimento.ErrNotFound {
		return httpserver.ErrNotFound()
	}
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[publicEstablishmentView]{Data: publicEstablishmentView{
		ID: e.ID, Slug: e.Slug, Name: e.Name, Timezone: e.Timezone}})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Autorização (R-PERM-01, R-PERM-05, R-PERM-06): o ator vem só dos cookies.

// requireAccount exige sessão de conta válida (qualquer contexto).
func requireAccount(r *http.Request) (*AccountSession, error) {
	p := PrincipalFrom(r.Context())
	if p.Session == nil {
		return nil, httpserver.ErrUnauthenticated()
	}
	return p.Session, nil
}

// staffActor resolve o ator de equipe da loja (R-PERM-01/05/06): o papel vem só da sessão e
// dos vínculos ativos, nunca do pedido.
//   - sem sessão → 401;
//   - sem vínculo nenhum com a loja → 404 (não revela que a loja existe);
//   - com vínculo, mas sessão de cliente → 403.
func (s *Service) staffActor(r *http.Request, estID uuid.UUID) (acesso.Actor, error) {
	sess, err := requireAccount(r)
	if err != nil {
		return acesso.Actor{}, err
	}
	ms, err := estabelecimento.MembershipsIn(r.Context(), s.pool, sess.AccountID, estID)
	if err != nil {
		return acesso.Actor{}, err
	}
	if len(ms) == 0 {
		return acesso.Actor{}, httpserver.ErrNotFound()
	}
	if sess.Kind != KindStaff {
		return acesso.Actor{}, httpserver.ErrForbidden("Para esta ação, entre com o acesso de equipe.")
	}
	a := acesso.Actor{AccountID: sess.AccountID, EstablishmentID: estID, Role: acesso.RoleProfessional}
	for _, m := range ms {
		if m.Role == estabelecimento.RoleOwner {
			a.Role = acesso.RoleOwner
		}
		if m.ProfessionalID != nil {
			a.ProfessionalID = m.ProfessionalID
		}
	}
	return a, nil
}

// Owner implementa acesso.Authorizer: dono da loja, com sessão de equipe.
func (s *Service) Owner(r *http.Request, estID uuid.UUID) (acesso.Actor, error) {
	a, err := s.staffActor(r, estID)
	if err != nil {
		return a, err
	}
	if !a.IsOwner() {
		return acesso.Actor{}, httpserver.ErrForbidden("Somente o dono da loja pode fazer isso.")
	}
	return a, nil
}

// Member implementa acesso.Authorizer: qualquer vínculo ativo de equipe na loja.
func (s *Service) Member(r *http.Request, estID uuid.UUID) (acesso.Actor, error) {
	return s.staffActor(r, estID)
}

// RequireReauth implementa acesso.Authorizer (x-reauth: 10 minutos).
func (s *Service) RequireReauth(r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	return s.requireReauth(sess)
}

var _ acesso.Authorizer = (*Service)(nil)

// requireOwner é o atalho usado pelas rotas deste módulo (loja vem do caminho).
func (s *Service) requireOwner(_ context.Context, r *http.Request) (*AccountSession, uuid.UUID, error) {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return nil, uuid.Nil, httpserver.ErrNotFound()
	}
	if _, err := s.Owner(r, estID); err != nil {
		return nil, uuid.Nil, err
	}
	sess, _ := requireAccount(r)
	return sess, estID, nil
}

// requireReauth exige confirmação de identidade nos últimos 10 minutos (x-reauth).
func (s *Service) requireReauth(sess *AccountSession) error {
	if sess.ReauthValidUntil == nil || !s.clock.Now().Before(*sess.ReauthValidUntil) {
		return &httpserver.Error{Status: 403, Code: "reauthentication_required", Title: "Confirme sua senha",
			Detail: "Por segurança, digite sua senha de novo para continuar."}
	}
	return nil
}
