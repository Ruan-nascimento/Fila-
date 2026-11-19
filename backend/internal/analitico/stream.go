package analitico

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// GET …/stream: invalidação do Dashboard (SSE). Não leva dado nenhum: só quais áreas mudaram,
// agrupadas por alguns segundos, para o programa recarregar SOMENTE as consultas afetadas.
// Mudança de posição na fila (queue.*) não invalida nada: nenhum gráfico depende dela.
//
// Mensagens:
//   - event: ready        data: {"areas": [...todas]}
//   - event: invalidate   data: {"areas": [...]}
//   - event: session_ended data: {"reason": "owner_session_replaced" | "session_revoked"}
//
// A conexão é reconferida a cada Revalidate e a cada aviso de revogação do banco (logout, troca de
// aparelho, instalação revogada, vínculo removido).

// StreamDeps configura o fluxo.
type StreamDeps struct {
	Hub         *outbox.Hub
	Heartbeat   time.Duration
	Revalidate  time.Duration
	Coalesce    time.Duration
	MaxLifetime time.Duration
}

// Áreas do Dashboard que um evento pode invalidar.
var allAreas = []string{"summary", "cash", "ticket", "movement", "wait", "duration", "outcomes", "peak_hours",
	"professionals", "services", "pending", "loyalty", "rewards", "payment_methods", "wait_bands", "recurrence"}

func areasFor(eventType string) []string {
	switch eventType {
	case "payment.updated", "financial_issue.updated":
		return []string{"summary", "cash", "ticket", "payment_methods", "pending"}
	case "order.updated", "change_request.updated":
		return []string{"summary", "ticket", "services", "pending", "professionals"}
	case "participation.updated":
		return []string{"summary", "movement", "wait", "duration", "outcomes", "peak_hours", "professionals",
			"services", "wait_bands", "recurrence"}
	case "pendencies.updated":
		return []string{"summary", "pending"}
	case "loyalty.updated", "reward_reservation.updated":
		return []string{"loyalty", "rewards"}
	}
	return nil
}

func (s *Service) stream(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return hs.ErrNotFound()
	}
	if _, err := s.Auth.DesktopOwner(r, estID); err != nil {
		return err
	}
	if s.Stream.Hub == nil {
		return &hs.Error{Status: 503, Code: "service_unavailable", Title: "Atualização em tempo real indisponível"}
	}
	d := s.Stream
	if d.Heartbeat == 0 {
		d.Heartbeat = 15 * time.Second
	}
	if d.Revalidate == 0 {
		d.Revalidate = 30 * time.Second
	}
	if d.Coalesce == 0 {
		d.Coalesce = 3 * time.Second
	}
	if d.MaxLifetime == 0 {
		d.MaxLifetime = 30 * time.Minute
	}
	ctx := r.Context()
	sub, err := d.Hub.Subscribe(ctx, estID, 256)
	if err != nil {
		return err
	}
	defer d.Hub.Unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store, no-transform")
	h.Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	send := func(event string, data any) error {
		raw, _ := json.Marshal(data)
		if err := rc.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
			return err
		}
		return rc.Flush()
	}
	if send("ready", map[string]any{"areas": allAreas}) != nil {
		return nil
	}
	heartbeat := time.NewTicker(d.Heartbeat)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(d.Revalidate)
	defer revalidate.Stop()
	lifetime := time.NewTimer(d.MaxLifetime)
	defer lifetime.Stop()
	var pending []string
	var flush <-chan time.Time
	recheck := func() bool {
		if err := s.Auth.DesktopRecheck(r, estID); err != nil {
			reason := "session_revoked"
			var he *hs.Error
			if errors.As(err, &he) && he.Code == "owner_session_replaced" {
				reason = he.Code
			}
			_ = send("session_ended", map[string]string{"reason": reason})
			return false
		}
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-lifetime.C:
			return nil // o programa reconecta
		case <-sub.Overflow:
			return nil
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return nil
			}
		case <-revalidate.C:
			if !recheck() {
				return nil
			}
		case <-sub.Revalidate:
			if !recheck() {
				return nil
			}
		case e := <-sub.C:
			for _, a := range areasFor(e.Type) {
				if !slices.Contains(pending, a) {
					pending = append(pending, a)
				}
			}
			if len(pending) > 0 && flush == nil {
				flush = time.After(d.Coalesce) // agrupa mudanças próximas num aviso só
			}
		case <-flush:
			flush = nil
			if !recheck() { // nunca avisa uma sessão que já perdeu o acesso
				return nil
			}
			if send("invalidate", map[string]any{"areas": pending}) != nil {
				return nil
			}
			pending = nil
		}
	}
}
