package atendimento

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
)

// ClosedByAccountDeletion é o motivo de encerramento sem cobrança quando a conta pede exclusão
// com visita aguardando ou chamada (T-P-10).
const ClosedByAccountDeletion = "cancelled_by_account_deletion"

// financialPending: comanda com valor a resolver (revisão, pagamento ou conferência). Enquanto
// existir, a exclusão fica "em processamento" e a equipe continua podendo receber (R-EXC-03).
var financialPending = []string{StatusPendingReview, StatusAwaitingPayment, "awaiting_confirmation"}

// PendingFinancialEstablishments devolve as lojas com comanda financeiramente pendente da conta.
func PendingFinancialEstablishments(ctx context.Context, q database.Querier, accountID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT establishment_id FROM service_order WHERE account_id = $1 AND status = ANY($2)`,
		accountID, financialPending)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// PendingInEstablishment conta comandas ainda não encerradas da loja (encerramento da loja).
func PendingInEstablishment(ctx context.Context, q database.Querier, estID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM service_order WHERE establishment_id = $1 AND status NOT IN ('settled', 'closed_no_charge')`,
		estID).Scan(&n)
	return n, err
}

// ClosedOrders informa quais destas comandas já estão encerradas (pagas ou sem cobrança).
func ClosedOrders(ctx context.Context, q database.Querier, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id FROM service_order WHERE id = ANY($1) AND status IN ('settled', 'closed_no_charge')`, ids)
	if err != nil {
		return nil, err
	}
	closed, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	for _, id := range closed {
		out[id] = true
	}
	return out, err
}

type historyItem struct {
	Name      string `json:"name"`
	UnitPrice Money  `json:"unit_price"`
	Charged   bool   `json:"charged"`
}

// historyEntry segue HistoryEntry.
type historyEntry struct {
	OrderID             uuid.UUID     `json:"order_id"`
	VisitedAt           time.Time     `json:"visited_at"`
	ProfessionalName    string        `json:"professional_name"`
	Items               []historyItem `json:"items"`
	Total               Money         `json:"total"`
	OrderStatus         string        `json:"order_status"`
	ParticipationStatus string        `json:"participation_status"`
	PointsCredited      int           `json:"points_credited"`
}

// listMyHistory: histórico PRIVADO da conta, por loja (R-PERM-02, R-VIS-09). Só a sessão de conta
// vê as próprias visitas; visitante não tem histórico, e uma conta nova não herda visitas de
// visitante (as comandas de visitante apontam para o visitante, nunca para a conta).
func (s *Service) listMyHistory(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	var fe []hs.FieldError
	estID, err := uuid.Parse(q.Get("establishment_id"))
	if err != nil {
		fe = append(fe, hs.FieldError{Field: "establishment_id", Code: hs.FieldInvalidFormat})
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
	hasCursor := false
	if raw := q.Get("cursor"); raw != "" {
		var ok bool
		if afterAt, afterID, ok = decodeCursor(raw); !ok || len(raw) > 512 {
			fe = append(fe, hs.FieldError{Field: "cursor", Code: hs.FieldInvalidFormat})
		}
		hasCursor = true
	}
	if len(fe) > 0 {
		return hs.ErrValidation(fe...)
	}
	account, _ := s.Customers.CustomerIdentities(r, estID)
	if account == nil {
		return hs.ErrUnauthenticated() // visitante ou ninguém: histórico é só de conta
	}
	ctx := r.Context()
	args := []any{estID, account.AccountID, limit + 1}
	sql := `SELECT id, participation_id, professional_id, status, total_cents, created_at FROM service_order
		WHERE establishment_id = $1 AND account_id = $2`
	if hasCursor {
		args = append(args, afterAt, afterID)
		sql += ` AND (created_at, id) < ($4, $5)`
	}
	rows, err := s.Pool.Query(ctx, sql+` ORDER BY created_at DESC, id DESC LIMIT $3`, args...)
	if err != nil {
		return err
	}
	type row struct {
		id, part, prof uuid.UUID
		status         string
		total          int64
		at             time.Time
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.id, &x.part, &x.prof, &x.status, &x.total, &x.at)
		return x, err
	})
	if err != nil {
		return err
	}
	page := pageInfo{}
	if len(list) > limit {
		list = list[:limit]
		c := encodeCursor(list[len(list)-1].at, list[len(list)-1].id)
		page = pageInfo{NextCursor: &c, HasMore: true}
	}
	orderIDs, partIDs, profIDs := make([]uuid.UUID, 0, len(list)), make([]uuid.UUID, 0, len(list)), make([]uuid.UUID, 0, len(list))
	for _, x := range list {
		orderIDs, partIDs, profIDs = append(orderIDs, x.id), append(partIDs, x.part), append(profIDs, x.prof)
	}
	names, err := estabelecimento.ProfessionalNames(ctx, s.Pool, profIDs)
	if err != nil {
		return err
	}
	statuses, err := s.Visits.Statuses(ctx, s.Pool, partIDs)
	if err != nil {
		return err
	}
	items := map[uuid.UUID][]historyItem{}
	irows, err := s.Pool.Query(ctx, `SELECT order_id, name, unit_price_cents, charged FROM order_item
		WHERE order_id = ANY($1) AND status <> 'removed' ORDER BY added_at, name`, orderIDs)
	if err != nil {
		return err
	}
	defer irows.Close()
	for irows.Next() {
		var oid uuid.UUID
		var it historyItem
		var price int64
		if err := irows.Scan(&oid, &it.Name, &price, &it.Charged); err != nil {
			return err
		}
		it.UnitPrice = brl(price)
		items[oid] = append(items[oid], it)
	}
	if err := irows.Err(); err != nil {
		return err
	}
	data := make([]historyEntry, 0, len(list))
	for _, x := range list {
		its := items[x.id]
		if its == nil {
			its = []historyItem{}
		}
		// Pontos creditados chegam com o programa de fidelidade (etapa 10); até lá, zero.
		data = append(data, historyEntry{OrderID: x.id, VisitedAt: x.at, ProfessionalName: names[x.prof], Items: its,
			Total: brl(x.total), OrderStatus: x.status, ParticipationStatus: statuses[x.part]})
	}
	hs.WriteJSON(w, http.StatusOK, struct {
		Data []historyEntry `json:"data"`
		Page pageInfo       `json:"page"`
	}{Data: data, Page: page})
	return nil
}
