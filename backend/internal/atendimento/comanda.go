package atendimento

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/platform/database"
)

// Estados da comanda (OrderStatus) e motivos de encerramento sem cobrança (ClosureReason).
const (
	StatusOpen           = "open"
	StatusClosedNoCharge = "closed_no_charge"

	ClosedByCustomer = "cancelled_by_customer"
	ClosedByOwner    = "cancelled_by_owner"
	ClosedNoShow     = "no_show"
)

// pendingStatuses bloqueiam nova entrada da mesma identidade na loja (R-FILA-03). Aguardando só
// a conferência da equipe (awaiting_confirmation) NÃO bloqueia (detalhe da etapa 00).
var pendingStatuses = []string{"open", "pending_review", "awaiting_payment"}

// ErrNotOpen indica comanda que não está mais aberta.
var ErrNotOpen = errors.New("comanda não está aberta")

// Identity identifica o dono da comanda (exatamente um dos dois).
type Identity struct {
	AccountID *uuid.UUID
	VisitorID *uuid.UUID
}

// NewOrder são os dados de uma comanda criada na entrada da fila (T-O-01).
type NewOrder struct {
	ID              uuid.UUID
	EstablishmentID uuid.UUID
	ParticipationID uuid.UUID
	Identity        Identity
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
		INSERT INTO service_order (id, establishment_id, participation_id, account_id, visitor_id, total_cents, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		in.ID, in.EstablishmentID, in.ParticipationID, in.Identity.AccountID, in.Identity.VisitorID, total, in.Now); err != nil {
		return 0, fmt.Errorf("criar comanda: %w", err)
	}
	for _, s := range in.Services {
		if _, err := q.Exec(ctx, `
			INSERT INTO order_item (establishment_id, order_id, service_id, name, unit_price_cents, duration_minutes, points, added_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			in.EstablishmentID, in.ID, s.ID, s.Name, s.PriceCents, s.DurationMinutes, s.Points, in.Now); err != nil {
			return 0, fmt.Errorf("criar item da comanda: %w", err)
		}
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action = &in.EstablishmentID, in.Now, "T-O-01"
	audit.TargetType, audit.TargetID, audit.ToState = "order", in.ID, StatusOpen
	return total, auditoria.Record(ctx, q, audit)
}

// CloseNoCharge encerra a comanda aberta sem cobrança (T-O-05): sem recebimento fictício e sem
// pontos. Usado em desistência, cancelamento pelo dono e ausência.
func CloseNoCharge(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, reason string, now time.Time, audit auditoria.Event) error {
	tag, err := q.Exec(ctx, `
		UPDATE service_order SET status = 'closed_no_charge', closure_reason = $3, closed_at = $4,
		       version = version + 1, updated_at = $4
		WHERE establishment_id = $1 AND id = $2 AND status = 'open'`, estID, orderID, reason, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotOpen
	}
	audit.EstablishmentID, audit.OccurredAt, audit.Action = &estID, now, "T-O-05"
	audit.TargetType, audit.TargetID, audit.FromState, audit.ToState = "order", orderID, StatusOpen, StatusClosedNoCharge
	return auditoria.Record(ctx, q, audit)
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

// Summary é o resumo da comanda usado pela fila: nomes dos serviços e duração estimada.
type Summary struct {
	ServiceNames    []string
	DurationMinutes int
}

// Summaries devolve o resumo dos itens não retirados de cada comanda.
func Summaries(ctx context.Context, q database.Querier, orderIDs []uuid.UUID) (map[uuid.UUID]Summary, error) {
	out := make(map[uuid.UUID]Summary, len(orderIDs))
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT order_id, array_agg(name ORDER BY added_at, name), sum(duration_minutes)::int
		FROM order_item WHERE order_id = ANY($1) AND status <> 'removed'
		GROUP BY order_id`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var s Summary
		if err := rows.Scan(&id, &s.ServiceNames, &s.DurationMinutes); err != nil {
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
