package identidade

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/midia"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Aparência preferida (claro, escuro ou do sistema). É da pessoa; a cor principal é da loja.
var appearances = map[string]bool{"system": true, "light": true, "dark": true}

type googleLinkView struct {
	Linked bool    `json:"linked"`
	Email  *string `json:"email"`
}

// profileView é o perfil da própria conta (esquema MyProfile). E-mail é só leitura: não há fluxo
// seguro de troca de e-mail, e ele nunca muda por PATCH.
type profileView struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	Email         string          `json:"email"`
	Status        string          `json:"status"`
	EmailVerified bool            `json:"email_verified"`
	Appearance    string          `json:"appearance"`
	Avatar        *midia.ImageRef `json:"avatar"`
	HasPassword   bool            `json:"has_password"`
	Google        googleLinkView  `json:"google"`
	Version       int64           `json:"version"`
}

// loadProfile lê o perfil (lock = trava a linha da conta para uma alteração condicional).
func (s *Service) loadProfile(ctx context.Context, q database.Querier, accountID uuid.UUID, lock bool) (profileView, *uuid.UUID, error) {
	sql := `SELECT id, name, email, status, email_verified_at IS NOT NULL, appearance, avatar_asset_id,
		password_hash IS NOT NULL, profile_version FROM account WHERE id = $1 AND status = 'active'`
	if lock {
		sql += ` FOR UPDATE`
	}
	var v profileView
	var avatarID *uuid.UUID
	err := q.QueryRow(ctx, sql, accountID).Scan(&v.ID, &v.Name, &v.Email, &v.Status, &v.EmailVerified, &v.Appearance,
		&avatarID, &v.HasPassword, &v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil, httpserver.ErrUnauthenticated()
	}
	if err != nil {
		return v, nil, fmt.Errorf("ler perfil: %w", err)
	}
	var gEmail *string
	err = q.QueryRow(ctx, `SELECT email FROM external_identity WHERE account_id = $1 AND provider = 'google'`, accountID).Scan(&gEmail)
	switch {
	case err == nil:
		v.Google = googleLinkView{Linked: true, Email: gEmail}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return v, nil, err
	}
	a, err := midia.AttachedOne(ctx, q, avatarID, "private")
	if err != nil {
		return v, nil, err
	}
	if a != nil {
		id := a.ID
		v.Avatar = a.Ref(func(size int) string { return fmt.Sprintf("/v1/me/avatar/%d?v=%s", size, id) })
	}
	return v, avatarID, nil
}

func profileConflict(cur profileView) error {
	return &httpserver.Error{Status: 409, Code: "version_conflict", Title: "Perfil alterado em outro lugar",
		Detail: "Seu perfil foi alterado em outra aba ou aparelho. Revise os dados atuais antes de salvar de novo.", Current: cur}
}

// getMyProfile: a própria conta (cliente, profissional ou dono), sem depender de loja selecionada.
func (s *Service) getMyProfile(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	v, _, err := s.loadProfile(r.Context(), s.pool, sess.AccountID, false)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[profileView]{Data: v})
	return nil
}

// updateMyProfile: nome exibido e aparência. Só esses campos existem no pedido: e-mail, papel,
// vínculo ou verificação enviados são recusados (campo desconhecido). O nome guardado em visitas e
// reservas já feitas não muda. Com expected_version, a alteração é condicional (sem sobrescrever
// outra aba em silêncio). Não é transição da matriz nem ação de acesso: sem auditoria.
func (s *Service) updateMyProfile(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	var body struct {
		Name            *string `json:"name"`
		Appearance      *string `json:"appearance"`
		ExpectedVersion *int64  `json:"expected_version"`
	}
	raw, err := httpserver.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/name", body.Name == nil && body.Appearance == nil)
	var name string
	if body.Name != nil {
		var fe *httpserver.FieldError
		name, fe = validate.Text("/name", *body.Name, 1, 80)
		v.Add(fe)
	}
	if body.Appearance != nil && !appearances[*body.Appearance] {
		v.Add(&httpserver.FieldError{Field: "/appearance", Code: httpserver.FieldNotAllowed, Message: "use system, light ou dark"})
	}
	if body.ExpectedVersion != nil && *body.ExpectedVersion < 1 {
		v.Add(&httpserver.FieldError{Field: "/expected_version", Code: httpserver.FieldInvalidFormat})
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + sess.AccountID.String(), Operation: "updateMyProfile", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, _, err := s.loadProfile(ctx, tx, sess.AccountID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if body.ExpectedVersion != nil && *body.ExpectedVersion != cur.Version {
			return idempotency.Result{}, profileConflict(cur)
		}
		if body.Name == nil {
			name = cur.Name
		}
		appearance := cur.Appearance
		if body.Appearance != nil {
			appearance = *body.Appearance
		}
		if _, err := tx.Exec(ctx, `UPDATE account SET name = $2, appearance = $3, profile_version = profile_version + 1, updated_at = $4
			WHERE id = $1`, sess.AccountID, name, appearance, s.clock.Now()); err != nil {
			return idempotency.Result{}, err
		}
		out, _, err := s.loadProfile(ctx, tx, sess.AccountID, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[profileView]{Data: out}}, nil
	})
}

// setMyAvatar associa uma foto JÁ PROCESSADA (envio próprio, finalidade avatar). A foto antiga
// continua até esta troca confirmar; uma troca mais antiga que termina depois não sobrescreve a
// escolha mais nova (versão esperada obrigatória).
func (s *Service) setMyAvatar(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	var body struct {
		AssetID         *uuid.UUID `json:"asset_id"`
		ExpectedVersion *int64     `json:"expected_version"`
	}
	raw, err := httpserver.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/asset_id", body.AssetID == nil)
	expected := v.Version("/expected_version", body.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + sess.AccountID.String(), Operation: "setMyAvatar", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, oldID, err := s.loadProfile(ctx, tx, sess.AccountID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, profileConflict(cur)
		}
		now := s.clock.Now()
		if err := midia.Claim(ctx, tx, *body.AssetID, sess.AccountID, midia.PurposeAvatar, nil, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := midia.Release(ctx, tx, oldID, now); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE account SET avatar_asset_id = $2, profile_version = profile_version + 1, updated_at = $3
			WHERE id = $1`, sess.AccountID, *body.AssetID, now); err != nil {
			return idempotency.Result{}, err
		}
		out, _, err := s.loadProfile(ctx, tx, sess.AccountID, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[profileView]{Data: out}}, nil
	})
}

// removeMyAvatar retira a referência na hora (novos acessos negados); os arquivos somem depois
// do prazo de retenção. Cópias já baixadas por quem viu a foto não podem ser revogadas.
func (s *Service) removeMyAvatar(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	var body struct {
		ExpectedVersion *int64 `json:"expected_version"`
	}
	raw, err := httpserver.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", body.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + sess.AccountID.String(), Operation: "removeMyAvatar", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, oldID, err := s.loadProfile(ctx, tx, sess.AccountID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, profileConflict(cur)
		}
		now := s.clock.Now()
		if err := midia.Release(ctx, tx, oldID, now); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE account SET avatar_asset_id = NULL, profile_version = profile_version + 1, updated_at = $2
			WHERE id = $1`, sess.AccountID, now); err != nil {
			return idempotency.Result{}, err
		}
		out, _, err := s.loadProfile(ctx, tx, sess.AccountID, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[profileView]{Data: out}}, nil
	})
}

// getMyAvatar: só a própria conta (a autorização é refeita a cada pedido, inclusive antes do 304).
func (s *Service) getMyAvatar(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	id, err := AvatarAssetOf(r.Context(), s.pool, sess.AccountID)
	if err != nil {
		return err
	}
	if id == nil || s.media == nil {
		return httpserver.ErrNotFound()
	}
	return s.media.ServePrivate(w, r, *id)
}

// AvatarAssetOf devolve a foto pessoal associada de uma conta ativa (nil se não houver). Usado
// pelo módulo fila, que decide quem da equipe pode ver (relação de atendimento ativa).
func AvatarAssetOf(ctx context.Context, q database.Querier, accountID uuid.UUID) (*uuid.UUID, error) {
	var id *uuid.UUID
	err := q.QueryRow(ctx, `SELECT avatar_asset_id FROM account WHERE id = $1 AND status = 'active'`, accountID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return id, err
}

// AvatarAssetsOf é AvatarAssetOf para várias contas (lista da fila da equipe).
func AvatarAssetsOf(ctx context.Context, q database.Querier, accountIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, avatar_asset_id FROM account
		WHERE id = ANY($1) AND status = 'active' AND avatar_asset_id IS NOT NULL`, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var acc, asset uuid.UUID
		if err := rows.Scan(&acc, &asset); err != nil {
			return nil, err
		}
		out[acc] = asset
	}
	return out, rows.Err()
}

// AccountOf devolve a conta da sessão do pedido (401 sem sessão). Usado pelo módulo midia.
func (s *Service) AccountOf(r *http.Request) (uuid.UUID, error) {
	sess, err := requireAccount(r)
	if err != nil {
		return uuid.Nil, err
	}
	return sess.AccountID, nil
}
