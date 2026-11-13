package atendimento

import (
	"context"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/database"
	"fila/internal/platform/outbox"
)

// Tipos de evento gravados por este módulo (EventType).
const (
	evOrder         = "order.updated"
	evPayment       = "payment.updated"
	evChangeRequest = "change_request.updated"
)

// recordOrder grava na outbox a visão atual da comanda para o dono da visita, o profissional do
// atendimento e o dono da loja — na transação q, depois da mudança (ADR-0010).
func recordOrder(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, now time.Time, types ...string) error {
	o, err := getOrder(ctx, q, estID, orderID, false)
	if err != nil {
		return err
	}
	view, err := LoadOrder(ctx, q, estID, orderID)
	if err != nil {
		return err
	}
	var events []outbox.Event
	for _, t := range types {
		base := outbox.Event{EstablishmentID: estID, Type: t, ResourceType: "order", ResourceID: orderID,
			ResourceVersion: view.Version, OccurredAt: now}
		events = append(events, outbox.ForVisitAndStaff(base, o.Identity.audience(), o.ProfessionalID, view, view)...)
	}
	return outbox.Record(ctx, q, events...)
}

// recordChangeRequest grava a solicitação (com o estado atual) para as três audiências da visita.
func recordChangeRequest(ctx context.Context, q database.Querier, estID, orderID, crID uuid.UUID, now time.Time) error {
	o, err := getOrder(ctx, q, estID, orderID, false)
	if err != nil {
		return err
	}
	list, err := changeRequests(ctx, q, orderID)
	if err != nil {
		return err
	}
	for _, cr := range list {
		if cr.ID != crID {
			continue
		}
		base := outbox.Event{EstablishmentID: estID, Type: evChangeRequest, ResourceType: "change_request", ResourceID: crID,
			ResourceVersion: cr.version, OccurredAt: now}
		return outbox.Record(ctx, q, outbox.ForVisitAndStaff(base, o.Identity.audience(), o.ProfessionalID, cr, cr)...)
	}
	return nil
}
