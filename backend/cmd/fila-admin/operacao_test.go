package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fila/internal/operacao"
	"fila/internal/platform/testdb"
)

func admin(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, env, &out, &errb)
	return code, out.String(), errb.String()
}

// Fluxo do operador: verificar → manutenção → restauração (travar, conciliar, liberar), com as
// recusas que protegem contra engano (conciliar sem trava, liberar sem conciliar, encerrar
// manutenção numa trava de restauração, conciliar sem dizer o que fazer com Pix e diário).
func TestOperatorMaintenanceAndRestoreCommands(t *testing.T) {
	t.Chdir(t.TempDir())
	pool := testdb.Migrated(t)
	env := []string{"FILA_ENV=test", "FILA_DATABASE_URL=" + pool.Config().ConnString(), testdb.SecretKeyVar()}
	ctx := context.Background()

	// Worker nunca rodou: alerta crítico, saída 3, com responsável e rotina.
	code, out, _ := admin(t, env, "verificar")
	if code != ExitAlerts || !strings.Contains(out, "FilaWorkerSemSinal") || !strings.Contains(out, "responsável: operador") ||
		!strings.Contains(out, "docs/operacao/rotinas.md#worker-parado") {
		t.Fatalf("verificar: %d %s", code, out)
	}
	if _, err := operacao.Gate(ctx, pool, time.Now().UTC()); err != nil { // worker passou por aqui
		t.Fatal(err)
	}
	dir := t.TempDir()
	if code, out, _ := admin(t, env, "verificar", "-backups", dir); code != ExitAlerts || !strings.Contains(out, "FilaBackupAusente") {
		t.Fatalf("sem backup: %d %s", code, out)
	}
	_ = os.WriteFile(filepath.Join(dir, BackupMarker), []byte(time.Now().UTC().Add(-30*time.Hour).Format(time.RFC3339)+"\n"), 0o600)
	if code, out, _ := admin(t, env, "verificar", "-backups", dir); code != ExitAlerts || !strings.Contains(out, "FilaBackupAtrasado") {
		t.Fatalf("backup velho: %d %s", code, out)
	}
	_ = os.WriteFile(filepath.Join(dir, BackupMarker), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	if code, out, errb := admin(t, env, "verificar", "-backups", dir); code != 0 || !strings.Contains(out, "nenhum alerta ativo") {
		t.Fatalf("tudo certo: %d %s %s", code, out, errb)
	}
	if code, out, _ := admin(t, env, "verificar", "-json"); code != 0 || !strings.Contains(out, `"alerts":null`) {
		t.Fatalf("json: %d %s", code, out)
	}

	// Conciliar sem trava de restauração: recusado.
	if code, _, errb := admin(t, env, "restauracao", "reconciliar", "-sem-pix", "-sem-diario"); code != 2 || !strings.Contains(errb, "não está travado por restauração") {
		t.Fatalf("conciliar sem trava: %d %s", code, errb)
	}
	// Manutenção.
	if code, _, _ := admin(t, env, "manutencao", "iniciar"); code != 2 {
		t.Fatal("manutenção sem motivo deveria ser recusada")
	}
	if code, out, errb := admin(t, env, "manutencao", "iniciar", "-motivo", "atualizar o servidor"); code != 0 || !strings.Contains(out, "travado") {
		t.Fatalf("manutenção: %d %s %s", code, out, errb)
	}
	if code, out, _ := admin(t, env, "verificar"); code != 0 || !strings.Contains(out, "TRAVADO (manutenção)") {
		t.Fatalf("verificar em manutenção (curta não alerta): %d %s", code, out)
	}
	if code, out, _ := admin(t, env, "manutencao", "encerrar"); code != 0 || !strings.Contains(out, "liberado") {
		t.Fatalf("encerrar manutenção: %d %s", code, out)
	}
	// Restauração.
	if code, out, errb := admin(t, env, "restauracao", "travar", "-motivo", "restaurado do backup de teste"); code != 0 || !strings.Contains(out, "PENDENTE") {
		t.Fatalf("travar: %d %s %s", code, out, errb)
	}
	if code, _, errb := admin(t, env, "manutencao", "encerrar"); code != 2 || !strings.Contains(errb, "RESTAURAÇÃO") {
		t.Fatalf("encerrar manutenção numa trava de restauração: %d %s", code, errb)
	}
	if code, _, errb := admin(t, env, "restauracao", "liberar"); code != 2 || !strings.Contains(errb, "conciliação") {
		t.Fatalf("liberar sem conciliar: %d %s", code, errb)
	}
	if code, _, errb := admin(t, env, "restauracao", "reconciliar", "-sem-diario"); code != 1 || !strings.Contains(errb, "sem-pix") {
		t.Fatalf("Pix desligado sem confirmação: %d %s", code, errb)
	}
	if code, _, errb := admin(t, env, "restauracao", "reconciliar", "-sem-pix"); code != 1 || !strings.Contains(errb, "-sem-diario") {
		t.Fatalf("diário sem confirmação: %d %s", code, errb)
	}
	code, out, errb := admin(t, env, "restauracao", "reconciliar", "-sem-pix", "-sem-diario")
	if code != 0 || !strings.Contains(out, "época nova") || !strings.Contains(out, "Pix: nunca ligado") || !strings.Contains(out, "liberar") {
		t.Fatalf("reconciliar: %d %s %s", code, out, errb)
	}
	if code, out, _ := admin(t, env, "restauracao", "status"); code != 0 || !strings.Contains(out, "pode liberar") {
		t.Fatalf("status: %d %s", code, out)
	}
	if code, out, errb := admin(t, env, "restauracao", "liberar"); code != 0 || !strings.Contains(out, "liberado") {
		t.Fatalf("liberar: %d %s %s", code, out, errb)
	}
	// Auditoria: travar, conciliar e liberar registrados com ator operador (sem dado pessoal).
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE actor_kind = 'operator' AND action IN ('T-OP-03', 'T-OP-04')`).Scan(&n)
	if n != 5 {
		t.Fatalf("auditoria das ações de operação: %d", n)
	}
}
