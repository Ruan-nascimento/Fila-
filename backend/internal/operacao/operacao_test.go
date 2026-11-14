package operacao_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"fila/internal/operacao"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	"fila/internal/platform/testdb"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func migrated(t *testing.T, dbURL string) {
	t.Helper()
	pool := testdb.Pool(t, dbURL)
	defer pool.Close() // a cópia do banco exige a origem sem conexões
	m, err := database.NewMigrator(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// copyDatabase cria uma CÓPIA do banco (CREATE DATABASE … TEMPLATE): mesmos dados, outro oid —
// como uma restauração lógica de backup num banco novo.
func copyDatabase(t *testing.T, srcURL string) string {
	t.Helper()
	ctx := context.Background()
	admin := testdb.URL(t)
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	src, _ := url.Parse(srcURL)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "fila_t_copia_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+
		pgx.Identifier{src.Path[1:]}.Sanitize()); err != nil {
		t.Fatalf("copiar banco: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	return u.String()
}

func TestGateDetectsCopiedDatabaseAndRequiresReconciliation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	origURL := testdb.New(t)
	migrated(t, origURL)
	orig := testdb.Pool(t, origURL)

	st, err := operacao.Gate(ctx, orig, now)
	if err != nil || st.Held() || st.BoundOID == nil || *st.BoundOID != st.CurrentOID || st.WorkerSeenAt == nil {
		t.Fatalf("primeira passagem deveria vincular e liberar: %+v %v", st, err)
	}
	orig.Close() // a cópia exige o banco de origem sem conexões

	copyURL := copyDatabase(t, origURL)
	cp := testdb.Pool(t, copyURL)
	st, err = operacao.Gate(ctx, cp, now.Add(time.Hour))
	if err != nil || !st.Held() || !st.RestoreHold() || *st.HoldReason != operacao.HoldRestoreDetected {
		t.Fatalf("cópia/restauração deveria travar: %+v %v", st, err)
	}
	if !st.WorkerSeenAt.Equal(now) {
		t.Fatal("travado não atualiza o sinal de vida (ele marca o instante do backup)")
	}
	// A trava fica gravada: mesmo que o vínculo fosse corrigido à mão, continua travado.
	if _, err := cp.Exec(ctx, `UPDATE operation_state SET bound_database_oid = (SELECT oid FROM pg_database WHERE datname = current_database())`); err != nil {
		t.Fatal(err)
	}
	if st, _ := operacao.Gate(ctx, cp, now.Add(2*time.Hour)); !st.Held() {
		t.Fatal("trava de restauração não pode sumir sozinha")
	}
	if _, err := operacao.Release(ctx, cp, now.Add(2*time.Hour)); !errors.Is(err, operacao.ErrNotReconciled) {
		t.Fatalf("soltar sem conciliar: %v", err)
	}
	// Pedir manutenção não rebaixa a trava de restauração.
	if st, _ := operacao.Hold(ctx, cp, operacao.HoldMaintenance, "manutenção qualquer", now.Add(2*time.Hour)); !st.RestoreHold() {
		t.Fatal("manutenção rebaixou a trava de restauração")
	}
	if _, err := operacao.Release(ctx, cp, now.Add(2*time.Hour)); !errors.Is(err, operacao.ErrNotReconciled) {
		t.Fatalf("ainda sem conciliar: %v", err)
	}
	if err := operacao.MarkReconciled(ctx, cp, "teste", now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Restaurado DE NOVO antes de liberar (ex.: backup errado): a conciliação feita não vale mais.
	if _, err := operacao.Hold(ctx, cp, operacao.HoldRestore, "restaurado outra vez", now.Add(3*time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := operacao.Release(ctx, cp, now.Add(3*time.Hour+time.Minute)); !errors.Is(err, operacao.ErrNotReconciled) {
		t.Fatalf("nova restauração sem nova conciliação: %v", err)
	}
	if err := operacao.MarkReconciled(ctx, cp, "teste", now.Add(3*time.Hour+2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, err = operacao.Release(ctx, cp, now.Add(3*time.Hour))
	if err != nil || st.Held() || *st.BoundOID != st.CurrentOID {
		t.Fatalf("liberar depois de conciliar: %+v %v", st, err)
	}
	if st, _ := operacao.Gate(ctx, cp, now.Add(4*time.Hour)); st.Held() || !st.WorkerSeenAt.Equal(now.Add(4*time.Hour)) {
		t.Fatalf("liberado: %+v", st)
	}
	// Nova restauração declarada: conciliação anterior não vale.
	if _, err := operacao.Hold(ctx, cp, operacao.HoldRestore, "restaurado de novo", now.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := operacao.Release(ctx, cp, now.Add(5*time.Hour)); !errors.Is(err, operacao.ErrNotReconciled) {
		t.Fatalf("nova restauração exige nova conciliação: %v", err)
	}
	if _, err := operacao.Release(ctx, cp, now); err == nil {
		t.Fatal("não deveria")
	}
}

func TestMaintenanceHoldAndRelease(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Migrated(t)
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	if _, err := operacao.Release(ctx, pool, now); !errors.Is(err, operacao.ErrNotHeld) {
		t.Fatalf("soltar sem trava: %v", err)
	}
	if err := operacao.MarkReconciled(ctx, pool, "teste", now); !errors.Is(err, operacao.ErrNotHeld) {
		t.Fatalf("conciliar sem trava de restauração: %v", err)
	}
	st, err := operacao.Hold(ctx, pool, operacao.HoldMaintenance, "trocar disco", now)
	if err != nil || !st.Held() || st.RestoreHold() {
		t.Fatalf("manutenção: %+v %v", st, err)
	}
	st, _ = operacao.Hold(ctx, pool, operacao.HoldMaintenance, "trocar disco (mais tempo)", now.Add(time.Minute))
	if !st.HoldSince.Equal(now) {
		t.Fatal("manutenção repetida deve manter o início")
	}
	if st, err := operacao.Release(ctx, pool, now.Add(time.Hour)); err != nil || st.Held() {
		t.Fatalf("manutenção sai sem conciliação: %+v %v", st, err)
	}
	if _, err := operacao.Hold(ctx, pool, "outro", "x", now); err == nil {
		t.Fatal("motivo inválido")
	}
}

// O supervisor liga as tarefas liberado, para com trava (esperando a rodada terminar) e religa.
func TestSupervisorStopsAndRestartsJobs(t *testing.T) {
	pool := testdb.Migrated(t)
	clk := clock.NewFake(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC))
	var starts, running atomic.Int32
	sup := &operacao.Supervisor{Pool: pool, Clock: clk, Log: quiet, Every: 50 * time.Millisecond,
		Start: func(ctx context.Context) {
			starts.Add(1)
			running.Add(1)
			<-ctx.Done()
			running.Add(-1)
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()
	wait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("esperando: %s (starts=%d running=%d)", what, starts.Load(), running.Load())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("ligar", func() bool { return running.Load() == 1 })
	if _, err := operacao.Hold(context.Background(), pool, operacao.HoldMaintenance, "teste", clk.Now()); err != nil {
		t.Fatal(err)
	}
	wait("parar", func() bool { return running.Load() == 0 })
	time.Sleep(200 * time.Millisecond)
	if starts.Load() != 1 {
		t.Fatal("travado não pode religar")
	}
	if _, err := operacao.Release(context.Background(), pool, clk.Now()); err != nil {
		t.Fatal(err)
	}
	wait("religar", func() bool { return running.Load() == 1 && starts.Load() == 2 })
	cancel()
	<-done
	if running.Load() != 0 {
		t.Fatal("tarefas devem terminar junto com o supervisor")
	}
}

// Indicadores num banco vazio: leitura funciona, nada alarmante além do worker nunca visto.
func TestCollectAndEvaluate(t *testing.T) {
	pool := testdb.Migrated(t)
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	ind, err := operacao.Collect(context.Background(), pool, now)
	if err != nil {
		t.Fatal(err)
	}
	alerts := operacao.Evaluate(ind)
	if len(alerts) != 1 || alerts[0].Name != "FilaWorkerSemSinal" || alerts[0].Owner != operacao.OwnerOperator {
		t.Fatalf("banco vazio sem worker: %+v", alerts)
	}
	if _, err := operacao.Gate(context.Background(), pool, now); err != nil {
		t.Fatal(err)
	}
	ind, _ = operacao.Collect(context.Background(), pool, now.Add(time.Minute))
	if a := operacao.Evaluate(ind); len(a) != 0 {
		t.Fatalf("worker visto há 1 min: %+v", a)
	}
	ind, _ = operacao.Collect(context.Background(), pool, now.Add(3*time.Minute))
	if a := operacao.Evaluate(ind); len(a) != 1 || a[0].Severity != operacao.Critical {
		t.Fatalf("worker sumido há 3 min: %+v", a)
	}
}
