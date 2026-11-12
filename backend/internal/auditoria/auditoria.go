package auditoria

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/database"
)

// Tipos de ator (enum AuditEvent.actor.kind do contrato).
const (
	ActorVisitor      = "visitor"
	ActorCustomer     = "customer"
	ActorProfessional = "professional"
	ActorOwner        = "owner"
	ActorSystem       = "system"
	ActorOperator     = "operator"
)

// Event é um registro de auditoria. Nunca coloque e-mail, nick, telefone ou segredo aqui:
// o ator e o alvo são identificados só por UUID (pseudônimo interno).
type Event struct {
	EstablishmentID *uuid.UUID
	OccurredAt      time.Time
	ActorKind       string
	ActorID         *uuid.UUID
	Action          string // código da transição, ex.: "T-SS-01"
	TargetType      string // ex.: "account", "session", "invite"
	TargetID        uuid.UUID
	FromState       string
	ToState         string
	Reason          string
	RequestID       string
}

// Record grava o evento dentro da transação do comando (a auditoria só existe se o efeito existir).
func Record(ctx context.Context, q database.Querier, e Event) error {
	_, err := q.Exec(ctx, `
		INSERT INTO audit_event (establishment_id, occurred_at, actor_kind, actor_id, action,
		                         target_type, target_id, from_state, to_state, reason, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, ''), nullif($9, ''), nullif($10, ''), nullif($11, ''))`,
		e.EstablishmentID, e.OccurredAt, e.ActorKind, e.ActorID, e.Action, e.TargetType, e.TargetID,
		e.FromState, e.ToState, e.Reason, e.RequestID)
	if err != nil {
		return fmt.Errorf("gravar auditoria %s: %w", e.Action, err)
	}
	return nil
}

// Ptr devolve o endereço de um UUID (atalho para campos opcionais).
func Ptr(id uuid.UUID) *uuid.UUID { return &id }
