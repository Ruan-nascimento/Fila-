package fidelidade

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// programView segue LoyaltyProgram.
type programView struct {
	Enabled   bool       `json:"enabled"`
	Version   int64      `json:"version"`
	UpdatedAt *time.Time `json:"updated_at"`
}

// programState lê o programa da loja. Sem registro = desligado, versão 1 (R-FID-01).
func programState(ctx context.Context, q database.Querier, estID uuid.UUID) (bool, programView, error) {
	v := programView{Version: 1}
	err := q.QueryRow(ctx, `SELECT enabled, version, updated_at FROM loyalty_program WHERE establishment_id = $1`, estID).
		Scan(&v.Enabled, &v.Version, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, v, nil
	}
	return v.Enabled, v, err
}

// ProgramEnabled informa se o programa está ligado. O módulo fila usa na ENTRADA para gravar o
// snapshot de elegibilidade (R-FID-03): desligar depois não retira o crédito prometido.
func ProgramEnabled(ctx context.Context, q database.Querier, estID uuid.UUID) (bool, error) {
	on, _, err := programState(ctx, q, estID)
	return on, err
}

func (s *Service) owner(r *http.Request) (uuid.UUID, acesso.Actor, error) {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return estID, acesso.Actor{}, err
	}
	actor, err := s.Auth.Owner(r, estID)
	return estID, actor, err
}

func (s *Service) getProgram(w http.ResponseWriter, r *http.Request) error {
	estID, _, err := s.owner(r)
	if err != nil {
		return err
	}
	_, v, err := programState(r.Context(), s.Pool, estID)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[programView]{Data: v})
	return nil
}

// updateProgram (T-LP-01/02): liga ou desliga. Vale para NOVAS visitas e NOVAS reservas; saldos,
// consulta, reservas ativas e créditos já prometidos são preservados (R-FID-13).
func (s *Service) updateProgram(w http.ResponseWriter, r *http.Request) error {
	estID, actor, err := s.owner(r)
	if err != nil {
		return err
	}
	var body struct {
		ExpectedVersion *int64 `json:"expected_version"`
		Enabled         *bool  `json:"enabled"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", body.ExpectedVersion)
	v.Required("/enabled", body.Enabled == nil)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "updateLoyaltyProgram", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			now := s.Clock.Now()
			if _, err := tx.Exec(ctx, `INSERT INTO loyalty_program (establishment_id, enabled, updated_at) VALUES ($1, false, $2)
				ON CONFLICT DO NOTHING`, estID, now); err != nil {
				return idempotency.Result{}, err
			}
			var cur programView
			if err := tx.QueryRow(ctx, `SELECT enabled, version, updated_at FROM loyalty_program WHERE establishment_id = $1 FOR UPDATE`,
				estID).Scan(&cur.Enabled, &cur.Version, &cur.UpdatedAt); err != nil {
				return idempotency.Result{}, err
			}
			if cur.Version != expected {
				return idempotency.Result{}, conflict("version_conflict", "O programa mudou", "Confira a situação atual e tente de novo.", cur)
			}
			if cur.Enabled != *body.Enabled {
				if err := tx.QueryRow(ctx, `UPDATE loyalty_program SET enabled = $2, version = version + 1, updated_at = $3
					WHERE establishment_id = $1 RETURNING enabled, version, updated_at`, estID, *body.Enabled, now).
					Scan(&cur.Enabled, &cur.Version, &cur.UpdatedAt); err != nil {
					return idempotency.Result{}, err
				}
				action, to := "T-LP-02", "disabled"
				if cur.Enabled {
					action, to = "T-LP-01", "enabled"
				}
				aid := actor.AccountID
				if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
					ActorID: &aid, Action: action, TargetType: "loyalty_program", TargetID: estID, ToState: to}); err != nil {
					return idempotency.Result{}, err
				}
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[programView]{Data: cur}}, nil
		})
}
