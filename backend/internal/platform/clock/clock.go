// Package clock fornece o relógio da aplicação.
//
// Todo prazo (expiração de sessão, token, código, janela de tentativas) é calculado com o
// relógio injetado, nunca com time.Now() espalhado pelo código. Assim os testes avançam o
// tempo de forma controlada (ADR-0014) e o servidor grava os instantes que valem.
package clock

import (
	"sync"
	"time"
)

// Clock informa o instante atual em UTC, com precisão de microssegundos (a mesma do PostgreSQL).
type Clock interface {
	Now() time.Time
}

// System é o relógio real.
type System struct{}

// Now devolve o instante atual em UTC, truncado em microssegundos.
func (System) Now() time.Time { return normalize(time.Now()) }

// Fake é um relógio controlável para testes. Seguro para uso concorrente.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake cria um relógio parado no instante informado.
func NewFake(t time.Time) *Fake { return &Fake{now: normalize(t)} }

// Now devolve o instante atual do relógio de teste.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance avança o relógio.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = normalize(f.now.Add(d))
}

func normalize(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }
