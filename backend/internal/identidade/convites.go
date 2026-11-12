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
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

var errInviteInvalid = &httpserver.Error{Status: 409, Code: "invite_invalid", Title: "Convite inválido ou vencido",
	Detail: "Este convite já foi usado, foi cancelado, venceu ou não existe. Peça um novo ao dono da loja."}

type inviteRow struct {
	ID              uuid.UUID
	EstablishmentID uuid.UUID
	Role            string
	ProfessionalID  *uuid.UUID
	Email           string
	Status          string
	ExpiresAt       time.Time
}

// view calcula o estado exibido: pendente vencido aparece como "expired" (T-IN-04, verificado no uso).
func (i inviteRow) view(now time.Time) inviteView {
	st := i.Status
	if st == "pending" && !now.Before(i.ExpiresAt) {
		st = "expired"
	}
	v := inviteView{ID: i.ID, Status: st, ExpiresAt: i.ExpiresAt}
	if i.ProfessionalID != nil {
		v.ProfessionalID = *i.ProfessionalID
	}
	return v
}

func insertInvite(ctx context.Context, q database.Querier, estID uuid.UUID, role string, professionalID *uuid.UUID,
	email string, createdBy *uuid.UUID, now time.Time) (inviteRow, string, error) {
	token, hash := newToken()
	row := inviteRow{EstablishmentID: estID, Role: role, ProfessionalID: professionalID, Email: email,
		Status: "pending", ExpiresAt: now.Add(InviteTTL)}
	err := q.QueryRow(ctx, `
		INSERT INTO invite (establishment_id, role, professional_id, email, token_hash, created_by_account_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		estID, role, professionalID, email, hash, createdBy, now, row.ExpiresAt).Scan(&row.ID)
	if err != nil {
		return inviteRow{}, "", fmt.Errorf("criar convite: %w", err)
	}
	return row, token, nil
}

// ---------------------------------------------------------------------------------------------
// createInvite (T-IN-01): dono, reautenticado, com Idempotency-Key.

type createInviteRequest struct {
	Email *string `json:"email"`
}

func (s *Service) createInvite(w http.ResponseWriter, r *http.Request) error {
	sess, estID, err := s.requireOwner(r.Context(), r)
	if err != nil {
		return err
	}
	if err := s.requireReauth(sess); err != nil {
		return err
	}
	profID, err := uuid.Parse(r.PathValue("professionalId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	var req createInviteRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	if req.Email == nil {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/email", Code: httpserver.FieldRequired})
	}
	email, _, fe := validate.Email("/email", *req.Email)
	if fe != nil {
		return httpserver.ErrValidation(*fe)
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + sess.AccountID.String(), Operation: "createInvite", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.clock.Now()
		pa, err := estabelecimento.LockProfessional(ctx, tx, estID, profID)
		if errors.Is(err, estabelecimento.ErrNotFound) {
			return idempotency.Result{}, httpserver.ErrNotFound()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		if !pa.Active {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "professional_inactive",
				Title: "Profissional desativado", Detail: "Reative o profissional antes de convidar."}
		}
		if pa.HasAccess {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "invalid_transition",
				Title: "Profissional já tem acesso", Detail: "Este profissional já ativou o acesso."}
		}
		rid := httpserver.RequestIDFrom(ctx)
		// O convite pendente anterior deixa de valer (T-IN-01).
		rows, err := tx.Query(ctx, `UPDATE invite SET status = 'revoked', revoked_at = $2
			WHERE professional_id = $1 AND status = 'pending' RETURNING id`, profID, now)
		if err != nil {
			return idempotency.Result{}, err
		}
		revoked, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return idempotency.Result{}, err
		}
		for _, id := range revoked {
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
				ActorKind: auditoria.ActorOwner, ActorID: auditoria.Ptr(sess.AccountID), Action: "T-IN-03",
				TargetType: "invite", TargetID: id, FromState: "pending", ToState: "revoked", RequestID: rid}); err != nil {
				return idempotency.Result{}, err
			}
		}
		inv, token, err := insertInvite(ctx, tx, estID, estabelecimento.RoleProfessional, &profID, email, &sess.AccountID, now)
		if err != nil {
			return idempotency.Result{}, err
		}
		est, err := estabelecimento.ActiveByID(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if err := s.mailInvite(ctx, tx, email, est.Name, estabelecimento.RoleProfessional, token, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorOwner, ActorID: auditoria.Ptr(sess.AccountID), Action: "T-IN-01",
			TargetType: "invite", TargetID: inv.ID, ToState: "pending", RequestID: rid}); err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusCreated, Body: httpserver.Data[inviteView]{Data: inv.view(now)}}, nil
	})
}

// ---------------------------------------------------------------------------------------------
// revokeInvite (T-IN-03): dono, com Idempotency-Key.

func (s *Service) revokeInvite(w http.ResponseWriter, r *http.Request) error {
	sess, estID, err := s.requireOwner(r.Context(), r)
	if err != nil {
		return err
	}
	inviteID, err := uuid.Parse(r.PathValue("inviteId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	var empty struct{}
	body, err := httpserver.DecodeJSON(r, &empty)
	if err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + sess.AccountID.String(), Operation: "revokeInvite", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.clock.Now()
		var inv inviteRow
		err := tx.QueryRow(ctx, `
			SELECT id, establishment_id, role, professional_id, email, status, expires_at FROM invite
			WHERE establishment_id = $1 AND id = $2 AND role = 'professional' FOR UPDATE`, estID, inviteID).
			Scan(&inv.ID, &inv.EstablishmentID, &inv.Role, &inv.ProfessionalID, &inv.Email, &inv.Status, &inv.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return idempotency.Result{}, httpserver.ErrNotFound()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		if v := inv.view(now); v.Status != "pending" {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "invalid_transition",
				Title: "Convite não está pendente", Detail: "Só é possível cancelar convite pendente.", Current: v}
		}
		if _, err := tx.Exec(ctx, `UPDATE invite SET status = 'revoked', revoked_at = $2 WHERE id = $1`, inv.ID, now); err != nil {
			return idempotency.Result{}, err
		}
		inv.Status = "revoked"
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorOwner, ActorID: auditoria.Ptr(sess.AccountID), Action: "T-IN-03",
			TargetType: "invite", TargetID: inv.ID, FromState: "pending", ToState: "revoked",
			RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[inviteView]{Data: inv.view(now)}}, nil
	})
}

// ---------------------------------------------------------------------------------------------
// acceptInvite (T-IN-02): público, com o token do e-mail.

type acceptInviteRequest struct {
	Token    *string `json:"token"`
	Name     *string `json:"name"`
	Password *string `json:"password"`
}

func (s *Service) acceptInvite(w http.ResponseWriter, r *http.Request) error {
	var req acceptInviteRequest
	if _, err := httpserver.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := validateToken("/token", req.Token); err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/password", req.Password == nil || *req.Password == "")
	var name string
	if req.Name != nil {
		n, fe := validate.Text("/name", *req.Name, 1, 80)
		v.Add(fe)
		name = n
	}
	if req.Password != nil && len([]rune(*req.Password)) > MaxPasswordLen {
		v.Add(&httpserver.FieldError{Field: "/password", Code: httpserver.FieldTooLong})
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	ipLimit := lim(ruleTokenIP, s.clientIP(r))
	if err := s.checkBlocked(ctx, ipLimit); err != nil {
		return err
	}

	now := s.clock.Now()
	var wrongPassword []limitCheck
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var inv inviteRow
		err := tx.QueryRow(ctx, `
			SELECT id, establishment_id, role, professional_id, email, status, expires_at FROM invite
			WHERE token_hash = $1 FOR UPDATE`, hashToken(*req.Token)).
			Scan(&inv.ID, &inv.EstablishmentID, &inv.Role, &inv.ProfessionalID, &inv.Email, &inv.Status, &inv.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInviteInvalid
		}
		if err != nil {
			return err
		}
		if inv.view(now).Status != "pending" {
			return errInviteInvalid
		}
		if _, err := estabelecimento.ActiveByID(ctx, tx, inv.EstablishmentID); errors.Is(err, estabelecimento.ErrNotFound) {
			return errInviteInvalid
		} else if err != nil {
			return err
		}
		if inv.ProfessionalID != nil {
			pa, err := estabelecimento.LockProfessional(ctx, tx, inv.EstablishmentID, *inv.ProfessionalID)
			if errors.Is(err, estabelecimento.ErrNotFound) {
				return errInviteInvalid
			}
			if err != nil {
				return err
			}
			if !pa.Active || pa.HasAccess {
				return errInviteInvalid
			}
		}
		email, emailNorm, fe := validate.Email("/email", inv.Email)
		if fe != nil {
			return errInviteInvalid
		}
		acct, err := findAccount(ctx, tx, emailNorm, true)
		if err != nil {
			return err
		}
		var accountID uuid.UUID
		switch {
		case acct == nil || acct.Status == "pending_verification":
			// Conta nova (ou pendente): o convite chegou ao e-mail, então ele está provado.
			// A senha informada passa a valer — inclusive sobre uma conta pendente criada por
			// quem não controla o e-mail.
			if fe := ValidateNewPassword("/password", *req.Password); fe != nil {
				return httpserver.ErrValidation(*fe)
			}
			if acct == nil && name == "" {
				return httpserver.ErrValidation(httpserver.FieldError{Field: "/name", Code: httpserver.FieldRequired,
					Message: "informe seu nome para criar a conta"})
			}
			hash, err := s.hasher.Hash(ctx, *req.Password)
			if err != nil {
				return err
			}
			if acct == nil {
				if err := tx.QueryRow(ctx, `
					INSERT INTO account (name, email, email_normalized, password_hash, status, email_verified_at,
					                     password_changed_at, created_at, updated_at)
					VALUES ($1, $2, $3, $4, 'active', $5, $5, $5, $5) RETURNING id`,
					name, email, emailNorm, hash, now).Scan(&accountID); err != nil {
					return fmt.Errorf("criar conta pelo convite: %w", err)
				}
				if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
					ActorID: auditoria.Ptr(accountID), Action: "T-AC-01", TargetType: "account", TargetID: accountID,
					ToState: "active", Reason: "conta criada por convite", RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
					return err
				}
			} else {
				accountID = acct.ID
				if name == "" {
					name = acct.Name
				}
				if _, err := tx.Exec(ctx, `
					UPDATE account SET name = $2, password_hash = $3, status = 'active', email_verified_at = $4,
					       password_changed_at = $4, updated_at = $4 WHERE id = $1`, accountID, name, hash, now); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE account_token SET revoked_at = $2
					WHERE account_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, accountID, now); err != nil {
					return err
				}
				if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer,
					ActorID: auditoria.Ptr(accountID), Action: "T-AC-02", TargetType: "account", TargetID: accountID,
					FromState: "pending_verification", ToState: "active", Reason: "e-mail provado por convite",
					RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
					return err
				}
			}
		case acct.Status == "active":
			// Conta existente: precisa provar que é a dona dela (senha atual).
			limits := []limitCheck{lim(rulePasswordAccount, emailNorm), lim(rulePasswordIP, s.clientIP(r))}
			if err := s.checkBlocked(ctx, limits...); err != nil {
				return err
			}
			ok, _, err := s.hasher.Verify(ctx, *req.Password, acct.PasswordHash)
			if err != nil {
				return err
			}
			if !ok {
				wrongPassword = limits
				return errInvalidCredentials
			}
			accountID = acct.ID
		default:
			return errInviteInvalid
		}

		if _, err := estabelecimento.AddMembership(ctx, tx, inv.EstablishmentID, accountID, inv.Role, inv.ProfessionalID, now); errors.Is(err, estabelecimento.ErrAlreadyMember) {
			return &httpserver.Error{Status: 409, Code: "invalid_transition", Title: "Acesso já existe",
				Detail: "Esta conta já tem esse acesso nesta loja."}
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invite SET status = 'accepted', accepted_at = $2, accepted_account_id = $3 WHERE id = $1`,
			inv.ID, now, accountID); err != nil {
			return err
		}
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &inv.EstablishmentID, OccurredAt: now,
			ActorKind: auditoria.ActorCustomer, ActorID: auditoria.Ptr(accountID), Action: "T-IN-02",
			TargetType: "invite", TargetID: inv.ID, FromState: "pending", ToState: "accepted",
			RequestID: httpserver.RequestIDFrom(ctx)})
	})
	switch {
	case errors.Is(err, errInviteInvalid):
		if herr := s.hit(ctx, ipLimit); herr != nil {
			return herr
		}
	case errors.Is(err, errInvalidCredentials):
		if herr := s.hit(ctx, wrongPassword...); herr != nil {
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
// Dono inicial (T-OP-01, ADR-0013): processo controlado do operador, sem rota HTTP.

// BootstrapInput são os dados informados pelo operador.
type BootstrapInput struct {
	Name     string
	Slug     string
	Timezone string
	Email    string
	Reason   string
}

// BootstrapResult resume o que foi criado (sem o token, que só vai por e-mail).
type BootstrapResult struct {
	EstablishmentID uuid.UUID
	InviteExpiresAt time.Time
}

// ErrSlugTaken indica endereço de loja já usado.
var ErrSlugTaken = estabelecimento.ErrSlugTaken

// Bootstrap cria a loja e o convite de dono (uso único, 24 h) enviado ao e-mail informado.
func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) (BootstrapResult, error) {
	var v validate.Collector
	name, fe := validate.Text("/nome", in.Name, 1, 80)
	v.Add(fe)
	if !slugPattern.MatchString(in.Slug) {
		v.Add(&httpserver.FieldError{Field: "/slug", Code: httpserver.FieldInvalidFormat,
			Message: "use de 3 a 50 letras minúsculas, números e hífens (sem hífen no início ou fim)"})
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" || in.Timezone == "Local" {
		v.Add(&httpserver.FieldError{Field: "/fuso", Code: httpserver.FieldInvalidFormat, Message: "fuso IANA, ex.: America/Sao_Paulo"})
	}
	email, _, fe := validate.Email("/email", in.Email)
	v.Add(fe)
	reason, fe := validate.Text("/motivo", in.Reason, 0, 500)
	v.Add(fe)
	if err := v.Err(); err != nil {
		return BootstrapResult{}, err
	}
	now := s.clock.Now()
	var res BootstrapResult
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		id, err := estabelecimento.Create(ctx, tx, name, in.Slug, in.Timezone, now)
		if err != nil {
			return err
		}
		inv, token, err := insertInvite(ctx, tx, id, estabelecimento.RoleOwner, nil, email, nil, now)
		if err != nil {
			return err
		}
		if err := s.mailInvite(ctx, tx, email, name, estabelecimento.RoleOwner, token, now); err != nil {
			return err
		}
		for _, e := range []auditoria.Event{
			{EstablishmentID: &id, OccurredAt: now, ActorKind: auditoria.ActorOperator, Action: "T-OP-01",
				TargetType: "establishment", TargetID: id, ToState: "active", Reason: reason},
			{EstablishmentID: &id, OccurredAt: now, ActorKind: auditoria.ActorOperator, Action: "T-IN-01",
				TargetType: "invite", TargetID: inv.ID, ToState: "pending", Reason: "convite de dono inicial"},
		} {
			if err := auditoria.Record(ctx, tx, e); err != nil {
				return err
			}
		}
		res = BootstrapResult{EstablishmentID: id, InviteExpiresAt: inv.ExpiresAt}
		return nil
	})
	return res, err
}
