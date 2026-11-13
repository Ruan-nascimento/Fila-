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

var dominio = []string{"identidade", "estabelecimento", "catalogo", "fila", "atendimento", "pagamento", "fidelidade", "auditoria"}

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
	"identidade":           {"account", "account_token", "account_session", "invite", "visitor", "visitor_browser", "visitor_credential"},
	"estabelecimento":      {"establishment", "professional", "staff_membership", "queue"},
	"catalogo":             {"service", "service_professional", "service_revision"},
	"fila":                 {"participation", "participation_transfer", "queue_counter"},
	"atendimento":          {"service_order", "order_item", "change_request", "change_request_addition", "order_adjustment"},
	"auditoria":            {"audit_event"},
	"platform/ratelimit":   {"rate_limit"},
	"platform/idempotency": {"idempotency_record"},
	"platform/mail":        {"mail_outbox"},
	"platform/outbox":      {"outbox", "event_log", "event_stream_state"},
}

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
			if !known || own == pkg || slices.Contains(maintenanceTables[pkg], tb) {
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
