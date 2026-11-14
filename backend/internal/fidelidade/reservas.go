package fidelidade

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/outbox"
	"fila/internal/platform/validate"
)

// Estados da reserva (ReservationStatus).
const (
	ResActive    = "active"
	ResCancelled = "cancelled"
	ResExpired   = "expired"
	ResDelivered = "delivered"
	ResReverted  = "delivery_reverted"
)

// reservationView segue RewardReservation. holder_display_name só vai para a equipe.
type reservationView struct {
	ID            uuid.UUID `json:"id"`
	RewardID      uuid.UUID `json:"reward_id"`
	AcceptedTerms struct {
		Name              string `json:"name"`
		Description       string `json:"description"`
		UsageInstructions string `json:"usage_instructions"`
		CostPoints        int    `json:"cost_points"`
	} `json:"accepted_terms"`
	Status     string     `json:"status"`
	HolderName *string    `json:"holder_display_name,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Version    int64      `json:"version"`
}

type reservationRow struct {
	reservationView
	EstablishmentID uuid.UUID
	AccountID       uuid.UUID
	Limited         bool
	holder          string
}

const reservationSelect = `SELECT r.id, r.reward_id, r.accepted_name, r.accepted_description, r.accepted_usage_instructions,
	r.accepted_cost_points, r.status, r.holder_name, r.created_at, r.expires_at, r.resolved_at, r.version,
	r.establishment_id, r.account_id, r.limited_stock FROM reward_reservation r`

func scanReservation(row pgx.Row) (reservationRow, error) {
	var v reservationRow
	t := &v.AcceptedTerms
	err := row.Scan(&v.ID, &v.RewardID, &t.Name, &t.Description, &t.UsageInstructions, &t.CostPoints, &v.Status, &v.holder,
		&v.CreatedAt, &v.ExpiresAt, &v.ResolvedAt, &v.Version, &v.EstablishmentID, &v.AccountID, &v.Limited)
	return v, err
}

// publicView ajusta o que vai para fora: o nome do titular só para a equipe.
func (r *reservationRow) publicView(staff bool) {
	if staff {
		h := r.holder
		r.HolderName = &h
	} else {
		r.HolderName = nil
	}
}

func (r reservationRow) staffView() reservationView {
	r.publicView(true)
	return r.reservationView
}

func (r reservationRow) customerView() reservationView {
	r.publicView(false)
	return r.reservationView
}

// reservationChanged grava reward_reservation.updated: titular (sem o próprio nome), dono e cada
// profissional ativo da loja (todos podem entregar).
func reservationChanged(ctx context.Context, tx pgx.Tx, r reservationRow, now time.Time) error {
	base := outbox.Event{EstablishmentID: r.EstablishmentID, Type: "reward_reservation.updated", ResourceType: "reward_reservation",
		ResourceID: r.ID, ResourceVersion: r.Version, OccurredAt: now}
	acc := r.AccountID
	events := []outbox.Event{}
	e := base
	e.Audience, e.Data = outbox.Audience{Kind: outbox.Account, AccountID: &acc}, r.customerView()
	events = append(events, e)
	e = base
	e.Audience, e.Data = outbox.Audience{Kind: outbox.Owner}, r.staffView()
	events = append(events, e)
	pros, err := estabelecimento.ActiveProfessionalIDs(ctx, tx, r.EstablishmentID)
	if err != nil {
		return err
	}
	for id := range pros {
		pid := id
		e = base
		e.Audience, e.Data = outbox.Audience{Kind: outbox.Professional, ProfessionalID: &pid}, r.staffView()
		events = append(events, e)
	}
	return outbox.Record(ctx, tx, events...)
}

// release encerra uma reserva ativa (cancelada ou vencida) e libera pontos e unidade EXATAMENTE
// uma vez: a mudança de estado é condicional (só se ainda ativa) e os lançamentos têm chave única.
func (s *Service) release(ctx context.Context, tx pgx.Tx, r *reservationRow, to, cancelledBy, action string, now time.Time) error {
	var by *string
	if cancelledBy != "" {
		by = &cancelledBy
	}
	tag, err := tx.Exec(ctx, `UPDATE reward_reservation SET status = $2, resolved_at = $3, cancelled_by = $4, version = version + 1
		WHERE id = $1 AND status = 'active'`, r.ID, to, now, by)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	r.Status, r.ResolvedAt, r.Version = to, &now, r.Version+1
	if _, err := post(ctx, tx, entry{EstablishmentID: r.EstablishmentID, AccountID: r.AccountID, Kind: KindHoldRelease,
		Held: -int64(r.AcceptedTerms.CostPoints), SourceType: "reservation", SourceID: r.ID}, now); err != nil {
		return err
	}
	if r.Limited {
		if _, err := tx.Exec(ctx, `INSERT INTO reward_stock_movement (reward_id, kind, delta, source_id, occurred_at)
			VALUES ($1, 'release', 1, $2, $3) ON CONFLICT DO NOTHING`, r.RewardID, r.ID, now); err != nil {
			return err
		}
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &r.EstablishmentID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: action, TargetType: "reward_reservation", TargetID: r.ID, FromState: ResActive, ToState: to}); err != nil {
		return err
	}
	if err := reservationChanged(ctx, tx, *r, now); err != nil {
		return err
	}
	return loyaltyChanged(ctx, tx, r.EstablishmentID, r.AccountID, now)
}

// createReservation (T-RW-01): conta reserva um prêmio: trava os pontos e uma unidade por 24 h.
func (s *Service) createReservation(w http.ResponseWriter, r *http.Request) error {
	estID, account, err := s.customer(r)
	if err != nil {
		return err
	}
	var body struct {
		RewardID        *string `json:"reward_id"`
		ExpectedVersion *int64  `json:"expected_reward_version"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/reward_id", body.RewardID == nil)
	expected := v.Version("/expected_reward_version", body.ExpectedVersion)
	var rewardID uuid.UUID
	if body.RewardID != nil {
		if rewardID, err = uuid.Parse(*body.RewardID); err != nil {
			v.Add(&hs.FieldError{Field: "/reward_id", Code: hs.FieldInvalidFormat})
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + account.AccountID.String(), Operation: "createRewardReservation", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			now := s.Clock.Now()
			on, err := ProgramEnabled(ctx, tx, estID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if !on {
				return idempotency.Result{}, conflict("loyalty_program_disabled", "Programa de pontos desligado",
					"A loja não está aceitando novas reservas de prêmio agora. Seu saldo continua guardado.", nil)
			}
			if _, _, err := lockAccount(ctx, tx, estID, account.AccountID); err != nil {
				return idempotency.Result{}, err
			}
			cur, err := scanReservation(tx.QueryRow(ctx, reservationSelect+` WHERE r.establishment_id = $1 AND r.account_id = $2
				AND r.status = 'active' FOR UPDATE OF r`, estID, account.AccountID))
			switch {
			case err == nil && !now.Before(cur.ExpiresAt):
				// Vencida e ainda não processada pelo worker: vence agora (libera uma vez).
				if err := s.release(ctx, tx, &cur, ResExpired, "", "T-RW-03", now); err != nil {
					return idempotency.Result{}, err
				}
			case err == nil:
				return idempotency.Result{}, conflict("reservation_already_active", "Você já tem um prêmio reservado",
					"Use ou cancele a reserva atual antes de reservar outro prêmio.", cur.customerView())
			case !errors.Is(err, pgx.ErrNoRows):
				return idempotency.Result{}, err
			}
			rw, err := lockReward(ctx, tx, estID, rewardID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if rw.Status != "active" {
				return idempotency.Result{}, conflict("reward_inactive", "Prêmio indisponível", "Este prêmio não está mais disponível.", rw)
			}
			if rw.Version != expected {
				return idempotency.Result{}, conflict("version_conflict", "O prêmio mudou",
					"As condições do prêmio mudaram. Confira e confirme de novo.", rw)
			}
			limited := rw.Stock.Kind == "limited"
			if limited && (rw.Stock.UnitsAvailable == nil || *rw.Stock.UnitsAvailable <= 0) {
				return idempotency.Result{}, conflict("reward_out_of_stock", "Prêmio esgotado", "Não há unidade disponível agora.", rw)
			}
			balance, held, err := lockAccount(ctx, tx, estID, account.AccountID)
			if err != nil {
				return idempotency.Result{}, err
			}
			if balance-held < int64(rw.CostPoints) {
				sum, err := summary(ctx, tx, estID, account.AccountID, now)
				if err != nil {
					return idempotency.Result{}, err
				}
				return idempotency.Result{}, conflict("insufficient_points", "Pontos insuficientes",
					"Você ainda não tem pontos disponíveis suficientes para este prêmio.", sum)
			}
			name := account.Name
			if name == "" {
				name = "Cliente"
			}
			res, err := scanReservation(tx.QueryRow(ctx, `
				WITH ins AS (
					INSERT INTO reward_reservation (establishment_id, account_id, holder_name, reward_id, accepted_name, accepted_description,
						accepted_usage_instructions, accepted_cost_points, accepted_reward_version, limited_stock, created_at, expires_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING *)
				SELECT r.id, r.reward_id, r.accepted_name, r.accepted_description, r.accepted_usage_instructions,
					r.accepted_cost_points, r.status, r.holder_name, r.created_at, r.expires_at, r.resolved_at, r.version,
					r.establishment_id, r.account_id, r.limited_stock FROM ins r`,
				estID, account.AccountID, name, rewardID, rw.Name, rw.Description, rw.UsageInstructions, rw.CostPoints, rw.Version,
				limited, now, now.Add(HoldFor)))
			if database.IsUniqueViolation(err, "reward_reservation_one_active_uq") {
				return idempotency.Result{}, conflict("reservation_already_active", "Você já tem um prêmio reservado",
					"Use ou cancele a reserva atual antes de reservar outro prêmio.", nil)
			}
			if err != nil {
				return idempotency.Result{}, err
			}
			if _, err := post(ctx, tx, entry{EstablishmentID: estID, AccountID: account.AccountID, Kind: KindHold,
				Held: int64(rw.CostPoints), SourceType: "reservation", SourceID: res.ID}, now); err != nil {
				if errors.Is(err, errInsufficient) {
					return idempotency.Result{}, conflict("insufficient_points", "Pontos insuficientes",
						"Você ainda não tem pontos disponíveis suficientes para este prêmio.", nil)
				}
				return idempotency.Result{}, err
			}
			if limited {
				_, err := tx.Exec(ctx, `INSERT INTO reward_stock_movement (reward_id, kind, delta, source_id, occurred_at)
					VALUES ($1, 'reserve', -1, $2, $3)`, rewardID, res.ID, now)
				var pe *pgconn.PgError
				if errors.As(err, &pe) && pe.Code == "23514" { // estoque não fica negativo (última unidade)
					return idempotency.Result{}, conflict("reward_out_of_stock", "Prêmio esgotado", "Não há unidade disponível agora.", nil)
				}
				if err != nil {
					return idempotency.Result{}, err
				}
			}
			aid := account.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorCustomer,
				ActorID: &aid, Action: "T-RW-01", TargetType: "reward_reservation", TargetID: res.ID, ToState: ResActive}); err != nil {
				return idempotency.Result{}, err
			}
			if err := reservationChanged(ctx, tx, res, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := loyaltyChanged(ctx, tx, estID, account.AccountID, now); err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusCreated, Body: hs.Data[reservationView]{Data: res.customerView()}}, nil
		})
}

// lockReservation trava, na ordem do módulo (saldo da conta → reserva), uma reserva DESTA loja.
// accountID != nil restringe ao titular (outra conta → 404).
func lockReservation(ctx context.Context, tx pgx.Tx, estID, id uuid.UUID, accountID *uuid.UUID) (reservationRow, error) {
	r, err := scanReservation(tx.QueryRow(ctx, reservationSelect+` WHERE r.establishment_id = $1 AND r.id = $2`, estID, id))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && accountID != nil && r.AccountID != *accountID) {
		return r, hs.ErrNotFound()
	}
	if err != nil {
		return r, err
	}
	if _, _, err := lockAccount(ctx, tx, estID, r.AccountID); err != nil {
		return r, err
	}
	return scanReservation(tx.QueryRow(ctx, reservationSelect+` WHERE r.id = $1 FOR UPDATE OF r`, id))
}

func versionBody(r *http.Request, withReason bool) (int64, string, []byte, error) {
	var body struct {
		ExpectedVersion *int64  `json:"expected_version"`
		Reason          *string `json:"reason"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return 0, "", nil, err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", body.ExpectedVersion)
	reason := ""
	if withReason {
		v.Required("/reason", body.Reason == nil)
		if body.Reason != nil {
			var fe *hs.FieldError
			reason, fe = validate.Text("/reason", *body.Reason, 3, 500)
			v.Add(fe)
		}
	} else if body.Reason != nil {
		v.Add(&hs.FieldError{Field: "/reason", Code: hs.FieldNotAllowed})
	}
	return expected, reason, raw, v.Err()
}

func checkState(r reservationRow, expected int64, now time.Time, want string) error {
	if r.Status != want {
		code := "invalid_transition"
		if r.Status == ResExpired {
			code = "reservation_expired"
		}
		return conflict(code, "Reserva já encerrada", "Esta reserva não está mais "+map[string]string{
			ResActive: "ativa", ResDelivered: "entregue"}[want]+".", r.customerView())
	}
	if r.Version != expected {
		return conflict("version_conflict", "A reserva mudou", "Confira a situação atual e tente de novo.", r.customerView())
	}
	return nil
}

// cancelReservation (T-RW-02): o titular cancela (também com o programa desligado). Libera
// pontos e unidade exatamente uma vez. Vencida pelo horário: vence agora (mesmo efeito).
func (s *Service) cancelReservation(w http.ResponseWriter, r *http.Request) error {
	estID, account, err := s.customer(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "reservationId")
	if err != nil {
		return err
	}
	expected, _, raw, err := versionBody(r, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	acc := account.AccountID
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + acc.String(), Operation: "cancelRewardReservation", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			res, err := lockReservation(ctx, tx, estID, id, &acc)
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			if err := checkState(res, expected, now, ResActive); err != nil {
				return idempotency.Result{}, err
			}
			to, action, by := ResCancelled, "T-RW-02", "customer"
			if !now.Before(res.ExpiresAt) {
				to, action, by = ResExpired, "T-RW-03", ""
			}
			if err := s.release(ctx, tx, &res, to, by, action, now); err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[reservationView]{Data: res.customerView()}}, nil
		})
}

// staff exige vínculo de equipe ativo na loja (profissional da loja ou dono).
func (s *Service) staff(r *http.Request) (uuid.UUID, acesso.Actor, error) {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return estID, acesso.Actor{}, err
	}
	actor, err := s.Auth.Member(r, estID)
	return estID, actor, err
}

func role(a acesso.Actor) (string, string) {
	if a.IsOwner() {
		return acesso.RoleOwner, auditoria.ActorOwner
	}
	return acesso.RoleProfessional, auditoria.ActorProfessional
}

// deliverReservation (T-RW-04): a equipe confirma a entrega presencial ANTES do vencimento
// (conferido no banco, na mesma transação que trava a reserva): consome pontos e a unidade.
func (s *Service) deliverReservation(w http.ResponseWriter, r *http.Request) error {
	estID, actor, err := s.staff(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "reservationId")
	if err != nil {
		return err
	}
	expected, _, raw, err := versionBody(r, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "deliverRewardReservation", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			res, err := lockReservation(ctx, tx, estID, id, nil)
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			if err := checkState(res, expected, now, ResActive); err != nil {
				return idempotency.Result{}, err
			}
			if !now.Before(res.ExpiresAt) {
				return idempotency.Result{}, conflict("reservation_expired", "Reserva vencida",
					"Esta reserva venceu (24 h) e os pontos voltaram para o cliente. Ele pode reservar de novo.", res.staffView())
			}
			roleName, actorKind := role(actor)
			if err := tx.QueryRow(ctx, `UPDATE reward_reservation SET status = 'delivered', resolved_at = $2, delivered_by_role = $3,
				delivered_by_account_id = $4, version = version + 1 WHERE id = $1 AND status = 'active' RETURNING version`,
				id, now, roleName, actor.AccountID).Scan(&res.Version); err != nil {
				return idempotency.Result{}, err
			}
			res.Status, res.ResolvedAt = ResDelivered, &now
			cost := int64(res.AcceptedTerms.CostPoints)
			if _, err := post(ctx, tx, entry{EstablishmentID: estID, AccountID: res.AccountID, Kind: KindConsume, Balance: -cost,
				Held: -cost, SourceType: "reservation", SourceID: id}, now); err != nil {
				return idempotency.Result{}, err
			}
			aid := actor.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: actorKind, ActorID: &aid,
				Action: "T-RW-04", TargetType: "reward_reservation", TargetID: id, FromState: ResActive, ToState: ResDelivered}); err != nil {
				return idempotency.Result{}, err
			}
			if err := reservationChanged(ctx, tx, res, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := loyaltyChanged(ctx, tx, estID, res.AccountID, now); err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[reservationView]{Data: res.staffView()}}, nil
		})
}

// revertDelivery (T-RW-05): o dono corrige uma entrega registrada por engano, com motivo.
// Devolve os pontos (consume_reversal) e a unidade, uma vez.
func (s *Service) revertDelivery(w http.ResponseWriter, r *http.Request) error {
	estID, actor, err := s.owner(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "reservationId")
	if err != nil {
		return err
	}
	expected, reason, raw, err := versionBody(r, true)
	if err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "revertRewardDelivery", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			res, err := lockReservation(ctx, tx, estID, id, nil)
			if err != nil {
				return idempotency.Result{}, err
			}
			now := s.Clock.Now()
			if err := checkState(res, expected, now, ResDelivered); err != nil {
				return idempotency.Result{}, err
			}
			if err := tx.QueryRow(ctx, `UPDATE reward_reservation SET status = 'delivery_reverted', reverted_reason = $2,
				version = version + 1 WHERE id = $1 AND status = 'delivered' RETURNING version`, id, reason).Scan(&res.Version); err != nil {
				return idempotency.Result{}, err
			}
			res.Status = ResReverted
			cost := int64(res.AcceptedTerms.CostPoints)
			if _, err := post(ctx, tx, entry{EstablishmentID: estID, AccountID: res.AccountID, Kind: KindConsumeReversal, Balance: cost,
				SourceType: "reservation", SourceID: id, Note: reason}, now); err != nil {
				return idempotency.Result{}, err
			}
			if res.Limited {
				if _, err := tx.Exec(ctx, `INSERT INTO reward_stock_movement (reward_id, kind, delta, source_id, occurred_at)
					VALUES ($1, 'delivery_reverted', 1, $2, $3) ON CONFLICT DO NOTHING`, res.RewardID, id, now); err != nil {
					return idempotency.Result{}, err
				}
			}
			aid := actor.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
				ActorID: &aid, Action: "T-RW-05", TargetType: "reward_reservation", TargetID: id, FromState: ResDelivered,
				ToState: ResReverted, Reason: reason}); err != nil {
				return idempotency.Result{}, err
			}
			if err := reservationChanged(ctx, tx, res, now); err != nil {
				return idempotency.Result{}, err
			}
			if err := loyaltyChanged(ctx, tx, estID, res.AccountID, now); err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[reservationView]{Data: res.staffView()}}, nil
		})
}

var reservationStatuses = map[string]bool{ResActive: true, ResCancelled: true, ResExpired: true, ResDelivered: true, ResReverted: true}

// listReservations: equipe da loja (profissional vinculado ou dono) vê as reservas para entregar.
func (s *Service) listReservations(w http.ResponseWriter, r *http.Request) error {
	estID, _, err := s.staff(r)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	if status != "" && !reservationStatuses[status] {
		return hs.ErrValidation(hs.FieldError{Field: "status", Code: hs.FieldNotAllowed})
	}
	limit, after, afterID, err := pagination(r)
	if err != nil {
		return err
	}
	sql := reservationSelect + ` WHERE r.establishment_id = $1 AND ($2 = '' OR r.status = $2)`
	args := []any{estID, status}
	if after != nil {
		args = append(args, *after, afterID)
		sql += ` AND (r.created_at, r.id) < ($3, $4)`
	}
	args = append(args, limit+1)
	sql += ` ORDER BY r.created_at DESC, r.id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (reservationRow, error) { return scanReservation(row) })
	if err != nil {
		return err
	}
	page := pageInfo{}
	if len(list) > limit {
		list = list[:limit]
		c := encodeCursor(list[len(list)-1].CreatedAt, list[len(list)-1].ID)
		page = pageInfo{NextCursor: &c, HasMore: true}
	}
	views := make([]reservationView, 0, len(list))
	for _, x := range list {
		views = append(views, x.staffView())
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []reservationView `json:"data"`
		Page pageInfo          `json:"page"`
	}{views, page})
	return nil
}

// ExpireDue vence as reservas cujo prazo passou (T-RW-03): libera pontos e unidade uma vez. Pode
// rodar em mais de um worker (SKIP LOCKED + mudança condicional).
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	rows, err := s.Pool.Query(ctx, `SELECT id, establishment_id FROM reward_reservation WHERE status = 'active' AND expires_at <= $1
		ORDER BY expires_at LIMIT 50`, now)
	if err != nil {
		return 0, err
	}
	type due struct{ id, est uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		err := r.Scan(&d.id, &d.est)
		return d, err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
			res, err := lockReservation(ctx, tx, d.est, d.id, nil)
			if err != nil {
				return err
			}
			if res.Status != ResActive || now.Before(res.ExpiresAt) {
				return nil // entregue ou cancelada antes (corrida resolvida pela trava)
			}
			n++
			return s.release(ctx, tx, &res, ResExpired, "", "T-RW-03", now)
		})
		if err != nil {
			s.Log.Error("falha ao vencer reserva de prêmio", "reserva", d.id.String(), "erro", err.Error())
		}
	}
	return n, nil
}

// Run vence reservas a cada intervalo até ctx terminar.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.ExpireDue(ctx); err != nil && ctx.Err() == nil {
			s.Log.Error("vencimento de reservas falhou", "erro", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
