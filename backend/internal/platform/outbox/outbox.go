// Package outbox grava eventos de mudança na tabela outbox, DENTRO da transação do comando
// (ADR-0010). Se o comando desfaz, o evento desaparece junto: nenhum evento anuncia algo que não
// foi gravado. O publicador (que copia para o event_log com cursor) e o SSE são da etapa 06.
//
// Cada evento tem UMA audiência e já leva a visão projetada para ela (docs/contrato/eventos-sse.md
// §3): quem monta o evento decide o que aquela audiência pode ver; a entrega só filtra.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/database"
)

// Audiências (AudienceKind).
const (
	Public       = "public"
	Visit        = "visit"
	Account      = "account"
	Professional = "professional"
	Owner        = "owner"
)

// Audience diz para quem o evento é. Visit/Account levam o dono da visita (conta OU visitante);
// Professional leva o profissional que pode receber.
type Audience struct {
	Kind           string
	ProfessionalID *uuid.UUID
	AccountID      *uuid.UUID
	VisitorID      *uuid.UUID
}

// Event é uma mudança já gravada, para uma audiência.
type Event struct {
	EstablishmentID uuid.UUID
	Type            string // EventType, ex.: "order.updated"
	ResourceType    string // ex.: "order"
	ResourceID      uuid.UUID
	ResourceVersion int64
	Audience        Audience
	Data            any // visão completa do recurso para a audiência (mesmo esquema do GET)
	OccurredAt      time.Time
}

// Record grava os eventos na transação q. Deve ser chamado depois de a mudança ser escrita, na
// mesma transação (nunca com o pool fora dela).
func Record(ctx context.Context, q database.Querier, events ...Event) error {
	for _, e := range events {
		payload, err := json.Marshal(e.Data)
		if err != nil {
			return fmt.Errorf("evento %s: %w", e.Type, err)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO outbox (establishment_id, audience, professional_id, account_id, visitor_id, type,
			                    resource_type, resource_id, resource_version, payload, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			e.EstablishmentID, e.Audience.Kind, e.Audience.ProfessionalID, e.Audience.AccountID, e.Audience.VisitorID,
			e.Type, e.ResourceType, e.ResourceID, e.ResourceVersion, payload, e.OccurredAt); err != nil {
			return fmt.Errorf("gravar evento %s: %w", e.Type, err)
		}
	}
	return nil
}

// ForVisitAndStaff repete o mesmo evento para as três audiências de uma visita: o dono da visita,
// o profissional do atendimento e o dono da loja. staffData pode diferir de visitData (ex.: a
// equipe vê o tipo de identidade do cliente).
func ForVisitAndStaff(base Event, visitor Audience, professionalID uuid.UUID, visitData, staffData any) []Event {
	v, p, o := base, base, base
	v.Audience, v.Data = visitor, visitData
	pid := professionalID
	p.Audience, p.Data = Audience{Kind: Professional, ProfessionalID: &pid}, staffData
	o.Audience, o.Data = Audience{Kind: Owner}, staffData
	return []Event{v, p, o}
}
