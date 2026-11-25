package agenda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/platform/database"
)

// CheckChairUse implementa fila.ChairGuard: o uso [From, From + tolerância + serviços + margem)
// não pode invadir compromisso (reserva confirmada ou já na cadeira) nem bloqueio ativo do
// profissional. A reserva da própria visita não conta. Exemplo (ADR-0038): agora 14h20, reserva
// às 15h, tolerância 10, limpeza 5 → um corte de 25 cabe (14h20 + 10 + 25 + 5 = 15h).
func (s *Service) CheckChairUse(ctx context.Context, q database.Querier, estID uuid.UUID, use fila.ChairUse) error {
	st, err := estabelecimento.LoadSettings(ctx, q, estID)
	if err != nil {
		return err
	}
	need := use.ToleranceMinutes + use.ServiceMinutes + st.BufferMinutes
	iv := Interval{use.From, use.From.Add(time.Duration(need) * time.Minute)}
	var own *uuid.UUID
	if use.ParticipationID != nil {
		var id uuid.UUID
		err := q.QueryRow(ctx, `SELECT id FROM appointment WHERE participation_id = $1`, *use.ParticipationID).Scan(&id)
		if err == nil {
			own = &id
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	var start time.Time
	var kind string
	err = q.QueryRow(ctx, `
		SELECT lower(period), kind FROM agenda_occupancy
		WHERE professional_id = $1 AND released_at IS NULL AND period && tstzrange($2, $3, '[)')
		  AND ($4::uuid IS NULL OR appointment_id IS DISTINCT FROM $4) AND (hold_until IS NULL OR hold_until > $5)
		ORDER BY lower(period) LIMIT 1`, use.ProfessionalID, iv.Start, iv.End, own, s.Clock.Now()).Scan(&start, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, loc, err := storeLoc(ctx, q, estID)
	if err != nil {
		return err
	}
	return chairReserved(use, st.BufferMinutes, start, kind, loc)
}

// chairReserved monta a recusa com o motivo em português (o cliente não muda de lugar).
func chairReserved(use fila.ChairUse, buffer int, next time.Time, kind string, loc *time.Location) error {
	free := max(int(next.Sub(use.From).Minutes()), 0)
	when := hora(next, loc)
	what := "Próximo horário marcado às " + when + "."
	if kind == "block" {
		what = "A agenda deste profissional está bloqueada a partir das " + when + "."
	}
	svc := use.ServiceMinutes + buffer
	detail := fmt.Sprintf("%s Este serviço precisa de %d min com a limpeza", what, svc)
	if use.ToleranceMinutes > 0 {
		detail += fmt.Sprintf(", mais %d min de tolerância de chegada", use.ToleranceMinutes)
	}
	detail += fmt.Sprintf("; até lá há %d min. O cliente continua no mesmo lugar.", free)
	if use.ToleranceMinutes > 0 && svc <= free {
		detail += " Se ele já está aqui, use “Chamar e iniciar agora”."
	}
	return conflict("chair_reserved", "Não cabe antes do próximo horário", detail,
		map[string]any{"next_start": next.UTC(), "needed_minutes": use.ToleranceMinutes + svc, "available_minutes": free})
}

// CheckQueueEntry implementa fila.ChairGuard (modo "os dois").
func (s *Service) CheckQueueEntry(ctx context.Context, q database.Querier, estID uuid.UUID, qi estabelecimento.QueueInfo,
	st estabelecimento.Settings, serviceMinutes int, who *acesso.Identity, acknowledged bool) error {
	_, loc, err := storeLoc(ctx, q, estID)
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	day := DayOf(now, loc)
	prof := estabelecimento.ProfessionalRef{ID: qi.ProfessionalID, Name: qi.ProfessionalName, QueueID: qi.ID}
	in, err := loadPlanInput(ctx, q, estID, loc, day, now, st, estabelecimento.ModeHybrid, []uuid.UUID{prof.ID}, nil)
	if err != nil {
		return err
	}
	p, err := s.planDay(ctx, q, in, prof, serviceMinutes)
	if err != nil {
		return err
	}
	if len(p.Shifts) == 0 {
		return nil // sem jornada cadastrada hoje: a fila segue como na 1.0 (abrir/fechar decide)
	}
	if len(p.QueueTime) == 0 {
		return conflict("queue_no_capacity_today", "Hoje só com horário marcado",
			"Hoje "+qi.ProfessionalName+" atende só com horário marcado. Escolha um horário na agenda ou outro profissional.", nil)
	}
	if p.Projection == nil || p.Projection.Unplaced > 0 {
		return conflict("queue_no_capacity_today", "Sem vaga na fila até o fechamento",
			"Pela previsão de hoje, "+qi.ProfessionalName+" não consegue mais atender pela fila até o fim do expediente. Escolha outro profissional ou marque um horário.", nil)
	}
	mine := p.Projection.Placed[len(p.Projection.Placed)-1]
	if who == nil || acknowledged {
		return nil
	}
	var start time.Time
	err = q.QueryRow(ctx, `
		SELECT starts_at FROM appointment WHERE establishment_id = $1 AND status IN ('held', 'confirmed')
		  AND (account_id = $2 OR visitor_id = $2) AND starts_at >= $3 AND starts_at < $4
		ORDER BY starts_at LIMIT 1`, estID, who.ID(), now, mine.End).Scan(&start)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return conflict("queue_overlaps_own_appointment", "A espera passa do seu horário marcado",
		fmt.Sprintf("Pela previsão, o atendimento na fila vai até %s e você tem horário marcado às %s nesta loja. Você pode entrar na fila mesmo assim (a reserva continua) ou cancelar a reserva.",
			mine.End.In(loc).Format("15:04"), start.In(loc).Format("15:04")),
		map[string]any{"appointment_starts_at": start.UTC(), "estimated_end": mine.End.UTC()})
}

// VisitChanged implementa fila.ChairGuard: a reserva acompanha a visita na mesma transação
// (trava: fila → participação → reserva). Estados finais liberam a ocupação da cadeira.
func (s *Service) VisitChanged(ctx context.Context, q database.Querier, estID, participationID uuid.UUID, status string, at time.Time, reason string) error {
	a, err := loadAppointmentBy(ctx, q, `establishment_id = $1 AND participation_id = $2`, true, estID, participationID)
	if errors.Is(err, errNoAppointment) {
		return nil
	}
	if err != nil {
		return err
	}
	to, by, why := status, (*string)(nil), reason
	switch status {
	case fila.InService, fila.Completed, fila.Interrupted, fila.NoShow:
	case fila.Cancelled:
		to = StatusCancelled
		who := "store"
		if reason == fila.MirrorCancelledByCustomer {
			who, why = "customer", ""
		} else {
			why = strings.TrimPrefix(reason, fila.MirrorCancelledByStore)
		}
		by = &who
	default:
		return nil
	}
	if a.Status == to || terminal(a.Status) {
		return nil
	}
	var finished *time.Time
	if terminal(to) {
		finished = &at
	}
	var whyPtr *string
	if why != "" {
		whyPtr = &why
	}
	if _, err := q.Exec(ctx, `UPDATE appointment SET status = $2, cancelled_by = $3, finished_at = $4,
		end_reason = coalesce($5, end_reason), version = version + 1, updated_at = $6 WHERE id = $1`,
		a.ID, to, by, finished, whyPtr, at); err != nil {
		return err
	}
	if terminal(to) {
		if err := releaseOccupancy(ctx, q, a.ID, at); err != nil {
			return err
		}
	}
	actor := actorInfo{kind: "system"}
	return s.afterChange(ctx, q, a.EstablishmentID, a.ID, "T-AP-07", a.Status, actor, why, at)
}

func releaseOccupancy(ctx context.Context, q database.Querier, appointmentID uuid.UUID, at time.Time) error {
	_, err := q.Exec(ctx, `UPDATE agenda_occupancy SET released_at = $2 WHERE appointment_id = $1 AND released_at IS NULL`, appointmentID, at)
	return err
}
