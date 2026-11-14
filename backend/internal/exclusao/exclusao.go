// Package exclusao é o módulo de exclusão de conta (R-EXC-*, T-AC-03/04).
//
// Pedido (titular, reautenticado): na MESMA transação revoga as sessões (SSE abertos recebem
// stream.session_ended), encerra vínculos de equipe, cancela visitas aguardando/chamadas, encerra
// benefícios (pontos e reservas chegam na etapa 10) e registra a solicitação com protocolo.
// Atendimento em curso, comanda a receber/conferir e fila de profissional com gente ficam com a
// equipe: a solicitação mostra "processing" com os bloqueios reais, nunca "completed" antes da hora.
//
// O worker reavalia as solicitações; sem bloqueios, anonimiza perfil e credenciais numa transação
// (tudo ou nada: interrupção no meio = nada feito, e a próxima volta refaz) e grava evidência sem
// dados pessoais. Comandas, itens, recebimentos e auditoria continuam, ligados só ao pseudônimo.
//
// Fronteiras (ADR-0001): dono de deletion_request e deletion_request_block. Os outros dados são
// tratados pelas funções dos módulos donos (identidade, estabelecimento, fila, atendimento).
package exclusao

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/fila"
	"fila/internal/identidade"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/keys"
	"fila/internal/platform/outbox"
	"fila/internal/platform/ratelimit"
)

// Situações e bloqueios (DeletionStatus, DeletionBlocker).
const (
	StatusProcessing = "processing"
	StatusCompleted  = "completed"

	BlockService      = "service_in_progress"
	BlockFinancial    = "financial_pending"
	BlockProfessional = "professional_queue_has_participants"

	// TargetDays é META OPERACIONAL (R-EXC-05), não prazo legal.
	TargetDays = 30
	// AlertAfter: a partir daqui a pendência aparece destacada e vira alerta no log do worker.
	AlertAfter = 25 * 24 * time.Hour

	// VisitorNickRetention: depois de a visita de visitante terminar e a comanda encerrar, o nick
	// fica mais este tempo (atendimento de dúvidas no balcão) e é removido. Prazo OPERACIONAL,
	// reversível; prazos legais dependem de validação antes de produção (docs/retencao.md).
	VisitorNickRetention = 30 * 24 * time.Hour

	purposeProtocol = "exclusao-protocolo/v1"
	evDeletion      = "deletion_request.updated"
)

// Benefits encerra benefícios da conta na exclusão (T-LG-03 saldo de pontos, T-RW-06 reservas).
// O programa de pontos e prêmios chega na etapa 10; até lá não há benefício (nil).
type Benefits interface {
	CloseForDeletion(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, now time.Time) error
}

// Deps são as dependências do módulo.
type Deps struct {
	Pool              *pgxpool.Pool
	Clock             clock.Clock
	Log               *slog.Logger
	Ring              *keys.Ring
	Auth              acesso.Authorizer
	Limiter           *ratelimit.Limiter
	TrustForwardedFor bool
	Identity          *identidade.Service
	Queue             *fila.Service
	Benefits          Benefits
	// Fault (só testes) simula queda no meio da anonimização: devolver erro no passo indicado.
	Fault func(step string) error
}

// Service atende as rotas de exclusão e o processamento no worker.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço.
func New(d Deps) *Service {
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

// Routes devolve as operações do contrato.
func (s *Service) Routes() []hs.Route {
	return []hs.Route{
		{Operation: "requestAccountDeletion", Method: http.MethodPost, Path: "/v1/me/deletion-request",
			Grants:  []hs.Grant{{Actor: "customer", Scope: "self"}, {Actor: "professional", Scope: "self"}, {Actor: "owner", Scope: "self"}},
			Handler: s.requestAccountDeletion},
		{Operation: "getDeletionRequestStatus", Method: http.MethodPost, Path: "/v1/deletion-requests/status",
			Grants: []hs.Grant{{Actor: "public", Scope: "protocol_holder"}}, Handler: s.getDeletionRequestStatus},
		{Operation: "listDeletionRequests", Method: http.MethodGet, Path: "/v1/establishments/{establishmentId}/deletion-requests",
			Grants: []hs.Grant{hs.GrantOwner}, Handler: s.listDeletionRequests},
	}
}

// ---------------------------------------------------------------------------------------------
// Visão (DeletionRequest)

type requestView struct {
	Protocol    string     `json:"protocol"`
	Status      string     `json:"status"`
	Blockers    []string   `json:"blockers"`
	RequestedAt time.Time  `json:"requested_at"`
	CompletedAt *time.Time `json:"completed_at"`
	TargetDays  int        `json:"target_days"`
}

type requestRow struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Suffix          string
	Status          string
	ProfessionalIDs []uuid.UUID
	RequestedAt     time.Time
	CompletedAt     *time.Time
	Version         int64
}

const requestSelect = `SELECT id, account_id, protocol_suffix, status, professional_ids, requested_at, completed_at, version FROM deletion_request`

func scanRequest(r pgx.Row) (requestRow, error) {
	var x requestRow
	err := r.Scan(&x.ID, &x.AccountID, &x.Suffix, &x.Status, &x.ProfessionalIDs, &x.RequestedAt, &x.CompletedAt, &x.Version)
	return x, err
}

// maskedProtocol mostra só o sufixo (equipe, repetição e consulta: o titular já tem o completo).
func maskedProtocol(suffix string) string { return "…" + suffix }

func (x requestRow) view(blocks []block, onlyEst *uuid.UUID) requestView {
	v := requestView{Protocol: maskedProtocol(x.Suffix), Status: x.Status, Blockers: []string{}, RequestedAt: x.RequestedAt,
		CompletedAt: x.CompletedAt, TargetDays: TargetDays}
	for _, b := range blocks {
		if (onlyEst == nil || *onlyEst == b.est) && !slices.Contains(v.Blockers, b.kind) {
			v.Blockers = append(v.Blockers, b.kind)
		}
	}
	slices.Sort(v.Blockers)
	return v
}

// ---------------------------------------------------------------------------------------------
// Protocolo: 20 bytes aleatórios em base32 (32 caracteres). Guardado só como HMAC.

func newProtocol() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

func normalizeProtocol(p string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(p)))
}

func (s *Service) protocolMAC(p string) []byte {
	return s.Ring.MAC(purposeProtocol, []byte(normalizeProtocol(p)))
}

// protocolMACs reconhece também protocolos emitidos antes de uma troca da chave do servidor.
func (s *Service) protocolMACs(p string) [][]byte {
	return s.Ring.MACs(purposeProtocol, []byte(normalizeProtocol(p)))
}

// ---------------------------------------------------------------------------------------------
// Bloqueios

type block struct {
	est  uuid.UUID
	kind string
}

// assess recalcula os bloqueios reais da solicitação e grava (deletion_request_block). Devolve
// também se mudaram (para avisar as lojas).
func (s *Service) assess(ctx context.Context, tx pgx.Tx, x *requestRow) ([]block, bool, error) {
	var out []block
	add := func(ests []uuid.UUID, kind string) {
		for _, e := range ests {
			out = append(out, block{est: e, kind: kind})
		}
	}
	ests, err := fila.InServiceEstablishments(ctx, tx, x.AccountID)
	if err != nil {
		return nil, false, err
	}
	add(ests, BlockService)
	if ests, err = atendimento.PendingFinancialEstablishments(ctx, tx, x.AccountID); err != nil {
		return nil, false, err
	}
	add(ests, BlockFinancial)
	if ests, err = fila.BusyProfessionalQueues(ctx, tx, x.ProfessionalIDs); err != nil {
		return nil, false, err
	}
	add(ests, BlockProfessional)
	prev, err := storedBlocks(ctx, tx, x.ID)
	if err != nil {
		return nil, false, err
	}
	changed := len(prev) != len(out)
	for _, b := range out {
		if !slices.Contains(prev, b) {
			changed = true
		}
	}
	if !changed {
		return out, false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM deletion_request_block WHERE request_id = $1`, x.ID); err != nil {
		return nil, false, err
	}
	for _, b := range out {
		if _, err := tx.Exec(ctx, `INSERT INTO deletion_request_block (request_id, establishment_id, blocker) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, x.ID, b.est, b.kind); err != nil {
			return nil, false, err
		}
	}
	// A visão mudou: nova versão (o evento só vale se for mais novo que o conhecido).
	if err := tx.QueryRow(ctx, `UPDATE deletion_request SET version = version + 1 WHERE id = $1 RETURNING version`, x.ID).Scan(&x.Version); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func storedBlocks(ctx context.Context, q database.Querier, requestID uuid.UUID) ([]block, error) {
	rows, err := q.Query(ctx, `SELECT establishment_id, blocker FROM deletion_request_block WHERE request_id = $1`, requestID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (block, error) {
		var b block
		err := r.Scan(&b.est, &b.kind)
		return b, err
	})
}

// notify grava deletion_request.updated (audiência owner) para cada loja afetada, com a visão
// daquela loja (só os bloqueios dela, protocolo mascarado).
func (s *Service) notify(ctx context.Context, tx pgx.Tx, x requestRow, blocks []block, ests []uuid.UUID, now time.Time) error {
	var events []outbox.Event
	for _, est := range ests {
		e := est
		events = append(events, outbox.Event{EstablishmentID: e, Type: evDeletion, ResourceType: "deletion_request", ResourceID: x.ID,
			ResourceVersion: x.Version, Audience: outbox.Audience{Kind: outbox.Owner}, Data: x.view(blocks, &e), OccurredAt: now})
	}
	return outbox.Record(ctx, tx, events...)
}

func establishmentsOf(blocks ...[]block) []uuid.UUID {
	var out []uuid.UUID
	for _, list := range blocks {
		for _, b := range list {
			if !slices.Contains(out, b.est) {
				out = append(out, b.est)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// requestAccountDeletion (T-AC-03)

func (s *Service) requestAccountDeletion(w http.ResponseWriter, r *http.Request) error {
	p := identidade.PrincipalFrom(r.Context())
	if p.Session == nil {
		return hs.ErrUnauthenticated()
	}
	accountID := p.Session.AccountID
	var body struct {
		ConfirmEffects *bool `json:"confirm_effects"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	if body.ConfirmEffects == nil || !*body.ConfirmEffects {
		return hs.ErrValidation(hs.FieldError{Field: "/confirm_effects", Code: hs.FieldRequired,
			Message: "confirme que viu os efeitos da exclusão"})
	}
	if err := s.Auth.RequireReauth(r); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + accountID.String(), Operation: "requestAccountDeletion", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.Clock.Now()
		prev, err := s.Identity.BeginDeletion(ctx, tx, accountID, now)
		if err != nil {
			return idempotency.Result{}, err
		}
		if prev != identidade.StatusActive {
			// Já pedida (outra aba, clique duplo): nada é repetido.
			x, err := scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE account_id = $1`, accountID))
			var current any
			if err == nil {
				blocks, _ := storedBlocks(ctx, tx, x.ID)
				current = x.view(blocks, nil)
			}
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "deletion_in_progress", Title: "Exclusão já solicitada",
				Detail: "A exclusão desta conta já está em andamento.", Current: current}
		}
		sole, err := estabelecimento.SoleOwnerOf(ctx, tx, accountID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if len(sole) > 0 {
			// A transação é desfeita: sessões, vínculos e visitas continuam como estavam.
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "sole_owner", Title: "Você é a única pessoa dona da loja",
				Detail: "Antes de excluir a conta, peça ao suporte para transferir a administração a outra pessoa ou encerrar a loja."}
		}
		ended, err := estabelecimento.EndMembershipsForDeletion(ctx, tx, accountID, now)
		if err != nil {
			return idempotency.Result{}, err
		}
		profIDs := []uuid.UUID{}
		for _, m := range ended {
			if m.ProfessionalID != nil {
				profIDs = append(profIDs, *m.ProfessionalID)
			}
		}
		if _, err := s.Queue.CancelForDeletion(ctx, tx, accountID, now); err != nil {
			return idempotency.Result{}, err
		}
		if s.Benefits != nil {
			if err := s.Benefits.CloseForDeletion(ctx, tx, accountID, now); err != nil {
				return idempotency.Result{}, err
			}
		}
		protocol := newProtocol()
		x, err := scanRequest(tx.QueryRow(ctx, `
			INSERT INTO deletion_request (account_id, protocol_hmac, protocol_suffix, professional_ids, requested_at, checked_at)
			VALUES ($1, $2, $3, $4, $5, $5)
			RETURNING id, account_id, protocol_suffix, status, professional_ids, requested_at, completed_at, version`,
			accountID, s.protocolMAC(protocol), protocol[len(protocol)-6:], profIDs, now))
		if database.IsUniqueViolation(err, "") {
			return idempotency.Result{}, &hs.Error{Status: 409, Code: "deletion_in_progress", Title: "Exclusão já solicitada",
				Detail: "A exclusão desta conta já está em andamento."}
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		blocks, _, err := s.assess(ctx, tx, &x)
		if err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorCustomer, ActorID: &accountID,
			Action: "T-AC-03", TargetType: "account", TargetID: accountID, FromState: identidade.StatusActive,
			ToState: identidade.StatusDeletionProcessing, RequestID: hs.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		if err := s.Identity.MailDeletionProtocol(ctx, tx, accountID, protocol, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := s.notify(ctx, tx, x, blocks, establishmentsOf(blocks), now); err != nil {
			return idempotency.Result{}, err
		}
		live := x.view(blocks, nil)
		stored := live
		live.Protocol = protocol // completo uma única vez, só nesta resposta
		return idempotency.Result{Status: http.StatusAccepted, Body: hs.Data[requestView]{Data: live},
			Stored: hs.Data[requestView]{Data: stored}}, nil
	})
}

// ---------------------------------------------------------------------------------------------
// getDeletionRequestStatus: público, pelo protocolo (no corpo, nunca na URL), com limite.

var ruleStatusIP = ratelimit.Rule{Name: "exclusao_consulta_ip", Limit: 30, Window: time.Hour}

func (s *Service) getDeletionRequestStatus(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Protocol *string `json:"protocol"`
	}
	if _, err := hs.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Protocol == nil || len(*body.Protocol) < 16 || len(*body.Protocol) > 64 {
		return hs.ErrValidation(hs.FieldError{Field: "/protocol", Code: hs.FieldInvalidFormat})
	}
	ctx := r.Context()
	ok, retry, err := s.Limiter.Allow(ctx, ruleStatusIP, hs.ClientIP(r, s.TrustForwardedFor))
	if err != nil {
		return err
	}
	if !ok {
		return hs.ErrRateLimited(retry)
	}
	var view requestView
	err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		x, err := scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE protocol_hmac = ANY($1)`, s.protocolMACs(*body.Protocol)))
		if errors.Is(err, pgx.ErrNoRows) {
			return hs.ErrNotFound()
		}
		if err != nil {
			return err
		}
		blocks, err := s.liveBlocks(ctx, tx, x)
		if err != nil {
			return err
		}
		view = x.view(blocks, nil)
		return nil
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	hs.WriteJSON(w, http.StatusOK, hs.Data[requestView]{Data: view})
	return nil
}

// liveBlocks devolve a situação real agora (recalcula enquanto em processamento).
func (s *Service) liveBlocks(ctx context.Context, tx pgx.Tx, x requestRow) ([]block, error) {
	if x.Status == StatusCompleted {
		return nil, nil
	}
	before, err := storedBlocks(ctx, tx, x.ID)
	if err != nil {
		return nil, err
	}
	blocks, changed, err := s.assess(ctx, tx, &x)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := s.notify(ctx, tx, x, blocks, establishmentsOf(blocks, before), s.Clock.Now()); err != nil {
			return nil, err
		}
	}
	return blocks, nil
}

// ---------------------------------------------------------------------------------------------
// listDeletionRequests: dono vê as exclusões em processamento que dependem da loja dele.

func (s *Service) listDeletionRequests(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return hs.ErrNotFound()
	}
	if _, err := s.Auth.Owner(r, estID); err != nil {
		return err
	}
	q := r.URL.Query()
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return hs.ErrValidation(hs.FieldError{Field: "limit", Code: hs.FieldOutOfRange})
		}
		limit = n
	}
	var after time.Time
	var afterID uuid.UUID
	if raw := q.Get("cursor"); raw != "" {
		at, id, ok := strings.Cut(raw, "_")
		t, err1 := time.Parse(time.RFC3339Nano, at)
		u, err2 := uuid.Parse(id)
		if !ok || err1 != nil || err2 != nil {
			return hs.ErrValidation(hs.FieldError{Field: "cursor", Code: hs.FieldInvalidFormat})
		}
		after, afterID = t, u
	}
	ctx := r.Context()
	var data []requestView
	var next *string
	err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		// Atualiza primeiro os bloqueios das solicitações ligadas à loja (situação real).
		rows, err := tx.Query(ctx, requestSelect+` WHERE status = 'processing' AND id IN
			(SELECT request_id FROM deletion_request_block WHERE establishment_id = $1)`, estID)
		if err != nil {
			return err
		}
		pending, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (requestRow, error) { return scanRequest(r) })
		if err != nil {
			return err
		}
		for _, x := range pending {
			if _, err := s.liveBlocks(ctx, tx, x); err != nil {
				return err
			}
		}
		sql := requestSelect + ` WHERE status = 'processing' AND id IN
			(SELECT request_id FROM deletion_request_block WHERE establishment_id = $1)`
		args := []any{estID, limit + 1}
		if !after.IsZero() {
			sql += ` AND (requested_at, id) > ($3, $4)`
			args = append(args, after, afterID)
		}
		rows, err = tx.Query(ctx, sql+` ORDER BY requested_at, id LIMIT $2`, args...)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (requestRow, error) { return scanRequest(r) })
		if err != nil {
			return err
		}
		if len(list) > limit {
			list = list[:limit]
			c := list[len(list)-1].RequestedAt.UTC().Format(time.RFC3339Nano) + "_" + list[len(list)-1].ID.String()
			next = &c
		}
		data = make([]requestView, 0, len(list))
		for _, x := range list {
			blocks, err := storedBlocks(ctx, tx, x.ID)
			if err != nil {
				return err
			}
			data = append(data, x.view(blocks, &estID))
		}
		return nil
	})
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []requestView `json:"data"`
		Page struct {
			NextCursor *string `json:"next_cursor"`
			HasMore    bool    `json:"has_more"`
		} `json:"page"`
	}{Data: data, Page: struct {
		NextCursor *string `json:"next_cursor"`
		HasMore    bool    `json:"has_more"`
	}{NextCursor: next, HasMore: next != nil}})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Processamento (worker): reavalia e anonimiza (T-AC-04). Retomável e idempotente.

// ProcessOnce processa as solicitações em andamento. Cada uma numa transação própria, travada
// com SKIP LOCKED (dois workers não processam a mesma ao mesmo tempo). Devolve quantas concluiu.
func (s *Service) ProcessOnce(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM deletion_request WHERE status = 'processing' ORDER BY requested_at LIMIT 50`)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		completed, err := s.processOne(ctx, id)
		if err != nil {
			s.Log.Error("exclusão de conta: processamento falhou; tentará de novo", "solicitacao", id.String(), "error", database.Describe(err))
			continue
		}
		if completed {
			done++
		}
	}
	return done, nil
}

func (s *Service) fault(step string) error {
	if s.Fault == nil {
		return nil
	}
	return s.Fault(step)
}

func (s *Service) processOne(ctx context.Context, id uuid.UUID) (bool, error) {
	completed := false
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		x, err := scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE id = $1 AND status = 'processing' FOR UPDATE SKIP LOCKED`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // outro worker está com ela, ou já concluiu
		}
		if err != nil {
			return err
		}
		now := s.Clock.Now()
		// Uma entrada na fila que confirmou ao mesmo tempo que o pedido pode ter escapado: cancela.
		if _, err := s.Queue.CancelForDeletion(ctx, tx, x.AccountID, now); err != nil {
			return err
		}
		before, _ := storedBlocks(ctx, tx, x.ID)
		blocks, changed, err := s.assess(ctx, tx, &x)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE deletion_request SET checked_at = $2 WHERE id = $1`, x.ID, now); err != nil {
			return err
		}
		if now.Sub(x.RequestedAt) >= AlertAfter {
			s.Log.Warn("exclusão de conta perto da meta operacional de 30 dias (não é prazo legal)",
				"solicitacao", x.ID.String(), "dias", int(now.Sub(x.RequestedAt).Hours()/24), "bloqueios", len(blocks))
		}
		if len(blocks) > 0 {
			if changed {
				return s.notify(ctx, tx, x, blocks, establishmentsOf(blocks, before), now)
			}
			return nil
		}
		evidence, err := s.anonymize(ctx, tx, x, now)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			UPDATE deletion_request SET status = 'completed', completed_at = $2, evidence = $3, version = version + 1
			WHERE id = $1 RETURNING version`, x.ID, now, raw).Scan(&x.Version); err != nil {
			return err
		}
		x.Status, x.CompletedAt = StatusCompleted, &now
		if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorSystem, Action: "T-AC-04",
			TargetType: "account", TargetID: x.AccountID, FromState: identidade.StatusDeletionProcessing,
			ToState: identidade.StatusDeleted}); err != nil {
			return err
		}
		completed = true
		return s.notify(ctx, tx, x, nil, establishmentsOf(before), now)
	})
	return completed, err
}

// evidence registra o que foi tratado, sem dado pessoal: categorias, contagens e instante.
type evidence struct {
	Categories map[string]int64 `json:"categories"`
	At         time.Time        `json:"at"`
	Kept       []string         `json:"kept"`
}

func (s *Service) anonymize(ctx context.Context, tx pgx.Tx, x requestRow, now time.Time) (evidence, error) {
	ev := evidence{Categories: map[string]int64{}, At: now,
		// O que fica (com o pseudônimo, sem perfil): necessário a reconciliação e auditoria.
		Kept: []string{"comandas_e_itens", "correcoes", "auditoria", "participacoes_sem_nick"}}
	profile, err := s.Identity.AnonymizeAccount(ctx, tx, x.AccountID, now)
	if err != nil {
		return ev, err
	}
	for k, v := range profile {
		ev.Categories[k] = v
	}
	if err := s.fault("depois_do_perfil"); err != nil {
		return ev, err
	}
	n, err := fila.AnonymizeParticipations(ctx, tx, x.AccountID)
	if err != nil {
		return ev, err
	}
	ev.Categories["participacoes"] = n
	if err := s.fault("depois_das_participacoes"); err != nil {
		return ev, err
	}
	if n, err = outbox.ForgetAccount(ctx, tx, x.AccountID); err != nil {
		return ev, err
	}
	ev.Categories["eventos"] = n
	return ev, nil
}

// MinimizeVisitorNicks remove o nick de visitantes depois da necessidade operacional (matriz de
// retenção): participações de visitante encerradas há mais de `after` com comanda já encerrada, e
// visitantes cujo acesso já expirou. Repetível.
func (s *Service) MinimizeVisitorNicks(ctx context.Context, after time.Duration) (int64, error) {
	now := s.Clock.Now()
	var total int64
	for {
		list, err := fila.FinishedVisitorParticipations(ctx, s.Pool, now.Add(-after), 500)
		if err != nil || len(list) == 0 {
			break
		}
		orderIDs := make([]uuid.UUID, len(list))
		for i := range list {
			orderIDs[i] = list[i].OrderID
		}
		closed, err := atendimento.ClosedOrders(ctx, s.Pool, orderIDs)
		if err != nil {
			return total, err
		}
		var ids []uuid.UUID
		for _, v := range list {
			if closed[v.OrderID] {
				ids = append(ids, v.ID)
			}
		}
		n, err := fila.ScrubNicks(ctx, s.Pool, ids)
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 || len(list) < 500 {
			break
		}
	}
	n, err := identidade.ScrubVisitorNicks(ctx, s.Pool, now)
	return total + n, err
}

// Run processa as exclusões a cada `every` e minimiza nicks de visitante a cada hora (worker).
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	lastMinimize := time.Time{}
	for {
		if n, err := s.ProcessOnce(ctx); err != nil && ctx.Err() == nil {
			s.Log.Error("exclusões: leitura falhou", "error", database.Describe(err))
		} else if n > 0 {
			s.Log.Info("exclusões de conta concluídas", "quantidade", n)
		}
		if time.Since(lastMinimize) >= time.Hour {
			lastMinimize = time.Now()
			if n, err := s.MinimizeVisitorNicks(ctx, VisitorNickRetention); err != nil && ctx.Err() == nil {
				s.Log.Error("minimização de nicks falhou", "error", database.Describe(err))
			} else if n > 0 {
				s.Log.Info("nicks de visitante removidos (retenção)", "quantidade", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ProcessingInShop conta as exclusões de conta em processamento com bloqueio nesta loja (painel do dono).
func ProcessingInShop(ctx context.Context, q database.Querier, estID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(DISTINCT b.request_id) FROM deletion_request_block b
		JOIN deletion_request d ON d.id = b.request_id WHERE b.establishment_id = $1 AND d.status = 'processing'`, estID).Scan(&n)
	return n, err
}
