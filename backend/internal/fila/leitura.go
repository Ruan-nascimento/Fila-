package fila

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// ---------------------------------------------------------------------------------------------
// Estado das filas: quem está em cada uma, em ordem, com a duração estimada de cada atendimento.

type queued struct {
	row
	summary atendimento.Summary
}

// queueStates lê as participações ativas das filas, em ordem: em atendimento, chamada e, depois,
// aguardando pela posição FIFO (R-FILA-05).
func queueStates(ctx context.Context, q database.Querier, queueIDs []uuid.UUID) (map[uuid.UUID][]queued, error) {
	out := map[uuid.UUID][]queued{}
	if len(queueIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, rowSelect+`
		WHERE queue_id = ANY($1) AND status = ANY($2)
		ORDER BY queue_id, CASE status WHEN 'in_service' THEN 0 WHEN 'called' THEN 1 ELSE 2 END, position`,
		queueIDs, activeStatuses)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) { return scanRow(r) })
	if err != nil {
		return nil, err
	}
	orderIDs := make([]uuid.UUID, len(list))
	for i := range list {
		orderIDs[i] = list[i].OrderID
	}
	sums, err := atendimento.Summaries(ctx, q, orderIDs)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		out[p.QueueID] = append(out[p.QueueID], queued{row: p, summary: sums[p.OrderID]})
	}
	return out, nil
}

// remaining estima quantos minutos o atendimento ainda ocupa a cadeira (estimativa simples):
// em atendimento = duração − tempo decorrido (mínimo 0); chamado ou aguardando = duração inteira.
func remaining(e queued, now time.Time) int {
	d := e.summary.DurationMinutes
	if e.Status == InService && e.StartedAt != nil {
		d -= int(now.Sub(*e.StartedAt).Minutes())
	}
	return max(d, 0)
}

// positionAndWait calcula posição (só aguardando) e espera estimada de uma participação:
// soma das durações de quem está à frente (R-FILA-16 — sempre "estimativa").
func positionAndWait(list []queued, id uuid.UUID, now time.Time) (pos int, wait int, found bool) {
	for _, e := range list {
		if e.ID == id {
			if e.Status != Waiting {
				return 0, 0, true
			}
			return pos + 1, wait, true
		}
		if e.Status == Waiting {
			pos++
		}
		wait += remaining(e, now)
	}
	return 0, 0, false
}

// PendingInQueue implementa estabelecimento.Occupancy: aguardando ou chamadas (bloqueiam fechar).
func (s *Service) PendingInQueue(ctx context.Context, q database.Querier, queueID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM participation WHERE queue_id = $1 AND status IN ('waiting', 'called')`, queueID).Scan(&n)
	return n, err
}

// ActiveForProfessional implementa estabelecimento.Occupancy: qualquer participação ativa na fila
// do profissional (bloqueia desativar).
func (s *Service) ActiveForProfessional(ctx context.Context, q database.Querier, profID uuid.UUID) (int, error) {
	queueID, err := estabelecimento.QueueOfProfessional(ctx, q, profID)
	if errors.Is(err, estabelecimento.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int
	err = q.QueryRow(ctx, `SELECT count(*) FROM participation WHERE queue_id = $1 AND status = ANY($2)`, queueID, activeStatuses).Scan(&n)
	return n, err
}

// StaffEntries implementa estabelecimento.QueueActivity (visão da equipe, com nick e serviços).
func (s *Service) StaffEntries(ctx context.Context, q database.Querier, queueIDs []uuid.UUID) (map[uuid.UUID][]estabelecimento.StaffEntry, error) {
	states, err := queueStates(ctx, q, queueIDs)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]estabelecimento.StaffEntry{}
	for qid, list := range states {
		for _, e := range list {
			names := e.summary.ServiceNames
			if names == nil {
				names = []string{}
			}
			out[qid] = append(out[qid], estabelecimento.StaffEntry{
				ParticipationID: e.ID, OrderID: e.OrderID, TicketCode: e.TicketCode, Nick: e.Nick, Status: e.Status,
				ServiceNames: names, CalledAt: e.CalledAt, ToleranceEndsAt: e.ToleranceEndsAt, Version: e.Version,
				HasPendingChangeRequest: e.summary.HasPendingChangeRequest,
			})
		}
	}
	return out, nil
}

// PublicSnapshots implementa estabelecimento.QueueActivity: só senhas, situação, quantidade e
// estimativa para quem chegar agora. Nunca nick, serviços ou valores (R-PERF-01).
func (s *Service) PublicSnapshots(ctx context.Context, q database.Querier, queueIDs []uuid.UUID, now time.Time) (map[uuid.UUID]estabelecimento.PublicSnapshot, error) {
	states, err := queueStates(ctx, q, queueIDs)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]estabelecimento.PublicSnapshot{}
	for qid, list := range states {
		var snap estabelecimento.PublicSnapshot
		for _, e := range list {
			snap.Tickets = append(snap.Tickets, estabelecimento.PublicTicket{TicketCode: e.TicketCode, Status: e.Status})
			if e.Status == Waiting {
				snap.WaitingCount++
			}
			snap.EstimateMinutes += remaining(e, now)
		}
		out[qid] = snap
	}
	return out, nil
}

// VisitorBusy é a verificação de T-VS-01 para o módulo identidade: visitante com participação
// ativa ou comanda pendente na loja não cria outro visitante neste navegador.
func (s *Service) VisitorBusy(ctx context.Context, q database.Querier, visitorID uuid.UUID) (bool, error) {
	var busy bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM participation WHERE visitor_id = $1 AND status = ANY($2))`,
		visitorID, activeStatuses).Scan(&busy)
	if err != nil || busy {
		return busy, err
	}
	var est uuid.UUID
	err = q.QueryRow(ctx, `SELECT establishment_id FROM participation WHERE visitor_id = $1 ORDER BY created_at DESC LIMIT 1`, visitorID).Scan(&est)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	v := visitorID
	return atendimento.PendingOrderExists(ctx, q, est, atendimento.Identity{VisitorID: &v})
}

// ---------------------------------------------------------------------------------------------
// Visões (Visit = participação + comanda)

type estimateView struct {
	Kind       string    `json:"kind"`
	Minutes    int       `json:"minutes"`
	ComputedAt time.Time `json:"computed_at"`
}

type transferView struct {
	FromQueueID uuid.UUID `json:"from_queue_id"`
	ToQueueID   uuid.UUID `json:"to_queue_id"`
	At          time.Time `json:"at"`
}

type participationView struct {
	ID              uuid.UUID      `json:"id"`
	EstablishmentID uuid.UUID      `json:"establishment_id"`
	QueueID         uuid.UUID      `json:"queue_id"`
	ProfessionalID  uuid.UUID      `json:"professional_id"`
	TicketCode      string         `json:"ticket_code"`
	Status          string         `json:"status"`
	Position        *int           `json:"position"`
	EstimatedWait   *estimateView  `json:"estimated_wait"`
	Nick            string         `json:"nick"`
	IdentityKind    string         `json:"identity_kind,omitempty"`
	LoyaltyEligible bool           `json:"loyalty_eligible"`
	CalledAt        *time.Time     `json:"called_at"`
	ToleranceEndsAt *time.Time     `json:"tolerance_ends_at"`
	StartedAt       *time.Time     `json:"started_at"`
	FinishedAt      *time.Time     `json:"finished_at"`
	EndReason       *string        `json:"end_reason"`
	Transfers       []transferView `json:"transfers"`
	Version         int64          `json:"version"`
	OrderID         uuid.UUID      `json:"order_id"`
	CreatedAt       time.Time      `json:"created_at"`
}

type visitView struct {
	Participation participationView     `json:"participation"`
	Order         atendimento.OrderView `json:"order"`
}

// visit monta a visita. forStaff inclui o tipo de identidade (visitante/conta), que o cliente não vê.
func (s *Service) visit(ctx context.Context, q database.Querier, p row, forStaff bool) (visitView, error) {
	qi, err := estabelecimento.GetQueue(ctx, q, p.EstablishmentID, p.QueueID)
	if err != nil {
		return visitView{}, err
	}
	pv := participationView{ID: p.ID, EstablishmentID: p.EstablishmentID, QueueID: p.QueueID, ProfessionalID: qi.ProfessionalID,
		TicketCode: p.TicketCode, Status: p.Status, Nick: p.Nick, LoyaltyEligible: p.LoyaltyEligible, CalledAt: p.CalledAt,
		ToleranceEndsAt: p.ToleranceEndsAt, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt, EndReason: p.EndReason,
		Version: p.Version, OrderID: p.OrderID, CreatedAt: p.CreatedAt, Transfers: []transferView{}}
	if forStaff {
		pv.IdentityKind = acesso.IdentityVisitor
		if p.AccountID != nil {
			pv.IdentityKind = acesso.IdentityAccount
		}
	}
	if p.Status == Waiting {
		states, err := queueStates(ctx, q, []uuid.UUID{p.QueueID})
		if err != nil {
			return visitView{}, err
		}
		now := s.Clock.Now()
		if pos, wait, ok := positionAndWait(states[p.QueueID], p.ID, now); ok {
			pv.Position = &pos
			pv.EstimatedWait = &estimateView{Kind: "estimate", Minutes: wait, ComputedAt: now}
		}
	}
	rows, err := q.Query(ctx, `SELECT from_queue_id, to_queue_id, at FROM participation_transfer WHERE participation_id = $1 ORDER BY at`, p.ID)
	if err != nil {
		return visitView{}, err
	}
	tr, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (transferView, error) {
		var t transferView
		err := r.Scan(&t.FromQueueID, &t.ToQueueID, &t.At)
		return t, err
	})
	if err != nil {
		return visitView{}, err
	}
	if tr != nil {
		pv.Transfers = tr
	}
	order, err := atendimento.LoadOrder(ctx, q, p.EstablishmentID, p.OrderID)
	if err != nil {
		return visitView{}, err
	}
	return visitView{Participation: pv, Order: order}, nil
}

// ---------------------------------------------------------------------------------------------
// getParticipation e getCurrentVisit

func (s *Service) getParticipation(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	id, err := pathID(r, "participationId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	p, err := load(ctx, s.Pool, estID, id, false)
	if err != nil {
		return err
	}
	qi, err := estabelecimento.GetQueue(ctx, s.Pool, estID, p.QueueID)
	if err != nil {
		return err
	}
	who, err := s.authorize(r, p, qi.ProfessionalID, allow{customer: true, professional: true, owner: true})
	if err != nil {
		return err
	}
	v, err := s.visit(ctx, s.Pool, p, who.isStaff)
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[participationView]{Data: v.Participation})
	return nil
}

// getCurrentVisit: visita da identidade em uso nesta loja (conta, se houver sessão de conta;
// senão o visitante). Mostra a participação ativa, ou a comanda ainda não encerrada, ou — para
// visitante — a visita encerrada há menos de 24 horas. O cursor é do mesmo instante da leitura.
func (s *Service) getCurrentVisit(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	account, visitor := s.Customers.CustomerIdentities(r, estID)
	if account == nil && visitor == nil {
		return hs.ErrUnauthenticated()
	}
	ctx := r.Context()
	if _, err := estabelecimento.ActiveByID(ctx, s.Pool, estID); errors.Is(err, estabelecimento.ErrNotFound) {
		return hs.ErrNotFound()
	} else if err != nil {
		return err
	}
	var data *visitView
	var cursor string
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		if data, err = s.currentVisit(ctx, tx, estID, account, visitor); err != nil {
			return err
		}
		cursor, err = outbox.CurrentCursor(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data   *visitView `json:"data"`
		Cursor *string    `json:"cursor"`
	}{Data: data, Cursor: &cursor})
	return nil
}

// CurrentVisit devolve a visita atual (Visit) da identidade nesta loja, ou nil. Usado pelo
// snapshot do SSE.
func (s *Service) CurrentVisit(ctx context.Context, q database.Querier, estID uuid.UUID, account, visitor *acesso.Identity) (any, error) {
	v, err := s.currentVisit(ctx, q, estID, account, visitor)
	if v == nil || err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Service) currentVisit(ctx context.Context, q database.Querier, estID uuid.UUID, account, visitor *acesso.Identity) (*visitView, error) {
	id := account
	if id == nil {
		id = visitor
	}
	if id == nil {
		return nil, nil
	}
	col := "account_id"
	if id.Kind == acesso.IdentityVisitor {
		col = "visitor_id"
	}
	p, err := scanRow(q.QueryRow(ctx, rowSelect+` WHERE establishment_id = $1 AND `+col+` = $2 ORDER BY created_at DESC LIMIT 1`, estID, id.ID()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := s.visit(ctx, q, p, false)
	if err != nil {
		return nil, err
	}
	closed := v.Order.Status == "settled" || v.Order.Status == "closed_no_charge"
	recent := id.Kind == acesso.IdentityVisitor && v.Order.ClosedAt != nil && s.Clock.Now().Sub(*v.Order.ClosedAt) < 24*time.Hour
	if p.active() || !closed || recent {
		return &v, nil
	}
	return nil, nil
}

// StaffQueues e PublicQueues expõem as visões das filas para o snapshot do SSE.
func (s *Service) StaffQueues(ctx context.Context, q database.Querier, estID uuid.UUID, professionalID *uuid.UUID) (any, error) {
	return estabelecimento.StaffQueues(ctx, q, s, estID, professionalID)
}

func (s *Service) PublicQueues(ctx context.Context, q database.Querier, estID uuid.UUID, now time.Time) (any, error) {
	return estabelecimento.PublicQueues(ctx, q, s, estID, now)
}
