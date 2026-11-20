package analitico

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/testdb"
)

// Medição com volume representativo (docs/desktop/desempenho.md). Só roda com FILA_PERF=1
// (leva ~1 min para semear): FILA_PERF=1 go -C backend test -run TestDesempenho -v ./internal/analitico/
//
// Volume: 1 loja, 4 profissionais, 3 serviços, 1 ano de movimento: ~60 mil participações
// concluídas (comanda liquidada, 1 ou 2 itens, recebimento conferido) e ~6 mil estornos.
const seed = `
DO $$
DECLARE est uuid; s1 uuid; s2 uuid; s3 uuid; q uuid[]; p uuid[]; i int; t timestamptz; o uuid; part uuid; v uuid; r uuid; k int; total bigint;
BEGIN
  INSERT INTO establishment (name, slug, timezone, created_at, updated_at) VALUES ('Perf', 'perf', 'America/Sao_Paulo', now() - interval '400 days', now()) RETURNING id INTO est;
  INSERT INTO account (name, email, email_normalized, password_hash, status, email_verified_at, password_changed_at, created_at, updated_at)
    VALUES ('Dono', 'dono@perf', 'dono@perf', '$argon2id$x', 'active', now(), now(), now(), now());
  FOR k IN 1..4 LOOP
    INSERT INTO professional (establishment_id, display_name, queue_letter, created_at, updated_at) VALUES (est, 'P' || k, chr(64 + k), now(), now()) RETURNING id INTO v;
    p := p || v;
    INSERT INTO queue (establishment_id, professional_id, created_at, updated_at) VALUES (est, v, now(), now()) RETURNING id INTO r;
    q := q || r;
  END LOOP;
  INSERT INTO service (establishment_id, name, price_cents, duration_minutes, points, created_at, updated_at) VALUES (est, 'Corte', 4500, 30, 0, now(), now()) RETURNING id INTO s1;
  INSERT INTO service (establishment_id, name, price_cents, duration_minutes, points, created_at, updated_at) VALUES (est, 'Barba', 3000, 20, 0, now(), now()) RETURNING id INTO s2;
  INSERT INTO service (establishment_id, name, price_cents, duration_minutes, points, created_at, updated_at) VALUES (est, 'Pezinho', 1500, 10, 0, now(), now()) RETURNING id INTO s3;
  SET CONSTRAINTS ALL DEFERRED;
  FOR i IN 1..60000 LOOP
    t := now() - interval '365 days' + (i * interval '525 seconds');
    k := 1 + (i % 4);
    INSERT INTO visitor (establishment_id, nick, created_at, absolute_expires_at, recovery_code_hmac, recovery_code_issued_at)
      VALUES (est, 'v', t, t + interval '30 days', sha256(convert_to(i::text, 'UTF8')), t) RETURNING id INTO v;
    o := uuidv7(); part := uuidv7();
    total := CASE WHEN i % 3 = 0 THEN 7500 ELSE 4500 END;
    INSERT INTO service_order (id, establishment_id, participation_id, visitor_id, status, total_cents, total_frozen_at, created_at, updated_at, closed_at, professional_id, payment_method)
      VALUES (o, est, part, v, 'settled', total, t + interval '40 minutes', t, t, t + interval '41 minutes', p[k], 'cash');
    INSERT INTO participation (id, establishment_id, queue_id, order_id, visitor_id, nick, loyalty_eligible, status, position, ticket_day, ticket_number, ticket_code,
      called_at, tolerance_ends_at, started_at, finished_at, created_at, updated_at)
      VALUES (part, est, q[k], o, v, 'v', false, 'completed', i, t::date, i, chr(64 + k) || lpad(i::text, 5, '0'),
      t + (i % 30) * interval '1 minute', t + (i % 30) * interval '1 minute' + interval '10 minutes', t + interval '12 minutes' + (i % 30) * interval '1 minute', t + interval '40 minutes', t, t);
    INSERT INTO order_item (establishment_id, order_id, service_id, name, unit_price_cents, duration_minutes, points, status, charged, added_at, performed_at)
      VALUES (est, o, s1, 'Corte', 4500, 30, 0, 'performed', true, t, t + interval '40 minutes');
    IF i % 3 = 0 THEN
      INSERT INTO order_item (establishment_id, order_id, service_id, name, unit_price_cents, duration_minutes, points, status, charged, added_at, performed_at)
        VALUES (est, o, s2, 'Barba', 3000, 20, 0, 'performed', true, t, t + interval '40 minutes');
    END IF;
    INSERT INTO receipt (establishment_id, order_id, method, amount_cents, classification, evidence, registered_by, registered_by_account_id, registered_at, cash_tendered_cents, change_cents)
      VALUES (est, o, 'cash', total, 'principal', 'in_person', 'owner', (SELECT id FROM account LIMIT 1), t + interval '41 minutes', total, 0) RETURNING id INTO r;
    INSERT INTO receipt_conferral (receipt_id, establishment_id, conferred_at, conferred_by_role, conferred_by_account_id)
      VALUES (r, est, t + interval '41 minutes', 'owner', (SELECT id FROM account LIMIT 1));
    IF i % 10 = 0 THEN
      INSERT INTO refund (establishment_id, order_id, receipt_id, method, amount_cents, status, reason, requested_at, requested_by_account_id, evidence, evidence_note,
        resolved_at, resolved_by_account_id, updated_at)
        VALUES (est, o, r, 'cash', total, 'confirmed', 'teste', t + interval '1 day', (SELECT id FROM account LIMIT 1), 'owner_documented', 'recibo', t + interval '1 day', (SELECT id FROM account LIMIT 1), t + interval '1 day');
    END IF;
  END LOOP;
END $$;
ANALYZE;
`

func TestDesempenho(t *testing.T) {
	if os.Getenv("FILA_PERF") != "1" {
		t.Skip("PULADO: defina FILA_PERF=1 para medir com volume representativo")
	}
	pool := testdb.Migrated(t)
	ctx := context.Background()
	t0 := time.Now()
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatal(err)
	}
	t.Logf("semeadura: %v", time.Since(t0).Round(time.Millisecond))
	var est uuid.UUID
	var created time.Time
	_ = pool.QueryRow(ctx, `SELECT id, created_at FROM establishment WHERE slug = 'perf'`).Scan(&est, &created)
	var n struct{ part, rec, ref, items int }
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM participation), (SELECT count(*) FROM receipt), (SELECT count(*) FROM refund), (SELECT count(*) FROM order_item)`).
		Scan(&n.part, &n.rec, &n.ref, &n.items)
	t.Logf("volume: %d participações, %d recebimentos, %d estornos, %d itens", n.part, n.rec, n.ref, n.items)

	loc, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Now()
	for _, per := range []struct{ nome, from, to string }{
		{"30 dias", now.AddDate(0, 0, -29).In(loc).Format(dateFmt), now.In(loc).Format(dateFmt)},
		{"366 dias", now.AddDate(0, 0, -365).In(loc).Format(dateFmt), now.In(loc).Format(dateFmt)},
	} {
		q, err := parseQuery(url.Values{"from": {per.from}, "to": {per.to}, "compare": {"previous"}}, loc, now, allowedParams("granularity"))
		if err != nil {
			t.Fatal(err)
		}
		sc := &scope{est: est, name: "Perf", loc: loc, tz: "America/Sao_Paulo", created: created, now: now, q: q}
		s := &Service{Deps: Deps{Pool: pool}}
		medir := func(nome string, f func() error) {
			best := time.Duration(1 << 62)
			for range 3 {
				t1 := time.Now()
				if err := f(); err != nil {
					t.Fatalf("%s: %v", nome, err)
				}
				best = min(best, time.Since(t1))
			}
			t.Logf("%-9s %-26s %8v", per.nome, nome, best.Round(100*time.Microsecond))
		}
		medir("resumo (com comparação)", func() error {
			_, err := s.summaryFor(ctx, pool, sc, sc.q.Period)
			if err == nil {
				_, err = s.summaryFor(ctx, pool, sc, *sc.q.Compare)
			}
			return err
		})
		for _, m := range []string{"cash", "ticket", "movement", "wait", "duration", "loyalty"} {
			medir("série "+m, func() error { _, _, err := seriesDefs[m].compute(ctx, pool, sc, sc.q.Period, GranDay); return err })
		}
		for _, d := range []string{"payment_methods", "outcomes", "professionals", "services", "peak_hours", "wait_bands", "pending", "recurrence"} {
			medir("detalhamento "+d, func() error { _, err := breakdowns[d].run(ctx, pool, sc); return err })
		}
		if per.nome == "30 dias" {
			rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+sqlReceipts, sc.args(sc.q.Period, GranDay).list()...)
			if err != nil {
				t.Fatal(err)
			}
			var plano []string
			for rows.Next() {
				var l string
				_ = rows.Scan(&l)
				plano = append(plano, l)
			}
			rows.Close()
			t.Logf("plano (caixa, 30 dias):\n%s", strings.Join(plano, "\n"))
		}
	}
	// Mesmas consultas sem os índices da migração 00018 (justificativa de cada índice).
	for _, ix := range []string{"receipt_est_registered_idx", "refund_est_resolved_idx", "participation_est_created_idx",
		"participation_est_called_idx", "participation_est_finished_idx", "service_order_est_closed_idx",
		"order_item_est_performed_idx", "loyalty_ledger_est_occurred_idx"} {
		if _, err := pool.Exec(ctx, "DROP INDEX "+ix); err != nil {
			t.Fatal(err)
		}
	}
	q, _ := parseQuery(url.Values{"from": {now.AddDate(0, 0, -29).In(loc).Format(dateFmt)}, "to": {now.In(loc).Format(dateFmt)}, "compare": {"previous"}}, loc, now, allowedParams())
	sc := &scope{est: est, loc: loc, tz: "America/Sao_Paulo", created: created, now: now, q: q}
	s := &Service{Deps: Deps{Pool: pool}}
	for _, m := range []string{"cash", "ticket", "movement", "wait", "duration"} {
		t1 := time.Now()
		if _, _, err := seriesDefs[m].compute(ctx, pool, sc, sc.q.Period, GranDay); err != nil {
			t.Fatal(err)
		}
		t.Logf("SEM ÍNDICES 30 dias série %-10s %8v", m, time.Since(t1).Round(100*time.Microsecond))
	}
	t1 := time.Now()
	_, _ = s.summaryFor(ctx, pool, sc, sc.q.Period)
	t.Logf("SEM ÍNDICES 30 dias resumo          %8v", time.Since(t1).Round(100*time.Microsecond))
	fmt.Println()
}
