// Package arquitetura verifica, por teste, regras de dependência entre pacotes (ADR-0001).
package arquitetura

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
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
