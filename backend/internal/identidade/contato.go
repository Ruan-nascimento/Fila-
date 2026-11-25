package identidade

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/mail"
	"fila/internal/platform/ratelimit"
	"fila/internal/platform/validate"
)

// Contato verificado do visitante e recuperação da reserva por e-mail (ADR-0039).
//
// O visitante (sem conta) pode informar um e-mail NA LOJA para receber os avisos da reserva e
// recuperar o acesso em outro aparelho. O endereço só vale depois do código de 6 dígitos enviado
// a ele; fica cifrado, e a busca usa um HMAC (o endereço em claro nunca vai para índice nem log).
// A recuperação manda um link de uso único (#token=…, 30 min); abrir o link não muda nada (GET
// não muda estado): a página troca o token por uma credencial de visitante com um POST.

const (
	purposeContactEmail = "contato-visitante/v1"
	purposeContactMAC   = "contato-visitante-mac/v1"
	purposeContactCode  = "contato-visitante-codigo/v1"
	contactCodeTTL      = 15 * time.Minute
	contactCodeAttempts = 5
	accessLinkTTL       = 30 * time.Minute
)

var (
	ruleContactVisitor = ratelimit.Rule{Name: "contato_visitante", Limit: 5, Window: time.Hour}
	ruleAccessIP       = ratelimit.Rule{Name: "acesso_reserva_ip", Limit: 10, Window: 15 * time.Minute}
	ruleAccessAddress  = ratelimit.Rule{Name: "acesso_reserva_endereco", Limit: 3, Window: time.Hour}
)

var errAccessInvalid = &httpserver.Error{Status: 401, Code: "access_link_invalid", Title: "Link inválido",
	Detail: "Este link já foi usado ou venceu. Peça um novo na página da barbearia."}

func (s *Service) contactMAC(estID uuid.UUID, normalized string) []byte {
	return s.ring.MAC(purposeContactMAC, estID[:], []byte(normalized))
}

func (s *Service) contactCodeMAC(visitorID uuid.UUID, code string) []byte {
	return s.ring.MAC(purposeContactCode, visitorID[:], []byte(code))
}

type contactView struct {
	EmailMasked   string     `json:"email_masked"`
	Verified      bool       `json:"verified"`
	CodeExpiresAt *time.Time `json:"code_expires_at"`
}

// maskEmail mostra só o começo e o domínio: "ma***@exemplo.com".
func maskEmail(addr string) string {
	user, domain, ok := strings.Cut(addr, "@")
	if !ok {
		return "***"
	}
	r := []rune(user)
	keep := min(len(r), 2)
	return string(r[:keep]) + "***@" + domain
}

// currentVisitor devolve o visitante desta loja no navegador (credencial válida).
func (s *Service) currentVisitor(r *http.Request, estID uuid.UUID) (uuid.UUID, error) {
	_, visitor := s.CustomerIdentities(r, estID)
	if visitor == nil {
		return uuid.Nil, httpserver.ErrUnauthenticated()
	}
	return visitor.VisitorID, nil
}

func (s *Service) contactOf(ctx context.Context, q database.Querier, visitorID uuid.UUID) (*contactView, string, error) {
	var sealed []byte
	var verified *time.Time
	var codeExp *time.Time
	err := q.QueryRow(ctx, `SELECT email_sealed, verified_at, code_expires_at FROM visitor_contact WHERE visitor_id = $1`, visitorID).
		Scan(&sealed, &verified, &codeExp)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	plain, err := s.ring.Open(purposeContactEmail, sealed, visitorID[:])
	if err != nil {
		return nil, "", fmt.Errorf("abrir contato do visitante: %w", err)
	}
	addr := string(plain)
	return &contactView{EmailMasked: maskEmail(addr), Verified: verified != nil, CodeExpiresAt: codeExp}, addr, nil
}

// getVisitorContact: o contato do visitante nesta loja (mascarado) ou nulo.
func (s *Service) getVisitorContact(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	visitorID, err := s.currentVisitor(r, estID)
	if err != nil {
		return err
	}
	v, _, err := s.contactOf(r.Context(), s.pool, visitorID)
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[*contactView]{Data: v})
	return nil
}

// setVisitorContact (T-VC-01): grava o e-mail (não verificado) e envia o código de 6 dígitos.
// Trocar o e-mail volta a exigir verificação.
func (s *Service) setVisitorContact(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	visitorID, err := s.currentVisitor(r, estID)
	if err != nil {
		return err
	}
	var body struct {
		Email *string `json:"email"`
	}
	if _, err := httpserver.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Email == nil {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/email", Code: httpserver.FieldRequired})
	}
	addr, norm, fe := validate.Email("/email", *body.Email)
	if fe != nil {
		return httpserver.ErrValidation(*fe)
	}
	ctx := r.Context()
	if err := s.allow(ctx, lim(ruleContactVisitor, visitorID.String()), lim(ruleMailIP, s.clientIP(r))); err != nil {
		return err
	}
	code, err := newNumericCode(6)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	exp := now.Add(contactCodeTTL)
	sealed, err := s.ring.Seal(purposeContactEmail, []byte(addr), visitorID[:])
	if err != nil {
		return err
	}
	var store estabelecimento.Public
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var e error
		if store, e = estabelecimento.ActiveByID(ctx, tx, estID); e != nil {
			return httpserver.ErrNotFound()
		}
		if _, e = tx.Exec(ctx, `
			INSERT INTO visitor_contact (visitor_id, establishment_id, email_sealed, email_hmac, code_hmac, code_expires_at, code_attempts,
			                             verified_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 0, NULL, $7, $7)
			ON CONFLICT (visitor_id) DO UPDATE SET email_sealed = EXCLUDED.email_sealed, email_hmac = EXCLUDED.email_hmac,
			    code_hmac = EXCLUDED.code_hmac, code_expires_at = EXCLUDED.code_expires_at, code_attempts = 0,
			    verified_at = CASE WHEN visitor_contact.email_hmac = EXCLUDED.email_hmac THEN visitor_contact.verified_at END,
			    updated_at = EXCLUDED.updated_at`,
			visitorID, estID, sealed, s.contactMAC(estID, norm), s.contactCodeMAC(visitorID, code), exp, now); e != nil {
			return e
		}
		mailBody := fmt.Sprintf(`Olá.

Seu código para confirmar este e-mail na %s é:

%s

Ele vale por 15 minutos. Com o e-mail confirmado, você recebe os avisos da sua reserva e pode
recuperar o acesso a ela em outro aparelho.

Se você não pediu isso, ignore este e-mail.`, store.Name, code)
		if e = s.outbox.Enqueue(ctx, tx, "codigo_contato", mail.Message{To: addr, Subject: "Código para confirmar seu e-mail", Body: mailBody}, now); e != nil {
			return e
		}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorVisitor,
			ActorID: auditoria.Ptr(visitorID), Action: "T-VC-01", TargetType: "visitor", TargetID: visitorID, ToState: "contact_pending",
			RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	v, _, err := s.contactOf(ctx, s.pool, visitorID)
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusAccepted, httpserver.Data[*contactView]{Data: v})
	return nil
}

// verifyVisitorContact (T-VC-02): confere o código (até 5 tentativas; uso único).
func (s *Service) verifyVisitorContact(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	visitorID, err := s.currentVisitor(r, estID)
	if err != nil {
		return err
	}
	var body struct {
		Code *string `json:"code"`
	}
	if _, err := httpserver.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Code == nil || len(strings.TrimSpace(*body.Code)) != 6 {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/code", Code: httpserver.FieldInvalidFormat, Message: "6 dígitos"})
	}
	code := strings.TrimSpace(*body.Code)
	ctx := r.Context()
	now := s.clock.Now()
	invalid := &httpserver.Error{Status: 409, Code: "contact_code_invalid", Title: "Código incorreto",
		Detail: "Confira o código do e-mail. Se ele venceu, peça outro."}
	ok := false
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var mac []byte
		var exp *time.Time
		var attempts int
		err := tx.QueryRow(ctx, `SELECT code_hmac, code_expires_at, code_attempts FROM visitor_contact
			WHERE visitor_id = $1 AND establishment_id = $2 FOR UPDATE`, visitorID, estID).Scan(&mac, &exp, &attempts)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && mac == nil) {
			return invalid
		}
		if err != nil {
			return err
		}
		if attempts >= contactCodeAttempts || !now.Before(*exp) {
			return &httpserver.Error{Status: 409, Code: "contact_code_expired", Title: "Código vencido",
				Detail: "Peça um novo código para confirmar o e-mail."}
		}
		if subtle.ConstantTimeCompare(mac, s.contactCodeMAC(visitorID, code)) != 1 {
			_, err := tx.Exec(ctx, `UPDATE visitor_contact SET code_attempts = code_attempts + 1, updated_at = $2 WHERE visitor_id = $1`, visitorID, now)
			return err // a tentativa conta (commit) e a resposta é a recusa
		}
		if _, err := tx.Exec(ctx, `UPDATE visitor_contact SET verified_at = $2, code_hmac = NULL, code_expires_at = NULL, code_attempts = 0,
			updated_at = $2 WHERE visitor_id = $1`, visitorID, now); err != nil {
			return err
		}
		ok = true
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorVisitor,
			ActorID: auditoria.Ptr(visitorID), Action: "T-VC-02", TargetType: "visitor", TargetID: visitorID, FromState: "contact_pending",
			ToState: "contact_verified", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	if !ok {
		return invalid
	}
	v, _, err := s.contactOf(ctx, s.pool, visitorID)
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[*contactView]{Data: v})
	return nil
}

// requestAppointmentAccess (T-VC-03): manda o link de acesso para o e-mail verificado. A
// resposta é sempre 202 (não revela se o e-mail existe na loja).
func (s *Service) requestAppointmentAccess(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	var body struct {
		Email *string `json:"email"`
	}
	if _, err := httpserver.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Email == nil {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/email", Code: httpserver.FieldRequired})
	}
	addr, norm, fe := validate.Email("/email", *body.Email)
	if fe != nil {
		return httpserver.ErrValidation(*fe)
	}
	ctx := r.Context()
	mac := s.contactMAC(estID, norm)
	if err := s.allow(ctx, lim(ruleAccessIP, estID.String()+"|"+s.clientIP(r)), lim(ruleAccessAddress, fmt.Sprintf("%x", mac))); err != nil {
		return err
	}
	now := s.clock.Now()
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		store, err := estabelecimento.ActiveByID(ctx, tx, estID)
		if err != nil {
			return nil // loja inexistente: mesma resposta
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM visitor_contact c JOIN visitor v ON v.id = c.visitor_id
			WHERE c.establishment_id = $1 AND c.email_hmac = $2 AND c.verified_at IS NOT NULL AND v.absolute_expires_at > $3`,
			estID, mac, now).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		token, hash := newToken()
		if _, err := tx.Exec(ctx, `INSERT INTO appointment_access_token (token_hash, establishment_id, email_hmac, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5)`, hash, estID, mac, now.Add(accessLinkTTL), now); err != nil {
			return err
		}
		mailBody := fmt.Sprintf(`Olá.

Para ver e acompanhar a sua reserva na %s neste aparelho, abra o link abaixo (válido por 30 minutos,
uso único):

%s

Se você não pediu isso, ignore este e-mail: nada muda na sua reserva.`, store.Name, s.link("/loja/"+store.Slug+"/acesso", token))
		return s.outbox.Enqueue(ctx, tx, "acesso_reserva", mail.Message{To: addr, Subject: "Acesso à sua reserva", Body: mailBody}, now)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// exchangeAppointmentAccess (T-VC-04): troca o token do link (uso único) por uma credencial de
// visitante neste navegador (o visitante mais recente com aquele e-mail verificado na loja).
func (s *Service) exchangeAppointmentAccess(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	var body struct {
		Token *string `json:"token"`
	}
	if _, err := httpserver.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Token == nil || len(*body.Token) < 20 || len(*body.Token) > 100 {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/token", Code: httpserver.FieldInvalidFormat})
	}
	ctx := r.Context()
	limits := []limitCheck{lim(ruleTokenIP, s.clientIP(r))}
	if err := s.checkBlocked(ctx, limits...); err != nil {
		return err
	}
	p := PrincipalFrom(ctx)
	now := s.clock.Now()
	var view visitorView
	var browserToken string
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var mac []byte
		err := tx.QueryRow(ctx, `UPDATE appointment_access_token SET used_at = $3
			WHERE token_hash = $1 AND establishment_id = $2 AND used_at IS NULL AND expires_at > $3 RETURNING email_hmac`,
			hashToken(*body.Token), estID, now).Scan(&mac)
		if errors.Is(err, pgx.ErrNoRows) {
			return errAccessInvalid
		}
		if err != nil {
			return err
		}
		var visitorID uuid.UUID
		var nick string
		var absolute time.Time
		var closed *time.Time
		err = tx.QueryRow(ctx, `SELECT v.id, v.nick, v.absolute_expires_at, v.visit_closed_at FROM visitor_contact c
			JOIN visitor v ON v.id = c.visitor_id
			WHERE c.establishment_id = $1 AND c.email_hmac = $2 AND c.verified_at IS NOT NULL AND v.absolute_expires_at > $3
			ORDER BY c.verified_at DESC LIMIT 1 FOR UPDATE OF v`, estID, mac, now).Scan(&visitorID, &nick, &absolute, &closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return errAccessInvalid
		}
		if err != nil {
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
		view = visitorView{ID: visitorID, EstablishmentID: estID, Nick: nick, ExpiresAt: visitorExpiry(absolute, now, closed)}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorVisitor,
			ActorID: auditoria.Ptr(visitorID), Action: "T-VC-04", TargetType: "visitor", TargetID: visitorID, FromState: "active",
			ToState: "recovered_by_email", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if errors.Is(err, errAccessInvalid) {
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
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[visitorView]{Data: view})
	return nil
}

// NoticeEmail devolve o e-mail de aviso de uma reserva: o da conta (ativa) ou o contato
// verificado do visitante. ok=false sem endereço utilizável (o aviso é pulado, não falha).
func (s *Service) NoticeEmail(ctx context.Context, q database.Querier, accountID, visitorID *uuid.UUID) (string, bool, error) {
	if accountID != nil {
		var addr string
		err := q.QueryRow(ctx, `SELECT email FROM account WHERE id = $1 AND status = 'active'`, *accountID).Scan(&addr)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return addr, err == nil, err
	}
	if visitorID == nil {
		return "", false, nil
	}
	v, addr, err := s.contactOf(ctx, q, *visitorID)
	if err != nil || v == nil || !v.Verified {
		return "", false, err
	}
	return addr, true, nil
}
