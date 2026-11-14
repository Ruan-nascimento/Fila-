package fidelidade

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
	"github.com/jackc/pgx/v5/pgconn"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// Tipos de lançamento (LedgerEntryKind). O saldo é a soma do extrato (ADR-0012).
const (
	KindCredit          = "credit"
	KindCreditReversal  = "credit_reversal"
	KindHold            = "hold"
	KindHoldRelease     = "hold_release"
	KindConsume         = "consume"
	KindConsumeReversal = "consume_reversal"
	KindAccountClosure  = "account_closure"
)

// entry é um lançamento a gravar.
type entry struct {
	EstablishmentID uuid.UUID
	AccountID       uuid.UUID
	Kind            string
	Balance, Held   int64
	SourceType      string // order, reservation, account
	SourceID        uuid.UUID
	Note            string
}

var errInsufficient = errors.New("pontos insuficientes")

// post grava o lançamento uma única vez por (loja, origem, tipo). Devolve false quando já
// existia (repetição: nada muda). O gatilho do banco atualiza saldo e retido.
func post(ctx context.Context, tx pgx.Tx, e entry, now time.Time) (bool, error) {
	var id uuid.UUID
	var note *string
	if e.Note != "" {
		note = &e.Note
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO loyalty_ledger (establishment_id, account_id, kind, balance_delta, held_delta, source_type, source_id, note, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (establishment_id, source_type, source_id, kind) DO NOTHING RETURNING id`,
		e.EstablishmentID, e.AccountID, e.Kind, e.Balance, e.Held, e.SourceType, e.SourceID, note, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23514" && e.Kind == KindHold {
		return false, errInsufficient
	}
	return err == nil, err
}

// lockAccount trava (e cria, se preciso) o saldo da conta na loja. Ordem de travas do módulo:
// saldo da conta → prêmio → reserva.
func lockAccount(ctx context.Context, tx pgx.Tx, estID, accountID uuid.UUID) (balance, held int64, err error) {
	if _, err = tx.Exec(ctx, `INSERT INTO loyalty_account (establishment_id, account_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		estID, accountID); err != nil {
		return 0, 0, err
	}
	err = tx.QueryRow(ctx, `SELECT balance, held FROM loyalty_account WHERE establishment_id = $1 AND account_id = $2 FOR UPDATE`,
		estID, accountID).Scan(&balance, &held)
	return balance, held, err
}

// summaryView segue LoyaltySummary.
type summaryView struct {
	ProgramEnabled    bool             `json:"program_enabled"`
	Balance           int64            `json:"balance"`
	Held              int64            `json:"held"`
	Available         int64            `json:"available"`
	ActiveReservation *reservationView `json:"active_reservation"`
	version           int64
}

// summary lê saldo, retido, disponível e reserva ativa (uma reserva vencida e ainda não
// processada pelo worker não aparece como ativa: o vencimento vale pelo horário).
func summary(ctx context.Context, q database.Querier, estID, accountID uuid.UUID, now time.Time) (summaryView, error) {
	var v summaryView
	err := q.QueryRow(ctx, `SELECT balance, held, version FROM loyalty_account WHERE establishment_id = $1 AND account_id = $2`,
		estID, accountID).Scan(&v.Balance, &v.Held, &v.version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	v.Available = v.Balance - v.Held
	if v.ProgramEnabled, _, err = programState(ctx, q, estID); err != nil {
		return v, err
	}
	r, err := scanReservation(q.QueryRow(ctx, reservationSelect+` WHERE r.establishment_id = $1 AND r.account_id = $2 AND r.status = 'active'`,
		estID, accountID))
	if err == nil {
		r.publicView(false)
		v.ActiveReservation = &r.reservationView
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	return v, nil
}

// Snapshot devolve o resumo de pontos da conta para o snapshot do SSE (canal account).
func (s *Service) Snapshot(ctx context.Context, q database.Querier, estID, accountID uuid.UUID) (any, error) {
	return summary(ctx, q, estID, accountID, s.Clock.Now())
}

// loyaltyChanged grava loyalty.updated para a própria conta (só ela vê o próprio saldo).
func loyaltyChanged(ctx context.Context, tx pgx.Tx, estID, accountID uuid.UUID, now time.Time) error {
	v, err := summary(ctx, tx, estID, accountID, now)
	if err != nil {
		return err
	}
	acc := accountID
	return outbox.Record(ctx, tx, outbox.Event{EstablishmentID: estID, Type: "loyalty.updated", ResourceType: "loyalty_account",
		ResourceID: accountID, ResourceVersion: v.version, Audience: outbox.Audience{Kind: outbox.Account, AccountID: &acc},
		Data: v, OccurredAt: now})
}

// Credit (T-LG-01): crédito dos pontos de uma comanda que acabou de ficar paga e conferida.
// Só conta (nunca visitante), snapshot elegível na entrada, conclusão normal (sem interrupção),
// comanda paga e soma de pontos > 0 (gratuidade não chega a ser paga). Uma vez por comanda.
// Chamado pelo módulo pagamento na MESMA transação em que a comanda vira "paga".
func (s *Service) Credit(ctx context.Context, tx pgx.Tx, estID, orderID uuid.UUID, now time.Time) error {
	c, err := atendimento.CreditInfo(ctx, tx, estID, orderID)
	if err != nil {
		return err
	}
	if c.AccountID == nil || !c.Eligible || c.Interrupted || c.Status != atendimento.StatusSettled || c.Points <= 0 {
		return nil
	}
	if _, _, err := lockAccount(ctx, tx, estID, *c.AccountID); err != nil {
		return err
	}
	ok, err := post(ctx, tx, entry{EstablishmentID: estID, AccountID: *c.AccountID, Kind: KindCredit, Balance: int64(c.Points),
		SourceType: "order", SourceID: orderID}, now)
	if err != nil || !ok {
		return err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-LG-01", TargetType: "order", TargetID: orderID, ToState: "credit:" + strconv.Itoa(c.Points)}); err != nil {
		return err
	}
	return loyaltyChanged(ctx, tx, estID, *c.AccountID, now)
}

// Reverse (T-LG-02): estorno confirmado do recebimento PRINCIPAL reverte o crédito daquela
// comanda uma vez. Sem crédito (visitante, não elegível…) não há o que reverter. O saldo pode
// ficar negativo (pontos já usados): sem dívida em dinheiro; só bloqueia novos resgates.
func (s *Service) Reverse(ctx context.Context, tx pgx.Tx, estID, orderID uuid.UUID, now time.Time) error {
	var accountID uuid.UUID
	var credited int64
	err := tx.QueryRow(ctx, `SELECT account_id, balance_delta FROM loyalty_ledger
		WHERE establishment_id = $1 AND source_type = 'order' AND source_id = $2 AND kind = 'credit'`, estID, orderID).
		Scan(&accountID, &credited)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, _, err := lockAccount(ctx, tx, estID, accountID); err != nil {
		return err
	}
	ok, err := post(ctx, tx, entry{EstablishmentID: estID, AccountID: accountID, Kind: KindCreditReversal, Balance: -credited,
		SourceType: "order", SourceID: orderID, Note: "estorno do pagamento"}, now)
	if err != nil || !ok {
		return err
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
		Action: "T-LG-02", TargetType: "order", TargetID: orderID, ToState: "credit_reversal:" + strconv.FormatInt(credited, 10)}); err != nil {
		return err
	}
	return loyaltyChanged(ctx, tx, estID, accountID, now)
}

// CloseForDeletion (T-RW-06 + T-LG-03): na exclusão de conta, cancela as reservas ativas
// (libera pontos e unidade uma vez) e encerra o saldo de cada loja, sem conversão em dinheiro.
func (s *Service) CloseForDeletion(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT establishment_id FROM loyalty_account WHERE account_id = $1 ORDER BY establishment_id`, accountID)
	if err != nil {
		return err
	}
	ests, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, estID := range ests {
		if _, _, err := lockAccount(ctx, tx, estID, accountID); err != nil {
			return err
		}
		r, err := scanReservation(tx.QueryRow(ctx, reservationSelect+` WHERE r.establishment_id = $1 AND r.account_id = $2
			AND r.status = 'active' FOR UPDATE OF r`, estID, accountID))
		if err == nil {
			if err := s.release(ctx, tx, &r, "cancelled", "system_deletion", "T-RW-06", now); err != nil {
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var balance int64
		if err := tx.QueryRow(ctx, `SELECT balance FROM loyalty_account WHERE establishment_id = $1 AND account_id = $2`, estID, accountID).
			Scan(&balance); err != nil {
			return err
		}
		if balance == 0 {
			continue
		}
		if _, err := post(ctx, tx, entry{EstablishmentID: estID, AccountID: accountID, Kind: KindAccountClosure, Balance: -balance,
			SourceType: "account", SourceID: accountID, Note: "conta excluída: saldo encerrado sem conversão em dinheiro"}, now); err != nil {
			return err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
			Action: "T-LG-03", TargetType: "account", TargetID: accountID, ToState: "account_closure"}); err != nil {
			return err
		}
	}
	// O nome guardado nas reservas (visto pela equipe na entrega) deixa de identificar a pessoa.
	_, err = tx.Exec(ctx, `UPDATE reward_reservation SET holder_name = 'Cliente' WHERE account_id = $1`, accountID)
	return err
}

// PointsCredited devolve, por comanda, os pontos creditados e não revertidos (histórico).
func PointsCredited(ctx context.Context, q database.Querier, orderIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := q.Query(ctx, `SELECT source_id, sum(balance_delta) FROM loyalty_ledger
		WHERE source_type = 'order' AND source_id = ANY($1) GROUP BY source_id`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = int(max(n, 0))
	}
	return out, rows.Err()
}

// customer exige sessão de conta (visitante não tem pontos) numa loja que existe.
func (s *Service) customer(r *http.Request) (uuid.UUID, *acesso.Identity, error) {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return estID, nil, err
	}
	account, _ := s.Customers.CustomerIdentities(r, estID)
	if account == nil {
		return estID, nil, hs.ErrUnauthenticated()
	}
	ok, err := estabelecimento.EstablishmentExists(r.Context(), s.Pool, estID)
	if err != nil {
		return estID, nil, err
	}
	if !ok {
		return estID, nil, hs.ErrNotFound()
	}
	return estID, account, nil
}

// getMyLoyalty: saldo, retido, disponível e reserva ativa da própria conta (consulta continua
// com o programa desligado, R-FID-13).
func (s *Service) getMyLoyalty(w http.ResponseWriter, r *http.Request) error {
	estID, account, err := s.customer(r)
	if err != nil {
		return err
	}
	v, err := summary(r.Context(), s.Pool, estID, account.AccountID, s.Clock.Now())
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, hs.Data[summaryView]{Data: v})
	return nil
}

// ledgerView segue LedgerEntry.
type ledgerView struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	BalanceDelta int64     `json:"balance_delta"`
	HeldDelta    int64     `json:"held_delta"`
	OccurredAt   time.Time `json:"occurred_at"`
	Source       struct {
		Type string    `json:"type"`
		ID   uuid.UUID `json:"id"`
	} `json:"source"`
	Note *string `json:"note"`
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, bool) {
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

type pageInfo struct {
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// pagination lê limit e cursor (mais recentes primeiro).
func pagination(r *http.Request) (int, *time.Time, uuid.UUID, error) {
	q := r.URL.Query()
	var fe []hs.FieldError
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			fe = append(fe, hs.FieldError{Field: "limit", Code: hs.FieldOutOfRange})
		}
		limit = n
	}
	var after *time.Time
	var afterID uuid.UUID
	if raw := q.Get("cursor"); raw != "" {
		at, id, ok := decodeCursor(raw)
		if !ok || len(raw) > 512 {
			fe = append(fe, hs.FieldError{Field: "cursor", Code: hs.FieldInvalidFormat})
		}
		after, afterID = &at, id
	}
	if len(fe) > 0 {
		return 0, nil, uuid.Nil, hs.ErrValidation(fe...)
	}
	return limit, after, afterID, nil
}

// listMyLedger: extrato imutável da própria conta nesta loja (explica o saldo).
func (s *Service) listMyLedger(w http.ResponseWriter, r *http.Request) error {
	estID, account, err := s.customer(r)
	if err != nil {
		return err
	}
	limit, after, afterID, err := pagination(r)
	if err != nil {
		return err
	}
	sql := `SELECT id, kind, balance_delta, held_delta, occurred_at, source_type, source_id, note FROM loyalty_ledger
		WHERE establishment_id = $1 AND account_id = $2`
	args := []any{estID, account.AccountID}
	if after != nil {
		args = append(args, *after, afterID)
		sql += ` AND (occurred_at, id) < ($3, $4)`
	}
	args = append(args, limit+1)
	sql += ` ORDER BY occurred_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ledgerView, error) {
		var v ledgerView
		err := row.Scan(&v.ID, &v.Kind, &v.BalanceDelta, &v.HeldDelta, &v.OccurredAt, &v.Source.Type, &v.Source.ID, &v.Note)
		return v, err
	})
	if err != nil {
		return err
	}
	page := pageInfo{}
	if len(list) > limit {
		list = list[:limit]
		c := encodeCursor(list[len(list)-1].OccurredAt, list[len(list)-1].ID)
		page = pageInfo{NextCursor: &c, HasMore: true}
	}
	if list == nil {
		list = []ledgerView{}
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []ledgerView `json:"data"`
		Page pageInfo     `json:"page"`
	}{list, page})
	return nil
}
