package catalogo

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Limites do contrato (CreateServiceRequest).
const (
	MaxPriceCents  = 10_000_000
	MaxDuration    = 480
	MaxPoints      = 100_000
	MaxNameLength  = 60
	ServiceActive  = "active"
	ServiceStopped = "inactive"
)

// Deps são as dependências do módulo.
type Deps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	Auth  acesso.Authorizer
}

// Service implementa o catálogo de serviços e as habilitações.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço.
func New(d Deps) *Service {
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

// money segue o esquema Money: centavos inteiros, sempre BRL.
type money struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

func brl(cents int64) money { return money{AmountCents: cents, Currency: "BRL"} }

// serviceView segue o esquema Service (visão da equipe).
type serviceView struct {
	ID              uuid.UUID   `json:"id"`
	Name            string      `json:"name"`
	Price           money       `json:"price"`
	DurationMinutes int         `json:"duration_minutes"`
	Points          int         `json:"points"`
	Status          string      `json:"status"`
	ProfessionalIDs []uuid.UUID `json:"professional_ids"`
	Version         int64       `json:"version"`
}

// publicServiceView segue PublicService: sem pontos, versão, situação nem dados internos.
type publicServiceView struct {
	ID              uuid.UUID   `json:"id"`
	Name            string      `json:"name"`
	Price           money       `json:"price"`
	DurationMinutes int         `json:"duration_minutes"`
	ProfessionalIDs []uuid.UUID `json:"professional_ids"`
}

const serviceSelect = `
	SELECT s.id, s.name, s.price_cents, s.duration_minutes, s.points, s.status, s.version,
	       coalesce(array_agg(sp.professional_id ORDER BY sp.professional_id) FILTER (WHERE sp.professional_id IS NOT NULL), '{}')
	FROM service s LEFT JOIN service_professional sp ON sp.service_id = s.id`

func scanService(row pgx.Row) (serviceView, error) {
	var v serviceView
	var cents int64
	err := row.Scan(&v.ID, &v.Name, &cents, &v.DurationMinutes, &v.Points, &v.Status, &v.Version, &v.ProfessionalIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, hs.ErrNotFound()
	}
	v.Price = brl(cents)
	return v, err
}

// loadService lê o serviço DESTA loja (de outra loja → 404). lock trava a linha do serviço.
func loadService(ctx context.Context, q database.Querier, estID, serviceID uuid.UUID, lock bool) (serviceView, error) {
	if lock {
		var dummy int
		err := q.QueryRow(ctx, `SELECT 1 FROM service WHERE establishment_id = $1 AND id = $2 FOR UPDATE`, estID, serviceID).Scan(&dummy)
		if errors.Is(err, pgx.ErrNoRows) {
			return serviceView{}, hs.ErrNotFound()
		}
		if err != nil {
			return serviceView{}, err
		}
	}
	return scanService(q.QueryRow(ctx, serviceSelect+` WHERE s.establishment_id = $1 AND s.id = $2 GROUP BY s.id`, estID, serviceID))
}

// recordRevision guarda a versão atual do serviço (histórico do catálogo).
func recordRevision(ctx context.Context, q database.Querier, v serviceView, by uuid.UUID, at any) error {
	_, err := q.Exec(ctx, `
		INSERT INTO service_revision (service_id, version, name, price_cents, duration_minutes, points, status,
		                              professional_ids, changed_at, changed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		v.ID, v.Version, v.Name, v.Price.AmountCents, v.DurationMinutes, v.Points, v.Status, v.ProfessionalIDs, at, by)
	return err
}

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, hs.ErrNotFound()
	}
	return id, nil
}

func versionConflict(current any) error {
	return &hs.Error{Status: 409, Code: "version_conflict", Title: "Os dados mudaram",
		Detail: "Outra pessoa alterou este serviço. Confira os dados atualizados e tente de novo.", Current: current}
}

// ---------------------------------------------------------------------------------------------
// Leitura

// listServices: equipe da loja; inclui inativos.
func (s *Service) listServices(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Member(r, estID); err != nil {
		return err
	}
	rows, err := s.Pool.Query(r.Context(), serviceSelect+` WHERE s.establishment_id = $1 GROUP BY s.id ORDER BY s.status, s.name, s.id`, estID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (serviceView, error) { return scanService(row) })
	if err != nil {
		return err
	}
	if list == nil {
		list = []serviceView{}
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]serviceView]{Data: list})
	return nil
}

// listPublicServices: público; só serviços ativos e só profissionais ativos (R-PERF-01).
func (s *Service) listPublicServices(w http.ResponseWriter, r *http.Request) error {
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
	active, err := estabelecimento.ActiveProfessionalIDs(ctx, s.Pool, estID)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, serviceSelect+` WHERE s.establishment_id = $1 AND s.status = 'active' GROUP BY s.id ORDER BY s.name, s.id`, estID)
	if err != nil {
		return err
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (serviceView, error) { return scanService(row) })
	if err != nil {
		return err
	}
	out := []publicServiceView{}
	for _, v := range all {
		ids := []uuid.UUID{}
		for _, id := range v.ProfessionalIDs {
			if active[id] {
				ids = append(ids, id)
			}
		}
		out = append(out, publicServiceView{ID: v.ID, Name: v.Name, Price: v.Price, DurationMinutes: v.DurationMinutes, ProfessionalIDs: ids})
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]publicServiceView]{Data: out})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Escrita (sempre dono; Idempotency-Key; versão esperada nas alterações)

type serviceFields struct {
	Name            *string `json:"name"`
	PriceCents      *int64  `json:"price_cents"`
	DurationMinutes *int64  `json:"duration_minutes"`
	Points          *int64  `json:"points"`
}

func (f serviceFields) validate(v *validate.Collector, required bool) (name string) {
	if required {
		v.Required("/name", f.Name == nil)
	}
	if f.Name != nil {
		n, fe := validate.Text("/name", *f.Name, 1, MaxNameLength)
		v.Add(fe)
		name = n
	}
	check := func(field string, p *int64, lo, hi int64) {
		if required || p != nil {
			v.IntRange(field, p, lo, hi)
		}
	}
	check("/price_cents", f.PriceCents, 1, MaxPriceCents)
	check("/duration_minutes", f.DurationMinutes, 1, MaxDuration)
	check("/points", f.Points, 0, MaxPoints)
	return name
}

type createServiceRequest struct {
	serviceFields
	ProfessionalIDs *[]uuid.UUID `json:"professional_ids"`
}

// createService (T-SV-01).
func (s *Service) createService(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	var req createServiceRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	name := req.validate(&v, true)
	var profs []uuid.UUID
	if req.ProfessionalIDs != nil {
		profs = *req.ProfessionalIDs
		uniqueIDs(&v, "/professional_ids", profs)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "createService", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		if err := s.checkProfessionals(ctx, tx, estID, profs, true); err != nil {
			return idempotency.Result{}, err
		}
		now := s.Clock.Now()
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO service (establishment_id, name, price_cents, duration_minutes, points, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6) RETURNING id`,
			estID, name, *req.PriceCents, *req.DurationMinutes, *req.Points, now).Scan(&id); err != nil {
			return idempotency.Result{}, err
		}
		if err := setProfessionals(ctx, tx, estID, id, profs); err != nil {
			return idempotency.Result{}, err
		}
		return s.finish(ctx, tx, actor, estID, id, "T-SV-01", "", ServiceActive, http.StatusCreated)
	})
}

type updateServiceRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	serviceFields
}

// updateService (T-SV-02): nova versão; itens já aceitos guardam o que foi aceito (R-CMD-05).
func (s *Service) updateService(w http.ResponseWriter, r *http.Request) error {
	estID, serviceID, actor, err := s.ownerAndService(r)
	if err != nil {
		return err
	}
	var req updateServiceRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	name := req.validate(&v, false)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "updateService", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadService(ctx, tx, estID, serviceID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		next := cur
		if req.Name != nil {
			next.Name = name
		}
		if req.PriceCents != nil {
			next.Price = brl(*req.PriceCents)
		}
		if req.DurationMinutes != nil {
			next.DurationMinutes = int(*req.DurationMinutes)
		}
		if req.Points != nil {
			next.Points = int(*req.Points)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE service SET name = $2, price_cents = $3, duration_minutes = $4, points = $5,
			       version = version + 1, updated_at = $6 WHERE id = $1`,
			serviceID, next.Name, next.Price.AmountCents, next.DurationMinutes, next.Points, s.Clock.Now()); err != nil {
			return idempotency.Result{}, err
		}
		return s.finish(ctx, tx, actor, estID, serviceID, "T-SV-02", cur.Status, cur.Status, http.StatusOK)
	})
}

type versionRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

// deactivateService (T-SV-03) e reactivateService (T-SV-04).
func (s *Service) deactivateService(w http.ResponseWriter, r *http.Request) error {
	return s.changeStatus(w, r, ServiceStopped)
}

func (s *Service) reactivateService(w http.ResponseWriter, r *http.Request) error {
	return s.changeStatus(w, r, ServiceActive)
}

func (s *Service) changeStatus(w http.ResponseWriter, r *http.Request, to string) error {
	estID, serviceID, actor, err := s.ownerAndService(r)
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
	op, action := "deactivateService", "T-SV-03"
	if to == ServiceActive {
		op, action = "reactivateService", "T-SV-04"
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: op, Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadService(ctx, tx, estID, serviceID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		if cur.Status == to {
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "invalid_transition", Title: "Ação não permitida agora",
				Detail: "O serviço já está nessa situação.", Current: cur}
		}
		if _, err := tx.Exec(ctx, `UPDATE service SET status = $2, version = version + 1, updated_at = $3 WHERE id = $1`,
			serviceID, to, s.Clock.Now()); err != nil {
			return idempotency.Result{}, err
		}
		return s.finish(ctx, tx, actor, estID, serviceID, action, cur.Status, to, http.StatusOK)
	})
}

type setProfessionalsRequest struct {
	ExpectedVersion *int64       `json:"expected_version"`
	ProfessionalIDs *[]uuid.UUID `json:"professional_ids"`
}

// setServiceProfessionals (T-SV-05): substitui as habilitações; vale para inclusões futuras.
func (s *Service) setServiceProfessionals(w http.ResponseWriter, r *http.Request) error {
	estID, serviceID, actor, err := s.ownerAndService(r)
	if err != nil {
		return err
	}
	var req setProfessionalsRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	v.Required("/professional_ids", req.ProfessionalIDs == nil)
	var profs []uuid.UUID
	if req.ProfessionalIDs != nil {
		profs = *req.ProfessionalIDs
		uniqueIDs(&v, "/professional_ids", profs)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "setServiceProfessionals", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadService(ctx, tx, estID, serviceID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(cur)
		}
		if err := s.checkProfessionals(ctx, tx, estID, profs, false); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM service_professional WHERE service_id = $1`, serviceID); err != nil {
			return idempotency.Result{}, err
		}
		if err := setProfessionals(ctx, tx, estID, serviceID, profs); err != nil {
			return idempotency.Result{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE service SET version = version + 1, updated_at = $2 WHERE id = $1`, serviceID, s.Clock.Now()); err != nil {
			return idempotency.Result{}, err
		}
		return s.finish(ctx, tx, actor, estID, serviceID, "T-SV-05", cur.Status, cur.Status, http.StatusOK)
	})
}

// ---------------------------------------------------------------------------------------------

func (s *Service) ownerAndService(r *http.Request) (uuid.UUID, uuid.UUID, acesso.Actor, error) {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return uuid.Nil, uuid.Nil, acesso.Actor{}, err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return uuid.Nil, uuid.Nil, acesso.Actor{}, err
	}
	serviceID, err := pathID(r, "serviceId")
	return estID, serviceID, actor, err
}

// checkProfessionals confere que todos os profissionais são DESTA loja. Na criação, id estranho
// é erro de validação (T-SV-01); ao trocar habilitações, 404 (T-SV-05). Nunca revela outra loja.
func (s *Service) checkProfessionals(ctx context.Context, q database.Querier, estID uuid.UUID, ids []uuid.UUID, onCreate bool) error {
	if len(ids) == 0 {
		return nil
	}
	found, err := estabelecimento.ProfessionalsInEstablishment(ctx, q, estID, ids)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if !found[id] {
			if onCreate {
				return hs.ErrValidation(hs.FieldError{Field: "/professional_ids/" + strconv.Itoa(i), Code: hs.FieldNotAllowed,
					Message: "profissional não encontrado nesta loja"})
			}
			return hs.ErrNotFound()
		}
	}
	return nil
}

func setProfessionals(ctx context.Context, q database.Querier, estID, serviceID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `
		INSERT INTO service_professional (establishment_id, service_id, professional_id)
		SELECT $1, $2, unnest($3::uuid[])`, estID, serviceID, ids)
	return err
}

// finish grava a revisão e a auditoria e devolve a visão atual do serviço.
func (s *Service) finish(ctx context.Context, tx pgx.Tx, actor acesso.Actor, estID, serviceID uuid.UUID,
	action, from, to string, status int) (idempotency.Result, error) {
	v, err := loadService(ctx, tx, estID, serviceID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	now := s.Clock.Now()
	if err := recordRevision(ctx, tx, v, actor.AccountID, now); err != nil {
		return idempotency.Result{}, err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
		ActorID: auditoria.Ptr(actor.AccountID), Action: action, TargetType: "service", TargetID: serviceID,
		FromState: from, ToState: to, RequestID: hs.RequestIDFrom(ctx)}); err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: status, Body: hs.Data[serviceView]{Data: v}}, nil
}

func uniqueIDs(v *validate.Collector, field string, ids []uuid.UUID) {
	for i, id := range ids {
		if slices.Index(ids, id) != i {
			v.Add(&hs.FieldError{Field: field + "/" + strconv.Itoa(i), Code: "duplicate"})
		}
	}
}

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	owner := []hs.Grant{hs.GrantOwner}
	const base = "/v1/establishments/{establishmentId}/services"
	return []hs.Route{
		{Operation: "listServices", Method: http.MethodGet, Path: base,
			Grants: []hs.Grant{{Actor: "professional", Scope: "establishment_member"}, hs.GrantOwner}, Handler: s.listServices},
		{Operation: "createService", Method: http.MethodPost, Path: base, Grants: owner, Handler: s.createService},
		{Operation: "updateService", Method: http.MethodPatch, Path: base + "/{serviceId}", Grants: owner, Handler: s.updateService},
		{Operation: "deactivateService", Method: http.MethodPost, Path: base + "/{serviceId}/deactivate", Grants: owner, Handler: s.deactivateService},
		{Operation: "reactivateService", Method: http.MethodPost, Path: base + "/{serviceId}/reactivate", Grants: owner, Handler: s.reactivateService},
		{Operation: "setServiceProfessionals", Method: http.MethodPut, Path: base + "/{serviceId}/professionals", Grants: owner, Handler: s.setServiceProfessionals},
		{Operation: "listPublicServices", Method: http.MethodGet, Path: "/v1/public/establishments/{establishmentId}/services",
			Grants: []hs.Grant{hs.GrantPublic}, Handler: s.listPublicServices},
	}
}
