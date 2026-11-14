package operacao_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"fila/internal/operacao"
)

// Os alertas avaliados aqui (fila-admin verificar) e os do Prometheus (deploy/monitoramento/
// alertas.yml) têm os MESMOS nomes, limiares, responsáveis e rotinas: quem usa um ou outro age igual.
func TestPrometheusRulesMatchEvaluate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "monitoramento", "alertas.yml"))
	if err != nil {
		t.Fatal(err)
	}
	yml := string(raw)
	for _, name := range []string{"FilaWorkerTravadoRestauracao", "FilaWorkerEmManutencaoLonga", "FilaWorkerSemSinal",
		"FilaOutboxAtrasada", "FilaPixDesconhecido", "FilaDivergenciaFinanceiraAntiga", "FilaDivergenciaPosRestauracao",
		"FilaEstornoParado", "FilaExclusaoSemProcessamento", "FilaExclusaoPertoDaMeta", "FilaReservaVencidaPresa",
		"FilaEmailParado", "FilaEmailFalhou"} {
		if !strings.Contains(yml, "alert: "+name+"\n") {
			t.Errorf("alerta %s não está no Prometheus", name)
		}
		if !strings.Contains(src(t), `"`+name+`"`) {
			t.Errorf("alerta %s não está em Evaluate", name)
		}
	}
	for _, want := range []string{
		fmt.Sprintf("fila_outbox_oldest_seconds > %d", int(operacao.OutboxLagMax.Seconds())),
		fmt.Sprintf("fila_worker_last_seen_age_seconds > %d", int(operacao.WorkerSilenceMax.Seconds())),
		fmt.Sprintf("fila_worker_hold_seconds > %d", int(operacao.MaintenanceHoldMax.Seconds())),
		fmt.Sprintf("fila_financial_issue_oldest_seconds > %d", int(operacao.IssueAgeMax.Seconds())),
		fmt.Sprintf("fila_deletions_unchecked_seconds > %d", int(operacao.DeletionUncheckedMax.Seconds())),
		fmt.Sprintf("fila_mail_oldest_pending_seconds > %d", int(operacao.MailPendingMax.Seconds())),
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("limiar diferente do de Evaluate: falta %q", want)
		}
	}
	// Toda regra diz severidade, responsável e rotina; toda rotina existe no documento de rotinas.
	rotinas, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "operacao", "rotinas.md"))
	if err != nil {
		t.Fatal(err)
	}
	labels := regexp.MustCompile(`labels: \{severity: (critico|aviso), responsavel: [^,]+, rotina: ([a-z0-9-]+)\}`)
	n := strings.Count(yml, "- alert: ")
	found := labels.FindAllStringSubmatch(yml, -1)
	if len(found) != n {
		t.Fatalf("%d alertas, %d com severidade/responsável/rotina", n, len(found))
	}
	for _, m := range found {
		if !strings.Contains(string(rotinas), `<a id="`+m[2]+`"></a>`) {
			t.Errorf("rotina %q citada no alerta não existe em docs/operacao/rotinas.md", m[2])
		}
	}
}

func src(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("indicadores.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
