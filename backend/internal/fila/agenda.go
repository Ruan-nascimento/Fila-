package fila

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/fidelidade"
	"fila/internal/platform/database"
)

// Integração com a agenda (ADR-0038). A fila não lê as tabelas da agenda: pergunta ao guarda de
// cadeira (implementado pelo módulo agenda) se um uso da cadeira invade um horário protegido, e
// avisa quando uma visita que veio de reserva muda de estado. A agenda, por sua vez, usa as funções
// exportadas abaixo para ler a carga da fila e para abrir a visita de uma reserva chamada.
//
// Ordem de travas (todos os caminhos): identidade (advisory) → fila(s) por id → participação →
// reserva → ocupação. Quem escreve ocupação de um profissional SEMPRE trava antes a fila dele.

// Origem da visita.
const (
	OriginQueue       = "queue"
	OriginAppointment = "appointment"
)

// ChairUse é um uso pretendido da cadeira do profissional: a partir de From, com tolerância de
// chegada (0 quando o atendimento começa já) e a duração dos serviços. A margem de preparação da
// loja é somada pelo guarda.
type ChairUse struct {
	ProfessionalID   uuid.UUID
	From             time.Time
	ToleranceMinutes int
	ServiceMinutes   int
	// ParticipationID: a visita que vai usar a cadeira (a reserva dela mesma não conta como conflito).
	ParticipationID *uuid.UUID
}

// ChairGuard são as regras de cadeira da agenda. nil = sem agenda (comportamento da 1.0).
type ChairGuard interface {
	// CheckChairUse recusa (409 chair_reserved) o uso que invade compromisso ou bloqueio.
	CheckChairUse(ctx context.Context, q database.Querier, estID uuid.UUID, use ChairUse) error
	// CheckQueueEntry, no modo "os dois": a projeção de hoje precisa atender a nova entrada dentro
	// do expediente (409 queue_no_capacity_today) e, se a entrada invadir uma reserva da própria
	// pessoa, ela precisa confirmar que sabe disso (409 queue_overlaps_own_appointment).
	CheckQueueEntry(ctx context.Context, q database.Querier, estID uuid.UUID, qi estabelecimento.QueueInfo,
		st estabelecimento.Settings, serviceMinutes int, who *acesso.Identity, acknowledged bool) error
	// VisitChanged espelha na reserva, na mesma transação, o estado da visita que veio dela.
	VisitChanged(ctx context.Context, q database.Querier, estID, participationID uuid.UUID, status string, at time.Time, reason string) error
}

// mode é a forma de atendimento efetiva: sem a agenda liberada no servidor, é sempre fila.
func (s *Service) mode(st estabelecimento.Settings) string {
	if !s.AgendaEnabled {
		return estabelecimento.ModeQueue
	}
	return st.ServiceMode
}

// guardUse aplica o guarda de cadeira (sem guarda = sem agenda = sem restrição).
func (s *Service) guardUse(ctx context.Context, q database.Querier, estID uuid.UUID, use ChairUse) error {
	if s.Agenda == nil {
		return nil
	}
	return s.Agenda.CheckChairUse(ctx, q, estID, use)
}

// visitChanged avisa a agenda quando a visita veio de uma reserva.
func (s *Service) visitChanged(ctx context.Context, q database.Querier, p row, status string, reason string) error {
	if s.Agenda == nil || p.Origin != OriginAppointment {
		return nil
	}
	return s.Agenda.VisitChanged(ctx, q, p.EstablishmentID, p.ID, status, s.Clock.Now(), reason)
}

// serviceMinutes é a duração dos serviços (quantidade × duração; produto não conta) da comanda.
func serviceMinutes(ctx context.Context, q database.Querier, orderID uuid.UUID) (int, error) {
	sums, err := atendimento.Summaries(ctx, q, []uuid.UUID{orderID})
	if err != nil {
		return 0, err
	}
	return sums[orderID].DurationMinutes, nil
}

// DurationChanged implementa atendimento.Visits: a nova duração de quem foi chamado ou está na
// cadeira não pode invadir o próximo horário protegido. Chamado: a partir de agora, com o que
// resta da tolerância. Em atendimento: a partir do início real.
func (s *Service) DurationChanged(ctx context.Context, tx pgx.Tx, estID, participationID uuid.UUID) error {
	p, err := load(ctx, tx, estID, participationID, false)
	if err != nil {
		return err
	}
	qi, err := estabelecimento.GetQueue(ctx, tx, estID, p.QueueID)
	if err != nil {
		return err
	}
	mins, err := serviceMinutes(ctx, tx, p.OrderID)
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	use := ChairUse{ProfessionalID: qi.ProfessionalID, From: now, ServiceMinutes: mins, ParticipationID: &p.ID}
	switch {
	case p.Status == InService && p.StartedAt != nil:
		use.From = *p.StartedAt
	case p.Status == Called && p.ToleranceEndsAt != nil && p.ToleranceEndsAt.After(now):
		use.ToleranceMinutes = int(p.ToleranceEndsAt.Sub(now).Round(time.Minute).Minutes())
	}
	return s.guardUse(ctx, tx, estID, use)
}

// ---------------------------------------------------------------------------------------------
// Funções usadas pelo módulo agenda

// LoadEntry é uma visita ativa da fila, para a projeção da agenda.
type LoadEntry struct {
	ParticipationID uuid.UUID
	Status          string
	// Minutes: em atendimento = o que falta da duração prevista; chamado/aguardando = duração inteira.
	Minutes int
	// Origin: fila ou reserva (a reserva chamada já tem a própria ocupação na agenda).
	Origin string
	// BusyUntil (chamado/em atendimento): fim previsto do uso da cadeira, sem a margem. Chamado
	// conta o resto da tolerância de chegada + a duração; em atendimento, início + duração (ou
	// agora, se já passou do previsto).
	BusyUntil time.Time
}

// QueueLoad devolve as visitas ativas da fila em ordem (em atendimento, chamada, aguardando FIFO).
func QueueLoad(ctx context.Context, q database.Querier, queueID uuid.UUID, now time.Time) ([]LoadEntry, error) {
	states, err := queueStates(ctx, q, []uuid.UUID{queueID})
	if err != nil {
		return nil, err
	}
	out := make([]LoadEntry, 0, len(states[queueID]))
	for _, e := range states[queueID] {
		le := LoadEntry{ParticipationID: e.ID, Status: e.Status, Minutes: remaining(e, now), Origin: e.Origin}
		switch {
		case e.Status == InService:
			le.BusyUntil = now.Add(time.Duration(le.Minutes) * time.Minute)
		case e.Status == Called:
			from := now
			if e.ToleranceEndsAt != nil && e.ToleranceEndsAt.After(now) {
				from = *e.ToleranceEndsAt
			}
			le.BusyUntil = from.Add(time.Duration(le.Minutes) * time.Minute)
		}
		out = append(out, le)
	}
	return out, nil
}

// ScheduledVisit são os dados para abrir a visita de uma reserva chamada.
type ScheduledVisit struct {
	EstablishmentID uuid.UUID
	Queue           estabelecimento.QueueInfo
	AccountID       *uuid.UUID
	VisitorID       *uuid.UUID
	Nick            string
	// Lines: itens congelados na reserva (preço e duração aceitos, não os do catálogo de hoje).
	Lines []atendimento.Line
	// ToleranceMinutes: prazo de chegada contado desta chamada (calculado pela agenda).
	ToleranceMinutes int
	Now              time.Time
	Audit            auditoria.Event
	// OrderID da comanda nova (a agenda liga o pagamento da reserva a ela); vazio = gerado aqui.
	OrderID uuid.UUID
}

// OpenScheduledVisit abre, na transação da chamada da reserva (fila JÁ travada pela agenda), a
// participação já CHAMADA e a comanda com os itens congelados. A senha e a posição técnica vêm dos
// contadores oficiais; a origem "appointment" impede contar a visita como entrada FIFO.
// Recusa (409 customer_has_active_visit) se a pessoa já tem outra visita ativa na loja.
func (s *Service) OpenScheduledVisit(ctx context.Context, tx pgx.Tx, in ScheduledVisit) (uuid.UUID, error) {
	id := acesso.Identity{Kind: acesso.IdentityAccount}
	if in.AccountID != nil {
		id.AccountID = *in.AccountID
	} else {
		id.Kind, id.VisitorID = acesso.IdentityVisitor, *in.VisitorID
	}
	if cur, err := s.activeOf(ctx, tx, in.EstablishmentID, id); err != nil {
		return uuid.Nil, err
	} else if cur != nil {
		return uuid.Nil, conflict("customer_has_active_visit", "Cliente já está em atendimento ou na fila",
			"Este cliente tem outra visita em andamento nesta loja (senha "+cur.TicketCode+"). Encerre-a antes de chamar o horário marcado; a reserva continua valendo.", nil)
	}
	loyaltyEligible := false
	if in.AccountID != nil {
		var err error
		if loyaltyEligible, err = fidelidade.ProgramEnabled(ctx, tx, in.EstablishmentID); err != nil {
			return uuid.Nil, err
		}
	}
	pos, day, number, err := nextSlot(ctx, tx, in.Queue, in.Now)
	if err != nil {
		return uuid.Nil, err
	}
	partID, _ := uuid.NewV7()
	orderID := in.OrderID
	if orderID == uuid.Nil {
		orderID, _ = uuid.NewV7()
	}
	oi := atendimento.Identity{AccountID: in.AccountID, VisitorID: in.VisitorID}
	if _, err := atendimento.CreateOrder(ctx, tx, atendimento.NewOrder{ID: orderID, EstablishmentID: in.EstablishmentID,
		ParticipationID: partID, ProfessionalID: in.Queue.ProfessionalID, Identity: oi, LoyaltyEligible: loyaltyEligible,
		Lines: in.Lines, Now: in.Now}, in.Audit); err != nil {
		return uuid.Nil, err
	}
	ends := in.Now.Add(time.Duration(in.ToleranceMinutes) * time.Minute)
	_, err = tx.Exec(ctx, `
		INSERT INTO participation (id, establishment_id, queue_id, order_id, account_id, visitor_id, nick, loyalty_eligible,
		                           position, ticket_day, ticket_number, ticket_code, created_at, updated_at,
		                           status, called_at, tolerance_minutes, tolerance_ends_at, origin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, 'called', $13, $14, $15, 'appointment')`,
		partID, in.EstablishmentID, in.Queue.ID, orderID, in.AccountID, in.VisitorID, truncateNick(in.Nick), loyaltyEligible,
		pos, day, number, ticketCode(in.Queue.Letter, number), in.Now, in.ToleranceMinutes, ends)
	switch {
	case database.IsUniqueViolation(err, "participation_chair_uq"):
		return uuid.Nil, conflict("professional_busy", "Cadeira ocupada", "Já há um cliente chamado ou em atendimento nesta cadeira.", nil)
	case database.IsUniqueViolation(err, "participation_active_account_uq"), database.IsUniqueViolation(err, "participation_active_visitor_uq"):
		return uuid.Nil, conflict("customer_has_active_visit", "Cliente já está em atendimento ou na fila",
			"Este cliente tem outra visita em andamento nesta loja. A reserva continua valendo.", nil)
	case err != nil:
		return uuid.Nil, fmt.Errorf("abrir visita da reserva: %w", err)
	}
	if in.VisitorID != nil && s.VisitStarted != nil {
		if err := s.VisitStarted(ctx, tx, *in.VisitorID); err != nil {
			return uuid.Nil, err
		}
	}
	a := in.Audit
	a.EstablishmentID, a.OccurredAt, a.Action = &in.EstablishmentID, in.Now, "T-P-02"
	a.TargetType, a.TargetID, a.ToState, a.Reason = "participation", partID, Called, "horário marcado"
	if err := auditoria.Record(ctx, tx, a); err != nil {
		return uuid.Nil, err
	}
	p, err := load(ctx, tx, in.EstablishmentID, partID, false)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.recordParticipation(ctx, tx, p); err != nil {
		return uuid.Nil, err
	}
	return partID, s.QueueChanged(ctx, tx, in.EstablishmentID, in.Queue.ID)
}

func truncateNick(n string) string {
	r := []rune(n)
	if len(r) > 30 {
		return string(r[:30])
	}
	if len(r) == 0 {
		return "Cliente"
	}
	return n
}

// EndScheduledNoShow encerra como ausente a visita de uma reserva chamada cujo prazo venceu (worker
// da agenda). Trava fila → participação; só age se ainda está chamada e o prazo gravado venceu no
// relógio do servidor (no instante exato do prazo, já venceu). Devolve false se não havia o que fazer.
func (s *Service) EndScheduledNoShow(ctx context.Context, tx pgx.Tx, estID, participationID uuid.UUID) (bool, error) {
	p, _, err := s.lockForCommand(ctx, tx, estID, participationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := s.Clock.Now()
	if p.Status != Called || p.ToleranceEndsAt == nil || now.Before(*p.ToleranceEndsAt) {
		return false, nil
	}
	_, err = s.end(ctx, tx, p, role{system: true}, NoShow, atendimento.ClosedNoShow, "T-P-06", "prazo do horário marcado venceu")
	return err == nil, err
}

// Motivo espelhado na reserva quando a visita é cancelada (o módulo agenda distingue quem cancelou).
const (
	MirrorCancelledByCustomer = "cancelled_by_customer"
	MirrorCancelledByStore    = "cancelled_by_store: "
)
