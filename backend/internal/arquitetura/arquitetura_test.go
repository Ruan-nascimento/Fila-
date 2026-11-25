// Package arquitetura verifica, por teste, regras de dependência entre pacotes (ADR-0001).
package arquitetura

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "fila"

var dominio = []string{"identidade", "estabelecimento", "catalogo", "fila", "atendimento", "pagamento", "fidelidade", "auditoria", "agenda"}

// imports devolve, para cada arquivo .go sob dir, os caminhos importados.
func imports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			out[path] = append(out[path], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDomainModulesExist(t *testing.T) {
	for _, m := range dominio {
		if _, err := os.Stat(filepath.Join("..", m, "doc.go")); err != nil {
			t.Errorf("módulo %s ausente: %v", m, err)
		}
	}
}

func TestPlatformDoesNotImportDomainModules(t *testing.T) {
	for file, ims := range imports(t, filepath.Join("..", "platform")) {
		for _, p := range ims {
			for _, m := range dominio {
				if p == module+"/internal/"+m || strings.HasPrefix(p, module+"/internal/"+m+"/") {
					t.Errorf("%s importa o módulo de domínio %s; a plataforma deve ser independente do domínio", file, p)
				}
			}
		}
	}
}

func TestNoPackageImportsExecutables(t *testing.T) {
	for file, ims := range imports(t, "..") {
		for _, p := range ims {
			if strings.HasPrefix(p, module+"/cmd/") {
				t.Errorf("%s importa %s; executáveis não são bibliotecas", file, p)
			}
		}
	}
}

// Dono de cada tabela (ADR-0001: cada módulo acessa só as próprias tabelas; os outros usam
// funções exportadas). A chave é o caminho do pacote sob internal/.
var tableOwners = map[string][]string{
	"identidade": {"account", "account_token", "account_session", "invite", "visitor", "visitor_browser", "visitor_credential",
		"owner_session_slot", "owner_session_challenge", "desktop_activation_code", "desktop_installation", "desktop_link_ticket",
		"desktop_binding", "desktop_proof_seen", "external_identity", "oidc_transaction"},
	"midia":           {"media_asset"},
	"estabelecimento": {"establishment", "establishment_slug_alias", "professional", "staff_membership", "queue", "establishment_settings"},
	"catalogo":        {"service", "service_professional", "service_revision", "catalog_category", "product", "product_revision"},
	"fila":            {"participation", "participation_transfer", "queue_counter"},
	"atendimento": {"service_order", "order_item", "change_request", "change_request_addition", "order_adjustment",
		"order_item_quantity_change", "change_request_item_quantity"},
	"auditoria":  {"audit_event"},
	"exclusao":   {"deletion_request", "deletion_request_block"},
	"fidelidade": {"loyalty_program", "loyalty_account", "loyalty_ledger", "reward", "reward_stock_movement", "reward_reservation"},
	"pagamento": {"pix_charge", "receipt", "receipt_conferral", "refund", "financial_issue", "provider_event",
		"payment_fee_config", "receipt_fee"},
	"platform/ratelimit":   {"rate_limit"},
	"platform/idempotency": {"idempotency_record"},
	"platform/mail":        {"mail_outbox"},
	"platform/outbox":      {"outbox", "event_log", "event_stream_state"},
	"operacao":             {"operation_state"},
	"agenda": {"agenda_schedule", "agenda_shift", "agenda_exception", "appointment", "appointment_item", "agenda_occupancy",
		"appointment_event"},
}

// Leitura analítica do dono (ADR-0027): o módulo analitico agrega, no banco, dados de vários
// módulos (fila, comanda, recebimentos, fidelidade). Pode só LER estas tabelas: nenhuma escrita
// (TestAnalyticsIsReadOnly) e nenhuma tabela de sessão, credencial ou dado pessoal de contato.
var analyticsReadTables = []string{"establishment", "professional", "queue", "service", "participation",
	"participation_transfer", "service_order", "order_item", "order_adjustment", "receipt", "refund",
	"financial_issue", "pix_charge", "loyalty_program", "loyalty_ledger", "reward", "reward_reservation", "receipt_fee", "product"}

// Pacotes que podem tocar tabelas da plataforma para manutenção técnica.
var maintenanceTables = map[string][]string{
	"platform/maintenance": {"rate_limit", "idempotency_record", "mail_outbox"},
}

func TestModulesOnlyQueryOwnTables(t *testing.T) {
	owner := map[string]string{}
	for pkg, tables := range tableOwners {
		for _, tb := range tables {
			owner[tb] = pkg
		}
	}
	sqlRef := regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE)\s+([a-z_]+)\b`)
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		pkg := filepath.ToSlash(rel)
		if pkg == "contrato" || strings.HasPrefix(pkg, "api") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range sqlRef.FindAllStringSubmatch(string(src), -1) {
			tb := strings.ToLower(m[1])
			own, known := owner[tb]
			if !known || own == pkg || slices.Contains(maintenanceTables[pkg], tb) ||
				(pkg == "analitico" && slices.Contains(analyticsReadTables, tb)) {
				continue
			}
			t.Errorf("%s consulta a tabela %s, que pertence a %s; use uma função exportada do módulo dono", path, tb, own)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// O módulo analitico nunca escreve: só SELECT (sem INSERT, UPDATE, DELETE, MERGE, TRUNCATE nem
// FOR UPDATE/SHARE) e só nas tabelas permitidas.
func TestAnalyticsIsReadOnly(t *testing.T) {
	write := regexp.MustCompile(`(?i)\b(INSERT\s+INTO|UPDATE\s+[a-z_]+\s+SET|DELETE\s+FROM|MERGE\s+INTO|TRUNCATE|FOR\s+(UPDATE|SHARE|NO\s+KEY))\b`)
	ref := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-z_]+)\b`)
	dir := filepath.Join("..", "analitico")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("módulo analitico ausente: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if m := write.FindString(string(src)); m != "" {
			t.Errorf("%s: o módulo analitico só lê; encontrado %q", e.Name(), m)
		}
		for _, m := range ref.FindAllStringSubmatch(string(src), -1) {
			tb := strings.ToLower(m[1])
			if _, known := tableIndex()[tb]; known && !slices.Contains(analyticsReadTables, tb) {
				t.Errorf("%s: o módulo analitico não pode ler a tabela %s", e.Name(), tb)
			}
		}
	}
}

func tableIndex() map[string]string {
	owner := map[string]string{}
	for pkg, tables := range tableOwners {
		for _, tb := range tables {
			owner[tb] = pkg
		}
	}
	return owner
}
