package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier é o que pool e transação têm em comum. Funções de repositório recebem Querier
// para funcionar dentro ou fora de uma transação maior.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = (pgx.Tx)(nil)
)

// InTx executa fn numa transação (READ COMMITTED). Erro ou pânico em fn desfaz tudo.
// Invariantes que dependem de concorrência são protegidos por restrições e bloqueios de
// linha (SELECT … FOR UPDATE) dentro de fn, não pelo nível de isolamento.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iniciar transação: %s", Describe(err))
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if guard, ok := ctx.Value(txGuardKey{}).(TxGuard); ok && guard != nil {
		if err = guard(ctx, tx); err != nil {
			return err
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("confirmar transação: %s", Describe(err))
	}
	return nil
}

// TxGuard roda como primeiro comando de cada transação aberta por InTx com o contexto marcado
// (WithTxGuard). Serve para revalidar a autoridade do ator no ponto de efetivação: o módulo
// identidade, por exemplo, trava em modo compartilhado a vaga de dono da sessão (ADR-0027) e
// recusa a transação se a sessão perdeu a vaga. Devolver erro desfaz a transação antes de fn.
type TxGuard func(ctx context.Context, tx pgx.Tx) error

type txGuardKey struct{}

// WithTxGuard marca o contexto: toda transação InTx aberta com ele roda guard primeiro.
func WithTxGuard(ctx context.Context, guard TxGuard) context.Context {
	return context.WithValue(ctx, txGuardKey{}, guard)
}

// WithoutTxGuard tira a guarda (usado por quem troca a própria autoridade, como login e logout,
// que travam a mesma linha em modo exclusivo).
func WithoutTxGuard(ctx context.Context) context.Context {
	return context.WithValue(ctx, txGuardKey{}, TxGuard(nil))
}

// IsUniqueViolation informa se err é violação de unicidade (23505), opcionalmente de uma
// restrição específica (nome do índice/constraint).
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// InSnapshot roda fn numa transação REPEATABLE READ somente leitura: todas as leituras de fn
// enxergam o MESMO instante do banco. Usado para devolver dados junto com o cursor de eventos
// daquele instante (eventos-sse.md §6).
func InSnapshot(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("iniciar leitura: %s", Describe(err))
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	return fn(tx)
}
