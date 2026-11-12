package idempotency_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"fila/internal/platform/clock"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/testdb"
)

type fixture struct {
	srv     *httptest.Server
	effects *atomic.Int64
	clock   *clock.Fake
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := testdb.Migrated(t)
	c := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rn := idempotency.New(pool, c, log)
	var effects atomic.Int64
	h := httpserver.Handle(log, func(w http.ResponseWriter, r *http.Request) error {
		body, err := httpserver.DecodeJSON(r, &struct {
			Nome string `json:"nome"`
		}{})
		if err != nil {
			return err
		}
		return rn.Run(w, r, idempotency.Command{ActorKey: r.Header.Get("X-Ator"), Operation: "teste", Body: body},
			func(tx pgx.Tx) (idempotency.Result, error) {
				time.Sleep(50 * time.Millisecond) // alarga a janela de corrida
				n := effects.Add(1)
				return idempotency.Result{Status: 201, Body: map[string]int64{"efeito": n}}, nil
			})
	})
	mux := http.NewServeMux()
	mux.Handle("POST /cmd/{id}", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fixture{srv: srv, effects: &effects, clock: c}
}

func (f fixture) post(t *testing.T, path, actor, key, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ator", actor)
	if key != "" {
		req.Header.Set(idempotency.Header, key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

const key = "chave-de-teste-0001"

func TestReplaySameRequestReturnsSameResponse(t *testing.T) {
	f := setup(t)
	r1, b1 := f.post(t, "/cmd/1", "ana", key, `{"nome":"x"}`)
	r2, b2 := f.post(t, "/cmd/1", "ana", key, `{"nome":"x"}`)
	if r1.StatusCode != 201 || r2.StatusCode != 201 {
		t.Fatalf("status %d/%d", r1.StatusCode, r2.StatusCode)
	}
	if r2.Header.Get("Idempotent-Replayed") != "true" || r1.Header.Get("Idempotent-Replayed") != "" {
		t.Fatal("cabeçalho Idempotent-Replayed incorreto")
	}
	if b1["efeito"] != b2["efeito"] || f.effects.Load() != 1 {
		t.Fatalf("repetição aplicou de novo: %v %v (efeitos=%d)", b1, b2, f.effects.Load())
	}
}

func TestDifferentBodyOrPathIsRejected(t *testing.T) {
	f := setup(t)
	f.post(t, "/cmd/1", "ana", key, `{"nome":"x"}`)
	r, b := f.post(t, "/cmd/1", "ana", key, `{"nome":"y"}`)
	if r.StatusCode != 422 || b["code"] != "idempotency_key_reused" {
		t.Fatalf("corpo diferente: %d %v", r.StatusCode, b)
	}
	r, _ = f.post(t, "/cmd/2", "ana", key, `{"nome":"x"}`)
	if r.StatusCode != 422 {
		t.Fatalf("rota com outro recurso deveria ser 422, veio %d", r.StatusCode)
	}
	if f.effects.Load() != 1 {
		t.Fatalf("efeitos = %d", f.effects.Load())
	}
}

func TestScopeIsPerActor(t *testing.T) {
	f := setup(t)
	f.post(t, "/cmd/1", "ana", key, `{"nome":"x"}`)
	r, _ := f.post(t, "/cmd/1", "bia", key, `{"nome":"x"}`)
	if r.StatusCode != 201 || r.Header.Get("Idempotent-Replayed") != "" || f.effects.Load() != 2 {
		t.Fatalf("mesma chave de outro ator não pode colidir: %d efeitos=%d", r.StatusCode, f.effects.Load())
	}
}

func TestMissingOrInvalidKey(t *testing.T) {
	f := setup(t)
	r, b := f.post(t, "/cmd/1", "ana", "", `{}`)
	if r.StatusCode != 400 || b["code"] != "idempotency_key_missing" {
		t.Fatalf("sem chave: %d %v", r.StatusCode, b)
	}
	r, b = f.post(t, "/cmd/1", "ana", "curta", `{}`)
	if r.StatusCode != 400 || b["code"] != "validation_failed" {
		t.Fatalf("chave inválida: %d %v", r.StatusCode, b)
	}
}

func TestConcurrentDuplicatesApplyOnce(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	results := make([]float64, 8)
	for i := range results {
		wg.Go(func() {
			r, b := f.post(t, "/cmd/1", "ana", key, `{"nome":"x"}`)
			if r.StatusCode != 201 {
				t.Errorf("status %d", r.StatusCode)
			}
			results[i], _ = b["efeito"].(float64)
		})
	}
	wg.Wait()
	if f.effects.Load() != 1 {
		t.Fatalf("8 cópias simultâneas aplicaram %d efeitos", f.effects.Load())
	}
	for _, v := range results {
		if v != results[0] {
			t.Fatalf("respostas diferentes: %v", results)
		}
	}
}

func TestExpiredKeyCanBeReused(t *testing.T) {
	f := setup(t)
	f.post(t, "/cmd/1", "ana", key, `{"nome":"x"}`)
	f.clock.Advance(idempotency.TTL + time.Second)
	r, _ := f.post(t, "/cmd/1", "ana", key, `{"nome":"y"}`)
	if r.StatusCode != 201 || f.effects.Load() != 2 {
		t.Fatalf("chave vencida deveria valer como nova: %d efeitos=%d", r.StatusCode, f.effects.Load())
	}
}
