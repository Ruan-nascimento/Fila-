package httpserver

import (
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IPLimiter é o limite GERAL de requisições por IP, em memória, por processo (balde de fichas).
// É a primeira linha contra abuso e robôs: barato e sem ir ao banco. Os limites contra
// adivinhação de senha, código e cadastro continuam no banco (pacote ratelimit), valendo para
// todos os processos. Limitação: com N processos da API, o teto efetivo por IP é N × o configurado.
type IPLimiter struct {
	mu        sync.Mutex
	rate      float64 // fichas por segundo
	capacity  float64
	buckets   map[string]*tokenBucket
	now       func() time.Time
	lastSweep time.Time
}

type tokenBucket struct {
	tokens float64
	at     time.Time
}

// NewIPLimiter aceita perMinute requisições por minuto por IP, com rajada de metade disso (uma
// página que abre faz várias requisições de uma vez).
func NewIPLimiter(perMinute int) *IPLimiter {
	return &IPLimiter{rate: float64(perMinute) / 60, capacity: math.Max(float64(perMinute)/2, 10),
		buckets: map[string]*tokenBucket{}, now: time.Now}
}

// Allow consome uma ficha do IP. Se não houver, devolve quanto esperar.
func (l *IPLimiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) > time.Minute {
		// Remove baldes cheios (IPs parados): a memória não cresce com IPs que só passaram.
		for k, b := range l.buckets {
			if b.tokens+now.Sub(b.at).Seconds()*l.rate >= l.capacity {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[ip]
	if !ok {
		b = &tokenBucket{tokens: l.capacity, at: now}
		l.buckets[ip] = b
	}
	b.tokens = math.Min(l.capacity, b.tokens+now.Sub(b.at).Seconds()*l.rate)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, max(wait, time.Second)
}

// Size informa quantos IPs estão sendo acompanhados (testes e métrica).
func (l *IPLimiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Options liga as proteções e a observação opcionais da cadeia de middlewares.
type Options struct {
	// Limiter: limite geral por IP (nil = desligado). Sondas de saúde e o webhook do provedor
	// ficam fora (o provedor repete avisos e tem limite de corpo próprio).
	Limiter           *IPLimiter
	TrustForwardedFor bool
	// Observe recebe rota-modelo, método, status e duração de cada requisição (métricas).
	Observe func(route, method string, status int, d time.Duration)
	// Limited é chamado a cada requisição recusada pelo limite geral (métrica).
	Limited func()
}

func limitByIP(o Options, next http.Handler) http.Handler {
	if o.Limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/healthz/") || strings.HasPrefix(p, "/v1/webhooks/") {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := o.Limiter.Allow(ClientIP(r, o.TrustForwardedFor)); !ok {
			if o.Limited != nil {
				o.Limited()
			}
			WriteError(w, r, nil, ErrRateLimited(wait))
			return
		}
		next.ServeHTTP(w, r)
	})
}
