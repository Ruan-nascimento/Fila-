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
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/keys"
	"fila/internal/platform/ratelimit"
	"fila/internal/platform/validate"
)

// Limites do visitante (ADR-0017). Reduzem abuso; não provam que identidades anônimas
// diferentes são pessoas diferentes (R-VIS-10).
var (
	ruleVisitorCreateIP      = ratelimit.Rule{Name: "visitante_criar_ip", Limit: 20, Window: time.Hour}
	ruleVisitorCreateBrowser = ratelimit.Rule{Name: "visitante_criar_navegador", Limit: 5, Window: time.Hour}
	ruleRecoveryIP           = ratelimit.Rule{Name: "visitante_codigo_ip", Limit: 10, Window: 15 * time.Minute}
	ruleRecoveryStore        = ratelimit.Rule{Name: "visitante_codigo_loja", Limit: 200, Window: time.Hour}
)

var errRecoveryInvalid = &httpserver.Error{Status: 401, Code: "recovery_code_invalid", Title: "Código inválido",
	Detail: "Código incorreto, já usado ou vencido. Se precisar, procure a equipe no balcão."}

// ActiveVisitFunc informa se o visitante tem participação ativa ou comanda pendente na loja.
// É fornecida pelos módulos de fila/comanda (etapas 04/05); enquanto eles não existem, não há
// visita possível e o valor é nil.
type ActiveVisitFunc func(ctx context.Context, q database.Querier, visitorID uuid.UUID) (bool, error)

func (s *Service) visitorCookieName() string {
	if s.cfg.CookieSecure {
		return "__Host-fila_visitor"
	}
	return "fila_visitor"
}

func (s *Service) recoveryMAC(code string) []byte {
	return s.ring.MAC(keys.PurposeRecoveryCode, []byte(code))
}

// visitorExpiry aplica R-VIS-02/03: 7 dias sem uso, 30 dias desde a criação do visitante e
// até 24 h depois do encerramento da comanda — o que vier primeiro.
func visitorExpiry(absolute, lastUsed time.Time, closed *time.Time) time.Time {
	exp := minTime(absolute, lastUsed.Add(VisitorIdle))
	if closed != nil {
		exp = minTime(exp, closed.Add(VisitorAfterClosed))
	}
	return exp
}

// codeValidUntil: o código vale 30 dias da criação e até 24 h após o encerramento (R-VIS-04).
func codeValidUntil(absolute time.Time, closed *time.Time) time.Time {
	if closed != nil {
		return minTime(absolute, closed.Add(VisitorAfterClosed))
	}
	return absolute
}

// loadVisitors resolve o cookie de visitante: navegador e credenciais válidas.
func (s *Service) loadVisitors(ctx context.Context, q database.Querier, p *Principal, tokenHash []byte, now time.Time) error {
	var browserID uuid.UUID
	var lastUsed time.Time
	err := q.QueryRow(ctx, `SELECT id, last_used_at FROM visitor_browser WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash).
		Scan(&browserID, &lastUsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ler navegador de visitante: %w", err)
	}
	p.BrowserID = &browserID
	if now.Sub(lastUsed) >= touchEvery {
		if _, err := q.Exec(ctx, `UPDATE visitor_browser SET last_used_at = $2 WHERE id = $1`, browserID, now); err != nil {
			return err
		}
	}
	rows, err := q.Query(ctx, `
		SELECT c.id, c.visitor_id, c.establishment_id, v.nick, c.last_used_at, v.absolute_expires_at, v.visit_closed_at
		FROM visitor_credential c JOIN visitor v ON v.id = c.visitor_id
		WHERE c.browser_id = $1 AND c.revoked_at IS NULL
		ORDER BY c.created_at`, browserID)
	if err != nil {
		return fmt.Errorf("ler credenciais de visitante: %w", err)
	}
	type row struct {
		vc       VisitorCredential
		lastUsed time.Time
		absolute time.Time
		closed   *time.Time
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.vc.ID, &x.vc.VisitorID, &x.vc.EstablishmentID, &x.vc.Nick, &x.lastUsed, &x.absolute, &x.closed)
		return x, err
	})
	if err != nil {
		return err
	}
	for _, x := range all {
		// T-VS-04: expiração verificada a cada uso.
		if !now.Before(visitorExpiry(x.absolute, x.lastUsed, x.closed)) {
			continue
		}
		if now.Sub(x.lastUsed) >= touchEvery {
			if _, err := q.Exec(ctx, `UPDATE visitor_credential SET last_used_at = $2 WHERE id = $1 AND revoked_at IS NULL`, x.vc.ID, now); err != nil {
				return err
			}
			x.lastUsed = now
		}
		x.vc.ExpiresAt = visitorExpiry(x.absolute, x.lastUsed, x.closed)
		p.Visitors = append(p.Visitors, x.vc)
	}
	return nil
}

type visitorCreatedView struct {
	Visitor                visitorView `json:"visitor"`
	RecoveryCode           string      `json:"recovery_code"`
	RecoveryCodeValidUntil time.Time   `json:"recovery_code_valid_until"`
}

// ensureBrowser devolve o navegador do cookie ou cria um novo (e o cookie correspondente).
func (s *Service) ensureBrowser(ctx context.Context, tx pgx.Tx, p *Principal, now time.Time) (uuid.UUID, string, error) {
	if p.BrowserID != nil {
		return *p.BrowserID, "", nil
	}
	token, hash := newToken()
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO visitor_browser (token_hash, created_at, last_used_at) VALUES ($1, $2, $2) RETURNING id`,
		hash, now).Scan(&id)
	return id, token, err
}

// issueCredential encerra a credencial ativa deste navegador na loja (se houver) e cria a nova.
func issueCredential(ctx context.Context, tx pgx.Tx, browserID, visitorID, estID uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE visitor_credential SET revoked_at = $3, revoke_reason = 'replaced'
		WHERE browser_id = $1 AND establishment_id = $2 AND revoked_at IS NULL`, browserID, estID, now); err != nil {
		return err
	}
	if err := notifyRevocation(ctx, tx); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO visitor_credential (visitor_id, establishment_id, browser_id, created_at, last_used_at)
		VALUES ($1, $2, $3, $4, $4)`, visitorID, estID, browserID, now)
	return err
}

// ---------------------------------------------------------------------------------------------
// createVisitorSession (T-VS-01)

type createVisitorRequest struct {
	Nick *string `json:"nick"`
}

func (s *Service) createVisitorSession(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	var req createVisitorRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Nick == nil {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/nick", Code: httpserver.FieldRequired})
	}
	nick, fe := validate.Text("/nick", *req.Nick, 1, 30)
	if fe != nil {
		return httpserver.ErrValidation(*fe)
	}
	ctx := r.Context()
	if _, err := estabelecimento.ActiveByID(ctx, s.pool, estID); errors.Is(err, estabelecimento.ErrNotFound) {
		return httpserver.ErrNotFound()
	} else if err != nil {
		return err
	}
	p := PrincipalFrom(ctx)
	limits := []limitCheck{lim(ruleVisitorCreateIP, estID.String()+"|"+s.clientIP(r))}
	if p.BrowserID != nil {
		limits = append(limits, lim(ruleVisitorCreateBrowser, estID.String()+"|"+p.BrowserID.String()))
	}
	if err := s.allow(ctx, limits...); err != nil {
		return err
	}
	// T-VS-01: não cria outro visitante enquanto o deste navegador tiver visita ativa/pendente.
	if cur := p.VisitorIn(estID); cur != nil && s.activeVisit != nil {
		busy, err := s.activeVisit(ctx, s.pool, cur.VisitorID)
		if err != nil {
			return err
		}
		if busy {
			return &httpserver.Error{Status: 409, Code: "visitor_visit_in_progress", Title: "Você já tem uma visita",
				Detail: "Este aparelho já tem uma visita em andamento nesta loja."}
		}
	}

	now := s.clock.Now()
	code := newRecoveryCode()
	norm, _ := normalizeRecoveryCode(code)
	var created visitorCreatedView
	var browserToken string
	var browserID uuid.UUID
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var token string
		var err error
		browserID, token, err = s.ensureBrowser(ctx, tx, p, now)
		if err != nil {
			return err
		}
		browserToken = token
		absolute := now.Add(VisitorAbsolute)
		var visitorID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO visitor (establishment_id, nick, created_at, absolute_expires_at, recovery_code_hmac, recovery_code_issued_at)
			VALUES ($1, $2, $3, $4, $5, $3) RETURNING id`, estID, nick, now, absolute, s.recoveryMAC(norm)).Scan(&visitorID); err != nil {
			return fmt.Errorf("criar visitante: %w", err)
		}
		if err := issueCredential(ctx, tx, browserID, visitorID, estID, now); err != nil {
			return err
		}
		created = visitorCreatedView{
			Visitor:      visitorView{ID: visitorID, EstablishmentID: estID, Nick: nick, ExpiresAt: visitorExpiry(absolute, now, nil)},
			RecoveryCode: code, RecoveryCodeValidUntil: absolute,
		}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorVisitor, ActorID: auditoria.Ptr(visitorID), Action: "T-VS-01",
			TargetType: "visitor", TargetID: visitorID, ToState: "active", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	if browserToken != "" {
		// Navegador novo: a primeira criação também conta no limite por navegador.
		if _, err := s.limiter.Hit(ctx, ruleVisitorCreateBrowser, estID.String()+"|"+browserID.String()); err != nil {
			return err
		}
		s.setCookie(w, s.visitorCookieName(), browserToken, VisitorAbsolute)
	}
	// WriteJSON já envia Cache-Control: no-store (o código é exibido uma única vez).
	httpserver.WriteJSON(w, http.StatusCreated, httpserver.Data[visitorCreatedView]{Data: created})
	return nil
}

// ---------------------------------------------------------------------------------------------
// recoverVisitorSession (T-VS-02)

type recoverRequest struct {
	RecoveryCode *string `json:"recovery_code"`
}

func (s *Service) recoverVisitorSession(w http.ResponseWriter, r *http.Request) error {
	var req recoverRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.RecoveryCode == nil {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/recovery_code", Code: httpserver.FieldRequired})
	}
	if n := len([]rune(*req.RecoveryCode)); n < 16 || n > 32 {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/recovery_code", Code: httpserver.FieldInvalidFormat,
			Message: "formato XXXX-XXXX-XXXX-XXXX"})
	}
	ctx := r.Context()
	estParam := r.PathValue("establishmentId")
	limits := []limitCheck{lim(ruleRecoveryIP, estParam+"|"+s.clientIP(r)), lim(ruleRecoveryStore, estParam)}
	if err := s.checkBlocked(ctx, limits...); err != nil {
		return err
	}
	estID, perr := uuid.Parse(estParam)
	norm, ok := normalizeRecoveryCode(*req.RecoveryCode)
	if perr != nil || !ok {
		if err := s.hit(ctx, limits...); err != nil {
			return err
		}
		return errRecoveryInvalid
	}

	p := PrincipalFrom(ctx)
	now := s.clock.Now()
	newCode := newRecoveryCode()
	newNorm, _ := normalizeRecoveryCode(newCode)
	var created visitorCreatedView
	var browserToken string
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var visitorID uuid.UUID
		var nick string
		var absolute time.Time
		var closed *time.Time
		err := tx.QueryRow(ctx, `
			SELECT id, nick, absolute_expires_at, visit_closed_at FROM visitor
			WHERE establishment_id = $1 AND recovery_code_hmac = $2 FOR UPDATE`, estID, s.recoveryMAC(norm)).
			Scan(&visitorID, &nick, &absolute, &closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return errRecoveryInvalid
		}
		if err != nil {
			return err
		}
		if !now.Before(codeValidUntil(absolute, closed)) {
			return errRecoveryInvalid
		}
		// Uso único: o código muda e as credenciais anteriores deixam de valer (R-VIS-05).
		if _, err := tx.Exec(ctx, `UPDATE visitor SET recovery_code_hmac = $2, recovery_code_issued_at = $3 WHERE id = $1`,
			visitorID, s.recoveryMAC(newNorm), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE visitor_credential SET revoked_at = $2, revoke_reason = 'recovered_elsewhere'
			WHERE visitor_id = $1 AND revoked_at IS NULL`, visitorID, now); err != nil {
			return err
		}
		if err := notifyRevocation(ctx, tx); err != nil {
			return err
		}
		browserID, token, err := s.ensureBrowser(ctx, tx, p, now)
		if err != nil {
			return err
		}
		browserToken = token
		if err := issueCredential(ctx, tx, browserID, visitorID, estID, now); err != nil {
			return err
		}
		created = visitorCreatedView{
			Visitor:      visitorView{ID: visitorID, EstablishmentID: estID, Nick: nick, ExpiresAt: visitorExpiry(absolute, now, closed)},
			RecoveryCode: newCode, RecoveryCodeValidUntil: codeValidUntil(absolute, closed),
		}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorVisitor, ActorID: auditoria.Ptr(visitorID), Action: "T-VS-02",
			TargetType: "visitor", TargetID: visitorID, FromState: "active", ToState: "recovered",
			RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if errors.Is(err, errRecoveryInvalid) {
		if herr := s.hit(ctx, limits...); herr != nil {
			return herr
		}
	}
	if err != nil {
		return err
	}
	if browserToken != "" {
		s.setCookie(w, s.visitorCookieName(), browserToken, VisitorAbsolute)
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[visitorCreatedView]{Data: created})
	return nil
}

// ---------------------------------------------------------------------------------------------
// endVisitorSession (T-VS-03): "sair deste aparelho". Não cancela a visita.

func (s *Service) endVisitorSession(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := PrincipalFrom(ctx)
	if p.BrowserID == nil || len(p.Visitors) == 0 {
		return httpserver.ErrUnauthenticated()
	}
	now := s.clock.Now()
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE visitor_credential SET revoked_at = $2, revoke_reason = 'ended'
			WHERE browser_id = $1 AND revoked_at IS NULL`, *p.BrowserID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_browser SET revoked_at = $2 WHERE id = $1`, *p.BrowserID, now); err != nil {
			return err
		}
		if err := notifyRevocation(ctx, tx); err != nil {
			return err
		}
		for _, v := range p.Visitors {
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: auditoria.Ptr(v.EstablishmentID), OccurredAt: now,
				ActorKind: auditoria.ActorVisitor, ActorID: auditoria.Ptr(v.VisitorID), Action: "T-VS-03",
				TargetType: "visitor_credential", TargetID: v.ID, FromState: "active", ToState: "revoked",
				RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.clearCookie(w, s.visitorCookieName())
	w.WriteHeader(http.StatusNoContent)
	return nil
}
