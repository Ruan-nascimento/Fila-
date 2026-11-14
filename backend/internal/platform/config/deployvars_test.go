package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// A lista DeployVars acompanha os arquivos de implantação: toda variável FILA_ usada lá é da
// aplicação (conhecida) ou da implantação (DeployVars).
func TestDeployVarsMatchDeployFiles(t *testing.T) {
	re := regexp.MustCompile(`FILA_[A-Z_]+`)
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "..", "deploy", "*"))
	more, _ := filepath.Glob(filepath.Join("..", "..", "..", "..", "deploy", "env", "*.example"))
	files = append(files, more...)
	seen := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue // pasta
		}
		for _, v := range re.FindAllString(string(raw), -1) {
			seen++
			base := v
			if len(v) > 5 && v[len(v)-5:] == "_FILE" {
				base = v[:len(v)-5]
			}
			if !knownVars[v] && !slices.Contains(DeployVars, v) && !slices.Contains(FileVars, base) {
				t.Errorf("%s usa %s, que não é variável da aplicação nem de DeployVars", filepath.Base(f), v)
			}
		}
	}
	if seen == 0 {
		t.Fatal("nenhum arquivo de implantação lido")
	}
}
