package operacao

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHeartbeatSignalsOkFailAndSilence(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	h := &Heartbeat{URL: srv.URL + "/ping/abc", Every: time.Minute}
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()

	if sent, err := h.Report(ctx, nil, false, now); !sent || err != nil {
		t.Fatalf("primeiro sinal: %v %v", sent, err)
	}
	if sent, _ := h.Report(ctx, nil, false, now.Add(10*time.Second)); sent {
		t.Fatal("não deve sinalizar antes do intervalo")
	}
	alerts := []Alert{{Name: "worker_travado", Summary: "contém ana@exemplo.com"}, {Name: "email_atrasado"}}
	if sent, _ := h.Report(ctx, alerts, false, now.Add(time.Minute)); !sent {
		t.Fatal("deveria sinalizar falha")
	}
	_, _ = h.Report(ctx, nil, true, now.Add(2*time.Minute))
	want := []string{"GET /ping/abc ", "POST /ping/abc/fail email_atrasado,worker_travado", "POST /ping/abc/fail indicadores_indisponiveis"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sinais:\n got=%q\nwant=%q", got, want)
	}

	srv.Close() // monitor fora do ar: erro sem a URL
	_, err := h.Report(ctx, nil, false, now.Add(time.Hour))
	if err == nil || strings.Contains(err.Error(), "abc") {
		t.Fatalf("erro esperado sem URL: %v", err)
	}
}
