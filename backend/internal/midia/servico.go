package midia

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/ratelimit"
)

// Estados do registro (media_asset.state).
const (
	StateProcessing = "processing"
	StateReady      = "ready"
	StateFailed     = "failed"
	StateAttached   = "attached"
	StateReleased   = "released"
	StatePurged     = "purged"
)

// Prazos de limpeza (ADR-0028): envio não associado e imagem removida ficam 24 h e somem.
const (
	OrphanAfter    = 24 * time.Hour
	RetainReleased = 24 * time.Hour
	// maxPendingPerAccount limita o staging por pessoa (envios ainda não associados).
	maxPendingPerAccount = 10
	maxAttempts          = 3
	leaseFor             = 2 * time.Minute
	processTimeout       = 30 * time.Second
)

// Deps são as dependências do módulo.
type Deps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	// Store nil = envio de imagens desligado (FILA_MEDIA_STORAGE=disabled): as telas usam iniciais.
	Store Storage
	Auth  acesso.Authorizer
	// AccountOf devolve a conta da sessão do pedido (401 sem sessão). Fornecida pelo módulo identidade.
	AccountOf func(r *http.Request) (uuid.UUID, error)
	Limiter   *ratelimit.Limiter
	PerMinute int
	PerDay    int
}

// Service implementa envio, consulta e entrega de imagens.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço.
func New(d Deps) *Service {
	if d.PerMinute == 0 {
		d.PerMinute = 5
	}
	if d.PerDay == 0 {
		d.PerDay = 20
	}
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

// Enabled informa se o envio de imagens está ligado.
func (s *Service) Enabled() bool { return s != nil && s.Store != nil }

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []httpserver.Route {
	self := []httpserver.Grant{{Actor: "customer", Scope: "self"}, {Actor: "professional", Scope: "self"}, {Actor: "owner", Scope: "self"}}
	upload := append(append([]httpserver.Grant{}, self...),
		httpserver.Grant{Actor: "professional", Scope: "establishment_member"}, httpserver.GrantOwner)
	return []httpserver.Route{
		{Operation: "uploadMedia", Method: http.MethodPost, Path: "/v1/media/uploads", Grants: upload, Handler: s.upload},
		{Operation: "getMediaUpload", Method: http.MethodGet, Path: "/v1/media/uploads/{assetId}", Grants: self, Handler: s.getUpload},
		{Operation: "getPublicMedia", Method: http.MethodGet, Path: "/v1/public/media/{assetId}/{size}",
			Grants: []httpserver.Grant{httpserver.GrantPublic}, Handler: s.getPublic},
	}
}

// ---------------------------------------------------------------------------------------------
// Visões

// ImageVariant é uma variante pronta (esquema ImageVariant).
type ImageVariant struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	URL    string `json:"url"`
}

// ImageRef aponta para as variantes de uma imagem (esquema ImageRef). Nunca leva bytes nem chaves
// de armazenamento.
type ImageRef struct {
	Variants []ImageVariant `json:"variants"`
}

type variantRow struct {
	Size   int    `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int    `json:"bytes"`
	Key    string `json:"key"`
	Mime   string `json:"mime"`
}

// Asset é uma imagem associada e pronta para exibir.
type Asset struct {
	ID       uuid.UUID
	variants []variantRow
}

// Ref monta a referência com a URL de cada tamanho (privada ou pública, conforme quem chama).
// A URL leva o id da imagem: trocar a imagem muda a URL (cache versionado).
func (a Asset) Ref(url func(size int) string) *ImageRef {
	ref := &ImageRef{Variants: make([]ImageVariant, 0, len(a.variants))}
	for _, v := range a.variants {
		ref.Variants = append(ref.Variants, ImageVariant{Width: v.Width, Height: v.Height, URL: url(v.Size)})
	}
	return ref
}

// PublicRef é a referência pública (só para imagens públicas: logo e foto profissional).
func (a Asset) PublicRef() *ImageRef {
	return a.Ref(func(size int) string { return fmt.Sprintf("/v1/public/media/%s/%d", a.ID, size) })
}

// Attached devolve as imagens associadas (state attached) entre os ids pedidos. Ids nulos,
// liberados ou de outra visibilidade simplesmente não aparecem (a tela usa iniciais/nome).
func Attached(ctx context.Context, q database.Querier, ids []uuid.UUID, visibility string) (map[uuid.UUID]Asset, error) {
	out := map[uuid.UUID]Asset{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, variants FROM media_asset
		WHERE id = ANY($1) AND state = 'attached' AND visibility = $2`, ids, visibility)
	if err != nil {
		return nil, fmt.Errorf("ler imagens: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a Asset
		var raw []byte
		if err := rows.Scan(&a.ID, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &a.variants); err != nil {
			return nil, err
		}
		out[a.ID] = a
	}
	return out, rows.Err()
}

// AttachedOne é Attached para um id opcional.
func AttachedOne(ctx context.Context, q database.Querier, id *uuid.UUID, visibility string) (*Asset, error) {
	if id == nil {
		return nil, nil
	}
	m, err := Attached(ctx, q, []uuid.UUID{*id}, visibility)
	if err != nil {
		return nil, err
	}
	if a, ok := m[*id]; ok {
		return &a, nil
	}
	return nil, nil
}

type uploadView struct {
	ID            uuid.UUID `json:"id"`
	Purpose       string    `json:"purpose"`
	Status        string    `json:"status"`
	FailureReason *string   `json:"failure_reason"`
}

// ---------------------------------------------------------------------------------------------
// Erros

func errDisabled() *httpserver.Error {
	return &httpserver.Error{Status: 503, Code: "media_disabled", Title: "Fotos indisponíveis",
		Detail: "O envio de fotos ainda não está disponível. Seu perfil continua funcionando com as iniciais."}
}

// ErrNotAttachable: a imagem não é desta pessoa, não é desta finalidade/loja ou ainda não está pronta.
var ErrNotAttachable = &httpserver.Error{Status: 409, Code: "media_not_attachable", Title: "Foto indisponível",
	Detail: "Esta foto não pode ser usada aqui. Envie a imagem de novo."}

func imageError(err error) *httpserver.Error {
	switch {
	case errors.Is(err, errUnsupported):
		return &httpserver.Error{Status: 415, Code: "image_unsupported", Title: "Formato não aceito",
			Detail: "Envie uma foto JPEG, PNG ou WebP (sem animação)."}
	case errors.Is(err, errDimensions):
		return &httpserver.Error{Status: 422, Code: "image_dimensions", Title: "Imagem grande demais",
			Detail: "Use uma imagem com até 8.192 px de lado e 12 megapixels."}
	default:
		return &httpserver.Error{Status: 422, Code: "image_invalid", Title: "Imagem inválida",
			Detail: "Não foi possível ler esta imagem. Tente outra foto."}
	}
}

// ---------------------------------------------------------------------------------------------
// uploadMedia

var acceptedTypes = map[string]string{"image/jpeg": formatJPEG, "image/png": formatPNG, "image/webp": formatWebP}

func parseCrop(q map[string][]string) (*Crop, error) {
	keys := []string{"crop_x", "crop_y", "crop_size"}
	vals := make([]float64, 3)
	present := 0
	for i, k := range keys {
		raw := ""
		if v := q[k]; len(v) > 0 {
			raw = v[0]
		}
		if raw == "" {
			continue
		}
		present++
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f != f || f < 0 || f > 1 {
			return nil, httpserver.ErrValidation(httpserver.FieldError{Field: "/query/" + k, Code: httpserver.FieldInvalidFormat,
				Message: "número entre 0 e 1"})
		}
		vals[i] = f
	}
	switch present {
	case 0:
		return nil, nil
	case 3:
	default:
		return nil, httpserver.ErrValidation(httpserver.FieldError{Field: "/query/crop_size", Code: httpserver.FieldRequired,
			Message: "informe crop_x, crop_y e crop_size juntos"})
	}
	if vals[0] >= 1 || vals[1] >= 1 || vals[2] <= 0 {
		return nil, httpserver.ErrValidation(httpserver.FieldError{Field: "/query/crop_size", Code: httpserver.FieldInvalidFormat,
			Message: "recorte fora da imagem"})
	}
	return &Crop{X: vals[0], Y: vals[1], Size: vals[2]}, nil
}

// upload recebe os bytes por rota própria (sem aumentar o limite de corpo das outras rotas),
// confere finalidade, permissão, limites e formato real, guarda no staging e responde 202.
func (s *Service) upload(w http.ResponseWriter, r *http.Request) error {
	// Sessão primeiro: sem conta, 401 (nunca revela se o recurso está ligado).
	accountID, err := s.AccountOf(r)
	if err != nil {
		return err
	}
	if !s.Enabled() {
		return errDisabled()
	}
	q := r.URL.Query()
	purpose := q.Get("purpose")
	var estID *uuid.UUID
	switch purpose {
	case PurposeAvatar:
		if q.Get("establishment_id") != "" {
			return httpserver.ErrValidation(httpserver.FieldError{Field: "/query/establishment_id", Code: httpserver.FieldNotAllowed,
				Message: "o avatar pessoal não pertence a uma loja"})
		}
	case PurposeLogo, PurposeProfessionalPhoto:
		id, err := uuid.Parse(q.Get("establishment_id"))
		if err != nil {
			return httpserver.ErrValidation(httpserver.FieldError{Field: "/query/establishment_id", Code: httpserver.FieldRequired})
		}
		if purpose == PurposeLogo {
			if _, err := s.Auth.Owner(r, id); err != nil {
				return err
			}
		} else {
			a, err := s.Auth.Member(r, id)
			if err != nil {
				return err
			}
			if a.ProfessionalID == nil {
				return httpserver.ErrForbidden("Só quem atende na loja tem foto profissional.")
			}
		}
		estID = &id
	default:
		return httpserver.ErrValidation(httpserver.FieldError{Field: "/query/purpose", Code: httpserver.FieldNotAllowed,
			Message: "use avatar, professional_photo ou logo"})
	}
	crop, err := parseCrop(q)
	if err != nil {
		return err
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	declared, ok := acceptedTypes[mt]
	if !ok {
		return &httpserver.Error{Status: 415, Code: "unsupported_media_type", Title: "Formato não suportado",
			Detail: "Envie uma foto JPEG, PNG ou WebP."}
	}
	// Tamanho conferido antes de ler o corpo (Content-Length) e durante a leitura (MaxBytesReader).
	if r.ContentLength > MaxUploadBytes {
		return tooLarge()
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxUploadBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return tooLarge()
		}
		return httpserver.ErrValidation(httpserver.FieldError{Field: "", Code: httpserver.FieldInvalidFormat, Message: "não foi possível ler a imagem"})
	}
	if len(body) == 0 {
		return httpserver.ErrValidation(httpserver.FieldError{Field: "", Code: httpserver.FieldRequired, Message: "envie a imagem"})
	}
	format, _, _, err := Probe(body)
	if err != nil {
		return imageError(err)
	}
	if format != declared {
		return imageError(errUnsupported) // conteúdo não confere com o tipo declarado
	}
	ctx := r.Context()
	subject := accountID.String()
	for _, rule := range []ratelimit.Rule{
		{Name: "midia_minuto", Limit: s.PerMinute, Window: time.Minute},
		{Name: "midia_dia", Limit: s.PerDay, Window: 24 * time.Hour},
	} {
		allowed, retry, err := s.Limiter.Allow(ctx, rule, subject)
		if err != nil {
			return err
		}
		if !allowed {
			return httpserver.ErrRateLimited(retry)
		}
	}
	sum := sha256.Sum256(body)
	fp := fmt.Sprintf("%x|%s|%s", sum, purpose, r.URL.RawQuery)
	cmd := idempotency.Command{ActorKey: "conta:" + accountID.String(), Operation: "uploadMedia", Body: []byte(fp)}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		var pending int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM media_asset
			WHERE owner_account_id = $1 AND state IN ('processing', 'ready')`, accountID).Scan(&pending); err != nil {
			return idempotency.Result{}, err
		}
		if pending >= maxPendingPerAccount {
			return idempotency.Result{}, httpserver.ErrRateLimited(time.Hour)
		}
		now := s.Clock.Now()
		visibility := "public"
		if purpose == PurposeAvatar {
			visibility = "private"
		}
		staging := "staging/" + uuid.NewString()
		var cx, cy, cs *float64
		if crop != nil {
			cx, cy, cs = &crop.X, &crop.Y, &crop.Size
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO media_asset (owner_account_id, establishment_id, purpose, visibility, state, source_bytes, staging_key,
			                         crop_x, crop_y, crop_size, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'processing', $5, $6, $7, $8, $9, $10, $10) RETURNING id`,
			accountID, estID, purpose, visibility, len(body), staging, cx, cy, cs, now).Scan(&id); err != nil {
			return idempotency.Result{}, err
		}
		// Grava antes do COMMIT: se a gravação falhar, nada fica registrado; se o COMMIT falhar,
		// o arquivo órfão é apagado pela limpeza (StagingOlderThan).
		if err := s.Store.Put(ctx, staging, body); err != nil {
			return idempotency.Result{}, fmt.Errorf("guardar original: %w", err)
		}
		return idempotency.Result{Status: http.StatusAccepted, Body: httpserver.Data[uploadView]{
			Data: uploadView{ID: id, Purpose: purpose, Status: StateProcessing}}}, nil
	})
}

func tooLarge() *httpserver.Error {
	return &httpserver.Error{Status: 413, Code: "payload_too_large", Title: "Imagem grande demais",
		Detail: "Envie uma imagem de até 5 MB."}
}

// getUpload: a própria pessoa acompanha o processamento do envio (consulta curta e limitada).
func (s *Service) getUpload(w http.ResponseWriter, r *http.Request) error {
	accountID, err := s.AccountOf(r)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(r.PathValue("assetId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	var v uploadView
	err = s.Pool.QueryRow(r.Context(), `SELECT id, purpose, state, failure_reason FROM media_asset
		WHERE id = $1 AND owner_account_id = $2 AND state <> 'purged'`, id, accountID).
		Scan(&v.ID, &v.Purpose, &v.Status, &v.FailureReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpserver.ErrNotFound() // de outra pessoa ou inexistente: igual
	}
	if err != nil {
		return err
	}
	if v.Status == StateReleased {
		v.Status = StateReady // para quem enviou, "liberada" é uma imagem que já foi usada e trocada
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[uploadView]{Data: v})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Entrega

// getPublic serve só imagens públicas associadas (logo e foto profissional publicadas). Imagem
// removida deixa de ser servida na hora (o cache curto de 5 min é o limite da propagação).
func (s *Service) getPublic(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("assetId"))
	if err != nil {
		return httpserver.ErrNotFound()
	}
	return s.serve(w, r, id, "public", "public, max-age=300")
}

// ServePrivate entrega uma imagem privada JÁ AUTORIZADA pelo chamador (a autorização é refeita a
// cada pedido, antes de responder 304). Nunca vai para cache compartilhado.
func (s *Service) ServePrivate(w http.ResponseWriter, r *http.Request, id uuid.UUID) error {
	return s.serve(w, r, id, "private", "private, no-cache, must-revalidate")
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request, id uuid.UUID, visibility, cacheControl string) error {
	if !s.Enabled() {
		return httpserver.ErrNotFound()
	}
	size, err := strconv.Atoi(r.PathValue("size"))
	if err != nil || size < 16 || size > 1024 {
		return httpserver.ErrNotFound()
	}
	a, err := AttachedOne(r.Context(), s.Pool, &id, visibility)
	if err != nil {
		return err
	}
	if a == nil || len(a.variants) == 0 {
		return httpserver.ErrNotFound()
	}
	v := pick(a.variants, size)
	etag := fmt.Sprintf(`"%s-%d"`, id, v.Size)
	h := w.Header()
	h.Set("Cache-Control", cacheControl)
	h.Set("ETag", etag)
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	data, err := s.Store.Get(r.Context(), v.Key)
	if errors.Is(err, ErrObjectNotFound) {
		s.Log.Warn("variante ausente no armazenamento", "asset_id", id.String(), "size", v.Size)
		return httpserver.ErrNotFound()
	}
	if err != nil {
		return err
	}
	h.Set("Content-Type", v.Mime)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
	return nil
}

// pick escolhe a variante do tamanho pedido, a menor maior que ele ou, por fim, a maior.
func pick(vs []variantRow, size int) variantRow {
	best := vs[len(vs)-1]
	for _, v := range vs {
		if v.Size >= size {
			return v
		}
	}
	return best
}

// ---------------------------------------------------------------------------------------------
// Associação (chamadas pelos módulos donos das referências, dentro da transação deles)

// Claim associa uma imagem pronta: precisa ser da mesma pessoa, da mesma finalidade e da mesma
// loja. Imagem de outra finalidade (ex.: avatar privado como logo) nunca é aceita.
func Claim(ctx context.Context, tx pgx.Tx, id, owner uuid.UUID, purpose string, estID *uuid.UUID, now time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE media_asset SET state = 'attached', attached_at = $5, updated_at = $5
		WHERE id = $1 AND owner_account_id = $2 AND purpose = $3 AND establishment_id IS NOT DISTINCT FROM $4 AND state = 'ready'`,
		id, owner, purpose, estID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotAttachable
	}
	return nil
}

// Release desassocia (troca ou remoção): a imagem deixa de ser servida na hora e os arquivos são
// apagados depois do prazo de retenção. Repetível.
func Release(ctx context.Context, tx pgx.Tx, id *uuid.UUID, now time.Time) error {
	if id == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE media_asset SET state = 'released', released_at = $2, updated_at = $2
		WHERE id = $1 AND state = 'attached'`, *id, now)
	return err
}

// ReleaseOwnedBy encerra todas as imagens de uma conta (exclusão de conta). Repetível.
func ReleaseOwnedBy(ctx context.Context, q database.Querier, accountID uuid.UUID, now time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE media_asset SET
		state = CASE WHEN state IN ('attached', 'ready') THEN 'released' ELSE 'failed' END,
		failure_reason = CASE WHEN state IN ('attached', 'ready') THEN NULL ELSE coalesce(failure_reason, 'processing_error') END,
		released_at = $2, updated_at = $2
		WHERE owner_account_id = $1 AND state IN ('processing', 'ready', 'failed', 'attached')`, accountID, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CopyAsPublic publica a foto pessoal (avatar) como foto profissional por ação explícita: copia
// as variantes para chaves e registro novos (visibilidade pública). O avatar continua privado e
// independente; remover um não remove o outro. Devolve o registro novo, pronto para Claim.
func (s *Service) CopyAsPublic(ctx context.Context, avatarID, owner, estID uuid.UUID) (uuid.UUID, error) {
	if !s.Enabled() {
		return uuid.Nil, errDisabled()
	}
	var raw []byte
	var bytesSrc int
	err := s.Pool.QueryRow(ctx, `SELECT variants, source_bytes FROM media_asset
		WHERE id = $1 AND owner_account_id = $2 AND purpose = 'avatar' AND state = 'attached'`, avatarID, owner).Scan(&raw, &bytesSrc)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotAttachable
	}
	if err != nil {
		return uuid.Nil, err
	}
	var vs []variantRow
	if err := json.Unmarshal(raw, &vs); err != nil {
		return uuid.Nil, err
	}
	newID := uuid.Must(uuid.NewV7())
	out := make([]variantRow, 0, len(vs))
	for _, v := range vs {
		data, err := s.Store.Get(ctx, v.Key)
		if err != nil {
			return uuid.Nil, fmt.Errorf("copiar variante: %w", err)
		}
		ext := "jpg"
		if v.Mime == "image/png" {
			ext = "png"
		}
		nv := v
		nv.Key = variantKey(newID.String(), v.Size, ext)
		if err := s.Store.Put(ctx, nv.Key, data); err != nil {
			return uuid.Nil, err
		}
		out = append(out, nv)
	}
	enc, _ := json.Marshal(out)
	now := s.Clock.Now()
	_, err = s.Pool.Exec(ctx, `INSERT INTO media_asset (id, owner_account_id, establishment_id, purpose, visibility, state,
		source_bytes, variants, created_at, updated_at, ready_at)
		VALUES ($1, $2, $3, 'professional_photo', 'public', 'ready', $4, $5, $6, $6, $6)`, newID, owner, estID, bytesSrc, enc, now)
	if err != nil {
		return uuid.Nil, err
	}
	return newID, nil
}
