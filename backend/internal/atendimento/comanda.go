package atendimento

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/platform/database"
	"fila/internal/platform/outbox"
)

// Estados da comanda (OrderStatus), dos itens (OrderItemStatus), das solicitações
// (ChangeRequestStatus) e motivos de encerramento sem cobrança (ClosureReason).
const (
	StatusOpen            = "open"
	StatusPendingReview   = "pending_review"
	StatusAwaitingPayment = "awaiting_payment"
	StatusClosedNoCharge  = "closed_no_charge"

	ItemAccepted     = "accepted"
	ItemPerformed    = "performed"
	ItemNotPerformed = "not_performed"
	ItemRemoved      = "removed"

	CRPending  = "pending"
	CRApproved = "approved"
	CRRejected = "rejected"
	CRVoided   = "voided"

	ClosedByCustomer      = "cancelled_by_customer"
	ClosedByOwner         = "cancelled_by_owner"
	ClosedNoShow          = "no_show"
	ClosedInterruptedZero = "interruption_zero_total"
	ClosedAdjustedZero    = "adjustment_zero_total"

	maxItems = 10
)

// pendingStatuses bloqueiam nova entrada da mesma identidade na loja (R-FILA-03). Aguardando só
// a conferência da equipe (awaiting_confirmation) NÃO bloqueia (detalhe da etapa 00).
var pendingStatuses = []string{StatusOpen, StatusPendingReview, StatusAwaitingPayment}

// ErrNotOpen indica comanda que não está mais aberta.
var ErrNotOpen = errors.New("comanda não está aberta")

// Identity identifica o dono da comanda (exatamente um dos dois).
type Identity struct {
	AccountID *uuid.UUID
	VisitorID *uuid.UUID
}

func (i Identity) audience() outbox.Audience {
	return outbox.Audience{Kind: outbox.Visit, AccountID: i.AccountID, VisitorID: i.VisitorID}
}

// orderRow é a comanda lida do banco (sem itens).
type orderRow struct {
	ID              uuid.UUID
	EstablishmentID uuid.UUID
	ParticipationID uuid.UUID
	ProfessionalID  uuid.UUID
	Identity        Identity
	Status          string
	TotalCents      int64
	Version         int64
}

const orderSelect = `
	SELECT id, establishment_id, participation_id, professional_id, account_id, visitor_id, status, total_cents, version
	FROM service_order`

func scanOrder(r pgx.Row) (orderRow, error) {
	var o orderRow
	err := r.Scan(&o.ID, &o.EstablishmentID, &o.ParticipationID, &o.ProfessionalID, &o.Identity.AccountID, &o.Identity.VisitorID,
		&o.Status, &o.TotalCents, &o.Version)
	return o, err
}

// getOrder lê a comanda DESTA loja; lock trava a linha (sempre DEPOIS de fila e participação).
func getOrder(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, lock bool) (orderRow, error) {
	sql := orderSelect + ` WHERE establishment_id = $1 AND id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanOrder(q.QueryRow(ctx, sql, estID, orderID))
}

// itemRow é um item da comanda.
type itemRow struct {
	ID         uuid.UUID
	ServiceID  uuid.UUID
	Name       string
	PriceCents int64
	Status     string
	Charged    bool
}

// liveItems devolve os itens não retirados, em ordem de inclusão.
func liveItems(ctx context.Context, q database.Querier, orderID uuid.UUID) ([]itemRow, error) {
	rows, err := q.Query(ctx, `
		SELECT id, service_id, name, unit_price_cents, status, charged
		FROM order_item WHERE order_id = $1 AND status <> 'removed' ORDER BY added_at, name, id`, orderID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (itemRow, error) {
		var it itemRow
		err := r.Scan(&it.ID, &it.ServiceID, &it.Name, &it.PriceCents, &it.Status, &it.Charged)
		return it, err
	})
}

// versionTag descreve estado e versão na auditoria (ex.: "open@3"): a correção registra de qual
// versão para qual versão a comanda passou (C-CMD-07).
func versionTag(status string, version int64) string {
	return status + "@" + strconv.FormatInt(version, 10)
}

// insertItem grava um item com o snapshot do catálogo (R-CMD-05).
func insertItem(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, s catalogo.Snapshot, now time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO order_item (establishment_id, order_id, service_id, name, unit_price_cents, duration_minutes, points, added_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		estID, orderID, s.ID, s.Name, s.PriceCents, s.DurationMinutes, s.Points, now).Scan(&id)
	if err != nil {
		return id, fmt.Errorf("criar item da comanda: %w", err)
	}
	return id, nil
}

// NewOrder são os dados de uma comanda criada na entrada da fila (T-O-01).
type NewOrder struct {
	ID              uuid.UUID
	EstablishmentID uuid.UUID
	ParticipationID uuid.UUID
	ProfessionalID  uuid.UUID
	Identity        Identity
	// LoyaltyEligible é o snapshot de elegibilidade de pontos da entrada (R-FID-03).
	LoyaltyEligible bool
	// Services já validados (ativos, desta loja, habilitados); o preço vem do catálogo, nunca do cliente.
	Services []catalogo.Snapshot
	Now      time.Time
}

// CreateOrder grava a comanda aberta com uma cópia de cada serviço e devolve o total em centavos,
// calculado aqui a partir do catálogo (R-CMD-02). Deve rodar na transação da entrada.
func CreateOrder(ctx context.Context, q database.Querier, in NewOrder, audit auditoria.Event) (int64, error) {
	var total int64
	for _, s := range in.Services {
		total += s.PriceCents
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO service_order (id, establishment_id, participation_id, professional_id, account_id, visitor_id,
		                           loyalty_eligible, total_cents, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		in.ID, in.EstablishmentID, in.ParticipationID, in.ProfessionalID, in.Identity.AccountID, in.Identity.VisitorID,
		in.LoyaltyEligible, total, in.Now); err != nil {
		return 0, fmt.Errorf("criar comanda: %w", err)
	}
	for _, s := range in.Services {
		if _, err := insertItem(ctx, q, in.EstablishmentID, in.ID, s, in.Now); err != nil {
			return 0, err
		}
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action = &in.EstablishmentID, in.Now, "T-O-01"
	audit.TargetType, audit.TargetID, audit.ToState = "order", in.ID, versionTag(StatusOpen, 1)
	if err := auditoria.Record(ctx, q, audit); err != nil {
		return 0, err
	}
	return total, recordOrder(ctx, q, in.EstablishmentID, in.ID, in.Now, evOrder)
}

// CloseNoCharge encerra a comanda aberta sem cobrança (T-O-05): valor fechado em zero, sem
// recebimento fictício e sem pontos. Usado em desistência, cancelamento pelo dono e ausência.
// Uma solicitação pendente deixa de valer (T-CR-04).
func CloseNoCharge(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, reason string, now time.Time, audit auditoria.Event) error {
	var version int64
	err := q.QueryRow(ctx, `
		UPDATE service_order SET status = 'closed_no_charge', closure_reason = $3, closed_at = $4, total_cents = 0,
		       total_frozen_at = $4, version = version + 1, updated_at = $4
		WHERE establishment_id = $1 AND id = $2 AND status = 'open' RETURNING version`, estID, orderID, reason, now).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotOpen
	}
	if err != nil {
		return err
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action = &estID, now, "T-O-05"
	audit.TargetType, audit.TargetID = "order", orderID
	audit.FromState, audit.ToState = versionTag(StatusOpen, version-1), versionTag(StatusClosedNoCharge, version)
	if err := auditoria.Record(ctx, q, audit); err != nil {
		return err
	}
	if err := voidPending(ctx, q, estID, orderID, now, audit); err != nil {
		return err
	}
	return recordOrder(ctx, q, estID, orderID, now, evOrder, evPayment)
}

// voidPending anula a solicitação pendente (T-CR-04, ação do sistema), se houver.
func voidPending(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, now time.Time, audit auditoria.Event) error {
	var crID uuid.UUID
	err := q.QueryRow(ctx, `
		UPDATE change_request SET status = 'voided', decided_at = $3, version = version + 1
		WHERE establishment_id = $1 AND order_id = $2 AND status = 'pending' RETURNING id`, estID, orderID, now).Scan(&crID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action = &estID, now, "T-CR-04"
	audit.ActorKind, audit.ActorID = auditoria.ActorSystem, nil
	audit.TargetType, audit.TargetID, audit.FromState, audit.ToState, audit.Reason = "change_request", crID, CRPending, CRVoided, ""
	if err := auditoria.Record(ctx, q, audit); err != nil {
		return err
	}
	return recordChangeRequest(ctx, q, estID, orderID, crID, now)
}

// Reassign muda o profissional do atendimento (transferência T-P-07). A visão da comanda não
// muda, então a versão também não.
func Reassign(ctx context.Context, q database.Querier, estID, orderID, professionalID uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE service_order SET professional_id = $3 WHERE establishment_id = $1 AND id = $2`,
		estID, orderID, professionalID)
	return err
}

// PendingOrderExists informa se a identidade tem comanda pendente na loja (R-FILA-03).
func PendingOrderExists(ctx context.Context, q database.Querier, estID uuid.UUID, id Identity) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM service_order
		               WHERE establishment_id = $1 AND status = ANY($4)
		                 AND (account_id = $2 OR visitor_id = $3))`,
		estID, id.AccountID, id.VisitorID, pendingStatuses).Scan(&exists)
	return exists, err
}

// Summary é o resumo da comanda usado pela fila: nomes dos serviços, duração estimada e se há
// solicitação de alteração esperando a equipe.
type Summary struct {
	ServiceNames            []string
	DurationMinutes         int
	HasPendingChangeRequest bool
}

// Summaries devolve o resumo dos itens não retirados de cada comanda.
func Summaries(ctx context.Context, q database.Querier, orderIDs []uuid.UUID) (map[uuid.UUID]Summary, error) {
	out := make(map[uuid.UUID]Summary, len(orderIDs))
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT i.order_id, array_agg(i.name ORDER BY i.added_at, i.name), sum(i.duration_minutes)::int,
		       EXISTS (SELECT 1 FROM change_request c WHERE c.order_id = i.order_id AND c.status = 'pending')
		FROM order_item i WHERE i.order_id = ANY($1) AND i.status <> 'removed'
		GROUP BY i.order_id`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var s Summary
		if err := rows.Scan(&id, &s.ServiceNames, &s.DurationMinutes, &s.HasPendingChangeRequest); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// ServiceIDs devolve os serviços aceitos (não retirados) da comanda (usado na transferência).
func ServiceIDs(ctx context.Context, q database.Querier, orderID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT service_id FROM order_item WHERE order_id = $1 AND status <> 'removed'`, orderID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
