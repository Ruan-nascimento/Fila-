package catalogo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/midia"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Produtos simples (ADR-0032): pomada, shampoo e afins, vendidos como complemento de uma visita.
// Sem estoque, lote, validade, compras ou venda avulsa: a disponibilidade é a situação
// ativa/inativa definida pelo dono. Produto não entra na duração da fila nem gera pontos.
const (
	MaxProducts          = 500
	MaxProductName       = 60
	MaxProductDescLength = 200
)

// errImageNotAttachable: a imagem enviada não pode virar foto do produto.
var errImageNotAttachable = hs.ErrValidation(hs.FieldError{Field: "/image_asset_id", Code: hs.FieldNotAllowed,
	Message: "envie a foto do produto novamente (finalidade product_photo, desta loja)"})

type productView struct {
	ID           uuid.UUID       `json:"id"`
	Name         string          `json:"name"`
	Description  *string         `json:"description"`
	CategoryID   *uuid.UUID      `json:"category_id"`
	Price        money           `json:"price"`
	Image        *midia.ImageRef `json:"image"`
	DisplayOrder int             `json:"display_order"`
	Status       string          `json:"status"`
	Version      int64           `json:"version"`
	imageID      *uuid.UUID
}

// publicProductView: sem situação, versão nem dados internos.
type publicProductView struct {
	ID           uuid.UUID       `json:"id"`
	Name         string          `json:"name"`
	Description  *string         `json:"description"`
	CategoryID   *uuid.UUID      `json:"category_id"`
	Price        money           `json:"price"`
	Image        *midia.ImageRef `json:"image"`
	DisplayOrder int             `json:"display_order"`
}

const productSelect = `
	SELECT p.id, p.name, p.description, p.category_id, p.price_cents, p.image_asset_id, p.display_order, p.status, p.version
	FROM product p`

// Ordem estável: posição, nome sem diferenciar maiúsculas, id.
const productOrder = ` ORDER BY p.display_order, lower(p.name), p.id`

func scanProduct(row pgx.Row) (productView, error) {
	var v productView
	var cents int64
	err := row.Scan(&v.ID, &v.Name, &v.Description, &v.CategoryID, &cents, &v.imageID, &v.DisplayOrder, &v.Status, &v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, hs.ErrNotFound()
	}
	v.Price = brl(cents)
	return v, err
}

func loadProduct(ctx context.Context, q database.Querier, estID, id uuid.UUID, lock bool) (productView, error) {
	sql := productSelect + ` WHERE p.establishment_id = $1 AND p.id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	v, err := scanProduct(q.QueryRow(ctx, sql, estID, id))
	if err != nil {
		return v, err
	}
	return v, fillImages(ctx, q, []*productView{&v})
}

// fillImages resolve as fotos publicadas (uma consulta para a lista toda, sem N+1).
func fillImages(ctx context.Context, q database.Querier, list []*productView) error {
	var ids []uuid.UUID
	for _, p := range list {
		if p.imageID != nil {
			ids = append(ids, *p.imageID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	assets, err := midia.Attached(ctx, q, ids, "public")
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.imageID == nil {
			continue
		}
		if a, ok := assets[*p.imageID]; ok {
			p.Image = a.PublicRef()
		}
	}
	return nil
}

func collectProducts(ctx context.Context, q database.Querier, rows pgx.Rows) ([]productView, error) {
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (productView, error) { return scanProduct(row) })
	if err != nil {
		return nil, err
	}
	ptrs := make([]*productView, len(list))
	for i := range list {
		ptrs[i] = &list[i]
	}
	if err := fillImages(ctx, q, ptrs); err != nil {
		return nil, err
	}
	if list == nil {
		list = []productView{}
	}
	return list, nil
}

// listProducts: equipe da loja (dono e profissionais), ativos e inativos. A lista inteira cabe
// no limite de 500 por loja; a busca e os filtros da gestão são locais (ADR-0036).
func (s *Service) listProducts(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Member(r, estID); err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.Pool.Query(ctx, productSelect+` WHERE p.establishment_id = $1`+productOrder, estID)
	if err != nil {
		return err
	}
	list, err := collectProducts(ctx, s.Pool, rows)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]productView]{Data: list})
	return nil
}

// listPublicProducts: público; só ativos e só quando a loja oferece produtos (configuração).
func (s *Service) listPublicProducts(w http.ResponseWriter, r *http.Request) error {
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
	out := []publicProductView{}
	settings, err := estabelecimento.LoadSettings(ctx, s.Pool, estID)
	if err != nil {
		return err
	}
	if settings.ProductsEnabled {
		rows, err := s.Pool.Query(ctx, productSelect+` WHERE p.establishment_id = $1 AND p.status = 'active'`+productOrder, estID)
		if err != nil {
			return err
		}
		list, err := collectProducts(ctx, s.Pool, rows)
		if err != nil {
			return err
		}
		for _, p := range list {
			out = append(out, publicProductView{ID: p.ID, Name: p.Name, Description: p.Description, CategoryID: p.CategoryID,
				Price: p.Price, Image: p.Image, DisplayOrder: p.DisplayOrder})
		}
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]publicProductView]{Data: out})
	return nil
}

// optionalUUID distingue "campo ausente" de "null" (limpar) num PATCH.
type optionalUUID struct {
	Set   bool
	Value *uuid.UUID
}

func (o *optionalUUID) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var id uuid.UUID
	if err := json.Unmarshal(b, &id); err != nil {
		return err
	}
	o.Value = &id
	return nil
}

// optionalText distingue "campo ausente" de "null" (limpar) num PATCH.
type optionalText struct {
	Set   bool
	Value *string
}

func (o *optionalText) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.Value = &s
	return nil
}

type productRequest struct {
	ExpectedVersion *int64       `json:"expected_version"`
	Name            *string      `json:"name"`
	Description     optionalText `json:"description"`
	CategoryID      optionalUUID `json:"category_id"`
	PriceCents      *int64       `json:"price_cents"`
	DisplayOrder    *int64       `json:"display_order"`
	ImageAssetID    optionalUUID `json:"image_asset_id"`
}

type productInput struct {
	name, desc *string
	expected   int64
}

func (req productRequest) validate(create bool) (productInput, error) {
	var v validate.Collector
	var in productInput
	if create {
		v.Required("/name", req.Name == nil)
		v.Required("/price_cents", req.PriceCents == nil)
		if req.ExpectedVersion != nil {
			v.Add(&hs.FieldError{Field: "/expected_version", Code: hs.FieldNotAllowed})
		}
	} else {
		in.expected = v.Version("/expected_version", req.ExpectedVersion)
	}
	if req.Name != nil {
		n, fe := validate.Text("/name", *req.Name, 1, MaxProductName)
		v.Add(fe)
		in.name = &n
	}
	if req.Description.Set && req.Description.Value != nil {
		d, fe := validate.Text("/description", *req.Description.Value, 0, MaxProductDescLength)
		v.Add(fe)
		if d != "" {
			in.desc = &d
		}
	}
	if req.PriceCents != nil {
		v.IntRange("/price_cents", req.PriceCents, 1, MaxPriceCents)
	}
	if req.DisplayOrder != nil {
		v.IntRange("/display_order", req.DisplayOrder, 0, MaxDisplayOrder)
	}
	return in, v.Err()
}

// createProduct (T-PD-01): dono.
func (s *Service) createProduct(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	var req productRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	in, err := req.validate(true)
	if err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "createProduct", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM product WHERE establishment_id = $1`, estID).Scan(&count); err != nil {
			return idempotency.Result{}, err
		}
		if count >= MaxProducts {
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "product_limit_reached", Title: "Limite de produtos",
				Detail: "A loja já tem 500 produtos cadastrados. Reaproveite um produto inativo."}
		}
		if err := checkCategory(ctx, tx, estID, req.CategoryID.Value, KindProduct); err != nil {
			return idempotency.Result{}, err
		}
		now := s.Clock.Now()
		order := int64(0)
		if req.DisplayOrder != nil {
			order = *req.DisplayOrder
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO product (establishment_id, name, description, category_id, price_cents, display_order, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7) RETURNING id`,
			estID, *in.name, in.desc, req.CategoryID.Value, *req.PriceCents, order, now).Scan(&id); err != nil {
			return idempotency.Result{}, err
		}
		if req.ImageAssetID.Value != nil {
			if err := s.attachImage(ctx, tx, actor, estID, id, nil, req.ImageAssetID.Value, now); err != nil {
				return idempotency.Result{}, err
			}
		}
		return s.finishProduct(ctx, tx, actor, estID, id, "T-PD-01", "", ServiceActive, now, http.StatusCreated)
	})
}

// updateProduct (T-PD-02): nova versão; produtos já incluídos em comandas guardam o que foi aceito.
func (s *Service) updateProduct(w http.ResponseWriter, r *http.Request) error {
	estID, productID, actor, err := s.ownerAndPath(r, "productId")
	if err != nil {
		return err
	}
	var req productRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	in, err := req.validate(false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "updateProduct", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadProduct(ctx, tx, estID, productID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != in.expected {
			return idempotency.Result{}, productConflict(cur)
		}
		next := cur
		if in.name != nil {
			next.Name = *in.name
		}
		if req.Description.Set {
			next.Description = in.desc
		}
		if req.CategoryID.Set {
			if err := checkCategory(ctx, tx, estID, req.CategoryID.Value, KindProduct); err != nil {
				return idempotency.Result{}, err
			}
			next.CategoryID = req.CategoryID.Value
		}
		if req.PriceCents != nil {
			next.Price = brl(*req.PriceCents)
		}
		if req.DisplayOrder != nil {
			next.DisplayOrder = int(*req.DisplayOrder)
		}
		now := s.Clock.Now()
		if req.ImageAssetID.Set {
			if err := s.attachImage(ctx, tx, actor, estID, productID, cur.imageID, req.ImageAssetID.Value, now); err != nil {
				return idempotency.Result{}, err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE product SET name = $2, description = $3, category_id = $4, price_cents = $5, display_order = $6,
			       version = version + 1, updated_at = $7 WHERE id = $1`,
			productID, next.Name, next.Description, next.CategoryID, next.Price.AmountCents, next.DisplayOrder, now); err != nil {
			return idempotency.Result{}, err
		}
		return s.finishProduct(ctx, tx, actor, estID, productID, "T-PD-02", cur.Status, cur.Status, now, http.StatusOK)
	})
}

// attachImage troca a foto do produto: a nova precisa ser do dono, da finalidade product_photo e
// desta loja; a antiga é liberada (deixa de ser servida). nil remove a foto.
func (s *Service) attachImage(ctx context.Context, tx pgx.Tx, actor acesso.Actor, estID, productID uuid.UUID, old, next *uuid.UUID, now time.Time) error {
	if old != nil && next != nil && *old == *next {
		return nil
	}
	if next != nil {
		if err := midia.Claim(ctx, tx, *next, actor.AccountID, midia.PurposeProductPhoto, &estID, now); err != nil {
			if errors.Is(err, midia.ErrNotAttachable) {
				return errImageNotAttachable
			}
			return err
		}
	}
	if err := midia.Release(ctx, tx, old, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE product SET image_asset_id = $2 WHERE id = $1`, productID, next)
	return err
}

func productConflict(cur productView) error {
	return &hs.Error{Status: 409, Code: "version_conflict", Title: "Os dados mudaram",
		Detail: "Outra pessoa alterou este produto. Confira os dados atualizados e tente de novo.", Current: cur}
}

func (s *Service) deactivateProduct(w http.ResponseWriter, r *http.Request) error {
	return s.productStatus(w, r, ServiceStopped)
}

func (s *Service) reactivateProduct(w http.ResponseWriter, r *http.Request) error {
	return s.productStatus(w, r, ServiceActive)
}

// productStatus (T-PD-03/04): inativo não aceita nova inclusão; comandas que já têm o produto
// continuam com ele (resolvidas pela equipe).
func (s *Service) productStatus(w http.ResponseWriter, r *http.Request, to string) error {
	estID, productID, actor, err := s.ownerAndPath(r, "productId")
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
	op, action := "deactivateProduct", "T-PD-03"
	if to == ServiceActive {
		op, action = "reactivateProduct", "T-PD-04"
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: op, Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadProduct(ctx, tx, estID, productID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, productConflict(cur)
		}
		if cur.Status == to {
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "invalid_transition", Title: "Ação não permitida agora",
				Detail: "O produto já está nessa situação.", Current: cur}
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE product SET status = $2, version = version + 1, updated_at = $3 WHERE id = $1`,
			productID, to, now); err != nil {
			return idempotency.Result{}, err
		}
		return s.finishProduct(ctx, tx, actor, estID, productID, action, cur.Status, to, now, http.StatusOK)
	})
}

// finishProduct grava a revisão e a auditoria e devolve a visão atual.
func (s *Service) finishProduct(ctx context.Context, tx pgx.Tx, actor acesso.Actor, estID, productID uuid.UUID,
	action, from, to string, now time.Time, status int) (idempotency.Result, error) {
	v, err := loadProduct(ctx, tx, estID, productID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO product_revision (product_id, version, name, description, category_id, price_cents, status, changed_at, changed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		v.ID, v.Version, v.Name, v.Description, v.CategoryID, v.Price.AmountCents, v.Status, now, actor.AccountID); err != nil {
		return idempotency.Result{}, err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
		ActorID: auditoria.Ptr(actor.AccountID), Action: action, TargetType: "product", TargetID: productID,
		FromState: from, ToState: to, RequestID: hs.RequestIDFrom(ctx)}); err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: status, Body: hs.Data[productView]{Data: v}}, nil
}

// ProductsForEntry lê os produtos DESTA loja com bloqueio compartilhado (como ServicesForEntry):
// o snapshot gravado na comanda é sempre uma versão real do catálogo.
func ProductsForEntry(ctx context.Context, q database.Querier, estID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Snapshot, error) {
	return products(ctx, q, estID, ids, " FOR SHARE")
}

// ProductsForRead é ProductsForEntry sem trava (leituras públicas).
func ProductsForRead(ctx context.Context, q database.Querier, estID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Snapshot, error) {
	return products(ctx, q, estID, ids, "")
}

func products(ctx context.Context, q database.Querier, estID uuid.UUID, ids []uuid.UUID, lock string) (map[uuid.UUID]Snapshot, error) {
	out := map[uuid.UUID]Snapshot{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id, name, price_cents, status = 'active' FROM product
		WHERE establishment_id = $1 AND id = ANY($2) ORDER BY id`+lock, estID, ids)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Snapshot, error) {
		s := Snapshot{Kind: KindProduct}
		err := r.Scan(&s.ID, &s.Name, &s.PriceCents, &s.Active)
		return s, err
	})
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		out[s.ID] = s
	}
	return out, nil
}
