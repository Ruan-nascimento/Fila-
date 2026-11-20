package analitico

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
)

// GET …/records/{kind}: os registros que formam um total, paginados no servidor (chave
// (instante, id) decrescente). O recorte (período, bucket, dia/hora, faixa, situação e filtros) é
// o mesmo do gráfico; "total" é a contagem do recorte inteiro, para a tabela reconciliar com o
// valor clicado. Nenhum dado de contato: o cliente aparece só pela senha da fila.

const pageSize = 50

type recordsView struct {
	Kind       string  `json:"kind"`
	Total      int64   `json:"total"`
	TotalCents *int64  `json:"total_cents"`
	Items      []any   `json:"items"`
	NextCursor *string `json:"next_cursor"`
	Window     struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"window"`
}

type cursor struct {
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"id"`
}

func encodeCursor(t time.Time, id uuid.UUID) string {
	raw, _ := json.Marshal(cursor{T: t, ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (*cursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) > 200 {
		return nil, fieldErr("/cursor", hs.FieldInvalidFormat, "")
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fieldErr("/cursor", hs.FieldInvalidFormat, "")
	}
	return &c, nil
}

// recordKind monta a consulta de um tipo de registro. where recebe os parâmetros posicionais a
// partir de $6 ($1 loja, $2 início, $3 fim, $4 cursor t, $5 cursor id).
type recordKind struct {
	extra        []string // parâmetros aceitos além dos comuns
	inapplicable []string
	ts           string // coluna do instante de referência
	from         string // FROM … (alias principal com id)
	idCol        string
	cents        string // expressão somada em total_cents ("" = sem valor)
	cols         string // colunas do item
	scan         func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error)
	where        func(v url.Values, sc *scope, next func(any) string) (string, error)
}

func filterProfOrder(sc *scope, next func(any) string) string {
	if sc.q.Filters.ProfessionalID == nil {
		return ""
	}
	return " AND o.professional_id = " + next(*sc.q.Filters.ProfessionalID)
}

func filterProfQueue(sc *scope, next func(any) string) string {
	if sc.q.Filters.ProfessionalID == nil {
		return ""
	}
	return " AND q.professional_id = " + next(*sc.q.Filters.ProfessionalID)
}

func filterServicePart(sc *scope, next func(any) string) string {
	if sc.q.Filters.ServiceID == nil {
		return ""
	}
	return " AND EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = p.order_id AND i.service_id = " +
		next(*sc.q.Filters.ServiceID) + " AND i.status <> 'removed')"
}

func comanda(est, order uuid.UUID) string {
	return "/gestao/" + est.String() + "/comandas/" + order.String()
}

var recordKinds = map[string]recordKind{
	"receipts": {
		inapplicable: []string{"service_id"},
		ts:           "r.registered_at", idCol: "r.id", cents: "r.amount_cents",
		from: "receipt r JOIN service_order o ON o.establishment_id = r.establishment_id AND o.id = r.order_id LEFT JOIN participation p ON p.id = o.participation_id",
		cols: "r.id, r.registered_at, r.method, r.amount_cents, r.classification, r.cash_tendered_cents, r.change_cents, o.id, p.ticket_code",
		scan: func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error) {
			var x struct {
				ID             uuid.UUID `json:"id"`
				At             time.Time `json:"at"`
				Method         string    `json:"method"`
				AmountCents    int64     `json:"amount_cents"`
				Classification string    `json:"classification"`
				TenderedCents  *int64    `json:"cash_tendered_cents"`
				ChangeCents    *int64    `json:"change_cents"`
				OrderID        uuid.UUID `json:"order_id"`
				Ticket         *string   `json:"ticket_code"`
				GestaoPath     string    `json:"gestao_path"`
			}
			err := rows.Scan(&x.ID, &x.At, &x.Method, &x.AmountCents, &x.Classification, &x.TenderedCents, &x.ChangeCents, &x.OrderID, &x.Ticket)
			x.GestaoPath = comanda(est, x.OrderID)
			return x, x.At, x.ID, err
		},
		where: func(v url.Values, sc *scope, next func(any) string) (string, error) {
			w := filterProfOrder(sc, next)
			if sc.q.Filters.Method != "" {
				w += " AND r.method = " + next(sc.q.Filters.Method)
			}
			return w, nil
		},
	},
	"refunds": {
		inapplicable: []string{"service_id"},
		ts:           "f.resolved_at", idCol: "f.id", cents: "f.amount_cents",
		from: "refund f JOIN service_order o ON o.establishment_id = f.establishment_id AND o.id = f.order_id LEFT JOIN participation p ON p.id = o.participation_id",
		cols: "f.id, f.resolved_at, f.method, f.amount_cents, f.requested_at, o.id, p.ticket_code",
		scan: func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error) {
			var x struct {
				ID          uuid.UUID `json:"id"`
				At          time.Time `json:"at"`
				Method      string    `json:"method"`
				AmountCents int64     `json:"amount_cents"`
				RequestedAt time.Time `json:"requested_at"`
				OrderID     uuid.UUID `json:"order_id"`
				Ticket      *string   `json:"ticket_code"`
				GestaoPath  string    `json:"gestao_path"`
			}
			err := rows.Scan(&x.ID, &x.At, &x.Method, &x.AmountCents, &x.RequestedAt, &x.OrderID, &x.Ticket)
			x.GestaoPath = comanda(est, x.OrderID)
			return x, x.At, x.ID, err
		},
		where: func(v url.Values, sc *scope, next func(any) string) (string, error) {
			w := " AND f.status = 'confirmed'" + filterProfOrder(sc, next)
			if sc.q.Filters.Method != "" {
				w += " AND f.method = " + next(sc.q.Filters.Method)
			}
			return w, nil
		},
	},
	"orders": {
		ts: "o.closed_at", idCol: "o.id", cents: "o.total_cents",
		from: "service_order o LEFT JOIN participation p ON p.id = o.participation_id JOIN professional pr ON pr.id = o.professional_id",
		cols: "o.id, o.closed_at, o.total_cents, o.payment_method, p.ticket_code, pr.display_name, " +
			"(SELECT count(*) FROM order_item i WHERE i.order_id = o.id AND i.charged), " +
			"EXISTS (SELECT 1 FROM order_adjustment a WHERE a.order_id = o.id)",
		scan: func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error) {
			var x struct {
				ID           uuid.UUID `json:"id"`
				At           time.Time `json:"at"`
				TotalCents   int64     `json:"total_cents"`
				Method       *string   `json:"method"`
				Ticket       *string   `json:"ticket_code"`
				Professional string    `json:"professional_name"`
				Items        int64     `json:"charged_items"`
				Adjusted     bool      `json:"adjusted_after_close"`
				GestaoPath   string    `json:"gestao_path"`
			}
			err := rows.Scan(&x.ID, &x.At, &x.TotalCents, &x.Method, &x.Ticket, &x.Professional, &x.Items, &x.Adjusted)
			x.GestaoPath = comanda(est, x.ID)
			return x, x.At, x.ID, err
		},
		where: func(v url.Values, sc *scope, next func(any) string) (string, error) {
			w := " AND o.status = 'settled'" + filterProfOrder(sc, next)
			if sc.q.Filters.ServiceID != nil {
				w += " AND EXISTS (SELECT 1 FROM order_item i WHERE i.order_id = o.id AND i.charged AND i.service_id = " + next(*sc.q.Filters.ServiceID) + ")"
			}
			if sc.q.Filters.Method != "" {
				w += " AND o.payment_method = " + next(sc.q.Filters.Method)
			}
			return w, nil
		},
	},
	"participations": {
		extra: []string{"ref", "dow", "hour", "status", "band"}, inapplicable: []string{"method"},
		idCol: "p.id",
		from:  "participation p JOIN queue q ON q.id = p.queue_id JOIN professional pr ON pr.id = q.professional_id",
		cols: "p.id, p.ticket_code, p.status, p.created_at, p.called_at, p.started_at, p.finished_at, pr.display_name, " +
			"EXISTS (SELECT 1 FROM participation_transfer t WHERE t.participation_id = p.id), p.order_id",
		scan: func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error) {
			var x struct {
				ID           uuid.UUID  `json:"id"`
				Ticket       string     `json:"ticket_code"`
				Status       string     `json:"status"`
				EnteredAt    time.Time  `json:"entered_at"`
				CalledAt     *time.Time `json:"called_at"`
				StartedAt    *time.Time `json:"started_at"`
				FinishedAt   *time.Time `json:"finished_at"`
				Professional string     `json:"professional_name"`
				Transferred  bool       `json:"transferred"`
				WaitSeconds  *int64     `json:"wait_seconds"`
				DurSeconds   *int64     `json:"duration_seconds"`
				OrderID      uuid.UUID  `json:"order_id"`
				GestaoPath   string     `json:"gestao_path"`
				refAt        time.Time
			}
			err := rows.Scan(&x.ID, &x.Ticket, &x.Status, &x.EnteredAt, &x.CalledAt, &x.StartedAt, &x.FinishedAt, &x.Professional,
				&x.Transferred, &x.OrderID, &x.refAt)
			if x.CalledAt != nil && !x.CalledAt.Before(x.EnteredAt) {
				w := int64(x.CalledAt.Sub(x.EnteredAt).Seconds())
				x.WaitSeconds = &w
			}
			if x.StartedAt != nil && x.FinishedAt != nil && x.Status == "completed" {
				d := int64(x.FinishedAt.Sub(*x.StartedAt).Seconds())
				x.DurSeconds = &d
			}
			x.GestaoPath = comanda(est, x.OrderID)
			return x, x.refAt, x.ID, err
		},
	},
	"service_items": {
		extra: []string{"ref"}, inapplicable: []string{"method"},
		idCol: "i.id", cents: "CASE WHEN i.charged THEN i.unit_price_cents ELSE 0 END",
		from: "order_item i JOIN service_order o ON o.establishment_id = i.establishment_id AND o.id = i.order_id LEFT JOIN participation p ON p.id = o.participation_id",
		cols: "i.id, i.service_id, i.name, i.status, i.unit_price_cents, i.charged, i.added_at, i.performed_at, o.id, p.ticket_code",
		scan: func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error) {
			var x struct {
				ID          uuid.UUID  `json:"id"`
				ServiceID   uuid.UUID  `json:"service_id"`
				Name        string     `json:"name"`
				Status      string     `json:"status"`
				UnitCents   int64      `json:"unit_price_cents"`
				Charged     bool       `json:"charged"`
				AddedAt     time.Time  `json:"added_at"`
				PerformedAt *time.Time `json:"performed_at"`
				OrderID     uuid.UUID  `json:"order_id"`
				Ticket      *string    `json:"ticket_code"`
				GestaoPath  string     `json:"gestao_path"`
				refAt       time.Time
			}
			err := rows.Scan(&x.ID, &x.ServiceID, &x.Name, &x.Status, &x.UnitCents, &x.Charged, &x.AddedAt, &x.PerformedAt, &x.OrderID, &x.Ticket, &x.refAt)
			x.GestaoPath = comanda(est, x.OrderID)
			return x, x.refAt, x.ID, err
		},
	},
	// Pendências financeiras (financial_issue). state=open: abertas AGORA (ignora o período);
	// opened / resolved: abertas ou resolvidas dentro do período.
	"financial_issues": {
		extra: []string{"state", "kind"}, inapplicable: []string{"professional_id", "service_id", "method"},
		idCol: "fi.id", cents: "coalesce(fi.amount_cents, 0)",
		from: "financial_issue fi LEFT JOIN service_order o ON o.id = fi.order_id LEFT JOIN participation p ON p.id = o.participation_id",
		cols: "fi.id, fi.kind, fi.status, fi.amount_cents, fi.created_at, fi.resolved_at, fi.order_id, p.ticket_code",
		scan: func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error) {
			var x struct {
				ID          uuid.UUID  `json:"id"`
				Kind        string     `json:"kind"`
				Status      string     `json:"status"`
				AmountCents *int64     `json:"amount_cents"`
				CreatedAt   time.Time  `json:"created_at"`
				ResolvedAt  *time.Time `json:"resolved_at"`
				OrderID     uuid.UUID  `json:"order_id"`
				Ticket      *string    `json:"ticket_code"`
				GestaoPath  string     `json:"gestao_path"`
				refAt       time.Time
			}
			err := rows.Scan(&x.ID, &x.Kind, &x.Status, &x.AmountCents, &x.CreatedAt, &x.ResolvedAt, &x.OrderID, &x.Ticket, &x.refAt)
			x.GestaoPath = "/gestao/" + est.String() + "/pendencias"
			return x, x.refAt, x.ID, err
		},
		where: func(v url.Values, sc *scope, next func(any) string) (string, error) {
			w := ""
			switch v.Get("state") {
			case "", "open":
				w = " AND fi.status = 'open'"
			case "opened":
			case "resolved":
				w = " AND fi.status = 'resolved'"
			default:
				return "", fieldErr("/state", hs.FieldNotAllowed, "use open, opened ou resolved")
			}
			if k := v.Get("kind"); k != "" {
				w += " AND fi.kind = " + next(k)
			}
			return w, nil
		},
	},
	"loyalty_entries": {
		extra: []string{"kind"}, inapplicable: []string{"professional_id", "service_id", "method"},
		ts: "l.occurred_at", idCol: "l.id",
		from: "loyalty_ledger l",
		cols: "l.id, l.occurred_at, l.kind, l.balance_delta, l.source_type",
		scan: func(rows pgx.Rows, est uuid.UUID) (any, time.Time, uuid.UUID, error) {
			var x struct {
				ID         uuid.UUID `json:"id"`
				At         time.Time `json:"at"`
				Kind       string    `json:"kind"`
				Delta      int64     `json:"points_delta"`
				SourceType string    `json:"source_type"`
			}
			err := rows.Scan(&x.ID, &x.At, &x.Kind, &x.Delta, &x.SourceType)
			return x, x.At, x.ID, err
		},
		where: func(v url.Values, sc *scope, next func(any) string) (string, error) {
			kinds := map[string]string{"credited": "'credit'", "used": "'consume'", "returned": "'consume_reversal'",
				"adjusted": "'credit_reversal', 'account_closure'"}
			k := v.Get("kind")
			if k == "" {
				return " AND l.kind IN ('credit', 'consume', 'consume_reversal', 'credit_reversal', 'account_closure')", nil
			}
			in, ok := kinds[k]
			if !ok {
				return "", fieldErr("/kind", hs.FieldNotAllowed, "use credited, used, returned ou adjusted")
			}
			return " AND l.kind IN (" + in + ")", nil
		},
	},
}

func init() {
	pk := recordKinds["participations"]
	pk.where = func(v url.Values, sc *scope, next func(any) string) (string, error) {
		w := filterProfQueue(sc, next) + filterServicePart(sc, next)
		if st := v.Get("status"); st != "" {
			switch st {
			case "waiting", "called", "in_service", "completed", "cancelled", "no_show", "interrupted":
				w += " AND p.status = " + next(st)
			default:
				return "", fieldErr("/status", hs.FieldNotAllowed, "")
			}
		}
		if v.Get("dow") != "" || v.Get("hour") != "" {
			dow, e1 := strconv.Atoi(v.Get("dow"))
			hour, e2 := strconv.Atoi(v.Get("hour"))
			if e1 != nil || e2 != nil || dow < 1 || dow > 7 || hour < 0 || hour > 23 {
				return "", fieldErr("/dow", hs.FieldOutOfRange, "dow 1–7 (segunda = 1) e hour 0–23, juntos")
			}
			tz := next(sc.tz)
			w += " AND extract(isodow FROM p.created_at AT TIME ZONE " + tz + ") = " + next(dow) +
				" AND extract(hour FROM p.created_at AT TIME ZONE " + tz + ") = " + next(hour)
		}
		if b := v.Get("band"); b != "" {
			found := false
			for _, wb := range waitBands {
				if wb.Key == b {
					found = true
					w += " AND p.called_at IS NOT NULL AND p.called_at >= p.created_at AND p.called_at - p.created_at >= make_interval(mins => " + next(wb.From) + ")"
					if wb.To > 0 {
						w += " AND p.called_at - p.created_at < make_interval(mins => " + next(wb.To) + ")"
					}
				}
			}
			if !found {
				return "", fieldErr("/band", hs.FieldNotAllowed, "")
			}
		}
		return w, nil
	}
	recordKinds["participations"] = pk

	si := recordKinds["service_items"]
	si.where = func(v url.Values, sc *scope, next func(any) string) (string, error) {
		w := filterProfOrder(sc, next)
		if sc.q.Filters.ServiceID != nil {
			w += " AND i.service_id = " + next(*sc.q.Filters.ServiceID)
		}
		if v.Get("ref") == "added" {
			w += " AND i.status <> 'removed'"
		} else {
			w += " AND i.status = 'performed'"
		}
		return w, nil
	}
	recordKinds["service_items"] = si
}

// refColumn escolhe o instante de referência de participações e itens.
func refColumn(kind, ref string) (string, error) {
	switch kind {
	case "participations":
		switch ref {
		case "", "entered":
			return "p.created_at", nil
		case "called":
			return "p.called_at", nil
		case "finished":
			return "p.finished_at", nil
		}
		return "", fieldErr("/ref", hs.FieldNotAllowed, "use entered, called ou finished")
	case "financial_issues":
		if ref == "resolved" {
			return "fi.resolved_at", nil
		}
		return "fi.created_at", nil
	case "service_items":
		switch ref {
		case "", "performed":
			return "i.performed_at", nil
		case "added":
			return "i.added_at", nil
		}
		return "", fieldErr("/ref", hs.FieldNotAllowed, "use performed ou added")
	}
	return "", nil
}

func (s *Service) records(w http.ResponseWriter, r *http.Request) error {
	kind := r.PathValue("kind")
	def, known := recordKinds[kind]
	if !known {
		return hs.ErrNotFound()
	}
	sc, err := s.authorize(r, append([]string{"bucket_start", "bucket_end", "cursor"}, def.extra...)...)
	if err != nil {
		return err
	}
	for _, f := range def.inapplicable {
		if sc.active(f) {
			return &hs.Error{Status: 422, Code: "analytics_filter_not_applicable", Title: "Filtro não se aplica",
				Detail: "Este detalhamento não tem atribuição confiável para o filtro escolhido. Limpe o filtro para ver os registros."}
		}
	}
	v := r.URL.Query()
	ts := def.ts
	if ts == "" {
		ref := v.Get("ref")
		if kind == "financial_issues" {
			ref = v.Get("state")
		}
		if ts, err = refColumn(kind, ref); err != nil {
			return err
		}
	}
	start, end := sc.q.Period.Start, sc.q.Period.EffectiveEnd
	if kind == "financial_issues" && (v.Get("state") == "" || v.Get("state") == "open") {
		start, end = sc.created, sc.now.Add(time.Second) // fotografia atual: todas as abertas
	}
	if bs := v.Get("bucket_start"); bs != "" {
		t, err := time.Parse(time.RFC3339, bs)
		if err != nil {
			return fieldErr("/bucket_start", hs.FieldInvalidFormat, "RFC 3339")
		}
		if t.After(start) {
			start = t
		}
	}
	if be := v.Get("bucket_end"); be != "" {
		t, err := time.Parse(time.RFC3339, be)
		if err != nil {
			return fieldErr("/bucket_end", hs.FieldInvalidFormat, "RFC 3339")
		}
		if t.Before(end) {
			end = t
		}
	}
	cur, err := decodeCursor(v.Get("cursor"))
	if err != nil {
		return err
	}
	params := []any{sc.est, start, end}
	next := func(x any) string {
		params = append(params, x)
		return "$" + strconv.Itoa(len(params))
	}
	extraWhere := ""
	if def.where != nil {
		if extraWhere, err = def.where(v, sc, next); err != nil {
			return err
		}
	}
	alias := strings.SplitN(def.idCol, ".", 2)[0]
	baseWhere := fmt.Sprintf(" WHERE %s.establishment_id = $1 AND %s >= $2 AND %s < $3", alias, ts, ts) + extraWhere
	countSQL := "SELECT count(*), " + coalesceCents(def.cents) + " FROM " + def.from + baseWhere
	pageWhere := baseWhere
	if cur != nil {
		pageWhere += fmt.Sprintf(" AND (%s, %s) < (%s, %s)", ts, def.idCol, next(cur.T), next(cur.ID))
	}
	listSQL := "SELECT " + def.cols + ", " + ts + " FROM " + def.from + pageWhere +
		fmt.Sprintf(" ORDER BY %s DESC, %s DESC LIMIT %d", ts, def.idCol, pageSize+1)
	if def.scan == nil {
		return hs.ErrNotFound()
	}
	// As colunas do item terminam com o instante de referência (repetido) só nos tipos que o leem.
	if def.ts != "" {
		listSQL = "SELECT " + def.cols + " FROM " + def.from + pageWhere +
			fmt.Sprintf(" ORDER BY %s DESC, %s DESC LIMIT %d", ts, def.idCol, pageSize+1)
	}
	var out recordsView
	out.Kind = kind
	out.Window.Start, out.Window.End = start.UTC(), end.UTC()
	out.Items = []any{}
	ctx := r.Context()
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		nParams := len(params)
		if cur != nil {
			nParams -= 2
		}
		var cents int64
		if err := tx.QueryRow(ctx, countSQL, params[:nParams]...).Scan(&out.Total, &cents); err != nil {
			return fmt.Errorf("contar registros: %w", err)
		}
		if def.cents != "" {
			out.TotalCents = &cents
		}
		rows, err := tx.Query(ctx, listSQL, params...)
		if err != nil {
			return fmt.Errorf("listar registros: %w", err)
		}
		defer rows.Close()
		var lastT time.Time
		var lastID uuid.UUID
		for rows.Next() {
			item, t, id, err := def.scan(rows, sc.est)
			if err != nil {
				return err
			}
			if len(out.Items) == pageSize {
				c := encodeCursor(lastT, lastID)
				out.NextCursor = &c
				break
			}
			out.Items = append(out.Items, item)
			lastT, lastID = t, id
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	write(w, out, sc.meta(def.inapplicable...))
	return nil
}

func coalesceCents(expr string) string {
	if expr == "" {
		return "0::bigint"
	}
	return "coalesce(sum(" + expr + "), 0)::bigint"
}
