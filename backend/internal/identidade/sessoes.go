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
	"fila/internal/midia"
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
	if a.PasswordHash == "" {
		// Conta só com Google: senha nenhuma confere (mesmo custo e mesma resposta de senha errada).
		s.hasher.Burn(ctx, *req.Password)
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
	ownerAccount := false
	if kind == KindStaff {
		ms, err := estabelecimento.ActiveMemberships(ctx, s.pool, a.ID)
		if err != nil {
			return err
		}
		if len(ms) == 0 {
			return httpserver.ErrForbidden("Esta conta não tem acesso de equipe ativo. Entre como cliente.")
		}
		// Qualquer sessão de equipe de uma conta com vínculo de dono pode exercer poderes de dono:
		// passa pela vaga única, inclusive quando a pessoa entra pela rota comum de profissional.
		ownerAccount = hasOwnerMembership(ms)
	}
	// Login e troca travam a vaga em modo exclusivo: sem a guarda de transação da sessão anterior.
	ctx = database.WithoutTxGuard(ctx)

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
	var conflict *ownerConflictView
	var attemptToken string
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if newHash != "" {
			if _, err := tx.Exec(ctx, `UPDATE account SET password_hash = $2, updated_at = $3 WHERE id = $1`, a.ID, newHash, now); err != nil {
				return err
			}
		}
		var st slotState
		if ownerAccount {
			var err error
			if st, err = lockSlot(ctx, tx, a.ID, now); err != nil {
				return err
			}
			// Outra sessão válida ocupa a vaga: ela continua funcionando; esta tentativa recebe só
			// um desafio (sem cookie de sessão nem dados da loja). A mesma sessão (mesmo cookie)
			// renovando o login não conta como conflito.
			if st.Holder != nil && (prev.Session == nil || prev.Session.ID != st.Holder.SessionID) {
				var attemptHash []byte
				attemptToken, attemptHash = newToken()
				v, err := newOwnerChallenge(ctx, tx, a.ID, st, attemptHash, now)
				if err != nil {
					return err
				}
				conflict = &v
				return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOwner,
					ActorID: auditoria.Ptr(a.ID), Action: "T-SS-07", TargetType: "session", TargetID: st.Holder.SessionID,
					FromState: "active", ToState: "active", Reason: "nova entrada de dono aguardando confirmação",
					RequestID: httpserver.RequestIDFrom(ctx)})
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
		if ownerAccount {
			if prev.Session != nil {
				if err := revokeBindings(ctx, tx, prev.Session.ID, now); err != nil {
					return err
				}
			}
			if err := occupySlot(ctx, tx, a.ID, id, now); err != nil {
				return err
			}
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
	if conflict != nil {
		s.setCookie(w, s.attemptCookieName(), attemptToken, attemptCookieAge)
		return ownerConflict(*conflict)
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
	// Logout trava a vaga em modo exclusivo (sem a guarda compartilhada da própria sessão).
	ctx := database.WithoutTxGuard(r.Context())
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Ordem de travas: vaga → sessão. O logout só libera a vaga se ESTA sessão (o cookie
		// apresentado) for a ocupante: um logout atrasado de uma sessão antiga nunca encerra a nova.
		if sess.OwnerHolder {
			if _, err := tx.Exec(ctx, `SELECT 1 FROM owner_session_slot WHERE account_id = $1 FOR UPDATE`, sess.AccountID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE owner_session_slot SET session_id = NULL, generation = generation + 1, updated_at = $3
				WHERE account_id = $1 AND session_id = $2`, sess.AccountID, sess.ID, now); err != nil {
				return err
			}
		}
		if err := revokeBindings(ctx, tx, sess.ID, now); err != nil {
			return err
		}
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
	Password  *string `json:"password"`
	EmailCode *string `json:"email_code"`
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
	ctx := r.Context()
	switch {
	case req.EmailCode != nil && req.Password == nil:
		// Conta sem senha (só Google): código enviado ao e-mail verificado, preso a esta sessão.
		if !codePattern.MatchString(*req.EmailCode) {
			return httpserver.ErrValidation(httpserver.FieldError{Field: "/email_code", Code: httpserver.FieldInvalidFormat,
				Message: "8 dígitos"})
		}
		if err := s.checkReauthCode(ctx, sess, *req.EmailCode); err != nil {
			return err
		}
	case req.EmailCode != nil:
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/email_code", Code: httpserver.FieldNotAllowed,
			Message: "envie a senha OU o código"})
	default:
		if req.Password == nil || *req.Password == "" {
			return httpserver.ErrValidation(httpserver.FieldError{Field: "/password", Code: httpserver.FieldRequired})
		}
		if len([]rune(*req.Password)) > MaxPasswordLen {
			return httpserver.ErrValidation(httpserver.FieldError{Field: "/password", Code: httpserver.FieldTooLong})
		}
		if err := s.checkPassword(ctx, r, sess.AccountID, sess.EmailNormalized, *req.Password); err != nil {
			return err
		}
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
	view := publicEstablishmentView{ID: e.ID, Slug: e.Slug, Name: e.Name, Timezone: e.Timezone, Palette: e.Palette,
		Description: e.Description, Address: e.Address, Phone: e.Phone}
	logo, err := midia.AttachedOne(r.Context(), s.pool, e.LogoAssetID, "public")
	if err != nil {
		return err
	}
	if logo != nil {
		view.Logo = logo.PublicRef()
	}
	st, err := estabelecimento.LoadSettings(r.Context(), s.pool, e.ID)
	if err != nil {
		return err
	}
	view.Ordering = estabelecimento.PublicOrderingOf(st, s.pixOperational != nil && s.pixOperational(), s.agendaEnabled)
	// Dados públicos: cache curto (troca de logo/paleta aparece em até 1 minuto).
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[publicEstablishmentView]{Data: view})
	return nil
}

type publicConfigView struct {
	GoogleLogin   bool   `json:"google_login"`
	MediaUploads  bool   `json:"media_uploads"`
	PublicBaseURL string `json:"public_base_url"`
}

// getPublicConfig informa à interface o que está ligado e a origem pública canônica (usada nos
// links e QR Codes). Nenhum segredo: só booleanos e o endereço público do site.
func (s *Service) getPublicConfig(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[publicConfigView]{Data: publicConfigView{
		GoogleLogin: s.cfg.Google.enabled(), MediaUploads: s.media.Enabled(), PublicBaseURL: s.cfg.PublicBaseURL}})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Autorização (R-PERM-01, R-PERM-05, R-PERM-06): o ator vem só dos cookies.

// requireAccount exige sessão de conta válida (qualquer contexto).
func requireAccount(r *http.Request) (*AccountSession, error) {
	p := PrincipalFrom(r.Context())
	if p.Session == nil {
		if p.EndedReason == EndedOwnerReplaced {
			return nil, errOwnerReplaced()
		}
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
	owner := false
	for _, m := range ms {
		if m.Role == estabelecimento.RoleOwner {
			owner = true
		}
		if m.ProfessionalID != nil {
			a.ProfessionalID = m.ProfessionalID
		}
	}
	// ADR-0027: só a sessão ocupante da vaga exerce privilégios de dono. Outra sessão de equipe da
	// mesma conta (anterior à migração, ou aberta antes de a conta virar dona) age só como
	// profissional, se tiver esse vínculo aqui.
	if owner && sess.OwnerHolder {
		a.Role = acesso.RoleOwner
	} else if a.ProfessionalID == nil {
		return acesso.Actor{}, &httpserver.Error{Status: 403, Code: "owner_session_required", Title: "Entre de novo como dono",
			Detail: "Esta sessão não tem o acesso de dono. Saia e entre de novo para continuar."}
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
