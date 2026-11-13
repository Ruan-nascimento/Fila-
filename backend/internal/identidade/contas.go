package identidade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
	"fila/internal/platform/validate"
)

// Mensagem genérica das rotas públicas que não podem revelar se o e-mail existe (R-CONTA-02).
const genericMailMessage = "Se os dados estiverem corretos, você receberá um e-mail em instantes. Confira também a caixa de spam."

var (
	errTokenInvalid = &httpserver.Error{Status: 409, Code: "token_invalid", Title: "Link inválido ou vencido",
		Detail: "Este link já foi usado, venceu ou não existe. Peça um novo."}
	errConcurrentSignup   = errors.New("cadastro simultâneo do mesmo e-mail")
	errInvalidCredentials = &httpserver.Error{Status: 401, Code: "invalid_credentials", Title: "E-mail ou senha incorretos",
		Detail: "Confira os dados e tente de novo."}
)

type accountRow struct {
	ID           uuid.UUID
	Name         string
	Email        string
	Status       string
	PasswordHash string
}

// findAccount busca a conta não excluída pelo e-mail normalizado (opcionalmente travando a linha).
func findAccount(ctx context.Context, q database.Querier, emailNorm string, lock bool) (*accountRow, error) {
	sql := `SELECT id, name, email, status, password_hash FROM account WHERE email_normalized = $1 AND status <> 'deleted'`
	if lock {
		sql += ` FOR UPDATE`
	}
	var a accountRow
	err := q.QueryRow(ctx, sql, emailNorm).Scan(&a.ID, &a.Name, &a.Email, &a.Status, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar conta: %w", err)
	}
	return &a, nil
}

// issueAccountToken revoga tokens anteriores do mesmo tipo e cria um novo (uso único).
func issueAccountToken(ctx context.Context, q database.Querier, accountID uuid.UUID, purpose string, ttl time.Duration, now time.Time) (string, error) {
	if _, err := q.Exec(ctx, `
		UPDATE account_token SET revoked_at = $3
		WHERE account_id = $1 AND purpose = $2 AND consumed_at IS NULL AND revoked_at IS NULL`,
		accountID, purpose, now); err != nil {
		return "", fmt.Errorf("revogar tokens anteriores: %w", err)
	}
	token, hash := newToken()
	if _, err := q.Exec(ctx, `
		INSERT INTO account_token (account_id, purpose, token_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, accountID, purpose, hash, now, now.Add(ttl)); err != nil {
		return "", fmt.Errorf("criar token: %w", err)
	}
	return token, nil
}

// consumeToken trava e consome um token válido; devolve a conta dona ou errTokenInvalid.
func consumeToken(ctx context.Context, tx pgx.Tx, token, purpose string, now time.Time) (uuid.UUID, error) {
	var id, accountID uuid.UUID
	var expires time.Time
	err := tx.QueryRow(ctx, `
		SELECT id, account_id, expires_at FROM account_token
		WHERE token_hash = $1 AND purpose = $2 AND consumed_at IS NULL AND revoked_at IS NULL
		FOR UPDATE`, hashToken(token), purpose).Scan(&id, &accountID, &expires)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !now.Before(expires)) {
		return uuid.Nil, errTokenInvalid
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("ler token: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE account_token SET consumed_at = $2 WHERE id = $1`, id, now); err != nil {
		return uuid.Nil, err
	}
	return accountID, nil
}

func revokeSessions(ctx context.Context, q database.Querier, accountID uuid.UUID, except *uuid.UUID, reason string, now time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `
		UPDATE account_session SET revoked_at = $3, revoke_reason = $4
		WHERE account_id = $1 AND revoked_at IS NULL AND ($2::uuid IS NULL OR id <> $2)`,
		accountID, except, now, reason)
	if err != nil {
		return 0, fmt.Errorf("revogar sessões: %w", err)
	}
	return tag.RowsAffected(), notifyRevocation(ctx, q)
}

// notifyRevocation avisa (no COMMIT) as conexões SSE abertas para reconferirem a sessão agora,
// em vez de esperar a reconferência periódica. O aviso não leva dado nenhum.
func notifyRevocation(ctx context.Context, q database.Querier) error {
	_, err := q.Exec(ctx, `SELECT pg_notify($1, '')`, outbox.ChannelRevocation)
	return err
}

// ---------------------------------------------------------------------------------------------
// registerAccount (T-AC-01)

type registerRequest struct {
	Name     *string `json:"name"`
	Email    *string `json:"email"`
	Password *string `json:"password"`
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	var req registerRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/name", req.Name == nil)
	v.Required("/email", req.Email == nil)
	v.Required("/password", req.Password == nil)
	if err := v.Err(); err != nil {
		return err
	}
	name, fe := validate.Text("/name", *req.Name, 1, 80)
	v.Add(fe)
	email, emailNorm, fe := validate.Email("/email", *req.Email)
	v.Add(fe)
	v.Add(ValidateNewPassword("/password", *req.Password))
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.allow(ctx, lim(ruleMailIP, s.clientIP(r)), lim(ruleMailAddress, "cadastro:"+emailNorm)); err != nil {
		return err
	}
	// O hash é calculado sempre, exista ou não a conta: o tempo de resposta não revela nada.
	hash, err := s.hasher.Hash(ctx, *req.Password)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		existing, err := findAccount(ctx, tx, emailNorm, true)
		if err != nil {
			return err
		}
		switch {
		case existing == nil:
			var id uuid.UUID
			err := tx.QueryRow(ctx, `
				INSERT INTO account (name, email, email_normalized, password_hash, status, password_changed_at, created_at, updated_at)
				VALUES ($1, $2, $3, $4, 'pending_verification', $5, $5, $5) RETURNING id`,
				name, email, emailNorm, hash, now).Scan(&id)
			if database.IsUniqueViolation(err, "account_email_uq") {
				return errConcurrentSignup // o outro cadastro simultâneo venceu; a resposta continua genérica
			}
			if err != nil {
				return fmt.Errorf("criar conta: %w", err)
			}
			token, err := issueAccountToken(ctx, tx, id, "email_verification", VerificationTTL, now)
			if err != nil {
				return err
			}
			if err := s.mailVerification(ctx, tx, email, name, token, now); err != nil {
				return err
			}
			return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
				ActorID: auditoria.Ptr(id), Action: "T-AC-01", TargetType: "account", TargetID: id,
				ToState: "pending_verification", RequestID: httpserver.RequestIDFrom(ctx)})
		case existing.Status == "pending_verification":
			// Não troca a senha (senão quem não controla o e-mail poderia defini-la); só reenvia o link.
			token, err := issueAccountToken(ctx, tx, existing.ID, "email_verification", VerificationTTL, now)
			if err != nil {
				return err
			}
			return s.mailVerification(ctx, tx, existing.Email, existing.Name, token, now)
		case existing.Status == "active":
			return s.mailAlreadyRegistered(ctx, tx, existing.Email, now)
		default:
			return nil // em exclusão: nada a fazer, resposta igual
		}
	})
	if errors.Is(err, errConcurrentSignup) {
		err = nil
	}
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusAccepted, httpserver.Data[messageView]{Data: messageView{Message: genericMailMessage}})
	return nil
}

// ---------------------------------------------------------------------------------------------
// verifyEmail (T-AC-02)

type tokenRequest struct {
	Token *string `json:"token"`
}

func validateToken(field string, tok *string) error {
	var v validate.Collector
	v.Required(field, tok == nil)
	if tok != nil && (len(*tok) < 32 || len(*tok) > 128) {
		v.Add(&httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat})
	}
	return v.Err()
}

func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var req tokenRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := validateToken("/token", req.Token); err != nil {
		return err
	}
	ctx := r.Context()
	ip := lim(ruleTokenIP, s.clientIP(r))
	if err := s.checkBlocked(ctx, ip); err != nil {
		return err
	}
	now := s.clock.Now()
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		accountID, err := consumeToken(ctx, tx, *req.Token, "email_verification", now)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE account SET status = 'active', email_verified_at = $2, updated_at = $2
			WHERE id = $1 AND status = 'pending_verification'`, accountID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errTokenInvalid
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(accountID), Action: "T-AC-02", TargetType: "account", TargetID: accountID,
			FromState: "pending_verification", ToState: "active", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if errors.Is(err, errTokenInvalid) {
		if herr := s.hit(ctx, ip); herr != nil {
			return herr
		}
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------------------------------------
// resendVerification

type emailRequest struct {
	Email *string `json:"email"`
}

func decodeEmail(r *http.Request) (email, norm string, err error) {
	var req emailRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return "", "", err
	}
	if req.Email == nil {
		return "", "", httpserver.ErrValidation(httpserver.FieldError{Field: "/email", Code: httpserver.FieldRequired})
	}
	email, norm, fe := validate.Email("/email", *req.Email)
	if fe != nil {
		return "", "", httpserver.ErrValidation(*fe)
	}
	return email, norm, nil
}

func (s *Service) resendVerification(w http.ResponseWriter, r *http.Request) error {
	_, emailNorm, err := decodeEmail(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.allow(ctx, lim(ruleMailIP, s.clientIP(r)), lim(ruleMailAddress, "reenvio:"+emailNorm)); err != nil {
		return err
	}
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := findAccount(ctx, tx, emailNorm, true)
		if err != nil || a == nil || a.Status != "pending_verification" {
			return err
		}
		token, err := issueAccountToken(ctx, tx, a.ID, "email_verification", VerificationTTL, now)
		if err != nil {
			return err
		}
		return s.mailVerification(ctx, tx, a.Email, a.Name, token, now)
	})
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusAccepted, httpserver.Data[messageView]{Data: messageView{Message: genericMailMessage}})
	return nil
}

// ---------------------------------------------------------------------------------------------
// requestPasswordReset (T-PW-01)

func (s *Service) requestPasswordReset(w http.ResponseWriter, r *http.Request) error {
	_, emailNorm, err := decodeEmail(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.allow(ctx, lim(ruleMailIP, s.clientIP(r)), lim(ruleMailAddress, "senha:"+emailNorm)); err != nil {
		return err
	}
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := findAccount(ctx, tx, emailNorm, true)
		if err != nil || a == nil || (a.Status != "active" && a.Status != "pending_verification") {
			return err
		}
		token, err := issueAccountToken(ctx, tx, a.ID, "password_reset", PasswordResetTTL, now)
		if err != nil {
			return err
		}
		if err := s.mailPasswordReset(ctx, tx, a.Email, token, now); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(a.ID), Action: "T-PW-01", TargetType: "account", TargetID: a.ID,
			ToState: "token_issued", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusAccepted, httpserver.Data[messageView]{Data: messageView{Message: genericMailMessage}})
	return nil
}

// ---------------------------------------------------------------------------------------------
// confirmPasswordReset (T-PW-02, T-SS-04)

type resetConfirmRequest struct {
	Token       *string `json:"token"`
	NewPassword *string `json:"new_password"`
}

func (s *Service) confirmPasswordReset(w http.ResponseWriter, r *http.Request) error {
	var req resetConfirmRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	var v validate.Collector
	if err := validateToken("/token", req.Token); err != nil {
		return err
	}
	v.Required("/new_password", req.NewPassword == nil)
	if req.NewPassword != nil {
		v.Add(ValidateNewPassword("/new_password", *req.NewPassword))
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	ip := lim(ruleTokenIP, s.clientIP(r))
	if err := s.checkBlocked(ctx, ip); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, *req.NewPassword)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		accountID, err := consumeToken(ctx, tx, *req.Token, "password_reset", now)
		if err != nil {
			return err
		}
		var from string
		err = tx.QueryRow(ctx, `SELECT status FROM account WHERE id = $1 FOR UPDATE`, accountID).Scan(&from)
		if err != nil {
			return err
		}
		if from != "active" && from != "pending_verification" {
			return errTokenInvalid
		}
		// O link chegou ao e-mail: isso também prova o endereço, então conta pendente fica ativa.
		if _, err := tx.Exec(ctx, `
			UPDATE account SET password_hash = $2, password_changed_at = $3, updated_at = $3,
			       status = 'active', email_verified_at = coalesce(email_verified_at, $3)
			WHERE id = $1`, accountID, hash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE account_token SET revoked_at = $2
			WHERE account_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, accountID, now); err != nil {
			return err
		}
		if _, err := revokeSessions(ctx, tx, accountID, nil, "password_reset", now); err != nil {
			return err
		}
		rid := httpserver.RequestIDFrom(ctx)
		if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(accountID), Action: "T-PW-02", TargetType: "account", TargetID: accountID,
			FromState: "token_issued", ToState: "token_consumed", RequestID: rid}); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(accountID), Action: "T-SS-04", TargetType: "account", TargetID: accountID,
			ToState: "revoked", RequestID: rid})
	})
	if errors.Is(err, errTokenInvalid) {
		if herr := s.hit(ctx, ip); herr != nil {
			return herr
		}
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------------------------------------
// changePassword (T-SS-03)

type changePasswordRequest struct {
	CurrentPassword *string `json:"current_password"`
	NewPassword     *string `json:"new_password"`
}

func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	var req changePasswordRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/current_password", req.CurrentPassword == nil || *req.CurrentPassword == "")
	v.Required("/new_password", req.NewPassword == nil)
	if req.CurrentPassword != nil && len([]rune(*req.CurrentPassword)) > MaxPasswordLen {
		v.Add(&httpserver.FieldError{Field: "/current_password", Code: httpserver.FieldTooLong})
	}
	if req.NewPassword != nil {
		v.Add(ValidateNewPassword("/new_password", *req.NewPassword))
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.checkPassword(ctx, r, sess.AccountID, sess.EmailNormalized, *req.CurrentPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, *req.NewPassword)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE account SET password_hash = $2, password_changed_at = $3, updated_at = $3 WHERE id = $1`,
			sess.AccountID, hash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE account_token SET revoked_at = $2
			WHERE account_id = $1 AND purpose = 'password_reset' AND consumed_at IS NULL AND revoked_at IS NULL`,
			sess.AccountID, now); err != nil {
			return err
		}
		if _, err := revokeSessions(ctx, tx, sess.AccountID, &sess.ID, "password_changed", now); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
			ActorID: auditoria.Ptr(sess.AccountID), Action: "T-SS-03", TargetType: "account", TargetID: sess.AccountID,
			ToState: "revoked", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// checkPassword confere a senha atual da conta com limite de tentativas por conta e por IP.
func (s *Service) checkPassword(ctx context.Context, r *http.Request, accountID uuid.UUID, emailNorm, password string) error {
	limits := []limitCheck{lim(rulePasswordAccount, emailNorm), lim(rulePasswordIP, s.clientIP(r))}
	if err := s.checkBlocked(ctx, limits...); err != nil {
		return err
	}
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM account WHERE id = $1`, accountID).Scan(&hash); err != nil {
		return fmt.Errorf("ler senha: %w", err)
	}
	ok, _, err := s.hasher.Verify(ctx, password, hash)
	if err != nil {
		return err
	}
	if !ok {
		if err := s.hit(ctx, limits...); err != nil {
			return err
		}
		return errInvalidCredentials
	}
	return nil
}
