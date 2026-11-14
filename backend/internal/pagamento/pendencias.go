package pagamento

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/outbox"
	"fila/internal/platform/validate"
)

// Tipos de pendência financeira (FinancialIssueKind).
const (
	IssueDuplicate        = "duplicate_receipt"
	IssueLate             = "late_receipt"
	IssueAmountMismatch   = "amount_mismatch"
	IssueChargeUnresolved = "charge_outcome_unresolved"
	IssueRefundReview     = "refund_under_review"
	IssueActivePixSwitch  = "active_pix_on_method_switch"
)

// issueView segue FinancialIssue.
type issueView struct {
	ID                uuid.UUID  `json:"id"`
	OrderID           *uuid.UUID `json:"order_id"`
	ProviderReference *string    `json:"provider_reference"`
	Kind              string     `json:"kind"`
	Status            string     `json:"status"`
	Amount            any        `json:"amount"`
	CreatedAt         time.Time  `json:"created_at"`
	ResolvedAt        *time.Time `json:"resolved_at"`
	ResolutionNote    *string    `json:"resolution_note"`
	establishment     uuid.UUID
}

const issueSelect = `SELECT id, order_id, kind, status, amount_cents, created_at, resolved_at, resolution_note, establishment_id,
	provider_reference FROM financial_issue`

func scanIssue(r pgx.Row) (issueView, error) {
	var v issueView
	var amount *int64
	err := r.Scan(&v.ID, &v.OrderID, &v.Kind, &v.Status, &amount, &v.CreatedAt, &v.ResolvedAt, &v.ResolutionNote, &v.establishment,
		&v.ProviderReference)
	if amount != nil {
		v.Amount = brl(*amount)
	}
	return v, err
}

// openIssue abre a pendência (T-FI-01) uma única vez por (tipo, origem) e grava os eventos do
// dono. Repetir (aviso duplicado, worker de novo) não abre outra. Devolve se abriu agora.
func (s *Service) openIssue(ctx context.Context, tx pgx.Tx, estID, orderID uuid.UUID, kind, sourceType string, sourceID uuid.UUID,
	amount *int64, now time.Time) (bool, error) {
	return s.openIssueRef(ctx, tx, estID, &orderID, kind, sourceType, sourceID, amount, nil, now)
}

// openIssueRef é openIssue com comanda opcional (só a cobrança órfã pós-restauração fica sem) e
// referência do provedor.
func (s *Service) openIssueRef(ctx context.Context, tx pgx.Tx, estID uuid.UUID, orderID *uuid.UUID, kind, sourceType string,
	sourceID uuid.UUID, amount *int64, providerRef *string, now time.Time) (bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO financial_issue (establishment_id, order_id, kind, amount_cents, source_type, source_id, created_at, provider_reference)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (kind, source_id) DO NOTHING RETURNING id`,
		estID, orderID, kind, amount, sourceType, sourceID, now, providerRef).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.Log.Warn("pendência financeira aberta", "tipo", kind, "pendencia", id.String())
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-FI-01", TargetType: "financial_issue", TargetID: id, ToState: "open:" + kind}); err != nil {
		return false, err
	}
	v, err := scanIssue(tx.QueryRow(ctx, issueSelect+` WHERE id = $1`, id))
	if err != nil {
		return false, err
	}
	return true, s.issueEvents(ctx, tx, v, now)
}

// issueEvents grava financial_issue.updated e pendencies.updated (audiência dono).
func (s *Service) issueEvents(ctx context.Context, tx pgx.Tx, v issueView, now time.Time) error {
	if err := outbox.Record(ctx, tx, outbox.Event{EstablishmentID: v.establishment, Audience: outbox.Audience{Kind: outbox.Owner},
		Type: "financial_issue.updated", ResourceType: "financial_issue", ResourceID: v.ID, ResourceVersion: issueVersion(v),
		OccurredAt: now, Data: v}); err != nil {
		return err
	}
	return s.pendenciesChanged(ctx, tx, v.establishment, now)
}

// issueVersion: aberta = 1, resolvida = 2 (a pendência só tem essas duas versões).
func issueVersion(v issueView) int64 {
	if v.Status == "resolved" {
		return 2
	}
	return 1
}

// pendenciesChanged grava pendencies.updated com os contadores atuais do painel (quando o
// painel estiver ligado; ver Deps.Pendencies).
func (s *Service) pendenciesChanged(ctx context.Context, tx pgx.Tx, estID uuid.UUID, now time.Time) error {
	if s.Pendencies == nil {
		return nil
	}
	return s.Pendencies(ctx, tx, estID, now)
}

// OpenIssueKinds devolve os tipos de pendência aberta da comanda (visíveis à equipe na comanda).
func OpenIssueKinds(ctx context.Context, q database.Querier, orderID uuid.UUID) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT kind FROM financial_issue WHERE order_id = $1 AND status = 'open' ORDER BY kind`, orderID)
	if err != nil {
		return nil, err
	}
	kinds, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if kinds == nil {
		kinds = []string{}
	}
	return kinds, err
}

// Counts são os contadores financeiros do painel do dono.
type Counts struct {
	ChargesUnknown      int
	OpenFinancialIssues int
	RefundsUnderReview  int
}

// PendencyCounts conta as pendências financeiras da loja.
func PendencyCounts(ctx context.Context, q database.Querier, estID uuid.UUID) (Counts, error) {
	var c Counts
	err := q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pix_charge WHERE establishment_id = $1 AND status IN ('creating', 'unknown')),
		       (SELECT count(*) FROM financial_issue WHERE establishment_id = $1 AND status = 'open'),
		       (SELECT count(*) FROM refund WHERE establishment_id = $1 AND status = 'under_review')`, estID).
		Scan(&c.ChargesUnknown, &c.OpenFinancialIssues, &c.RefundsUnderReview)
	return c, err
}

func encodeIssueCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeIssueCursor(c string) (time.Time, uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	t, err1 := time.Parse(time.RFC3339Nano, at)
	u, err2 := uuid.Parse(id)
	return t, u, err1 == nil && err2 == nil
}

// listFinancialIssues: só o dono (painel). Mais recentes primeiro; filtro opcional por estado.
func (s *Service) listFinancialIssues(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	if !actor.IsOwner() {
		return hs.ErrForbidden("Somente o dono vê o painel de pendências financeiras.")
	}
	q := r.URL.Query()
	var fe []hs.FieldError
	status := q.Get("status")
	if status != "" && status != "open" && status != "resolved" {
		fe = append(fe, hs.FieldError{Field: "status", Code: hs.FieldNotAllowed})
	}
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			fe = append(fe, hs.FieldError{Field: "limit", Code: hs.FieldOutOfRange})
		}
		limit = n
	}
	var afterAt time.Time
	var afterID uuid.UUID
	cursor := q.Get("cursor")
	if cursor != "" {
		var ok bool
		if afterAt, afterID, ok = decodeIssueCursor(cursor); !ok || len(cursor) > 512 {
			fe = append(fe, hs.FieldError{Field: "cursor", Code: hs.FieldInvalidFormat})
		}
	}
	if len(fe) > 0 {
		return hs.ErrValidation(fe...)
	}
	sql := issueSelect + ` WHERE establishment_id = $1 AND ($2 = '' OR status = $2)`
	args := []any{estID, status}
	if cursor != "" {
		sql += ` AND (created_at, id) < ($3, $4)`
		args = append(args, afterAt, afterID)
	}
	args = append(args, limit+1)
	sql += ` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (issueView, error) { return scanIssue(row) })
	if err != nil {
		return err
	}
	page := struct {
		NextCursor *string `json:"next_cursor"`
		HasMore    bool    `json:"has_more"`
	}{}
	if len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		c := encodeIssueCursor(last.CreatedAt, last.ID)
		page.NextCursor, page.HasMore = &c, true
	}
	if list == nil {
		list = []issueView{}
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []issueView `json:"data"`
		Page any         `json:"page"`
	}{list, page})
	return nil
}

// resolveFinancialIssue (T-FI-02): o dono marca como tratada, com nota. Não altera nem apaga
// recebimentos, cobranças ou estornos.
func (s *Service) resolveFinancialIssue(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	issueID, err := pathID(r, "issueId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	if !actor.IsOwner() {
		return hs.ErrForbidden("Somente o dono resolve pendências financeiras.")
	}
	var body struct {
		Note *string `json:"resolution_note"`
	}
	raw, err := hs.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/resolution_note", body.Note == nil)
	var note string
	if body.Note != nil {
		var fe *hs.FieldError
		note, fe = validate.Text("/resolution_note", *body.Note, 3, 500)
		v.Add(fe)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	return s.idem.Run(w, r, idempotency.Command{ActorKey: "conta:" + actor.AccountID.String(), Operation: "resolveFinancialIssue", Body: raw},
		func(tx pgx.Tx) (idempotency.Result, error) {
			cur, err := scanIssue(tx.QueryRow(ctx, issueSelect+` WHERE establishment_id = $1 AND id = $2 FOR UPDATE`, estID, issueID))
			if errors.Is(err, pgx.ErrNoRows) {
				return idempotency.Result{}, hs.ErrNotFound()
			}
			if err != nil {
				return idempotency.Result{}, err
			}
			if cur.Status != "open" {
				return idempotency.Result{}, conflict("invalid_transition", "Pendência já resolvida", "Esta pendência já foi tratada.", cur)
			}
			now := s.Clock.Now()
			if _, err := tx.Exec(ctx, `UPDATE financial_issue SET status = 'resolved', resolved_at = $2, resolved_by_account_id = $3,
				resolution_note = $4 WHERE id = $1`, issueID, now, actor.AccountID, note); err != nil {
				return idempotency.Result{}, err
			}
			aid := actor.AccountID
			if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
				ActorID: &aid, Action: "T-FI-02", TargetType: "financial_issue", TargetID: issueID, FromState: "open",
				ToState: "resolved", Reason: note}); err != nil {
				return idempotency.Result{}, err
			}
			done, err := scanIssue(tx.QueryRow(ctx, issueSelect+` WHERE id = $1`, issueID))
			if err != nil {
				return idempotency.Result{}, err
			}
			if err := s.issueEvents(ctx, tx, done, now); err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{Status: http.StatusOK, Body: hs.Data[issueView]{Data: done}}, nil
		})
}
