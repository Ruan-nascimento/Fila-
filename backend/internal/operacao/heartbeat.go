package operacao

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Heartbeat avisa um monitor EXTERNO (Healthchecks.io, Better Stack, etc.) de que o worker está
// vivo e sem alerta. É um "dead man's switch": se o worker morrer, travar ou perder a rede, os
// avisos param e o monitor externo dispara a notificação — o canal de alerta não depende do
// próprio sistema estar funcionando.
//
//   - Sem alerta: GET <URL>.
//   - Com alerta (ou sem conseguir ler os indicadores): POST <URL>/fail com os NOMES dos alertas
//     (ex.: "worker_travado,email_atrasado"). Nunca dados pessoais, valores ou mensagens.
//
// A URL contém um identificador secreto do monitor: trate como segredo (FILA_HEARTBEAT_URL).
type Heartbeat struct {
	URL    string
	Client *http.Client
	Every  time.Duration
	last   time.Time
}

// Report envia o sinal se já passou o intervalo. Devolve erro só para log (sem a URL).
func (h *Heartbeat) Report(ctx context.Context, alerts []Alert, readFailed bool, now time.Time) (sent bool, err error) {
	if h == nil || h.URL == "" {
		return false, nil
	}
	every := h.Every
	if every <= 0 {
		every = time.Minute
	}
	if !h.last.IsZero() && now.Sub(h.last) < every {
		return false, nil
	}
	method, url, body := http.MethodGet, h.URL, ""
	if readFailed || len(alerts) > 0 {
		names := make([]string, 0, len(alerts))
		for _, a := range alerts {
			names = append(names, a.Name)
		}
		sort.Strings(names)
		if readFailed {
			names = append(names, "indicadores_indisponiveis")
		}
		method, url, body = http.MethodPost, strings.TrimRight(h.URL, "/")+"/fail", strings.Join(names, ",")
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, method, url, strings.NewReader(body))
	if err != nil {
		return false, errHeartbeat
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "fila-worker/1")
	c := h.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, errHeartbeat
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, errHeartbeat
	}
	h.last = now
	return true, nil
}

type heartbeatError struct{}

func (heartbeatError) Error() string { return "monitor externo não recebeu o sinal (rede ou URL)" }

var errHeartbeat error = heartbeatError{}
