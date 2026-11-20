package identidade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/validate"
)

// Instalações desktop autorizadas (ADR-0027).
//
//  1. Ativação: o operador emite um código de uso único (fila-admin desktop emitir-codigo), ligado
//     à conta do dono e válido por 15 min. O programa gera um par de chaves P-256 próprio, guarda a
//     privada no cofre do sistema operacional e envia o código com uma prova DPoP: o servidor
//     registra a chave pública (impressão digital jkt). Uma página web comum não tem como fazer
//     isso: não tem código nem chave.
//  2. Vínculo: a gestão aberta na janela (cookie da sessão de dono ocupante) pede um ticket de 60 s;
//     o componente nativo troca o ticket, com prova DPoP, por um token de acesso preso à chave. O
//     token vale só enquanto a sessão for a ocupante da vaga de dono: não é outra sessão.
//  3. Consultas analíticas: token + prova DPoP com ath, conferidos a cada pedido, mais o vínculo
//     de dono com a loja pedida.
//
// Limite da garantia: o servidor confere a sessão e a posse da chave de uma instalação autorizada.
// Ele não atesta o executável num computador controlado pelo usuário.

const (
	DesktopActivationTTL = 15 * time.Minute
	DesktopTicketTTL     = 60 * time.Second
	// DesktopBindingTTL limita o token mesmo com sessão longa; o programa pede outro ticket.
	DesktopBindingTTL = 2 * time.Hour
)

func errDesktopActivation() *httpserver.Error {
	return &httpserver.Error{Status: 409, Code: "desktop_activation_invalid", Title: "Código de ativação inválido",
		Detail: "O código está errado, já foi usado ou venceu. Peça um código novo ao responsável pelo sistema."}
}

func errDesktopLink() *httpserver.Error {
	return &httpserver.Error{Status: 409, Code: "desktop_link_invalid", Title: "Não foi possível abrir o Dashboard",
		Detail: "O pedido de abertura venceu ou já foi usado. Volte para a gestão e abra o Dashboard de novo."}
}

func errDesktopAccess(detail string) *httpserver.Error {
	return &httpserver.Error{Status: 401, Code: "desktop_access_invalid", Title: "Acesso do Dashboard encerrado", Detail: detail}
}

// htu é a URL pública do pedido (sem consulta), como o programa a assina.
func (s *Service) htu(r *http.Request) string {
	return strings.TrimRight(s.cfg.PublicBaseURL, "/") + r.URL.EscapedPath()
}

// ---------------------------------------------------------------------------------------------
// POST /v1/desktop/installations/activate

type activateRequest struct {
	Code *string `json:"code"`
}

type installationView struct {
	ID        uuid.UUID `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) activateDesktopInstallation(w http.ResponseWriter, r *http.Request) error {
	var req activateRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Code == nil {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/code", Code: httpserver.FieldRequired})
	}
	ctx := r.Context()
	limits := []limitCheck{lim(ruleTokenIP, s.clientIP(r))}
	if err := s.checkBlocked(ctx, limits...); err != nil {
		return err
	}
	now := s.clock.Now()
	proof, err := verifyDPoP(r.Header.Get("DPoP"), r.Method, s.htu(r), "", now)
	if err != nil {
		return err
	}
	code, ok := normalizeRecoveryCode(*req.Code)
	if !ok {
		if err := s.hit(ctx, limits...); err != nil {
			return err
		}
		return errDesktopActivation()
	}
	jwk, err := publicJWK(proof)
	if err != nil {
		return err
	}
	var view installationView
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := rememberProof(ctx, tx, proof, now); err != nil {
			return err
		}
		var codeID, accountID uuid.UUID
		var label string
		err := tx.QueryRow(ctx, `SELECT c.id, c.account_id, c.label FROM desktop_activation_code c JOIN account a ON a.id = c.account_id
			WHERE c.code_hash = $1 AND c.consumed_at IS NULL AND c.revoked_at IS NULL AND c.expires_at > $2 AND a.status = 'active'
			FOR UPDATE OF c`, hashToken(code), now).Scan(&codeID, &accountID, &label)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDesktopActivation()
		}
		if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM desktop_installation WHERE jkt = $1)`, proof.JKT).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return &httpserver.Error{Status: 409, Code: "desktop_activation_invalid", Title: "Chave já registrada",
				Detail: "Esta instalação já foi ativada. Se ela foi revogada, apague a chave local pelo programa e ative de novo."}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO desktop_installation (account_id, label, jkt, public_jwk, created_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id, label, created_at`, accountID, label, proof.JKT, jwk, now).
			Scan(&view.ID, &view.Label, &view.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE desktop_activation_code SET consumed_at = $2, installation_id = $3 WHERE id = $1`,
			codeID, now, view.ID); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(accountID), Action: "T-DK-02", TargetType: "desktop_installation", TargetID: view.ID,
			ToState: "active", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		var he *httpserver.Error
		if errors.As(err, &he) && he.Code == "desktop_activation_invalid" {
			_ = s.hit(ctx, limits...)
		}
		return err
	}
	httpserver.WriteJSON(w, http.StatusCreated, httpserver.Data[installationView]{Data: view})
	return nil
}

// ---------------------------------------------------------------------------------------------
// POST /v1/desktop/link-tickets (gestão com o cookie da sessão de dono)

type ticketView struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Service) createDesktopLinkTicket(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	if sess.Kind != KindStaff || !sess.OwnerHolder {
		return &httpserver.Error{Status: 403, Code: "owner_session_required", Title: "Só com o acesso de dono",
			Detail: "O Dashboard abre só a partir da sessão de dono ativa."}
	}
	ctx := r.Context()
	now := s.clock.Now()
	token, tokenHash := newToken()
	view := ticketView{Ticket: token, ExpiresAt: now.Add(DesktopTicketTTL)}
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error { // a guarda confere a vaga no commit
		_, err := tx.Exec(ctx, `INSERT INTO desktop_link_ticket (session_id, ticket_hash, created_at, expires_at)
			VALUES ($1, $2, $3, $4)`, sess.ID, tokenHash, now, view.ExpiresAt)
		return err
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusCreated, httpserver.Data[ticketView]{Data: view})
	return nil
}

// ---------------------------------------------------------------------------------------------
// POST /v1/desktop/bindings (componente nativo: ticket + prova DPoP)

type bindingRequest struct {
	Ticket *string `json:"ticket"`
}

type bindingView struct {
	AccessToken  string           `json:"access_token"`
	TokenType    string           `json:"token_type"`
	ExpiresAt    time.Time        `json:"expires_at"`
	Installation installationView `json:"installation"`
}

func (s *Service) createDesktopBinding(w http.ResponseWriter, r *http.Request) error {
	var req bindingRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Ticket == nil || len(*req.Ticket) < 20 || len(*req.Ticket) > 64 {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/ticket", Code: httpserver.FieldInvalidFormat})
	}
	ctx := r.Context()
	now := s.clock.Now()
	proof, err := verifyDPoP(r.Header.Get("DPoP"), r.Method, s.htu(r), "", now)
	if err != nil {
		return err
	}
	token, tokenHash := newToken()
	var view bindingView
	err = database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := rememberProof(ctx, tx, proof, now); err != nil {
			return err
		}
		var inst installationView
		var instAccount uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id, account_id, label, created_at FROM desktop_installation
			WHERE jkt = $1 AND revoked_at IS NULL`, proof.JKT).Scan(&inst.ID, &instAccount, &inst.Label, &inst.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return &httpserver.Error{Status: 403, Code: "desktop_installation_invalid", Title: "Computador não autorizado",
				Detail: "Este computador não está autorizado (ou foi revogado). Ative-o com um código novo."}
		}
		if err != nil {
			return err
		}
		var ticketID, sessionID, sessAccount uuid.UUID
		var sessExpires time.Time
		var holder bool
		err = tx.QueryRow(ctx, `
			SELECT t.id, s.id, s.account_id, s.absolute_expires_at,
			       EXISTS (SELECT 1 FROM owner_session_slot o WHERE o.account_id = s.account_id AND o.session_id = s.id)
			FROM desktop_link_ticket t JOIN account_session s ON s.id = t.session_id JOIN account a ON a.id = s.account_id
			WHERE t.ticket_hash = $1 AND t.consumed_at IS NULL AND t.expires_at > $2
			  AND s.revoked_at IS NULL AND s.absolute_expires_at > $2 AND s.kind = 'staff' AND a.status = 'active'
			FOR UPDATE OF t`, hashToken(*req.Ticket), now).Scan(&ticketID, &sessionID, &sessAccount, &sessExpires, &holder)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDesktopLink()
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE desktop_link_ticket SET consumed_at = $2 WHERE id = $1`, ticketID, now); err != nil {
			return err
		}
		// A instalação é da conta: um ticket de outra conta (outro dono no mesmo PC) não serve.
		if !holder || sessAccount != instAccount {
			return errDesktopLink()
		}
		expires := now.Add(DesktopBindingTTL)
		if sessExpires.Before(expires) {
			expires = sessExpires
		}
		// Um vínculo por sessão e instalação: o anterior (de uma abertura antiga) é encerrado.
		if _, err := tx.Exec(ctx, `UPDATE desktop_binding SET revoked_at = $3
			WHERE session_id = $1 AND installation_id = $2 AND revoked_at IS NULL`, sessionID, inst.ID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO desktop_binding (installation_id, session_id, token_hash, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5)`, inst.ID, sessionID, tokenHash, now, expires); err != nil {
			return err
		}
		view = bindingView{AccessToken: token, TokenType: "DPoP", ExpiresAt: expires, Installation: inst}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(sessAccount), Action: "T-DK-03", TargetType: "desktop_installation", TargetID: inst.ID,
			ToState: "linked", RequestID: httpserver.RequestIDFrom(ctx)})
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusCreated, httpserver.Data[bindingView]{Data: view})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Autorização dos pedidos nativos

// DesktopPrincipal é o resultado de uma prova válida: sessão de dono ocupante + instalação.
type DesktopPrincipal struct {
	AccountID      uuid.UUID
	AccountName    string
	SessionID      uuid.UUID
	BindingID      uuid.UUID
	InstallationID uuid.UUID
	Installation   string
	ExpiresAt      time.Time
}

// desktopPrincipal confere token, prova DPoP (com ath), instalação, sessão e vaga de dono.
func (s *Service) desktopPrincipal(r *http.Request) (*DesktopPrincipal, error) {
	token, err := bearerDPoP(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	now := s.clock.Now()
	proof, err := verifyDPoP(r.Header.Get("DPoP"), r.Method, s.htu(r), token, now)
	if err != nil {
		return nil, err
	}
	p, jkt, lastSeen, err := s.bindingState(ctx, token, now)
	if err != nil {
		return nil, err
	}
	if jkt != proof.JKT {
		return nil, errProof("A prova não é da instalação dona deste acesso.")
	}
	if err := rememberProof(ctx, s.pool, proof, now); err != nil {
		return nil, err
	}
	if lastSeen == nil || now.Sub(*lastSeen) >= touchEvery {
		if _, err := s.pool.Exec(ctx, `UPDATE desktop_installation SET last_seen_at = $2 WHERE id = $1`, p.InstallationID, now); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func bearerDPoP(r *http.Request) (string, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "DPoP ")
	if !ok || len(token) < 20 || len(token) > 64 {
		return "", errProof("Este pedido não veio de uma instalação autorizada do Fila Gestão.")
	}
	return token, nil
}

// bindingState lê o vínculo do token e confere instalação, sessão e vaga de dono (sem a prova).
func (s *Service) bindingState(ctx context.Context, token string, now time.Time) (*DesktopPrincipal, string, *time.Time, error) {
	var p DesktopPrincipal
	var jkt string
	var bindingExpires, sessExpires time.Time
	var sessRevoked *time.Time
	var revokeReason *string
	var holder bool
	var accStatus string
	var lastSeen, bindingRevoked *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT b.id, b.expires_at, b.revoked_at, i.id, i.jkt, i.label, i.last_seen_at, s.id, s.account_id, s.revoked_at, s.revoke_reason,
		       s.absolute_expires_at, a.name, a.status,
		       EXISTS (SELECT 1 FROM owner_session_slot o WHERE o.account_id = s.account_id AND o.session_id = s.id)
		FROM desktop_binding b
		JOIN desktop_installation i ON i.id = b.installation_id
		JOIN account_session s ON s.id = b.session_id
		JOIN account a ON a.id = s.account_id
		WHERE b.token_hash = $1 AND i.revoked_at IS NULL AND i.account_id = s.account_id`,
		hashToken(token)).Scan(&p.BindingID, &bindingExpires, &bindingRevoked, &p.InstallationID, &jkt, &p.Installation, &lastSeen, &p.SessionID,
		&p.AccountID, &sessRevoked, &revokeReason, &sessExpires, &p.AccountName, &accStatus, &holder)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil, errDesktopAccess("O acesso do Dashboard terminou. Abra o Dashboard de novo a partir da gestão.")
	}
	if err != nil {
		return nil, "", nil, fmt.Errorf("ler vínculo desktop: %w", err)
	}
	if sessRevoked != nil {
		if revokeReason != nil && *revokeReason == EndedOwnerReplaced {
			return nil, "", nil, errOwnerReplaced()
		}
		return nil, "", nil, errDesktopAccess("A sessão de dono terminou. Entre de novo pela gestão.")
	}
	if bindingRevoked != nil {
		return nil, "", nil, errDesktopAccess("O acesso do Dashboard terminou. Abra o Dashboard de novo a partir da gestão.")
	}
	if !now.Before(sessExpires) || !now.Before(bindingExpires) || !holder || accStatus != "active" {
		return nil, "", nil, errDesktopAccess("A sessão de dono terminou ou venceu. Entre de novo pela gestão.")
	}
	p.ExpiresAt = bindingExpires
	return &p, jkt, lastSeen, nil
}

// DesktopRecheck reconfere (sem nova prova) o vínculo de um fluxo já aberto com prova válida:
// sessão, vaga, instalação e vínculo de dono com a loja. Usado a cada 30 s e a cada revogação.
func (s *Service) DesktopRecheck(r *http.Request, estID uuid.UUID) error {
	token, err := bearerDPoP(r)
	if err != nil {
		return err
	}
	p, _, _, err := s.bindingState(r.Context(), token, s.clock.Now())
	if err != nil {
		return err
	}
	ms, err := estabelecimento.MembershipsIn(r.Context(), s.pool, p.AccountID, estID)
	if err != nil {
		return err
	}
	if !hasOwnerMembership(ms) {
		return httpserver.ErrNotFound()
	}
	return nil
}

// DesktopOwner implementa acesso.DesktopAuthorizer: sessão de dono ocupante + instalação
// autorizada com prova válida + vínculo de dono com a loja pedida. Loja sem vínculo de dono
// (inclusive loja em que a conta é só profissional) → 404, sem revelar que existe.
func (s *Service) DesktopOwner(r *http.Request, estID uuid.UUID) (acesso.Actor, error) {
	p, err := s.desktopPrincipal(r)
	if err != nil {
		return acesso.Actor{}, err
	}
	ms, err := estabelecimento.MembershipsIn(r.Context(), s.pool, p.AccountID, estID)
	if err != nil {
		return acesso.Actor{}, err
	}
	if !hasOwnerMembership(ms) {
		return acesso.Actor{}, httpserver.ErrNotFound()
	}
	return acesso.Actor{AccountID: p.AccountID, EstablishmentID: estID, Role: acesso.RoleOwner}, nil
}

var _ acesso.DesktopAuthorizer = (*Service)(nil)

// GET /v1/desktop/context: quem está no programa e as lojas em que a conta é dona.
type desktopContextView struct {
	Account struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"account"`
	Installation struct {
		ID    uuid.UUID `json:"id"`
		Label string    `json:"label"`
	} `json:"installation"`
	AccessExpiresAt time.Time              `json:"access_expires_at"`
	Establishments  []desktopEstablishment `json:"establishments"`
}

type desktopEstablishment struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Timezone string    `json:"timezone"`
}

func (s *Service) getDesktopContext(w http.ResponseWriter, r *http.Request) error {
	p, err := s.desktopPrincipal(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	ms, err := estabelecimento.ActiveMemberships(ctx, s.pool, p.AccountID)
	if err != nil {
		return err
	}
	var v desktopContextView
	v.Account.ID, v.Account.Name = p.AccountID, p.AccountName
	v.Installation.ID, v.Installation.Label = p.InstallationID, p.Installation
	v.AccessExpiresAt = p.ExpiresAt
	v.Establishments = []desktopEstablishment{}
	for _, m := range ms {
		if m.Role != estabelecimento.RoleOwner {
			continue // loja em que a conta é só profissional não aparece no Dashboard
		}
		e, err := estabelecimento.ActiveByID(ctx, s.pool, m.EstablishmentID)
		if err != nil {
			return err
		}
		v.Establishments = append(v.Establishments, desktopEstablishment{ID: e.ID, Name: e.Name, Timezone: e.Timezone})
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[desktopContextView]{Data: v})
	return nil
}

// POST /v1/desktop/bindings/current/end: o Dashboard sai (a sessão de dono continua na gestão).
func (s *Service) endDesktopBinding(w http.ResponseWriter, r *http.Request) error {
	p, err := s.desktopPrincipal(r)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(r.Context(), `UPDATE desktop_binding SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`,
		p.BindingID, s.clock.Now()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------------------------------------
// Ferramentas do operador (fila-admin desktop …)

// DesktopActivation é o código emitido para ativar um computador.
type DesktopActivation struct {
	Code      string
	ExpiresAt time.Time
	AccountID uuid.UUID
}

// IssueDesktopActivation emite um código de uso único (15 min) para a conta de dono do e-mail.
func (s *Service) IssueDesktopActivation(ctx context.Context, email, label string) (DesktopActivation, error) {
	var out DesktopActivation
	_, emailNorm, fe := validate.Email("/email", email)
	if fe != nil {
		return out, fmt.Errorf("e-mail inválido")
	}
	label = strings.TrimSpace(label)
	if n := len([]rune(label)); n < 1 || n > 60 {
		return out, fmt.Errorf("o nome do computador precisa ter de 1 a 60 caracteres")
	}
	now := s.clock.Now()
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := findAccount(ctx, tx, emailNorm, true)
		if err != nil {
			return err
		}
		if a == nil || a.Status != "active" {
			return fmt.Errorf("não há conta ativa com esse e-mail")
		}
		ms, err := estabelecimento.ActiveMemberships(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if !hasOwnerMembership(ms) {
			return fmt.Errorf("a conta não é dona de nenhuma loja ativa")
		}
		// Só o código mais novo vale: emitir outro cancela os pendentes da conta.
		if _, err := tx.Exec(ctx, `UPDATE desktop_activation_code SET revoked_at = $2
			WHERE account_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, a.ID, now); err != nil {
			return err
		}
		out.Code = newRecoveryCode()
		norm, _ := normalizeRecoveryCode(out.Code)
		out.ExpiresAt = now.Add(DesktopActivationTTL)
		out.AccountID = a.ID
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO desktop_activation_code (account_id, code_hash, label, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, a.ID, hashToken(norm), label, now, out.ExpiresAt).Scan(&id); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOperator,
			Action: "T-DK-01", TargetType: "account", TargetID: a.ID, ToState: "activation_code_issued"})
	})
	return out, err
}

// DesktopInstallation é uma linha da listagem do operador.
type DesktopInstallation struct {
	ID         uuid.UUID
	Label      string
	CreatedAt  time.Time
	LastSeenAt *time.Time
	RevokedAt  *time.Time
}

// ListDesktopInstallations lista os computadores da conta (ativos e revogados).
func (s *Service) ListDesktopInstallations(ctx context.Context, email string) ([]DesktopInstallation, error) {
	_, emailNorm, fe := validate.Email("/email", email)
	if fe != nil {
		return nil, fmt.Errorf("e-mail inválido")
	}
	rows, err := s.pool.Query(ctx, `SELECT i.id, i.label, i.created_at, i.last_seen_at, i.revoked_at
		FROM desktop_installation i JOIN account a ON a.id = i.account_id
		WHERE a.email_normalized = $1 AND a.status <> 'deleted' ORDER BY i.created_at, i.id`, emailNorm)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DesktopInstallation, error) {
		var d DesktopInstallation
		err := r.Scan(&d.ID, &d.Label, &d.CreatedAt, &d.LastSeenAt, &d.RevokedAt)
		return d, err
	})
}

// RevokeDesktopInstallation revoga o computador: os vínculos dele terminam na hora e as consultas
// seguintes são recusadas. A sessão de dono na gestão continua (é do navegador da janela).
func (s *Service) RevokeDesktopInstallation(ctx context.Context, id uuid.UUID) error {
	now := s.clock.Now()
	return database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE desktop_installation SET revoked_at = $2, revoke_reason = 'revoked_by_admin'
			WHERE id = $1 AND revoked_at IS NULL`, id, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("instalação não encontrada ou já revogada")
		}
		if _, err := tx.Exec(ctx, `UPDATE desktop_binding SET revoked_at = $2 WHERE installation_id = $1 AND revoked_at IS NULL`, id, now); err != nil {
			return err
		}
		if err := notifyRevocation(ctx, tx); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOperator,
			Action: "T-DK-04", TargetType: "desktop_installation", TargetID: id, FromState: "active", ToState: "revoked"})
	})
}
