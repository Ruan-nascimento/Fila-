package estabelecimento

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/midia"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Palettes é o catálogo fechado de cores de ação da loja (ADR-0028). O banco guarda só o
// identificador: nunca CSS livre nem hexadecimal arbitrário. Padrão: purple.
var Palettes = []string{"purple", "violet", "indigo", "blue", "sky", "cyan", "teal", "emerald", "green", "lime",
	"amber", "orange", "red", "rose", "pink", "fuchsia"}

// ValidPalette confere o identificador contra o catálogo.
func ValidPalette(p string) bool { return slices.Contains(Palettes, p) }

// optionalText valida um texto simples opcional: string vazia (depois de aparar) remove o valor.
func optionalText(field string, v *string, maxLen int) (*string, *httpserver.FieldError) {
	if v == nil {
		return nil, nil
	}
	if strings.TrimSpace(*v) == "" {
		return nil, nil
	}
	t, fe := validate.Text(field, *v, 1, maxLen)
	if fe != nil {
		return nil, fe
	}
	return &t, nil
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// NormalizePhone aceita formatos comuns ("(11) 99999-8888", "+55 11 99999-8888") e devolve E.164.
// Número com 10 ou 11 dígitos sem código do país é tratado como brasileiro (+55). Vazio remove.
func NormalizePhone(field, raw string) (*string, *httpserver.FieldError) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	plus := strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.' || (r == '+' && b.Len() == 0):
		default:
			return nil, &httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat, Message: "use só números, ex.: (11) 99999-8888"}
		}
	}
	digits := b.String()
	out := "+" + digits
	if !plus {
		if len(digits) == 10 || len(digits) == 11 {
			out = "+55" + digits
		} else {
			return nil, &httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat,
				Message: "informe DDD e número, ex.: (11) 99999-8888, ou o código do país com +"}
		}
	}
	if !e164.MatchString(out) {
		return nil, &httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat, Message: "número de telefone inválido"}
	}
	return &out, nil
}

// ---------------------------------------------------------------------------------------------
// Logo da loja (só o dono desta loja)

type assetVersionRequest struct {
	AssetID         *uuid.UUID `json:"asset_id"`
	ExpectedVersion *int64     `json:"expected_version"`
}

func (s *Service) setLogo(w http.ResponseWriter, r *http.Request) error {
	return s.changeLogo(w, r, true)
}

func (s *Service) removeLogo(w http.ResponseWriter, r *http.Request) error {
	return s.changeLogo(w, r, false)
}

func (s *Service) changeLogo(w http.ResponseWriter, r *http.Request, set bool) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	var req assetVersionRequest
	raw, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	if set {
		v.Required("/asset_id", req.AssetID == nil)
	} else if req.AssetID != nil {
		v.Add(&httpserver.FieldError{Field: "/asset_id", Code: httpserver.FieldUnknown})
	}
	expected := v.Version("/expected_version", req.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	op := "removeEstablishmentLogo"
	if set {
		op = "setEstablishmentLogo"
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: op, Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := s.loadEstablishment(ctx, tx, estID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		now := s.Clock.Now()
		var next *uuid.UUID
		if set {
			// A logo precisa ter sido enviada por esta pessoa, para esta loja, com finalidade logo.
			if err := midia.Claim(ctx, tx, *req.AssetID, actor.AccountID, midia.PurposeLogo, &estID, now); err != nil {
				return idempotency.Result{}, err
			}
			next = req.AssetID
		}
		if err := midia.Release(ctx, tx, cur.logoID, now); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE establishment SET logo_asset_id = $2, version = version + 1, updated_at = $3 WHERE id = $1`,
			estID, next, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorOwner, ActorID: auditoria.Ptr(actor.AccountID), Action: "T-ES-01",
			TargetType: "establishment", TargetID: estID, Reason: "campos: logo", RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		out, err := s.loadEstablishment(ctx, tx, estID, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[establishmentView]{Data: out}}, nil
	})
}

// ---------------------------------------------------------------------------------------------
// Perfil do profissional na loja (o próprio profissional): nome atual (só leitura; quem renomeia
// é o dono, como já era) e foto pública com publicação explícita.

type myProfessionalView struct {
	ProfessionalID    uuid.UUID       `json:"professional_id"`
	EstablishmentID   uuid.UUID       `json:"establishment_id"`
	EstablishmentName string          `json:"establishment_name"`
	DisplayName       string          `json:"display_name"`
	Photo             *midia.ImageRef `json:"photo"`
	Version           int64           `json:"version"`
}

// myProfessional resolve o profissional DA PRÓPRIA CONTA nesta loja (sem profissional → 403).
func (s *Service) myProfessional(r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	if actor.ProfessionalID == nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, httpserver.ErrForbidden("Esta conta não atende nesta loja; não há perfil profissional.")
	}
	return estID, *actor.ProfessionalID, actor.AccountID, nil
}

func (s *Service) loadMyProfessional(ctx context.Context, q database.Querier, estID, profID uuid.UUID, lock bool) (myProfessionalView, *uuid.UUID, error) {
	sql := `SELECT p.id, e.id, e.name, p.display_name, p.photo_asset_id, p.version
		FROM professional p JOIN establishment e ON e.id = p.establishment_id
		WHERE p.establishment_id = $1 AND p.id = $2 AND e.status = 'active'`
	if lock {
		sql += ` FOR UPDATE OF p`
	}
	var v myProfessionalView
	var photo *uuid.UUID
	err := q.QueryRow(ctx, sql, estID, profID).Scan(&v.ProfessionalID, &v.EstablishmentID, &v.EstablishmentName, &v.DisplayName, &photo, &v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil, httpserver.ErrNotFound()
	}
	if err != nil {
		return v, nil, err
	}
	a, err := midia.AttachedOne(ctx, q, photo, "public")
	if err != nil {
		return v, nil, err
	}
	if a != nil {
		v.Photo = a.PublicRef()
	}
	return v, photo, nil
}

func (s *Service) getMyProfessionalProfile(w http.ResponseWriter, r *http.Request) error {
	estID, profID, _, err := s.myProfessional(r)
	if err != nil {
		return err
	}
	v, _, err := s.loadMyProfessional(r.Context(), s.Pool, estID, profID, false)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[myProfessionalView]{Data: v})
	return nil
}

type professionalPhotoRequest struct {
	AssetID         *uuid.UUID `json:"asset_id"`
	FromAvatar      *bool      `json:"from_avatar"`
	ExpectedVersion *int64     `json:"expected_version"`
}

// setMyProfessionalPhoto publica a foto pública: um envio próprio com finalidade
// professional_photo, OU (from_avatar) uma CÓPIA da foto pessoal — nunca a mesma imagem privada.
func (s *Service) setMyProfessionalPhoto(w http.ResponseWriter, r *http.Request) error {
	estID, profID, accountID, err := s.myProfessional(r)
	if err != nil {
		return err
	}
	var req professionalPhotoRequest
	raw, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	fromAvatar := req.FromAvatar != nil && *req.FromAvatar
	if (req.AssetID == nil) == !fromAvatar {
		v.Add(&httpserver.FieldError{Field: "/asset_id", Code: httpserver.FieldRequired, Message: "envie asset_id OU from_avatar"})
	}
	expected := v.Version("/expected_version", req.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	assetID := uuid.Nil
	if req.AssetID != nil {
		assetID = *req.AssetID
	}
	if fromAvatar {
		if s.Media == nil || s.AvatarOf == nil {
			return midia.ErrNotAttachable
		}
		avatar, err := s.AvatarOf(ctx, s.Pool, accountID)
		if err != nil {
			return err
		}
		if avatar == nil {
			return midia.ErrNotAttachable
		}
		if assetID, err = s.Media.CopyAsPublic(ctx, *avatar, accountID, estID); err != nil {
			return err
		}
	}
	cmd := idempotency.Command{ActorKey: "conta:" + accountID.String(), Operation: "setMyProfessionalPhoto", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, old, err := s.loadMyProfessional(ctx, tx, estID, profID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		now := s.Clock.Now()
		if err := midia.Claim(ctx, tx, assetID, accountID, midia.PurposeProfessionalPhoto, &estID, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := midia.Release(ctx, tx, old, now); err != nil {
			return idempotency.Result{}, err
		}
		return s.savePhoto(ctx, tx, estID, profID, &assetID, now, auditoria.Event{ActorKind: auditoria.ActorProfessional,
			ActorID: auditoria.Ptr(accountID), Action: "T-PR-05", FromState: photoState(old), ToState: "published"})
	})
}

// removeMyProfessionalPhoto despublica a foto pública; o avatar pessoal não muda.
func (s *Service) removeMyProfessionalPhoto(w http.ResponseWriter, r *http.Request) error {
	estID, profID, accountID, err := s.myProfessional(r)
	if err != nil {
		return err
	}
	var req versionRequest
	raw, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + accountID.String(), Operation: "removeMyProfessionalPhoto", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, old, err := s.loadMyProfessional(ctx, tx, estID, profID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		now := s.Clock.Now()
		if err := midia.Release(ctx, tx, old, now); err != nil {
			return idempotency.Result{}, err
		}
		return s.savePhoto(ctx, tx, estID, profID, nil, now, auditoria.Event{ActorKind: auditoria.ActorProfessional,
			ActorID: auditoria.Ptr(accountID), Action: "T-PR-05", FromState: photoState(old), ToState: "unpublished"})
	})
}

func photoState(id *uuid.UUID) string {
	if id == nil {
		return "unpublished"
	}
	return "published"
}

func (s *Service) savePhoto(ctx context.Context, tx pgx.Tx, estID, profID uuid.UUID, photo *uuid.UUID, now time.Time, ev auditoria.Event) (idempotency.Result, error) {
	if _, err := tx.Exec(ctx, `UPDATE professional SET photo_asset_id = $3, version = version + 1, updated_at = $4
		WHERE establishment_id = $1 AND id = $2`, estID, profID, photo, now); err != nil {
		return idempotency.Result{}, err
	}
	ev.EstablishmentID, ev.OccurredAt, ev.TargetType, ev.TargetID = &estID, now, "professional", profID
	ev.RequestID = httpserver.RequestIDFrom(ctx)
	if err := auditoria.Record(ctx, tx, ev); err != nil {
		return idempotency.Result{}, err
	}
	// O cartão público do profissional faz parte da fila pública: o evento da fila avisa as telas.
	if queueID, err := QueueOfProfessional(ctx, tx, profID); err == nil {
		if err := s.queueChanged(ctx, tx, estID, queueID); err != nil {
			return idempotency.Result{}, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return idempotency.Result{}, err
	}
	out, _, err := s.loadMyProfessional(ctx, tx, estID, profID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[myProfessionalView]{Data: out}}, nil
}

// removeProfessionalPhoto: o DONO retira a publicação de uma foto profissional inadequada, com
// motivo e auditoria. Não toca o avatar pessoal da conta da pessoa (que continua privado).
func (s *Service) removeProfessionalPhoto(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	profID, err := pathID(r, "professionalId")
	if err != nil {
		return err
	}
	var req struct {
		ExpectedVersion *int64  `json:"expected_version"`
		Reason          *string `json:"reason"`
	}
	raw, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	v.Required("/reason", req.Reason == nil)
	var reason string
	if req.Reason != nil {
		var fe *httpserver.FieldError
		reason, fe = validate.Text("/reason", *req.Reason, 3, 500)
		v.Add(fe)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "removeProfessionalPhoto", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadProfessional(ctx, tx, estID, profID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		now := s.Clock.Now()
		if err := midia.Release(ctx, tx, cur.photoID, now); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE professional SET photo_asset_id = NULL, version = version + 1, updated_at = $3
			WHERE establishment_id = $1 AND id = $2`, estID, profID, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now,
			ActorKind: auditoria.ActorOwner, ActorID: auditoria.Ptr(actor.AccountID), Action: "T-PR-06",
			TargetType: "professional", TargetID: profID, FromState: photoState(cur.photoID), ToState: "unpublished",
			Reason: reason, RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		return s.professionalResult(ctx, tx, estID, profID, http.StatusOK)
	})
}

// withPhotos resolve a foto pública de cada profissional (só as publicadas e prontas).
func withPhotos(ctx context.Context, q database.Querier, vs []professionalView) error {
	var ids []uuid.UUID
	for _, v := range vs {
		if v.photoID != nil {
			ids = append(ids, *v.photoID)
		}
	}
	assets, err := midia.Attached(ctx, q, ids, "public")
	if err != nil {
		return err
	}
	for i := range vs {
		if vs[i].photoID == nil {
			continue
		}
		if a, ok := assets[*vs[i].photoID]; ok {
			vs[i].Photo = a.PublicRef()
		}
	}
	return nil
}
