package fila

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// Tipos de evento gravados por este módulo (EventType).
const (
	evParticipation = "participation.updated"
	evQueuePublic   = "queue.public.updated"
	evQueueStaff    = "queue.updated"
)

// recordParticipation grava na outbox a participação p (já atualizada) para o dono da visita
// (sem o tipo de identidade), o profissional da fila e o dono da loja (ADR-0010).
func (s *Service) recordParticipation(ctx context.Context, tx pgx.Tx, p row) error {
	forVisit, err := s.visit(ctx, tx, p, false)
	if err != nil {
		return err
	}
	forStaff, err := s.visit(ctx, tx, p, true)
	if err != nil {
		return err
	}
	base := outbox.Event{EstablishmentID: p.EstablishmentID, Type: evParticipation, ResourceType: "participation",
		ResourceID: p.ID, ResourceVersion: p.Version, OccurredAt: s.Clock.Now()}
	aud := outbox.Audience{Kind: outbox.Visit, AccountID: p.AccountID, VisitorID: p.VisitorID}
	return outbox.Record(ctx, tx, outbox.ForVisitAndStaff(base, aud, forStaff.Participation.ProfessionalID,
		forVisit.Participation, forStaff.Participation)...)
}

// QueueChanged implementa atendimento.Visits: sobe o contador de atividade da fila e grava a
// visão pública (audiência public) e a da equipe (professional da fila e owner). A versão do
// evento é versão da fila + atividade (as duas só crescem; ver migração 00010).
func (s *Service) QueueChanged(ctx context.Context, tx pgx.Tx, estID, queueID uuid.UUID) error {
	var seq int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO queue_counter (queue_id, activity_seq) VALUES ($1, 1)
		ON CONFLICT (queue_id) DO UPDATE SET activity_seq = queue_counter.activity_seq + 1
		RETURNING activity_seq`, queueID).Scan(&seq); err != nil {
		return err
	}
	now := s.Clock.Now()
	v, err := estabelecimento.QueueEventViews(ctx, tx, s, estID, queueID, now)
	if err != nil {
		return err
	}
	base := outbox.Event{EstablishmentID: estID, ResourceType: "queue", ResourceID: queueID, ResourceVersion: v.Version + seq, OccurredAt: now}
	pub, prof, own := base, base, base
	pub.Type, pub.Audience, pub.Data = evQueuePublic, outbox.Audience{Kind: outbox.Public}, v.Public
	pid := v.ProfessionalID
	prof.Type, prof.Audience, prof.Data = evQueueStaff, outbox.Audience{Kind: outbox.Professional, ProfessionalID: &pid}, v.Staff
	own.Type, own.Audience, own.Data = evQueueStaff, outbox.Audience{Kind: outbox.Owner}, v.Staff
	return outbox.Record(ctx, tx, pub, prof, own)
}

// LockVisit implementa atendimento.Visits: trava fila → participação (ADR-0019). Se a
// participação mudou de fila no meio (transferência simultânea), responde 409 para repetir.
func (s *Service) LockVisit(ctx context.Context, tx pgx.Tx, estID, participationID uuid.UUID) (atendimento.VisitState, error) {
	p, _, err := s.lockForCommand(ctx, tx, estID, participationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return atendimento.VisitState{}, hs.ErrNotFound()
	}
	if err != nil {
		return atendimento.VisitState{}, err
	}
	if p.Version == -1 {
		return atendimento.VisitState{}, conflict("version_conflict", "A visita mudou",
			"O cliente foi transferido enquanto você alterava. Tente de novo.", nil)
	}
	return atendimento.VisitState{ParticipationID: p.ID, QueueID: p.QueueID, Status: p.Status}, nil
}

var _ atendimento.Visits = (*Service)(nil)

// Statuses implementa atendimento.Visits (histórico do cliente).
func (s *Service) Statuses(ctx context.Context, q database.Querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return Statuses(ctx, q, ids)
}
