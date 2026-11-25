package fila

import (
	"context"
	"errors"
	"fila/internal/identidade"
	"fila/internal/midia"
	"fmt"
	"net/http"
	"slices"
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
	avatars, err := activeAvatars(ctx, q, states)
	if err != nil {
		return nil, err
	}
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
				CustomerAvatar:          avatars[e.ID],
				LateNoticeAt:            e.LateNoticeAt,
				Origin:                  e.Origin,
			})
		}
	}
	return out, nil
}

// activeAvatars resolve a foto pessoal de quem tem conta nas participações ATIVAS listadas. A URL
// passa pelo endpoint da participação, que confere de novo equipe, loja e situação a cada pedido.
func activeAvatars(ctx context.Context, q database.Querier, states map[uuid.UUID][]queued) (map[uuid.UUID]*midia.ImageRef, error) {
	out := map[uuid.UUID]*midia.ImageRef{}
	var accounts []uuid.UUID
	for _, list := range states {
		for _, e := range list {
			if e.AccountID != nil {
				accounts = append(accounts, *e.AccountID)
			}
		}
	}
	if len(accounts) == 0 {
		return out, nil
	}
	assetOf, err := identidade.AvatarAssetsOf(ctx, q, accounts)
	if err != nil || len(assetOf) == 0 {
		return out, err
	}
	ids := make([]uuid.UUID, 0, len(assetOf))
	for _, id := range assetOf {
		ids = append(ids, id)
	}
	assets, err := midia.Attached(ctx, q, ids, "private")
	if err != nil {
		return nil, err
	}
	for _, list := range states {
		for _, e := range list {
			if e.AccountID == nil {
				continue
			}
			a, ok := assets[assetOf[*e.AccountID]]
			if !ok {
				continue
			}
			est, part, asset := e.EstablishmentID, e.ID, a.ID
			out[e.ID] = a.Ref(func(size int) string {
				return fmt.Sprintf("/v1/establishments/%s/participations/%s/customer-avatar/%d?v=%s", est, part, size, asset)
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
			if e.Status == Waiting || e.Status == Called {
				snap.PendingCount++
				snap.PendingServiceUnits += e.summary.ServiceUnits
				snap.PendingMinutes += e.summary.DurationMinutes
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
	ID              uuid.UUID     `json:"id"`
	EstablishmentID uuid.UUID     `json:"establishment_id"`
	QueueID         uuid.UUID     `json:"queue_id"`
	ProfessionalID  uuid.UUID     `json:"professional_id"`
	TicketCode      string        `json:"ticket_code"`
	Status          string        `json:"status"`
	Position        *int          `json:"position"`
	EstimatedWait   *estimateView `json:"estimated_wait"`
	Nick            string        `json:"nick"`
	IdentityKind    string        `json:"identity_kind,omitempty"`
	LoyaltyEligible bool          `json:"loyalty_eligible"`
	CalledAt        *time.Time    `json:"called_at"`
	ToleranceEndsAt *time.Time    `json:"tolerance_ends_at"`
	// ToleranceMinutes: tolerância aplicada nesta chamada (não muda se a loja mudar a configuração).
	ToleranceMinutes *int           `json:"tolerance_minutes"`
	StartedAt        *time.Time     `json:"started_at"`
	FinishedAt       *time.Time     `json:"finished_at"`
	EndReason        *string        `json:"end_reason"`
	Transfers        []transferView `json:"transfers"`
	Version          int64          `json:"version"`
	OrderID          uuid.UUID      `json:"order_id"`
	CreatedAt        time.Time      `json:"created_at"`
	// LateNoticeAt: o cliente avisou pelo app que vai se atrasar (T-P-12); nova chamada limpa.
	LateNoticeAt *time.Time `json:"late_notice_at"`
	// Origin: fila ou horário marcado (ADR-0038).
	Origin string `json:"origin"`
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
		ToleranceEndsAt: p.ToleranceEndsAt, ToleranceMinutes: p.ToleranceMinutes, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt, EndReason: p.EndReason,
		Version: p.Version, OrderID: p.OrderID, CreatedAt: p.CreatedAt, Transfers: []transferView{}, LateNoticeAt: p.LateNoticeAt,
		Origin: p.Origin}
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

// getCustomerAvatar entrega a foto pessoal do cliente à equipe autorizada (dono da loja ou o
// profissional da fila) SÓ enquanto a participação está ativa (aguardando, chamada ou em
// atendimento). Encerrada a finalidade operacional, o acesso é negado; visitante não tem foto.
// A autorização é refeita a cada pedido, antes de responder 304.
func (s *Service) getCustomerAvatar(w http.ResponseWriter, r *http.Request) error {
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
	who, err := s.authorize(r, p, qi.ProfessionalID, allow{professional: true, owner: true})
	if err != nil {
		return err
	}
	if !who.isStaff || p.AccountID == nil || !slices.Contains(activeStatuses, p.Status) || s.Media == nil {
		return hs.ErrNotFound()
	}
	asset, err := identidade.AvatarAssetOf(ctx, s.Pool, *p.AccountID)
	if err != nil {
		return err
	}
	if asset == nil {
		return hs.ErrNotFound()
	}
	return s.Media.ServePrivate(w, r, *asset)
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

// ---------------------------------------------------------------------------------------------
// listMyVisits: painel do cliente — as visitas em andamento em TODAS as lojas, com a identidade
// deste navegador (conta e/ou visitantes de cada loja). Mesmo critério de getCurrentVisit:
// participação ativa, comanda ainda não encerrada ou (visitante) encerrada há menos de 24 h.

type myVisitStore struct {
	ID      uuid.UUID       `json:"id"`
	Slug    string          `json:"slug"`
	Name    string          `json:"name"`
	Palette string          `json:"palette"`
	Logo    *midia.ImageRef `json:"logo"`
}

type myVisitView struct {
	Establishment    myVisitStore `json:"establishment"`
	ProfessionalName string       `json:"professional_name"`
	Visit            visitView    `json:"visit"`
}

// maxMyVisitStores limita quantas lojas de uma conta são consultadas (as mais recentes).
const maxMyVisitStores = 20

func (s *Service) listMyVisits(w http.ResponseWriter, r *http.Request) error {
	account, visitors := s.Customers.AllCustomerIdentities(r)
	if account == nil && len(visitors) == 0 {
		return hs.ErrUnauthenticated()
	}
	ctx := r.Context()
	out := []myVisitView{}
	err := database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		stores := map[uuid.UUID]bool{}
		var order []uuid.UUID
		add := func(id uuid.UUID) {
			if !stores[id] {
				stores[id] = true
				order = append(order, id)
			}
		}
		if account != nil {
			rows, err := tx.Query(ctx, `SELECT establishment_id FROM participation WHERE account_id = $1
				GROUP BY establishment_id ORDER BY max(created_at) DESC LIMIT $2`, account.AccountID, maxMyVisitStores)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			for _, id := range ids {
				add(id)
			}
		}
		for id := range visitors {
			add(id)
		}
		for _, estID := range order {
			v, err := s.currentVisit(ctx, tx, estID, account, nil)
			if err != nil {
				return err
			}
			if v == nil && visitors[estID] != nil {
				if v, err = s.currentVisit(ctx, tx, estID, nil, visitors[estID]); err != nil {
					return err
				}
			}
			if v == nil {
				continue
			}
			e, err := estabelecimento.ActiveByID(ctx, tx, estID)
			if errors.Is(err, estabelecimento.ErrNotFound) {
				continue // loja encerrada: some do painel
			}
			if err != nil {
				return err
			}
			item := myVisitView{Establishment: myVisitStore{ID: e.ID, Slug: e.Slug, Name: e.Name, Palette: e.Palette}, Visit: *v}
			logo, err := midia.AttachedOne(ctx, tx, e.LogoAssetID, "public")
			if err != nil {
				return err
			}
			if logo != nil {
				item.Establishment.Logo = logo.PublicRef()
			}
			if qi, err := estabelecimento.GetQueue(ctx, tx, estID, v.Participation.QueueID); err == nil {
				item.ProfessionalName = qi.ProfessionalName
			} else if !errors.Is(err, estabelecimento.ErrNotFound) {
				return err
			}
			out = append(out, item)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Primeiro o que pede atenção: chamado, em atendimento, aguardando; depois comandas pendentes.
	rank := map[string]int{Called: 0, InService: 1, Waiting: 2}
	slices.SortStableFunc(out, func(a, b myVisitView) int {
		ra, ok := rank[a.Visit.Participation.Status]
		if !ok {
			ra = 3
		}
		rb, ok := rank[b.Visit.Participation.Status]
		if !ok {
			rb = 3
		}
		return ra - rb
	})
	w.Header().Set("Cache-Control", "no-store")
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []myVisitView `json:"data"`
	}{Data: out})
	return nil
}
