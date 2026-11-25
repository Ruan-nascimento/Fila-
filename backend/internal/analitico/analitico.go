// Package analitico atende o Dashboard do programa desktop do dono (ADR-0027).
//
// Só lê (TestAnalyticsIsReadOnly) e agrega no banco: o programa nunca baixa as transações para
// somar. Cada resposta diz o período efetivo, o fuso, o instante da leitura, as unidades e o que
// significa um valor ausente. Autorização: acesso.DesktopAuthorizer (sessão de dono ocupante +
// prova DPoP de instalação autorizada + vínculo de dono com a loja). Sem cache no servidor; a
// resposta sai com Cache-Control: no-store.
package analitico

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
)

// Deps são as dependências do módulo.
type Deps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	Auth  acesso.DesktopAuthorizer
	// Hub e Revalidate alimentam o fluxo de invalidação (stream.go); nil = sem fluxo.
	Stream StreamDeps
}

// Service atende /v1/desktop/analytics/….
type Service struct{ Deps }

// New cria o serviço.
func New(d Deps) *Service { return &Service{Deps: d} }

const base = "/v1/desktop/analytics/establishments/{establishmentId}"

var grant = []hs.Grant{{Actor: "owner_desktop", Scope: "establishment"}}

// Routes devolve as operações do contrato.
func (s *Service) Routes() []hs.Route {
	return []hs.Route{
		{Operation: "getAnalyticsSummary", Method: http.MethodGet, Path: base + "/summary", Grants: grant, Handler: s.summary},
		{Operation: "getAnalyticsSeries", Method: http.MethodGet, Path: base + "/series/{metric}", Grants: grant, Handler: s.series},
		{Operation: "getAnalyticsBreakdown", Method: http.MethodGet, Path: base + "/breakdowns/{dimension}", Grants: grant, Handler: s.breakdown},
		{Operation: "listAnalyticsRecords", Method: http.MethodGet, Path: base + "/records/{kind}", Grants: grant, Handler: s.records},
		{Operation: "getAnalyticsAppointments", Method: http.MethodGet, Path: base + "/appointments", Grants: grant, Handler: s.appointments},
		{Operation: "openAnalyticsStream", Method: http.MethodGet, Path: base + "/stream", Grants: grant, Handler: s.stream},
	}
}

// ---------------------------------------------------------------------------------------------
// Envelope

// Value é um número com a semântica de ausência explícita.
//   - ok: value é o número (0 = zero real);
//   - empty: não houve observações no período (value null);
//   - not_collected: o período é anterior ao início da coleta da loja (value null);
//   - not_applicable: o filtro ativo não tem atribuição confiável nesta métrica (value null).
type Value struct {
	Value  *float64 `json:"value"`
	Status string   `json:"status"`
	Sample *int64   `json:"sample,omitempty"`
}

func ok(v float64) Value           { return Value{Value: &v, Status: "ok"} }
func okN(v float64, n int64) Value { return Value{Value: &v, Status: "ok", Sample: &n} }
func empty(n int64) Value          { return Value{Status: "empty", Sample: &n} }
func status(st string) Value       { return Value{Status: st} }
func fromPtr(v *float64, n int64) Value {
	if v == nil || n == 0 {
		return empty(n)
	}
	return okN(*v, n)
}

type periodView struct {
	From         string    `json:"from"`
	To           string    `json:"to"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	EffectiveEnd time.Time `json:"effective_end"`
}

func viewOf(p Period) periodView {
	return periodView{From: p.From, To: p.To, Start: p.Start.UTC(), End: p.End.UTC(), EffectiveEnd: p.EffectiveEnd.UTC()}
}

type meta struct {
	EstablishmentID     uuid.UUID   `json:"establishment_id"`
	EstablishmentName   string      `json:"establishment_name"`
	Timezone            string      `json:"timezone"`
	Period              periodView  `json:"period"`
	ComparePeriod       *periodView `json:"compare_period"`
	Granularity         *string     `json:"granularity,omitempty"`
	GeneratedAt         time.Time   `json:"generated_at"`
	SnapshotAt          *time.Time  `json:"snapshot_at,omitempty"`
	CollectionStartedAt time.Time   `json:"collection_started_at"`
	Filters             filtersView `json:"filters"`
	IgnoredFilters      []string    `json:"ignored_filters"`
	Units               unitsView   `json:"units"`
	Missing             string      `json:"missing"`
}

type filtersView struct {
	ProfessionalID *uuid.UUID `json:"professional_id"`
	ServiceID      *uuid.UUID `json:"service_id"`
	Method         *string    `json:"method"`
}

type unitsView struct {
	Money    string `json:"money"`
	Duration string `json:"duration"`
	Currency string `json:"currency"`
}

const missingText = "value null: status explica (empty = sem observações; not_collected = antes do início da coleta; not_applicable = filtro sem atribuição confiável). 0 com status ok é zero real."

type envelope[T any] struct {
	Data T    `json:"data"`
	Meta meta `json:"meta"`
}

// scope é o contexto autorizado de uma consulta.
type scope struct {
	est     uuid.UUID
	name    string
	loc     *time.Location
	tz      string
	created time.Time
	now     time.Time
	q       Query
}

// authorize confere a autorização desktop e carrega o fuso da loja.
func (s *Service) authorize(r *http.Request, extra ...string) (*scope, error) {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return nil, hs.ErrNotFound()
	}
	if _, err := s.Auth.DesktopOwner(r, estID); err != nil {
		return nil, err
	}
	created, tz, name, err := collectionStart(r.Context(), s.Pool, estID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hs.ErrNotFound()
	}
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	q, err := parseQuery(r.URL.Query(), loc, now, allowedParams(extra...))
	if err != nil {
		return nil, err
	}
	return &scope{est: estID, name: name, loc: loc, tz: tz, created: created, now: now, q: q}, nil
}

func (sc *scope) meta(ignored ...string) meta {
	m := meta{EstablishmentID: sc.est, EstablishmentName: sc.name, Timezone: sc.tz, Period: viewOf(sc.q.Period),
		GeneratedAt: sc.now.UTC(), CollectionStartedAt: sc.created.UTC(),
		Filters: filtersView{ProfessionalID: sc.q.Filters.ProfessionalID, ServiceID: sc.q.Filters.ServiceID},
		Units:   unitsView{Money: "cents", Duration: "seconds", Currency: "BRL"}, Missing: missingText, IgnoredFilters: []string{}}
	if sc.q.Filters.Method != "" {
		m.Filters.Method = &sc.q.Filters.Method
	}
	if sc.q.Compare != nil {
		v := viewOf(*sc.q.Compare)
		m.ComparePeriod = &v
	}
	for _, f := range ignored {
		if sc.active(f) {
			m.IgnoredFilters = append(m.IgnoredFilters, f)
		}
	}
	return m
}

func (sc *scope) active(filter string) bool {
	switch filter {
	case "professional_id":
		return sc.q.Filters.ProfessionalID != nil
	case "service_id":
		return sc.q.Filters.ServiceID != nil
	case "method":
		return sc.q.Filters.Method != ""
	}
	return false
}

func (sc *scope) args(p Period, gran string) args {
	return args{est: sc.est, gran: gran, tz: sc.tz, start: p.Start, end: p.EffectiveEnd, f: sc.q.Filters}
}

// beforeCollection: o período termina antes de a loja existir no sistema.
func (sc *scope) beforeCollection(p Period) bool { return !p.End.After(sc.created) }

func write[T any](w http.ResponseWriter, data T, m meta) {
	w.Header().Set("Cache-Control", "no-store")
	hs.WriteJSON(w, http.StatusOK, envelope[T]{Data: data, Meta: m})
}

// ---------------------------------------------------------------------------------------------
// GET …/summary: os quatro indicadores da primeira dobra.

type cashKPI struct {
	Received Value `json:"received_cents"`
	Refunded Value `json:"refunded_cents"`
	Net      Value `json:"net_cents"`
	Receipts Value `json:"receipts"`
	Refunds  Value `json:"refunds"`
	// Custos de recebimento (ADR-0034): estimados pela configuração do dono, efetivos quando o
	// provedor informa. AfterCosts = Net − custos conhecidos ou estimados (NÃO é lucro).
	// FeeCoverage = fração dos recebimentos com custo conhecido ou estimado (dinheiro conta como
	// coberto); abaixo de 1, o resultado é incompleto.
	FeesEstimated Value    `json:"fees_estimated_cents"`
	FeesKnown     Value    `json:"fees_known_cents"`
	AfterCosts    Value    `json:"after_costs_cents"`
	FeeCoverage   Value    `json:"fee_coverage"`
	Ignored       []string `json:"ignored_filters"`
}

type countKPI struct {
	Count   Value    `json:"count"`
	Ignored []string `json:"ignored_filters"`
}

type waitKPI struct {
	Median  Value    `json:"median_seconds"`
	P90     Value    `json:"p90_seconds"`
	Ignored []string `json:"ignored_filters"`
}

type pendingKPI struct {
	SnapshotAt                 time.Time `json:"snapshot_at"`
	Total                      int       `json:"total"`
	OpenFinancialIssues        int       `json:"open_financial_issues"`
	OpenFinancialIssuesCents   int64     `json:"open_financial_issues_cents"`
	OrdersAwaitingPayment      int       `json:"orders_awaiting_payment"`
	OrdersAwaitingPaymentCents int64     `json:"orders_awaiting_payment_cents"`
	OrdersAwaitingConfirmation int       `json:"orders_awaiting_confirmation"`
	ChargesUnknown             int       `json:"charges_unknown"`
	RefundsUnderReview         int       `json:"refunds_under_review"`
	Ignored                    []string  `json:"ignored_filters"`
}

type summaryPart struct {
	Cash      cashKPI  `json:"cash"`
	Completed countKPI `json:"completed"`
	Wait      waitKPI  `json:"wait"`
}

type summaryView struct {
	Current summaryPart  `json:"current"`
	Compare *summaryPart `json:"compare"`
	Pending pendingKPI   `json:"pending_now"`
}

func (s *Service) summary(w http.ResponseWriter, r *http.Request) error {
	sc, err := s.authorize(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out summaryView
	var snap time.Time
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		cur, err := s.summaryFor(ctx, tx, sc, sc.q.Period)
		if err != nil {
			return err
		}
		out.Current = cur
		if sc.q.Compare != nil {
			c, err := s.summaryFor(ctx, tx, sc, *sc.q.Compare)
			if err != nil {
				return err
			}
			out.Compare = &c
		}
		out.Pending, err = pendingNow(ctx, tx, sc)
		snap = out.Pending.SnapshotAt
		return err
	})
	if err != nil {
		return err
	}
	m := sc.meta()
	m.SnapshotAt = &snap
	write(w, out, m)
	return nil
}

func (s *Service) summaryFor(ctx context.Context, q database.Querier, sc *scope, p Period) (summaryPart, error) {
	var out summaryPart
	cashIgn := sc.ignored("service_id")
	partIgn := sc.ignored("method")
	out.Cash.Ignored, out.Completed.Ignored, out.Wait.Ignored = cashIgn, partIgn, partIgn
	if sc.beforeCollection(p) {
		nc := status("not_collected")
		out.Cash = cashKPI{Received: nc, Refunded: nc, Net: nc, Receipts: nc, Refunds: nc, FeesEstimated: nc, FeesKnown: nc,
			AfterCosts: nc, FeeCoverage: nc, Ignored: cashIgn}
		out.Completed.Count = nc
		out.Wait.Median, out.Wait.P90 = nc, nc
		return out, nil
	}
	a := sc.args(p, GranDay)
	rec, err := sumByBucket(ctx, q, sqlReceipts, a)
	if err != nil {
		return out, err
	}
	ref, err := sumByBucket(ctx, q, sqlRefunds, a)
	if err != nil {
		return out, err
	}
	rt, ft := sumTotal(rec), sumTotal(ref)
	out.Cash.Received, out.Cash.Refunded = ok(float64(rt.Cents)), ok(float64(ft.Cents))
	out.Cash.Net = ok(float64(rt.Cents - ft.Cents))
	out.Cash.Receipts, out.Cash.Refunds = ok(float64(rt.Count)), ok(float64(ft.Count))
	var est, known, covered, total int64
	if err := q.QueryRow(ctx, sqlFees, a.list()...).Scan(&est, &known, &covered, &total); err != nil {
		return out, err
	}
	out.Cash.FeesEstimated, out.Cash.FeesKnown = ok(float64(est)), ok(float64(known))
	out.Cash.AfterCosts = ok(float64(rt.Cents - ft.Cents - est - known))
	if total > 0 {
		out.Cash.FeeCoverage = okN(float64(covered)/float64(total), total)
	} else {
		out.Cash.FeeCoverage = empty(0)
	}
	if sc.active("service_id") {
		na := status("not_applicable")
		out.Cash = cashKPI{Received: na, Refunded: na, Net: na, Receipts: na, Refunds: na, FeesEstimated: na, FeesKnown: na,
			AfterCosts: na, FeeCoverage: na, Ignored: cashIgn}
	}
	done, err := sumByBucket(ctx, q, sqlCompleted, a)
	if err != nil {
		return out, err
	}
	out.Completed.Count = ok(float64(sumTotal(done).Count))
	_, wt, err := pctByBucket(ctx, q, sqlWait, a, true)
	if err != nil {
		return out, err
	}
	out.Wait.Median, out.Wait.P90 = fromPtr(wt.Median, wt.Sample), fromPtr(wt.P90, wt.Sample)
	if sc.active("method") { // meio de pagamento não se atribui a uma entrada na fila
		na := status("not_applicable")
		out.Completed.Count, out.Wait.Median, out.Wait.P90 = na, na, na
	}
	return out, nil
}

func (sc *scope) ignored(filters ...string) []string {
	out := []string{}
	for _, f := range filters {
		if sc.active(f) {
			out = append(out, f)
		}
	}
	return out
}

// pendingNow é uma fotografia do instante atual (não depende do período).
func pendingNow(ctx context.Context, q database.Querier, sc *scope) (pendingKPI, error) {
	p := pendingKPI{SnapshotAt: sc.now.UTC(), Ignored: sc.ignored("professional_id", "service_id", "method")}
	err := q.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM financial_issue WHERE establishment_id = $1 AND status = 'open'),
		  (SELECT coalesce(sum(amount_cents), 0)::bigint FROM financial_issue WHERE establishment_id = $1 AND status = 'open'),
		  (SELECT count(*) FROM service_order WHERE establishment_id = $1 AND status = 'awaiting_payment'),
		  (SELECT coalesce(sum(total_cents), 0)::bigint FROM service_order WHERE establishment_id = $1 AND status = 'awaiting_payment'),
		  (SELECT count(*) FROM service_order WHERE establishment_id = $1 AND status = 'awaiting_confirmation'),
		  (SELECT count(*) FROM pix_charge WHERE establishment_id = $1 AND status = 'unknown'),
		  (SELECT count(*) FROM refund WHERE establishment_id = $1 AND status = 'under_review')`, sc.est).
		Scan(&p.OpenFinancialIssues, &p.OpenFinancialIssuesCents, &p.OrdersAwaitingPayment, &p.OrdersAwaitingPaymentCents,
			&p.OrdersAwaitingConfirmation, &p.ChargesUnknown, &p.RefundsUnderReview)
	p.Total = p.OpenFinancialIssues + p.OrdersAwaitingPayment + p.OrdersAwaitingConfirmation + p.ChargesUnknown
	return p, err
}

// ---------------------------------------------------------------------------------------------
// GET …/series/{metric}

type bucketView struct {
	Start     time.Time        `json:"start"`
	End       time.Time        `json:"end"`
	Label     string           `json:"label"`
	Collected bool             `json:"collected"`
	Partial   bool             `json:"partial"`
	Values    map[string]Value `json:"values"`
}

type seriesView struct {
	Metric  string           `json:"metric"`
	Series  []seriesDef      `json:"series"`
	Buckets []bucketView     `json:"buckets"`
	Totals  map[string]Value `json:"totals"`
	Compare map[string]Value `json:"compare_totals"`
}

type seriesDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Unit  string `json:"unit"`
}

func (s *Service) series(w http.ResponseWriter, r *http.Request) error {
	metric := r.PathValue("metric")
	def, known := seriesDefs[metric]
	if !known {
		return hs.ErrNotFound()
	}
	sc, err := s.authorize(r, "granularity")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out seriesView
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		vals, totals, err := def.compute(ctx, tx, sc, sc.q.Period, sc.q.Granularity)
		if err != nil {
			return err
		}
		out = seriesView{Metric: metric, Series: def.series, Totals: totals}
		for _, st := range bucketStarts(sc.q.Period, sc.q.Granularity) {
			end := nextBucket(st, sc.q.Granularity)
			b := bucketView{Start: st.UTC(), End: end.UTC(), Label: label(st, sc.q.Granularity), Collected: end.After(sc.created),
				Partial: st.Before(sc.created) && end.After(sc.created), Values: map[string]Value{}}
			if st.After(sc.now) || st.Equal(sc.now) {
				continue // futuro: o bucket não existe ainda
			}
			b.Partial = b.Partial || end.After(sc.now)
			for _, sd := range def.series {
				switch {
				case !b.Collected:
					b.Values[sd.Key] = status("not_collected")
				case def.notApplicable(sc):
					b.Values[sd.Key] = status("not_applicable")
				default:
					v, has := vals[localKey(st)][sd.Key]
					if !has {
						v = def.zero(sd.Key)
					}
					b.Values[sd.Key] = v
				}
			}
			out.Buckets = append(out.Buckets, b)
		}
		if out.Buckets == nil {
			out.Buckets = []bucketView{}
		}
		if sc.q.Compare != nil {
			_, ct, err := def.compute(ctx, tx, sc, *sc.q.Compare, GranDay)
			if err != nil {
				return err
			}
			out.Compare = ct
		}
		return nil
	})
	if err != nil {
		return err
	}
	m := sc.meta(def.ignored...)
	m.Granularity = &sc.q.Granularity
	write(w, out, m)
	return nil
}

func label(t time.Time, gran string) string {
	switch gran {
	case GranHour:
		return t.Format("02/01 15h")
	case GranMonth:
		return t.Format("01/2006")
	case GranWeek:
		return "sem. " + t.Format("02/01")
	default:
		return t.Format("02/01")
	}
}
