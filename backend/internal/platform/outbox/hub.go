package outbox

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Hub é o distribuidor de eventos de UM processo da API. Ele não guarda estado de verdade: lê o
// event_log (PostgreSQL) quando recebe o aviso fila_eventos (ou a cada PollEvery, como rede de
// segurança) e entrega às conexões abertas deste processo. Por isso vários processos de API
// entregam a mesma sequência, e reiniciar um processo não perde nada (o cliente volta pelo cursor).
type Hub struct {
	Pool      *pgxpool.Pool
	Log       *slog.Logger
	PollEvery time.Duration

	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	last   int64
	inited bool
	wake   chan struct{}
	ready  chan struct{}
	once   sync.Once
}

// Subscription é a fila de entrega de uma conexão. C tem capacidade limitada: se encher (cliente
// lento), Overflow é fechado e a conexão deve ser encerrada (o cliente volta pelo cursor).
type Subscription struct {
	est        uuid.UUID
	C          chan Published
	Overflow   chan struct{}
	Revalidate chan struct{}
	once       sync.Once
}

func (s *Subscription) overflow() { s.once.Do(func() { close(s.Overflow) }) }

// NewHub cria o distribuidor (Run precisa estar rodando para entregar ao vivo).
func NewHub(pool *pgxpool.Pool, log *slog.Logger) *Hub {
	return &Hub{Pool: pool, Log: log, PollEvery: 2 * time.Second, subs: map[*Subscription]struct{}{},
		wake: make(chan struct{}, 1), ready: make(chan struct{})}
}

// Subscribe registra uma conexão da loja estID. Deve ser chamado ANTES de ler o snapshot: assim
// todo evento publicado depois do registro chega por C, e todo evento anterior está no snapshot
// ou no replay (o chamador descarta repetidos pela posição).
func (h *Hub) Subscribe(ctx context.Context, estID uuid.UUID, buffer int) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.inited {
		head, err := ReadHead(ctx, h.Pool)
		if err != nil {
			return nil, err
		}
		h.last, h.inited = head.Last, true
	}
	s := &Subscription{est: estID, C: make(chan Published, buffer), Overflow: make(chan struct{}), Revalidate: make(chan struct{}, 1)}
	h.subs[s] = struct{}{}
	return s, nil
}

// Unsubscribe remove a conexão (sempre chamar ao encerrar).
func (h *Hub) Unsubscribe(s *Subscription) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// Count devolve quantas conexões estão registradas (monitoramento e testes).
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Run escuta os avisos e entrega eventos até ctx terminar.
func (h *Hub) Run(ctx context.Context) {
	go listen(ctx, h.Pool, h.Log, []string{ChannelEvents, ChannelRevocation}, func(ch string) {
		if ch == ChannelRevocation {
			h.revalidateAll()
			return
		}
		h.poke()
	}, func() {
		// Ao (re)conectar: relê o que pode ter sido publicado enquanto estava fora e pede a todos
		// que reconfiram a sessão (um aviso de revogação pode ter se perdido nesse intervalo).
		h.poke()
		h.revalidateAll()
		h.once.Do(func() { close(h.ready) })
	})
	t := time.NewTicker(h.PollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.wake:
		case <-t.C:
		}
		if err := h.pump(ctx); err != nil && ctx.Err() == nil {
			h.Log.Warn("leitura de eventos falhou; tentando de novo", "error", err.Error())
		}
	}
}

// Listening fecha quando a escuta de avisos do banco está ativa pela primeira vez.
func (h *Hub) Listening() <-chan struct{} { return h.ready }

func (h *Hub) poke() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// revalidateAll pede a todas as conexões que reconfiram a sessão agora (alguma foi revogada; o
// aviso não diz qual, para não carregar dados).
func (h *Hub) revalidateAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.Revalidate <- struct{}{}:
		default:
		}
	}
}

// pump lê os eventos novos e entrega, em ordem, às conexões da mesma loja.
func (h *Hub) pump(ctx context.Context) error {
	h.mu.Lock()
	if !h.inited {
		h.mu.Unlock()
		return nil // ninguém conectado ainda
	}
	after := h.last
	h.mu.Unlock()
	for {
		list, err := readAll(ctx, h.Pool, after, 500)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			// Época nova (banco restaurado) pode ter posições menores: realinha.
			head, err := ReadHead(ctx, h.Pool)
			if err == nil && head.Last < after {
				h.mu.Lock()
				h.last = head.Last
				h.mu.Unlock()
			}
			return err
		}
		h.mu.Lock()
		for _, e := range list {
			for s := range h.subs {
				if s.est != e.EstablishmentID {
					continue
				}
				select {
				case s.C <- e:
				default:
					s.overflow()
				}
			}
			h.last = e.Position
		}
		after = h.last
		h.mu.Unlock()
		if len(list) < 500 {
			return nil
		}
	}
}
