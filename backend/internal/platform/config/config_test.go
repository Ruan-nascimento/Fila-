package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const devURL = "postgres://fila:segredo-local-123@localhost:5432/fila?sslmode=disable"

func valid(extra ...string) []string {
	return append([]string{"FILA_ENV=development", "FILA_DATABASE_URL=" + devURL, "PATH=/usr/bin"}, extra...)
}

func TestLoadValidUsesDefaults(t *testing.T) {
	cfg, err := Load(valid())
	if err != nil {
		t.Fatalf("esperava configuração válida: %v", err)
	}
	if cfg.Env != EnvDevelopment || cfg.HTTP.Addr != "127.0.0.1:8080" || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("padrões inesperados: %+v", cfg)
	}
	if cfg.HTTP.ReadHeaderTimeout != 5*time.Second || cfg.HTTP.ShutdownTimeout != 20*time.Second {
		t.Fatalf("timeouts padrão inesperados: %+v", cfg.HTTP)
	}
	if cfg.Database.MaxConns != 10 || cfg.Database.URL.Reveal() != devURL {
		t.Fatalf("banco inesperado")
	}
}

func TestLoadReportsAllProblemsWithoutValues(t *testing.T) {
	_, err := Load([]string{
		"FILA_ENV=producao",
		"FILA_HTTP_ADDR=sem-porta",
		"FILA_HTTP_WRITE_TIMEOUT=10 anos",
		"FILA_DB_MAX_CONNS=0",
		"FILA_LOG_LEVEL=verbose",
		"FILA_DATABSE_URL=" + devURL, // erro de digitação
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("esperava ValidationError, veio %v", err)
	}
	want := []string{"FILA_DATABASE_URL", "FILA_DATABSE_URL", "FILA_DB_MAX_CONNS", "FILA_ENV",
		"FILA_HTTP_ADDR", "FILA_HTTP_WRITE_TIMEOUT", "FILA_LOG_LEVEL"}
	got := map[string]bool{}
	for _, p := range ve.Problems {
		got[p.Var] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("faltou problema para %s; mensagem:\n%s", w, err)
		}
	}
	if strings.Contains(err.Error(), "segredo-local-123") || strings.Contains(err.Error(), "producao") {
		t.Fatalf("a mensagem de erro não pode conter valores: %s", err)
	}
	if !IsValidationError(err) {
		t.Fatal("IsValidationError deveria reconhecer o erro")
	}
}

func TestDatabaseURLValidation(t *testing.T) {
	cases := map[string]string{
		"mysql://u:p@h/db":               "deve ser uma URL postgres",
		"postgres://u:p@/db":             "falta o host",
		"postgres://u:p@localhost:5432":  "falta o nome do banco",
		"postgres://localhost:5432/fila": "falta o usuário",
		"::::":                           "deve ser uma URL postgres",
	}
	for raw, msg := range cases {
		_, err := Load([]string{"FILA_ENV=development", "FILA_DATABASE_URL=" + raw})
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: esperava %q, veio %v", raw, msg, err)
		}
	}
}

func TestProductionRequiresTLSAndRealPassword(t *testing.T) {
	_, err := Load([]string{"FILA_ENV=production",
		"FILA_DATABASE_URL=postgres://fila:troque-esta-senha@db.interno:5432/fila?sslmode=disable"})
	if err == nil {
		t.Fatal("produção com sslmode=disable e senha de exemplo deveria falhar")
	}
	for _, msg := range []string{"sslmode=require", "senha de exemplo"} {
		if !strings.Contains(err.Error(), msg) {
			t.Errorf("faltou %q em: %s", msg, err)
		}
	}
	cfg, err := Load([]string{"FILA_ENV=production",
		"FILA_DATABASE_URL=postgres://fila:Xk29-forte@db.interno:5432/fila?sslmode=verify-full"})
	if err != nil || cfg.Env != EnvProduction {
		t.Fatalf("produção válida falhou: %v", err)
	}
}

func TestDurationAndRangeLimits(t *testing.T) {
	_, err := Load(valid("FILA_SHUTDOWN_TIMEOUT=500ms", "FILA_HTTP_IDLE_TIMEOUT=11m", "FILA_DB_CONNECT_TIMEOUT=abc"))
	for _, v := range []string{VarShutdownTimeout, VarIdleTimeout, VarDBConnectTimeout} {
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Errorf("esperava erro em %s: %v", v, err)
		}
	}
	cfg, err := Load(valid("FILA_HTTP_ADDR=:9000", "FILA_LOG_FORMAT=text", "FILA_LOG_LEVEL=debug"))
	if err != nil || cfg.HTTP.Addr != ":9000" || cfg.LogFormat != "text" || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("valores válidos rejeitados: %v", err)
	}
}

func TestSecretNeverPrints(t *testing.T) {
	s := Secret("postgres://u:muito-secreto@h/db")
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s", s, s, s, s), s.LogValue().String()} {
		if strings.Contains(out, "muito-secreto") {
			t.Fatalf("segredo vazou: %s", out)
		}
	}
	b, _ := s.MarshalText()
	if strings.Contains(string(b), "muito-secreto") {
		t.Fatal("segredo vazou em MarshalText")
	}
	cfg := Config{Database: DatabaseConfig{URL: s}}
	if strings.Contains(fmt.Sprintf("%+v", cfg), "muito-secreto") {
		t.Fatal("segredo vazou ao imprimir Config")
	}
}

func TestParseDotEnv(t *testing.T) {
	got, err := ParseDotEnv("\uFEFF# comentário\n\nexport FILA_ENV=development\nFILA_LOG_LEVEL = \"debug\"\nA='x=y'\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["FILA_ENV"] != "development" || got["FILA_LOG_LEVEL"] != "debug" || got["A"] != "x=y" {
		t.Fatalf("parse inesperado: %v", got)
	}
	_, err = ParseDotEnv("FILA_ENV=development\nlinha sem igual com segredo123\n")
	if err == nil || !strings.Contains(err.Error(), "linha 2") || strings.Contains(err.Error(), "segredo123") {
		t.Fatalf("erro deveria citar só a linha: %v", err)
	}
}

func fakeFS(files map[string]string) readFileFunc {
	return func(p string) ([]byte, error) {
		if c, ok := files[p]; ok {
			return []byte(c), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestEffectiveEnvironPrecedenceAndLookup(t *testing.T) {
	parent := filepath.Join("..", ".env")
	files := map[string]string{parent: "FILA_ENV=development\nFILA_LOG_LEVEL=debug\n"}
	env, used, err := effectiveEnviron([]string{"FILA_LOG_LEVEL=warn"}, fakeFS(files))
	if err != nil || used != parent {
		t.Fatalf("deveria usar ../.env: %v %q", err, used)
	}
	m := toMap(env)
	if m["FILA_LOG_LEVEL"] != "warn" || m["FILA_ENV"] != "development" {
		t.Fatalf("variável do processo deve vencer o arquivo: %v", m)
	}
}

func TestEffectiveEnvironIgnoresDotEnvInProduction(t *testing.T) {
	files := map[string]string{".env": "FILA_DATABASE_URL=" + devURL}
	env, used, err := effectiveEnviron([]string{"FILA_ENV=production"}, fakeFS(files))
	if err != nil || used != "" || toMap(env)["FILA_DATABASE_URL"] != "" {
		t.Fatalf("produção não pode ler .env: used=%q err=%v", used, err)
	}
}

func TestEffectiveEnvironExplicitFileMustExist(t *testing.T) {
	_, _, err := effectiveEnviron([]string{"FILA_ENV_FILE=config/inexistente.env"}, fakeFS(nil))
	if err == nil || !strings.Contains(err.Error(), "inexistente.env") {
		t.Fatalf("arquivo explícito ausente deveria falhar claramente: %v", err)
	}
}
