package catalogo

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Categorias do catálogo (ADR-0032): pertencem à loja e a um tipo (serviço ou produto). Servem só
// para organizar: item sem categoria continua acessível; desativar a categoria não apaga nem
// esconde os itens (eles aparecem como "sem categoria" para o cliente).
const (
	KindService = "service"
	KindProduct = "product"

	MaxCategoryName   = 40
	MaxDisplayOrder   = 9999
	maxCategoriesKind = 50
)

type categoryView struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	DisplayOrder int       `json:"display_order"`
	Status       string    `json:"status"`
	Version      int64     `json:"version"`
}

type publicCategoryView struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	DisplayOrder int       `json:"display_order"`
}

const categorySelect = `SELECT id, kind, name, display_order, status, version FROM catalog_category`

// Ordem estável: posição, nome, id (desempate determinístico).
const categoryOrder = ` ORDER BY kind, display_order, lower(name), id`

func scanCategory(row pgx.Row) (categoryView, error) {
	var c categoryView
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.DisplayOrder, &c.Status, &c.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, hs.ErrNotFound()
	}
	return c, err
}

func loadCategory(ctx context.Context, q database.Querier, estID, id uuid.UUID, lock bool) (categoryView, error) {
	sql := categorySelect + ` WHERE establishment_id = $1 AND id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanCategory(q.QueryRow(ctx, sql, estID, id))
}

// checkCategory confere que a categoria existe NESTA loja e é do tipo certo (id estranho →
// erro de validação, sem revelar outra loja). Categoria inativa também é aceita: o dono pode
// organizar antes de reativar.
func checkCategory(ctx context.Context, q database.Querier, estID uuid.UUID, id *uuid.UUID, kind string) error {
	if id == nil {
		return nil
	}
	c, err := loadCategory(ctx, q, estID, *id, false)
	if err != nil || c.Kind != kind {
		return hs.ErrValidation(hs.FieldError{Field: "/category_id", Code: hs.FieldNotAllowed, Message: "categoria não encontrada"})
	}
	return nil
}

// listCategories: equipe da loja, ativas e inativas.
func (s *Service) listCategories(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Member(r, estID); err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), categorySelect+` WHERE establishment_id = $1`+categoryOrder, estID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (categoryView, error) { return scanCategory(row) })
	if err != nil {
		return err
	}
	if list == nil {
		list = []categoryView{}
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]categoryView]{Data: list})
	return nil
}

// listPublicCategories: público; só categorias ativas.
func (s *Service) listPublicCategories(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := estabelecimento.ActiveByID(ctx, s.Pool, estID); errors.Is(err, estabelecimento.ErrNotFound) {
		return hs.ErrNotFound()
	} else if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, kind, name, display_order FROM catalog_category
		WHERE establishment_id = $1 AND status = 'active'`+categoryOrder, estID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (publicCategoryView, error) {
		var c publicCategoryView
		return c, row.Scan(&c.ID, &c.Kind, &c.Name, &c.DisplayOrder)
	})
	if err != nil {
		return err
	}
	if list == nil {
		list = []publicCategoryView{}
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]publicCategoryView]{Data: list})
	return nil
}

type categoryRequest struct {
	ExpectedVersion *int64  `json:"expected_version"`
	Kind            *string `json:"kind"`
	Name            *string `json:"name"`
	DisplayOrder    *int64  `json:"display_order"`
}

// createCategory (T-CT-01): dono.
func (s *Service) createCategory(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	var req categoryRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/kind", req.Kind == nil)
	if req.Kind != nil && *req.Kind != KindService && *req.Kind != KindProduct {
		v.Add(&hs.FieldError{Field: "/kind", Code: hs.FieldNotAllowed, Message: "use service ou product"})
	}
	if req.ExpectedVersion != nil {
		v.Add(&hs.FieldError{Field: "/expected_version", Code: hs.FieldNotAllowed})
	}
	v.Required("/name", req.Name == nil)
	var name string
	if req.Name != nil {
		n, fe := validate.Text("/name", *req.Name, 1, MaxCategoryName)
		v.Add(fe)
		name = n
	}
	order := int64(0)
	if req.DisplayOrder != nil {
		v.IntRange("/display_order", req.DisplayOrder, 0, MaxDisplayOrder)
		order = *req.DisplayOrder
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "createCategory", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM catalog_category WHERE establishment_id = $1 AND kind = $2`,
			estID, *req.Kind).Scan(&count); err != nil {
			return idempotency.Result{}, err
		}
		if count >= maxCategoriesKind {
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "category_limit_reached", Title: "Limite de categorias",
				Detail: "A loja já tem 50 categorias deste tipo. Reaproveite ou renomeie uma existente."}
		}
		now := s.Clock.Now()
		var id uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO catalog_category (establishment_id, kind, name, display_order, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5) RETURNING id`, estID, *req.Kind, name, order, now).Scan(&id)
		if database.IsUniqueViolation(err, "catalog_category_name_uq") {
			return idempotency.Result{}, duplicateCategory()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		return s.finishCategory(ctx, tx, actor, estID, id, "T-CT-01", "", ServiceActive, now, http.StatusCreated)
	})
}

func duplicateCategory() error {
	return hs.ErrValidation(hs.FieldError{Field: "/name", Code: "duplicate", Message: "já existe uma categoria com esse nome"})
}

// updateCategory (T-CT-02): nome e posição; os itens não mudam.
func (s *Service) updateCategory(w http.ResponseWriter, r *http.Request) error {
	estID, catID, actor, err := s.ownerAndPath(r, "categoryId")
	if err != nil {
		return err
	}
	var req categoryRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	if req.Kind != nil {
		v.Add(&hs.FieldError{Field: "/kind", Code: hs.FieldNotAllowed, Message: "o tipo da categoria não muda"})
	}
	var name string
	if req.Name != nil {
		n, fe := validate.Text("/name", *req.Name, 1, MaxCategoryName)
		v.Add(fe)
		name = n
	}
	if req.DisplayOrder != nil {
		v.IntRange("/display_order", req.DisplayOrder, 0, MaxDisplayOrder)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "updateCategory", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadCategory(ctx, tx, estID, catID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, categoryConflict(cur)
		}
		next := cur
		if req.Name != nil {
			next.Name = name
		}
		if req.DisplayOrder != nil {
			next.DisplayOrder = int(*req.DisplayOrder)
		}
		now := s.Clock.Now()
		_, err = tx.Exec(ctx, `UPDATE catalog_category SET name = $2, display_order = $3, version = version + 1, updated_at = $4
			WHERE id = $1`, catID, next.Name, next.DisplayOrder, now)
		if database.IsUniqueViolation(err, "catalog_category_name_uq") {
			return idempotency.Result{}, duplicateCategory()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		return s.finishCategory(ctx, tx, actor, estID, catID, "T-CT-02", cur.Status, cur.Status, now, http.StatusOK)
	})
}

func categoryConflict(cur categoryView) error {
	return &hs.Error{Status: 409, Code: "version_conflict", Title: "Os dados mudaram",
		Detail: "Outra pessoa alterou esta categoria. Confira os dados atualizados e tente de novo.", Current: cur}
}

func (s *Service) deactivateCategory(w http.ResponseWriter, r *http.Request) error {
	return s.categoryStatus(w, r, ServiceStopped)
}

func (s *Service) reactivateCategory(w http.ResponseWriter, r *http.Request) error {
	return s.categoryStatus(w, r, ServiceActive)
}

// categoryStatus (T-CT-03/04): desativar só tira a categoria dos filtros públicos.
func (s *Service) categoryStatus(w http.ResponseWriter, r *http.Request, to string) error {
	estID, catID, actor, err := s.ownerAndPath(r, "categoryId")
	if err != nil {
		return err
	}
	var req versionRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	op, action := "deactivateCategory", "T-CT-03"
	if to == ServiceActive {
		op, action = "reactivateCategory", "T-CT-04"
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: op, Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadCategory(ctx, tx, estID, catID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, categoryConflict(cur)
		}
		if cur.Status == to {
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "invalid_transition", Title: "Ação não permitida agora",
				Detail: "A categoria já está nessa situação.", Current: cur}
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE catalog_category SET status = $2, version = version + 1, updated_at = $3 WHERE id = $1`,
			catID, to, now); err != nil {
			return idempotency.Result{}, err
		}
		return s.finishCategory(ctx, tx, actor, estID, catID, action, cur.Status, to, now, http.StatusOK)
	})
}

func (s *Service) finishCategory(ctx context.Context, tx pgx.Tx, actor acesso.Actor, estID, catID uuid.UUID,
	action, from, to string, now time.Time, status int) (idempotency.Result, error) {
	c, err := loadCategory(ctx, tx, estID, catID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
		ActorID: auditoria.Ptr(actor.AccountID), Action: action, TargetType: "catalog_category", TargetID: catID,
		FromState: from, ToState: to, RequestID: hs.RequestIDFrom(ctx)}); err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: status, Body: hs.Data[categoryView]{Data: c}}, nil
}

// ownerAndPath confere o dono da loja e lê o id do recurso no caminho.
func (s *Service) ownerAndPath(r *http.Request, param string) (uuid.UUID, uuid.UUID, acesso.Actor, error) {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return uuid.Nil, uuid.Nil, acesso.Actor{}, err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return uuid.Nil, uuid.Nil, acesso.Actor{}, err
	}
	id, err := pathID(r, param)
	return estID, id, actor, err
}
