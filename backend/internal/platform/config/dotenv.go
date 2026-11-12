package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FromProcess é Resolve(os.Environ()).
func FromProcess() (*Config, string, error) { return Resolve(os.Environ()) }

// Resolve monta o ambiente efetivo a partir de environ e valida a configuração.
//
// Fora de produção, lê um arquivo .env para facilitar o desenvolvimento local:
//   - caminho em FILA_ENV_FILE, se definido; senão ".env" na pasta atual; senão "../.env"
//     (permite rodar a partir de backend/ com o .env na raiz do repositório).
//   - variáveis já definidas no processo têm prioridade sobre o arquivo.
//
// Em produção (FILA_ENV=production definido no processo) o .env é ignorado: segredos vêm
// do ambiente fornecido pela plataforma de implantação.
//
// Devolve também o caminho do .env usado (vazio se nenhum), para registrar no log.
func Resolve(environ []string) (*Config, string, error) {
	environ, used, err := effectiveEnviron(environ, os.ReadFile)
	if err != nil {
		return nil, "", err
	}
	cfg, err := Load(environ)
	return cfg, used, err
}

type readFileFunc func(string) ([]byte, error)

func effectiveEnviron(environ []string, read readFileFunc) ([]string, string, error) {
	proc := toMap(environ)
	if proc[VarEnv] == string(EnvProduction) {
		return environ, "", nil
	}
	candidates := []string{".env", filepath.Join("..", ".env")}
	explicit := proc[VarEnvFile] != ""
	if explicit {
		candidates = []string{proc[VarEnvFile]}
	}
	for _, path := range candidates {
		data, err := read(path)
		if errors.Is(err, fs.ErrNotExist) && !explicit {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("não foi possível ler o arquivo de ambiente %s: %w", path, err)
		}
		values, err := ParseDotEnv(string(data))
		if err != nil {
			return nil, "", fmt.Errorf("arquivo de ambiente %s: %w", path, err)
		}
		merged := append([]string(nil), environ...)
		for k, v := range values {
			if _, exists := proc[k]; !exists {
				merged = append(merged, k+"="+v)
			}
		}
		return merged, path, nil
	}
	return environ, "", nil
}

// ParseDotEnv interpreta um .env simples: linhas CHAVE=valor, comentários com #, "export " opcional,
// aspas simples ou duplas em volta do valor. Não expande variáveis. Erros citam só o número da linha.
func ParseDotEnv(content string) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(content))
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return nil, fmt.Errorf("linha %d: formato esperado CHAVE=valor", n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
