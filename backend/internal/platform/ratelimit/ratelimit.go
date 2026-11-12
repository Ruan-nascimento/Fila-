// Package ratelimit limita tentativas com contadores no PostgreSQL (R-CONTA-11, R-SEG-04).
//
// Por que no banco: a API pode rodar em mais de um processo; um contador em memória seria
// burlado trocando de processo. Janela fixa: cada regra conta eventos por "assunto"
// (conta, IP, navegador, loja) dentro de janelas alinhadas ao relógio. O assunto nunca é
// gravado em claro: guardamos só um HMAC dele (sem e-mail nem IP no banco).
//
// Limitação conhecida: janela fixa permite até 2× o limite na virada da janela. Para os
// limites usados (dezenas de tentativas contra segredos de 80+ bits) isso não muda o risco.
package ratelimit

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"fila/internal/platform/clock"
	"fila/internal/platform/keys"
)

// Rule é uma regra de limite: no máximo Limit eventos por assunto a cada Window.
type Rule struct {
	Name   string
	Limit  int
	Window time.Duration
}

// Limiter conta eventos por regra e assunto.
type Limiter struct {
	db    DB
	clock clock.Clock
	ring  *keys.Ring
}

// DB é o banco usado pelo limitador. Use o pool: contagens precisam persistir mesmo se a
// transação do pedido for desfeita (uma tentativa errada continua contando).
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// New cria o limitador.
func New(db DB, c clock.Clock, ring *keys.Ring) *Limiter {
	return &Limiter{db: db, clock: c, ring: ring}
}

func (l *Limiter) bucket(rule Rule, subject string) string {
	mac := l.ring.MAC(keys.PurposeRateLimit, []byte(rule.Name), []byte(subject))
	return rule.Name + ":" + hex.EncodeToString(mac[:16])
}

func (l *Limiter) window(rule Rule) (start time.Time, retry time.Duration) {
	now := l.clock.Now()
	start = now.Truncate(rule.Window)
	return start, start.Add(rule.Window).Sub(now)
}

// Exceeded informa se o assunto já atingiu o limite na janela atual, sem contar um novo evento.
// Use antes de verificar um segredo: bloqueado não chega a testar a senha/código.
func (l *Limiter) Exceeded(ctx context.Context, rule Rule, subject string) (bool, time.Duration, error) {
	start, retry := l.window(rule)
	var hits int
	err := l.db.QueryRow(ctx,
		`SELECT coalesce((SELECT hits FROM rate_limit WHERE bucket = $1 AND window_start = $2), 0)`,
		l.bucket(rule, subject), start).Scan(&hits)
	if err != nil {
		return false, 0, fmt.Errorf("consultar limite %s: %w", rule.Name, err)
	}
	return hits >= rule.Limit, retry, nil
}

// Hit registra um evento (ex.: tentativa errada) e devolve o total na janela.
func (l *Limiter) Hit(ctx context.Context, rule Rule, subject string) (int, error) {
	start, _ := l.window(rule)
	var hits int
	err := l.db.QueryRow(ctx, `
		INSERT INTO rate_limit (bucket, window_start, hits, expires_at)
		VALUES ($1, $2, 1, $3)
		ON CONFLICT (bucket) DO UPDATE SET
			hits = CASE WHEN rate_limit.window_start = EXCLUDED.window_start THEN rate_limit.hits + 1 ELSE 1 END,
			window_start = EXCLUDED.window_start,
			expires_at = EXCLUDED.expires_at
		RETURNING hits`,
		l.bucket(rule, subject), start, start.Add(rule.Window)).Scan(&hits)
	if err != nil {
		return 0, fmt.Errorf("registrar tentativa %s: %w", rule.Name, err)
	}
	return hits, nil
}

// Allow conta o evento e informa se ele ainda cabe no limite (para ações que contam sempre,
// como criar visitante ou pedir e-mail).
func (l *Limiter) Allow(ctx context.Context, rule Rule, subject string) (bool, time.Duration, error) {
	hits, err := l.Hit(ctx, rule, subject)
	if err != nil {
		return false, 0, err
	}
	_, retry := l.window(rule)
	return hits <= rule.Limit, retry, nil
}
