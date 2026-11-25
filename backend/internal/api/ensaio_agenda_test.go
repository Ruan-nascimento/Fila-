package api_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/api"
)

// C-AG-18 — Ensaio de carga da agenda (Fila 2.0, parte 3). NÃO roda na suíte normal (leva alguns minutos):
//
//	FILA_PERF=1 go -C backend test -run TestEnsaioCargaAgenda -v -timeout 30m ./internal/api/
//
// Volume: 20 lojas × 5 profissionais, jornada 09–18 todos os dias, 30 dias de histórico concluído
// e 30 dias à frente com metade dos horários reservados (≈ 70 mil reservas e ocupações). Mede, com
// a API inteira em processo (HTTP + autorização + banco real): leitura do dia e do mês (1, 10 e 100
// leitores simultâneos), criação gratuita sem disputa, 30 tentativas simultâneas pelo último
// horário, consultas SQL por requisição (e se continuam constantes ao dobrar os horários), rodada do
// worker em repouso e conexões retidas depois da carga. Resultados em docs/operacao/agenda-2.0.md.

// contador conta as consultas SQL que passam pelo pool da API.
type contador struct{ n atomic.Int64 }

func (c *contador) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}
func (c *contador) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type amostra []time.Duration

func (a amostra) pct(p float64) time.Duration {
	if len(a) == 0 {
		return 0
	}
	s := append(amostra{}, a...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[min(len(s)-1, int(float64(len(s))*p))]
}

func (a amostra) String() string {
	return fmt.Sprintf("n=%d p50=%s p95=%s máx=%s", len(a), a.pct(0.5).Round(time.Millisecond/10), a.pct(0.95).Round(time.Millisecond/10),
		a.pct(1).Round(time.Millisecond/10))
}

func TestEnsaioCargaAgenda(t *testing.T) {
	if os.Getenv("FILA_PERF") != "1" {
		t.Skip("ensaio de carga: defina FILA_PERF=1")
	}
	var apiPool *pgxpool.Pool
	cont := &contador{}
	e := newEnvDeps(t, func(d *api.Deps) {
		cfg := d.Pool.Config().Copy()
		cfg.ConnConfig.Tracer = cont
		p, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		d.Pool, apiPool = p, p
	})
	ctx := context.Background()
	var out strings.Builder
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Log(line)
		out.WriteString(line + "\n")
	}

	// --- Semeadura -------------------------------------------------------------------------------
	t0 := time.Now()
	type loja struct {
		agendaShop
		profs []uuid.UUID
	}
	var lojas []loja
	for i := range 20 {
		b := e.shop(fmt.Sprintf("ensaio-%02d", i), fmt.Sprintf("dona%02d@ensaio.test", i))
		l := loja{agendaShop: agendaShop{barbearia: barbearia{shop: b}}}
		for k := range 5 {
			l.profs = append(l.profs, e.professional(b.id, fmt.Sprintf("P%d", k)))
		}
		l.beto = l.profs[0]
		ids := []string{}
		for _, p := range l.profs {
			ids = append(ids, p.String())
		}
		l.corte25 = b.owner.createService(b.id, map[string]any{"name": "Corte 25", "price_cents": 4000, "duration_minutes": 25,
			"points": 0, "professional_ids": ids})["id"].(string)
		shifts := []map[string]any{}
		for d := range 7 {
			shifts = append(shifts, map[string]any{"weekday": d, "start_minute": 9 * 60, "end_minute": 18 * 60, "modality": "both"})
		}
		start := e.clock.Now().In(spLoc).AddDate(0, 0, -31).Format("2006-01-02")
		if _, err := e.pool.Exec(ctx, `INSERT INTO agenda_schedule (establishment_id, effective_from, inherits_store, created_by, created_at)
			VALUES ($1, $2::date, false, (SELECT account_id FROM staff_membership WHERE establishment_id = $1 AND role = 'owner' LIMIT 1), $3)`,
			b.id, start, e.clock.Now()); err != nil {
			t.Fatal(err)
		}
		b.owner.mustStatus(b.owner.cmd("PUT", estPath(b.id, "/agenda/hours"), map[string]any{
			"effective_from": e.clock.Now().In(spLoc).Format("2006-01-02"), "shifts": shifts}), 200)
		if _, err := e.pool.Exec(ctx, `INSERT INTO agenda_shift (schedule_id, weekday, start_minute, end_minute, modality)
			SELECT s.id, d, 540, 1080, 'both' FROM agenda_schedule s, generate_series(0, 6) d
			WHERE s.establishment_id = $1 AND s.effective_from = $2::date`, b.id, start); err != nil {
			t.Fatal(err)
		}
		st := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/settings"), nil), 200).data()["agenda"].(map[string]any)
		b.owner.mustStatus(b.owner.cmd("PATCH", estPath(b.id, "/settings/agenda"), map[string]any{"expected_version": num(st["version"]),
			"service_mode": "schedule", "slot_step_minutes": 30, "horizon_days": 60, "max_future_appointments": 20}), 200)
		for k, p := range l.profs {
			var vis uuid.UUID
			if err := e.pool.QueryRow(ctx, `INSERT INTO visitor (establishment_id, nick, created_at, absolute_expires_at, recovery_code_hmac,
				recovery_code_issued_at) VALUES ($1, 'h', $2::timestamptz, $2::timestamptz + interval '400 days', sha256(convert_to($3::text, 'UTF8')), $2::timestamptz) RETURNING id`,
				b.id, e.clock.Now().Add(-40*24*time.Hour), fmt.Sprintf("%s-%d", b.id, k)).Scan(&vis); err != nil {
				t.Fatal(err)
			}
			// Histórico (30 dias, todo horário cheio; encerrado, ocupação liberada) e futuro (30 dias) com
			// metade dos horários reservada. O histórico entra como encerrado SEM visita (a visita real exige
			// chamada e comanda): o que pesa nas consultas é o volume de linhas, que é o mesmo.
			if _, err := e.pool.Exec(ctx, `
				WITH slots AS (
				  SELECT d, h, ((($3::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date + d) + make_interval(hours => h))
				         AT TIME ZONE 'America/Sao_Paulo' AS s
				  FROM generate_series(-30, 29) d, generate_series(9, 17) h
				  WHERE d < 0 OR (d > 0 AND h % 2 = 0))
				INSERT INTO appointment (establishment_id, professional_id, visitor_id, nick, origin, local_date, timezone, starts_at, ends_at,
				  service_minutes, buffer_minutes, tolerance_minutes, checkin_window_minutes, policy_version, price_cents, created_at, updated_at,
				  status, finished_at, cancelled_by)
				SELECT $1, $2, $4, 'h', 'online', (s AT TIME ZONE 'America/Sao_Paulo')::date, 'America/Sao_Paulo', s, s + interval '30 minutes',
				  25, 5, 10, 15, 1, 4000, s - interval '2 days', s,
				  CASE WHEN d < 0 THEN 'cancelled' ELSE 'confirmed' END, CASE WHEN d < 0 THEN s + interval '30 minutes' END,
				  CASE WHEN d < 0 THEN 'customer' END
				FROM slots`, b.id, p, e.clock.Now(), vis); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.Exec(ctx, `INSERT INTO agenda_occupancy (establishment_id, professional_id, kind, appointment_id, period, released_at)
				SELECT establishment_id, professional_id, 'appointment', id, tstzrange(starts_at, ends_at, '[)'),
				       CASE WHEN status = 'cancelled' THEN finished_at END
				FROM appointment WHERE visitor_id = $1`, vis); err != nil {
				t.Fatal(err)
			}
		}
		lojas = append(lojas, l)
	}
	if _, err := e.pool.Exec(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}
	var nAppt, nOcc int
	_ = e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM appointment), (SELECT count(*) FROM agenda_occupancy)`).Scan(&nAppt, &nOcc)
	logf("semeadura: %d lojas × 5 profissionais, %d reservas, %d ocupações (%s)", len(lojas), nAppt, nOcc, time.Since(t0).Round(time.Second))

	l := lojas[7]
	amanha := e.clock.Now().In(spLoc).AddDate(0, 0, 1).Format("2006-01-02")
	dia := "/v1/public/establishments/" + l.id.String() + "/availability?date=" + amanha + "&items=" + l.corte25
	mes := "/v1/public/establishments/" + l.id.String() + "/availability/days?from=" + e.today() + "&to=" +
		e.clock.Now().In(spLoc).AddDate(0, 0, 29).Format("2006-01-02") + "&professional_id=" + l.beto.String() + "&items=" + l.corte25

	// --- Consultas SQL por requisição (e ao dobrar os horários) -----------------------------------
	consultas := func(path string) int64 {
		b := e.browser()
		b.mustStatus(b.do("GET", path, nil), 200) // aquece
		antes := cont.n.Load()
		b.mustStatus(b.do("GET", path, nil), 200)
		return cont.n.Load() - antes
	}
	q30, qm30 := consultas(dia), consultas(mes)
	st := l.owner.mustStatus(l.owner.do("GET", estPath(l.id, "/settings"), nil), 200).data()["agenda"].(map[string]any)
	l.owner.mustStatus(l.owner.cmd("PATCH", estPath(l.id, "/settings/agenda"), map[string]any{"expected_version": num(st["version"]),
		"slot_step_minutes": 15}), 200)
	q15, qm15 := consultas(dia), consultas(mes)
	logf("consultas SQL por leitura do dia: %d com grade de 30 min, %d com grade de 15 min (o dobro de horários)", q30, q15)
	logf("consultas SQL por leitura do mês (30 dias, 1 profissional): %d com grade de 30 min, %d com grade de 15 min", qm30, qm15)
	if q15 != q30 || qm15 != qm30 {
		t.Errorf("consultas deveriam ser constantes ao dobrar os horários: dia %d→%d, mês %d→%d", q30, q15, qm30, qm15)
	}

	// --- Leitura do mesmo dia: 1, 10 e 100 leitores ----------------------------------------------
	ler := func(path string, leitores, porLeitor int) amostra {
		var mu sync.Mutex
		var a amostra
		var wg sync.WaitGroup
		for range leitores {
			wg.Go(func() {
				b := e.browser()
				for range porLeitor {
					t1 := time.Now()
					r := b.do("GET", path, nil)
					d := time.Since(t1)
					if r.status != 200 {
						t.Errorf("leitura %d: %s", r.status, r.raw)
						return
					}
					mu.Lock()
					a = append(a, d)
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		return a
	}
	for _, n := range []int{1, 10, 100} {
		logf("leitura do dia, %3d leitor(es): %s", n, ler(dia, n, max(5, 200/n)))
	}
	for _, n := range []int{1, 10} {
		logf("leitura do mês, %3d leitor(es): %s", n, ler(mes, n, max(5, 50/n)))
	}

	// --- Criação gratuita sem disputa e 30 tentativas pelo último horário --------------------------
	var criar amostra
	for i := range 20 {
		c := e.verifiedCustomer(fmt.Sprintf("cli%02d@ensaio.test", i))
		prof := l.profs[i%5]
		h := fmt.Sprintf("%02d:00", 9+2*(i/5)) // horas ímpares estão livres no futuro
		t1 := time.Now()
		c.mustStatus(c.book(l.id, prof, e.at(h).AddDate(0, 0, 3), l.corte25), 201)
		criar = append(criar, time.Since(t1))
	}
	logf("criação de reserva sem disputa: %s", criar)
	disputa := e.at("17:00").AddDate(0, 0, 5)
	pessoas := make([]*browser, 30)
	for i := range pessoas {
		pessoas[i] = e.verifiedCustomer(fmt.Sprintf("disp%02d@ensaio.test", i))
	}
	var venc, perd atomic.Int64
	var mu sync.Mutex
	var lat amostra
	var wg sync.WaitGroup
	for _, p := range pessoas {
		wg.Go(func() {
			t1 := time.Now()
			r := p.book(l.id, l.profs[4], disputa, l.corte25)
			d := time.Since(t1)
			mu.Lock()
			lat = append(lat, d)
			mu.Unlock()
			switch {
			case r.status == 201:
				venc.Add(1)
			case r.status == 409:
				perd.Add(1)
			default:
				t.Errorf("disputa: %d %s", r.status, r.raw)
			}
		})
	}
	wg.Wait()
	logf("30 tentativas simultâneas pelo último horário: %d venceu, %d receberam conflito; %s", venc.Load(), perd.Load(), lat)
	if venc.Load() != 1 {
		t.Errorf("overbooking: %d vencedores", venc.Load())
	}
	var sobrepostas int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM agenda_occupancy a JOIN agenda_occupancy b ON a.professional_id = b.professional_id
		AND a.id < b.id AND a.released_at IS NULL AND b.released_at IS NULL AND a.period && b.period`).Scan(&sobrepostas)
	logf("ocupações sobrepostas no banco depois da carga: %d", sobrepostas)
	if sobrepostas != 0 {
		t.Errorf("sobreposição: %d", sobrepostas)
	}

	// --- Worker em repouso e conexões --------------------------------------------------------------
	antes := cont.n.Load()
	t1 := time.Now()
	n, err := e.api.Agenda.Tick(ctx) // envia os avisos das reservas criadas no ensaio
	if err != nil {
		t.Fatal(err)
	}
	logf("rodada do worker com trabalho (%d aviso(s)/mudança(s)): %d consultas, %s", n, cont.n.Load()-antes, time.Since(t1).Round(time.Millisecond/10))
	antes, t1 = cont.n.Load(), time.Now()
	if _, err := e.api.Agenda.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	logf("rodada do worker da agenda em repouso (nada a fazer, %d reservas no banco): %d consultas, %s", nAppt, cont.n.Load()-antes,
		time.Since(t1).Round(time.Millisecond/10))
	time.Sleep(200 * time.Millisecond)
	s := apiPool.Stat()
	logf("pool da API depois da carga: %d conexões em uso, %d ociosas, %d no total (máx. %d)", s.AcquiredConns(), s.IdleConns(),
		s.TotalConns(), s.MaxConns())
	if s.AcquiredConns() != 0 {
		t.Errorf("conexões retidas: %d", s.AcquiredConns())
	}

	// --- Planos de execução (EXPLAIN ANALYZE, BUFFERS) das leituras quentes ----------------------
	explain := func(nome, sql string, args ...any) {
		rows, err := e.pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) "+sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plano []string
		for rows.Next() {
			var linha string
			_ = rows.Scan(&linha)
			plano = append(plano, linha)
		}
		rows.Close()
		logf("plano — %s:\n    %s", nome, strings.Join(plano, "\n    "))
	}
	ini := e.at("00:00").AddDate(0, 0, 1)
	explain("ocupações do dia (disponibilidade)", `SELECT professional_id, appointment_id, lower(period), upper(period) FROM agenda_occupancy
		WHERE professional_id = ANY($1) AND released_at IS NULL AND period && tstzrange($2, $3, '[)') AND (hold_until IS NULL OR hold_until > $4)`,
		l.profs, ini, ini.Add(24*time.Hour), e.clock.Now())
	explain("faltas a processar (worker)", `SELECT establishment_id, participation_id FROM appointment
		WHERE status = 'called' AND deadline_at <= $1 AND deadline_at > $2 AND arrived_at IS NULL ORDER BY deadline_at LIMIT 50`,
		e.clock.Now(), e.clock.Now().Add(-30*time.Minute))
	explain("atraso da loja (worker)", `SELECT id FROM appointment WHERE status = 'confirmed' AND store_late_at IS NULL AND starts_at <= $1
		AND starts_at + make_interval(mins => tolerance_minutes) <= $1 ORDER BY starts_at LIMIT 50`, e.clock.Now())

	if p := os.Getenv("FILA_PERF_OUT"); p != "" {
		_ = os.WriteFile(p, []byte(out.String()), 0o644)
	}
}
