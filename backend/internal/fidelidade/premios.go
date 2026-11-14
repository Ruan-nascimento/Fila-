package fidelidade

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// rewardView segue Reward. O prêmio é cortesia separada: não dá desconto na comanda, não
// reserva horário e não gera pontos (R-FID-09).
type rewardView struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	UsageInstructions string    `json:"usage_instructions"`
	CostPoints        int       `json:"cost_points"`
	Stock             struct {
		Kind           string `json:"kind"`
		UnitsAvailable *int   `json:"units_available"`
	} `json:"stock"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

const rewardSelect = `SELECT id, name, description, usage_instructions, cost_points, stock_kind, units_available, status, version FROM reward`

func scanReward(r pgx.Row) (rewardView, error) {
	var v rewardView
	err := r.Scan(&v.ID, &v.Name, &v.Description, &v.UsageInstructions, &v.CostPoints, &v.Stock.Kind, &v.Stock.UnitsAvailable,
		&v.Status, &v.Version)
	return v, err
}

func rewards(ctx context.Context, q database.Querier, estID uuid.UUID, activeOnly bool) ([]rewardView, error) {
	sql := rewardSelect + ` WHERE establishment_id = $1`
	if activeOnly {
		sql += ` AND status = 'active'`
	}
	rows, err := q.Query(ctx, sql+` ORDER BY status, cost_points, name, id`, estID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rewardView, error) { return scanReward(r) })
	if list == nil {
		list = []rewardView{}
	}
	return list, err
}

// listRewards: dono vê todos (inclusive inativos).
func (s *Service) listRewards(w http.ResponseWriter, r *http.Request) error {
	estID, _, err := s.owner(r)
	if err != nil {
		return err
	}
	list, err := rewards(r.Context(), s.Pool, estID, false)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]rewardView]{Data: list})
	return nil
}

// listCustomerRewards: conta vê os prêmios ativos (custo, descrição, disponibilidade).
func (s *Service) listCustomerRewards(w http.ResponseWriter, r *http.Request) error {
	estID, _, err := s.customer(r)
	if err != nil {
		return err
	}
	list, err := rewards(r.Context(), s.Pool, estID, true)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[[]rewardView]{Data: list})
	return nil
}

type stockInput struct {
	Kind  *string `json:"kind"`
	Units *int64  `json:"units_available"`
}

type rewardInput struct {
	ExpectedVersion *int64      `json:"expected_version"`
	Name            *string     `json:"name"`
	Description     *string     `json:"description"`
	Instructions    *string     `json:"usage_instructions"`
	Cost            *int64      `json:"cost_points"`
	Stock           *stockInput `json:"stock"`
}

type rewardFields struct {
	name, description, instructions *string
	cost                            *int64
	kind                            string // "" = sem mudança de estoque
	units                           int64
}

// validateReward valida os campos presentes; create exige todos.
func validateReward(in rewardInput, create bool) (rewardFields, error) {
	var v validate.Collector
	var f rewardFields
	text := func(field string, raw *string, max int) *string {
		if create {
			v.Required(field, raw == nil)
		}
		if raw == nil {
			return nil
		}
		t, fe := validate.Text(field, *raw, 1, max)
		v.Add(fe)
		return &t
	}
	f.name = text("/name", in.Name, 60)
	f.description = text("/description", in.Description, 500)
	f.instructions = text("/usage_instructions", in.Instructions, 500)
	if create {
		v.Required("/cost_points", in.Cost == nil)
		v.Required("/stock", in.Stock == nil)
	}
	if in.Cost != nil {
		c := v.IntRange("/cost_points", in.Cost, 1, 1_000_000)
		f.cost = &c
	}
	if in.Stock != nil {
		switch {
		case in.Stock.Kind == nil:
			v.Required("/stock/kind", true)
		case *in.Stock.Kind == "unlimited":
			if in.Stock.Units != nil {
				v.Add(&hs.FieldError{Field: "/stock/units_available", Code: hs.FieldNotAllowed, Message: "só para estoque limitado"})
			}
			f.kind = "unlimited"
		case *in.Stock.Kind == "limited":
			v.Required("/stock/units_available", in.Stock.Units == nil)
			f.kind, f.units = "limited", v.IntRange("/stock/units_available", in.Stock.Units, 0, 100_000)
		default:
			v.Add(&hs.FieldError{Field: "/stock/kind", Code: hs.FieldInvalidFormat, Message: "use unlimited ou limited"})
		}
	}
	return f, v.Err()
}

// setStock aplica a quantidade escolhida pelo dono como MOVIMENTO (o estoque é explicado pelos
// movimentos; nunca é editado direto).
func setStock(ctx context.Context, tx pgx.Tx, rewardID uuid.UUID, cur rewardView, f rewardFields, now time.Time) error {
	switch {
	case f.kind == "":
		return nil
	case f.kind == "unlimited":
		_, err := tx.Exec(ctx, `UPDATE reward SET stock_kind = 'unlimited', units_available = NULL WHERE id = $1`, rewardID)
		return err
	}
	current := int64(0)
	if cur.Stock.Kind == "limited" && cur.Stock.UnitsAvailable != nil {
		current = int64(*cur.Stock.UnitsAvailable)
	} else if _, err := tx.Exec(ctx, `UPDATE reward SET stock_kind = 'limited', units_available = 0 WHERE id = $1`, rewardID); err != nil {
		return err
	}
	if f.units == current {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO reward_stock_movement (reward_id, kind, delta, source_id, occurred_at)
		VALUES ($1, 'set_by_owner', $2, uuidv7(), $3)`, rewardID, f.units-current, now)
	return err
}

func (s *Service) createReward(w http.ResponseWriter, r *http.Request) error {
	estID, actor, err := s.owner(r)
	if err != nil {
		return err
	}
	var in rewardInput
	raw, err := hs.DecodeJSON(r, &in)
	if err != nil {
		return err
	}
	if in.ExpectedVersion != nil {
		return hs.ErrValidation(hs.FieldError{Field: "/expected_version", Code: hs.FieldNotAllowed})
	}
	f, err := validateReward(in, true)
	if err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "createReward", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			now := s.Clock.Now()
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO reward (establishment_id, name, description, usage_instructions, cost_points,
				stock_kind, units_available, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, 'unlimited', NULL, $6, $6) RETURNING id`,
				estID, *f.name, *f.description, *f.instructions, *f.cost, now).Scan(&id); err != nil {
				return idempotency.Result{}, err
			}
			cur, err := scanReward(tx.QueryRow(ctx, rewardSelect+` WHERE id = $1`, id))
			if err != nil {
				return idempotency.Result{}, err
			}
			if err := setStock(ctx, tx, id, cur, f, now); err != nil {
				return idempotency.Result{}, err
			}
			aid := actor.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
				ActorID: &aid, Action: "T-RD-01", TargetType: "reward", TargetID: id, ToState: "active"}); err != nil {
				return idempotency.Result{}, err
			}
			out, err := scanReward(tx.QueryRow(ctx, rewardSelect+` WHERE id = $1`, id))
			return idempotency.Result{Status: http.StatusCreated, Body: hs.Data[rewardView]{Data: out}}, err
		})
}

// lockReward trava o prêmio DESTA loja.
func lockReward(ctx context.Context, tx pgx.Tx, estID, rewardID uuid.UUID) (rewardView, error) {
	v, err := scanReward(tx.QueryRow(ctx, rewardSelect+` WHERE establishment_id = $1 AND id = $2 FOR UPDATE`, estID, rewardID))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, hs.ErrNotFound()
	}
	return v, err
}

// updateReward (T-RD-02): nova versão do prêmio. Reservas existentes guardam as condições aceitas.
func (s *Service) updateReward(w http.ResponseWriter, r *http.Request) error {
	estID, actor, err := s.owner(r)
	if err != nil {
		return err
	}
	rewardID, err := pathID(r, "rewardId")
	if err != nil {
		return err
	}
	var in rewardInput
	raw, err := hs.DecodeJSON(r, &in)
	if err != nil {
		return err
	}
	var vc validate.Collector
	expected := vc.Version("/expected_version", in.ExpectedVersion)
	if err := vc.Err(); err != nil {
		return err
	}
	f, err := validateReward(in, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "updateReward", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			cur, err := lockReward(ctx, tx, estID, rewardID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Version != expected {
				return idempotency.Result{}, conflict("version_conflict", "O prêmio mudou", "Confira os dados atuais e tente de novo.", cur)
			}
			now := s.Clock.Now()
			if _, err := tx.Exec(ctx, `UPDATE reward SET name = coalesce($2, name), description = coalesce($3, description),
				usage_instructions = coalesce($4, usage_instructions), cost_points = coalesce($5, cost_points),
				version = version + 1, updated_at = $6 WHERE id = $1`,
				rewardID, f.name, f.description, f.instructions, f.cost, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := setStock(ctx, tx, rewardID, cur, f, now); err != nil {
				return idempotency.Result{}, err
			}
			out, err := scanReward(tx.QueryRow(ctx, rewardSelect+` WHERE id = $1`, rewardID))
			if err != nil {
				return idempotency.Result{}, err
			}
			aid := actor.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
				ActorID: &aid, Action: "T-RD-02", TargetType: "reward", TargetID: rewardID,
				FromState: "v" + strconv.FormatInt(cur.Version, 10), ToState: "v" + strconv.FormatInt(out.Version, 10)}); err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[rewardView]{Data: out}}, nil
		})
}

// deactivateReward (T-RD-03): sem novas reservas; as ativas continuam valendo.
func (s *Service) deactivateReward(w http.ResponseWriter, r *http.Request) error {
	estID, actor, err := s.owner(r)
	if err != nil {
		return err
	}
	rewardID, err := pathID(r, "rewardId")
	if err != nil {
		return err
	}
	var body struct {
		ExpectedVersion *int64 `json:"expected_version"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var vc validate.Collector
	expected := vc.Version("/expected_version", body.ExpectedVersion)
	if err := vc.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "deactivateReward", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			cur, err := lockReward(ctx, tx, estID, rewardID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Version != expected {
				return idempotency.Result{}, conflict("version_conflict", "O prêmio mudou", "Confira os dados atuais e tente de novo.", cur)
			}
			if cur.Status == "inactive" {
				return idempotency.Result{Status: http.StatusOK, Body: hs.Data[rewardView]{Data: cur}}, nil
			}
			now := s.Clock.Now()
			out, err := scanReward(tx.QueryRow(ctx, `UPDATE reward SET status = 'inactive', version = version + 1, updated_at = $2
				WHERE id = $1 RETURNING id, name, description, usage_instructions, cost_points, stock_kind, units_available, status, version`,
				rewardID, now))
			if err != nil {
				return idempotency.Result{}, err
			}
			aid := actor.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
				ActorID: &aid, Action: "T-RD-03", TargetType: "reward", TargetID: rewardID, FromState: "active", ToState: "inactive"}); err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[rewardView]{Data: out}}, nil
		})
}
