package database

import (
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/platform/metrics"
)

// RegisterRuntimeMetrics registra métricas do processo e do pool de conexões com o banco
// (capacidade: conexões em uso, esperas por conexão livre).
func RegisterRuntimeMetrics(reg *metrics.Registry, pool *pgxpool.Pool) {
	start := float64(time.Now().Unix())
	reg.NewGaugeFunc("fila_process_start_time_seconds", "Início do processo (Unix).", func() float64 { return start })
	reg.NewGaugeFunc("fila_go_goroutines", "Goroutines do processo.", func() float64 { return float64(runtime.NumGoroutine()) })
	reg.NewGaugeFunc("fila_go_memory_bytes", "Memória obtida do sistema pelo runtime Go.", func() float64 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return float64(m.Sys)
	})
	reg.NewGaugeFunc("fila_db_pool_acquired_connections", "Conexões do pool em uso.", func() float64 { return float64(pool.Stat().AcquiredConns()) })
	reg.NewGaugeFunc("fila_db_pool_max_connections", "Máximo de conexões do pool.", func() float64 { return float64(pool.Stat().MaxConns()) })
	reg.NewGaugeFunc("fila_db_pool_empty_acquire_total", "Vezes que alguém esperou por conexão livre (pool cheio).", func() float64 {
		return float64(pool.Stat().EmptyAcquireCount())
	})
}
