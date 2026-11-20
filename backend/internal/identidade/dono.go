package identidade

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
)

// Sessão única do dono (ADR-0027).
//
// Uma conta com vínculo ativo de dono tem no máximo UMA sessão capaz de exercer privilégios de
// dono, somando navegador, programa desktop, computadores e janelas anônimas. A autoridade é a
// linha owner_session_slot da conta:
//
//   - login de equipe, troca de aparelho e logout travam a linha (FOR UPDATE): disputas da mesma
//     conta ficam em fila no banco, em qualquer processo da API;
//   - cada transação aberta por um pedido da sessão ocupante começa travando a mesma linha em modo
//     compartilhado (ownerGuard, FOR SHARE): a troca espera as mutações já em andamento e, depois
//     dela, a sessão antiga não efetiva mais nada;
//   - a escolha da loja não cria sessão: a vaga é por conta, valendo para todas as lojas dela.

const (
	// OwnerChallengeTTL é a validade da confirmação "continuar neste aparelho".
	OwnerChallengeTTL = 2 * time.Minute
	// ownerRecoveryWindow: depois de confirmar, a mesma tentativa (desafio + cookie da tentativa)
	// pode receber o acesso de novo se a resposta se perdeu na rede, sem nova troca.
	ownerRecoveryWindow = 5 * time.Minute
	attemptCookieAge    = 5 * time.Minute
)

// Motivos de encerramento que a interface explica ao dono.
const (
	EndedOwnerReplaced  = "owner_session_replaced"
	EndedOwnerMigration = "owner_session_migration"
)

func hasOwnerMembership(ms []estabelecimento.Membership) bool {
	for _, m := range ms {
		if m.Role == estabelecimento.RoleOwner {
			return true
		}
	}
	return false
}

// slotState é a vaga travada dentro de uma transação.
type slotState struct {
	Generation int64
	// Holder é a sessão ocupante, se ainda for válida (não revogada nem vencida).
	Holder *holderInfo
}

type holderInfo struct {
	SessionID  uuid.UUID
	LastUsedAt time.Time
	Desktop    *string // rótulo da instalação desktop autorizada vinculada (contexto confiável)
}

// lockSlot cria (se preciso) e trava a vaga de dono da conta até o fim da transação. A linha
// existe antes do FOR UPDATE: dois logins simultâneos sem vaga prévia também ficam em fila.
func lockSlot(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, now time.Time) (slotState, error) {
	var st slotState
	if _, err := tx.Exec(ctx, `INSERT INTO owner_session_slot (account_id, generation, updated_at)
		VALUES ($1, 0, $2) ON CONFLICT (account_id) DO NOTHING`, accountID, now); err != nil {
		return st, fmt.Errorf("criar vaga de dono: %w", err)
	}
	var sessID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT session_id, generation FROM owner_session_slot WHERE account_id = $1 FOR UPDATE`,
		accountID).Scan(&sessID, &st.Generation); err != nil {
		return st, fmt.Errorf("travar vaga de dono: %w", err)
	}
	if sessID == nil {
		return st, nil
	}
	var h holderInfo
	err := tx.QueryRow(ctx, `
		SELECT s.id, s.last_used_at,
		       (SELECT i.label FROM desktop_binding b JOIN desktop_installation i ON i.id = b.installation_id
		         WHERE b.session_id = s.id AND b.revoked_at IS NULL AND b.expires_at > $2 AND i.revoked_at IS NULL
		         ORDER BY b.created_at DESC LIMIT 1)
		FROM account_session s
		WHERE s.id = $1 AND s.revoked_at IS NULL AND s.absolute_expires_at > $2`, *sessID, now).
		Scan(&h.SessionID, &h.LastUsedAt, &h.Desktop)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil // ocupante revogado ou vencido: vaga livre
	}
	if err != nil {
		return st, fmt.Errorf("ler ocupante da vaga: %w", err)
	}
	st.Holder = &h
	return st, nil
}

// occupySlot passa a vaga para newSession (a geração cresce: desafios antigos deixam de valer).
func occupySlot(ctx context.Context, tx pgx.Tx, accountID, newSession uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE owner_session_slot SET session_id = $2, generation = generation + 1, updated_at = $3
		WHERE account_id = $1`, accountID, newSession, now)
	return err
}

// revokeBindings encerra os vínculos desktop presos à sessão (eles são subordinados a ela).
func revokeBindings(ctx context.Context, q database.Querier, sessionID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE desktop_binding SET revoked_at = $2 WHERE session_id = $1 AND revoked_at IS NULL`, sessionID, now)
	return err
}

// ownerGuard trava a vaga em modo compartilhado e confere que a sessão ainda é a ocupante válida.
func (s *Service) ownerGuard(accountID, sessionID uuid.UUID) database.TxGuard {
	return func(ctx context.Context, tx pgx.Tx) error {
		var one int
		err := tx.QueryRow(ctx, `
			SELECT 1 FROM owner_session_slot o JOIN account_session s ON s.id = o.session_id
			WHERE o.account_id = $1 AND o.session_id = $2 AND s.revoked_at IS NULL
			FOR SHARE OF o`, accountID, sessionID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			var reason *string
			_ = tx.QueryRow(ctx, `SELECT revoke_reason FROM account_session WHERE id = $1`, sessionID).Scan(&reason)
			if reason != nil && *reason == EndedOwnerReplaced {
				return errOwnerReplaced()
			}
			return httpserver.ErrUnauthenticated()
		}
		if err != nil {
			return fmt.Errorf("conferir vaga de dono: %w", err)
		}
		return nil
	}
}

func errOwnerReplaced() *httpserver.Error {
	return &httpserver.Error{Status: 401, Code: EndedOwnerReplaced, Title: "Sessão encerrada",
		Detail: "Sua sessão foi encerrada porque você entrou em outro aparelho."}
}

// endedReason devolve o motivo de um cookie que não vale mais, quando é a vaga de dono (a tela
// explica "você entrou em outro aparelho"). Só nas 12 h seguintes; depois disso, sessão comum.
func endedReason(ctx context.Context, q database.Querier, tokenHash []byte, now time.Time) (string, error) {
	var reason string
	err := q.QueryRow(ctx, `SELECT revoke_reason FROM account_session
		WHERE token_hash = $1 AND revoke_reason IN ('owner_session_replaced', 'owner_session_migration') AND revoked_at > $2`,
		tokenHash, now.Add(-StaffAbsolute)).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("ler motivo do encerramento: %w", err)
	}
	return reason, nil
}

// ---------------------------------------------------------------------------------------------
// Conflito no login: a outra sessão continua; a tentativa recebe um desafio descartável.

type ownerConflictView struct {
	Challenge    string    `json:"challenge"`
	ExpiresAt    time.Time `json:"expires_at"`
	OtherSession struct {
		// desktop_authorized só quando a sessão tem vínculo com instalação autorizada (prova de
		// posse da chave); senão unknown ("outro aparelho ou navegador").
		Channel          string    `json:"channel"`
		InstallationName *string   `json:"installation_name"`
		LastUsedAt       time.Time `json:"last_used_at"`
	} `json:"other_session"`
}

func (s *Service) attemptCookieName() string {
	if s.cfg.CookieSecure {
		return "__Host-fila_owner_attempt"
	}
	return "fila_owner_attempt"
}

// newOwnerChallenge grava o desafio (só hashes) na transação que travou a vaga.
func newOwnerChallenge(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, st slotState, attemptHash []byte, now time.Time) (ownerConflictView, error) {
	var v ownerConflictView
	token, tokenHash := newToken()
	v.Challenge = token
	v.ExpiresAt = now.Add(OwnerChallengeTTL)
	if _, err := tx.Exec(ctx, `INSERT INTO owner_session_challenge
		(account_id, token_hash, attempt_hash, replaces_session_id, generation, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, accountID, tokenHash, attemptHash, st.Holder.SessionID, st.Generation, now, v.ExpiresAt); err != nil {
		return v, fmt.Errorf("gravar desafio: %w", err)
	}
	v.OtherSession.Channel = "unknown"
	if st.Holder.Desktop != nil {
		v.OtherSession.Channel = "desktop_authorized"
		v.OtherSession.InstallationName = st.Holder.Desktop
	}
	// Último uso aproximado (5 min): o servidor só atualiza o uso a cada minuto.
	v.OtherSession.LastUsedAt = st.Holder.LastUsedAt.Truncate(5 * time.Minute)
	return v, nil
}

func ownerConflict(v ownerConflictView) *httpserver.Error {
	return &httpserver.Error{Status: 409, Code: "owner_session_active", Title: "Acesso de dono aberto em outro lugar",
		Detail:  "Esta conta já está com o acesso de dono aberto em outro aparelho ou navegador. Você pode continuar aqui e encerrar a outra sessão, ou cancelar e mantê-la.",
		Current: v}
}

func errChallengeInvalid(detail string) *httpserver.Error {
	return &httpserver.Error{Status: 409, Code: "owner_session_challenge_invalid", Title: "Confirmação não vale mais",
		Detail: detail}
}

// ---------------------------------------------------------------------------------------------
// POST /v1/auth/owner-session/takeover (T-SS-07)

type challengeRequest struct {
	Challenge *string `json:"challenge"`
}

func readChallenge(r *http.Request) (string, error) {
	var req challengeRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return "", err
	}
	if req.Challenge == nil || len(*req.Challenge) < 20 || len(*req.Challenge) > 64 {
		return "", httpserver.ErrValidation(httpserver.FieldError{Field: "/challenge", Code: httpserver.FieldInvalidFormat})
	}
	return *req.Challenge, nil
}

func (s *Service) attemptHash(r *http.Request) []byte {
	c, err := r.Cookie(s.attemptCookieName())
	if err != nil || c.Value == "" || len(c.Value) > 64 {
		return nil
	}
	return hashToken(c.Value)
}

type challengeRow struct {
	ID          uuid.UUID
	AccountID   uuid.UUID
	AttemptHash []byte
	Replaces    uuid.UUID
	Generation  int64
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
	Outcome     *string
	Result      *uuid.UUID
}

func (s *Service) takeoverOwnerSession(w http.ResponseWriter, r *http.Request) error {
	challenge, err := readChallenge(r)
	if err != nil {
		return err
	}
	ctx := database.WithoutTxGuard(r.Context()) // esta transação trava a vaga em modo exclusivo
	limits := []limitCheck{lim(ruleTokenIP, s.clientIP(r))}
	if err := s.allow(ctx, limits...); err != nil {
		return err
	}
	attempt := s.attemptHash(r)
	now := s.clock.Now()
	prev := PrincipalFrom(ctx)
	token, tokenHash := newToken()
	var sess *AccountSession
	var stale bool
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var c challengeRow
		err := tx.QueryRow(ctx, `SELECT id, account_id, attempt_hash, replaces_session_id, generation, expires_at,
			consumed_at, outcome, result_session_id FROM owner_session_challenge WHERE token_hash = $1 FOR UPDATE`,
			hashToken(challenge)).Scan(&c.ID, &c.AccountID, &c.AttemptHash, &c.Replaces, &c.Generation, &c.ExpiresAt,
			&c.ConsumedAt, &c.Outcome, &c.Result)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (attempt == nil || subtle.ConstantTimeCompare(attempt, c.AttemptHash) != 1)) {
			return errChallengeInvalid("Entre de novo para continuar neste aparelho.")
		}
		if err != nil {
			return err
		}
		// A ordem de travas é sempre vaga → sessões (igual ao login e ao logout).
		st, err := lockSlot(ctx, tx, c.AccountID, now)
		if err != nil {
			return err
		}
		if c.ConsumedAt != nil {
			// Repetição da mesma tentativa (a resposta anterior se perdeu): reentrega o acesso só se
			// esta tentativa venceu e continua ocupando a vaga. Nunca revoga nada de novo.
			if c.Outcome != nil && *c.Outcome == "confirmed" && st.Holder != nil && c.Result != nil &&
				st.Holder.SessionID == *c.Result && now.Before(c.ExpiresAt.Add(ownerRecoveryWindow)) {
				if _, err := tx.Exec(ctx, `UPDATE account_session SET token_hash = $2 WHERE id = $1 AND revoked_at IS NULL`,
					*c.Result, tokenHash); err != nil {
					return err
				}
				sess, err = s.loadSession(ctx, tx, tokenHash, now)
				return err
			}
			return errChallengeInvalid("Esta confirmação já foi usada. Entre de novo para continuar neste aparelho.")
		}
		if !now.Before(c.ExpiresAt) {
			if _, err := tx.Exec(ctx, `UPDATE owner_session_challenge SET consumed_at = $2, outcome = 'stale' WHERE id = $1`, c.ID, now); err != nil {
				return err
			}
			stale = true
			return nil
		}
		// A sessão ativa mudou desde o aviso (outra troca, logout, vencimento): o aviso está
		// desatualizado e não derruba uma sessão mais recente.
		if st.Generation != c.Generation || st.Holder == nil || st.Holder.SessionID != c.Replaces {
			if _, err := tx.Exec(ctx, `UPDATE owner_session_challenge SET consumed_at = $2, outcome = 'stale' WHERE id = $1`, c.ID, now); err != nil {
				return err
			}
			stale = true
			return nil
		}
		// Vínculos podem ter mudado durante a confirmação: confere de novo antes de dar acesso de dono.
		ms, err := estabelecimento.ActiveMemberships(ctx, tx, c.AccountID)
		if err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM account WHERE id = $1`, c.AccountID).Scan(&status); err != nil {
			return err
		}
		if status != "active" || !hasOwnerMembership(ms) {
			if _, err := tx.Exec(ctx, `UPDATE owner_session_challenge SET consumed_at = $2, outcome = 'stale' WHERE id = $1`, c.ID, now); err != nil {
				return err
			}
			stale = true
			return nil
		}
		old := st.Holder.SessionID
		if _, err := tx.Exec(ctx, `UPDATE account_session SET revoked_at = $2, revoke_reason = 'owner_session_replaced'
			WHERE id = $1 AND revoked_at IS NULL`, old, now); err != nil {
			return err
		}
		if err := revokeBindings(ctx, tx, old, now); err != nil {
			return err
		}
		// Um navegador tem uma sessão de conta por vez (igual ao login).
		if prev.Session != nil && prev.Session.ID != old {
			if _, err := tx.Exec(ctx, `UPDATE account_session SET revoked_at = $2, revoke_reason = 'replaced'
				WHERE id = $1 AND revoked_at IS NULL`, prev.Session.ID, now); err != nil {
				return err
			}
		}
		id, err := insertStaffSession(ctx, tx, c.AccountID, tokenHash, now)
		if err != nil {
			return err
		}
		if err := occupySlot(ctx, tx, c.AccountID, id, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE owner_session_challenge SET consumed_at = $2, outcome = 'confirmed', result_session_id = $3
			WHERE id = $1`, c.ID, now, id); err != nil {
			return err
		}
		if err := notifyRevocation(ctx, tx); err != nil {
			return err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(c.AccountID), Action: "T-SS-07", TargetType: "session", TargetID: old,
			FromState: "active", ToState: "revoked", Reason: "acesso de dono transferido para outro aparelho",
			RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(c.AccountID), Action: "T-SS-01", TargetType: "session", TargetID: id,
			ToState: "active", Reason: "acesso de dono assumido neste aparelho", RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return err
		}
		sess, err = s.loadSession(ctx, tx, tokenHash, now)
		return err
	})
	if err != nil {
		return err
	}
	if stale {
		s.clearCookie(w, s.attemptCookieName())
		return errChallengeInvalid("O acesso de dono mudou enquanto esta confirmação estava aberta. Entre de novo para ver a situação atual.")
	}
	if sess == nil {
		return httpserver.ErrUnauthenticated()
	}
	view, err := s.buildSessionView(ctx, s.pool, &Principal{Session: sess, BrowserID: prev.BrowserID, Visitors: prev.Visitors})
	if err != nil {
		return err
	}
	// O cookie da tentativa fica até vencer (5 min): se esta resposta se perder, a mesma tentativa
	// repete a confirmação e recebe o acesso de novo (sem nova revogação).
	s.setCookie(w, s.sessionCookieName(), token, StaffAbsolute)
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[sessionView]{Data: view})
	return nil
}

// POST /v1/auth/owner-session/cancel: descarta o desafio; a outra sessão continua como estava.
// Sempre 204 (repetível): não revela se o desafio existia.
func (s *Service) cancelOwnerTakeover(w http.ResponseWriter, r *http.Request) error {
	challenge, err := readChallenge(r)
	if err != nil {
		return err
	}
	ctx := database.WithoutTxGuard(r.Context())
	if attempt := s.attemptHash(r); attempt != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE owner_session_challenge SET consumed_at = $3, outcome = 'cancelled'
			WHERE token_hash = $1 AND attempt_hash = $2 AND consumed_at IS NULL`, hashToken(challenge), attempt, s.clock.Now()); err != nil {
			return err
		}
	}
	s.clearCookie(w, s.attemptCookieName())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func insertStaffSession(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, tokenHash []byte, now time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO account_session (account_id, token_hash, kind, created_at, last_used_at, absolute_expires_at, idle_timeout_seconds)
		VALUES ($1, $2, 'staff', $3, $3, $4, NULL) RETURNING id`, accountID, tokenHash, now, now.Add(StaffAbsolute)).Scan(&id)
	return id, err
}

// EndedReasonOf informa (para o SSE) por que a sessão do pedido relido acabou, se for a vaga de dono.
func (s *Service) EndedReasonOf(r *http.Request) string {
	p := PrincipalFrom(r.Context())
	if p.Session != nil {
		return ""
	}
	return p.EndedReason
}
