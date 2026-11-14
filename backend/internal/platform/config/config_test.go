package config

import (
	"encoding/base64"
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
		"FILA_DATABASE_URL=postgres://fila:Xk29-forte@db.interno:5432/fila?sslmode=verify-full",
		"FILA_ALLOWED_ORIGINS=https://fila.exemplo.com.br", "FILA_PUBLIC_BASE_URL=https://fila.exemplo.com.br"})
	if err != nil || cfg.Env != EnvProduction {
		t.Fatalf("produção válida falhou: %v", err)
	}
	if !cfg.Security.CookieSecure || cfg.Mail.Mode != MailDisabled {
		t.Fatalf("produção deve usar cookie Secure e e-mail desativado por padrão: %+v %+v", cfg.Security, cfg.Mail)
	}
}

func TestProductionSecurityRules(t *testing.T) {
	_, err := Load([]string{"FILA_ENV=production",
		"FILA_DATABASE_URL=postgres://fila:Xk29-forte@db.interno:5432/fila?sslmode=verify-full",
		"FILA_ALLOWED_ORIGINS=http://fila.exemplo.com.br", "FILA_COOKIE_SECURE=false", "FILA_MAIL_MODE=file"})
	for _, v := range []string{VarAllowedOrigins, VarPublicBaseURL, VarCookieSecure, VarMailMode} {
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Errorf("esperava erro em %s: %v", v, err)
		}
	}
}

func TestSecurityDefaultsAndSecretKey(t *testing.T) {
	cfg, err := Load(valid())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.CookieSecure || cfg.Mail.Mode != MailFile || len(cfg.Security.AllowedOrigins) != 2 ||
		cfg.Security.PublicBaseURL != "http://127.0.0.1:5173" || cfg.Security.SecretKey != "" {
		t.Fatalf("padrões de desenvolvimento inesperados: %+v %+v", cfg.Security, cfg.Mail)
	}
	if err := cfg.RequireSecretKey(); err == nil || !strings.Contains(err.Error(), VarSecretKey) {
		t.Fatalf("chave ausente deveria ser exigida: %v", err)
	}

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	key := base64.RawURLEncoding.EncodeToString(raw)
	cfg, err = Load(valid("FILA_SECRET_KEY="+key, "FILA_ALLOWED_ORIGINS=http://localhost:5173/, https://a.b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Security.SecretKey.Reveal()) != 32 || cfg.Security.SecretKey.Reveal()[31] != 31 {
		t.Fatal("chave decodificada incorretamente")
	}
	if strings.Join(cfg.Security.AllowedOrigins, " ") != "http://localhost:5173 https://a.b" {
		t.Fatalf("origens normalizadas incorretamente: %v", cfg.Security.AllowedOrigins)
	}
	if err := cfg.RequireSecretKey(); err != nil {
		t.Fatal(err)
	}

	_, err = Load(valid("FILA_SECRET_KEY=curta", "FILA_ALLOWED_ORIGINS=https://a.b/caminho", "FILA_COOKIE_SECURE=talvez"))
	for _, v := range []string{VarSecretKey, VarAllowedOrigins, VarCookieSecure} {
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Errorf("esperava erro em %s: %v", v, err)
		}
	}
	if err != nil && strings.Contains(err.Error(), "curta=") {
		t.Fatal("valor não pode aparecer")
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

// Pix: desligado por padrão; fora de produção só chave de teste; produção só com chave real e
// validação marcada; a chave nunca aparece em mensagem de erro (R-PAG-15).
func TestPaymentsConfig(t *testing.T) {
	cfg, err := Load(valid())
	if err != nil || cfg.Payments.Pix != PixDisabled || cfg.Payments.ExpiresIn != 15*time.Minute {
		t.Fatalf("padrão: %+v %v", cfg.Payments, err)
	}
	testKey := "abc_dev_" + "fixtureSemSegredo"  // gitleaks:allow
	prodKey := "abc_prod_" + "fixtureSemSegredo" // gitleaks:allow
	cfg, err = Load(valid("FILA_PIX_PROVIDER=abacatepay", "FILA_ABACATEPAY_API_KEY="+testKey))
	if err != nil || cfg.Payments.APIKey.Reveal() != testKey || fmt.Sprint(cfg.Payments.APIKey) != "[OCULTO]" {
		t.Fatalf("chave de teste em desenvolvimento: %v", err)
	}
	prodBase := []string{"FILA_ENV=production",
		"FILA_DATABASE_URL=postgres://fila:Xk29-forte@db.interno:5432/fila?sslmode=verify-full",
		"FILA_ALLOWED_ORIGINS=https://fila.exemplo.com.br", "FILA_PUBLIC_BASE_URL=https://fila.exemplo.com.br",
		"FILA_MAIL_MODE=disabled", "FILA_PIX_PROVIDER=abacatepay"}
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"sem chave":                  {valid("FILA_PIX_PROVIDER=abacatepay"), VarAbacateKey},
		"chave de produção em dev":   {valid("FILA_PIX_PROVIDER=abacatepay", "FILA_ABACATEPAY_API_KEY="+prodKey), VarAbacateKey},
		"chave de teste em produção": {append(append([]string{}, prodBase...), "FILA_ABACATEPAY_API_KEY="+testKey), VarAbacateKey},
		"produção sem validação":     {append(append([]string{}, prodBase...), "FILA_ABACATEPAY_API_KEY="+prodKey), VarAbacateValidated},
		"formato estranho":           {valid("FILA_PIX_PROVIDER=abacatepay", "FILA_ABACATEPAY_API_KEY=sk_live_123"), VarAbacateKey},
		"validade curta demais":      {valid("FILA_PIX_EXPIRES_IN=1m"), VarPixExpiresIn},
		"secret do webhook curto":    {valid("FILA_ABACATEPAY_WEBHOOK_SECRET=curtoDemais123"), VarAbacateWebhook}, // gitleaks:allow (valor de teste curto demais de propósito)
		"produção sem https":         {append(append([]string{}, prodBase...), "FILA_ABACATEPAY_API_KEY="+prodKey, "FILA_ABACATEPAY_PRODUCTION_VALIDATED=true", "FILA_ABACATEPAY_BASE_URL=http://api.exemplo"), VarAbacateBaseURL},
	} {
		_, err := Load(c.env)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: esperava erro em %s, veio %v", name, c.want, err)
		}
		if err != nil && (strings.Contains(err.Error(), "fixtureSemSegredo") || strings.Contains(err.Error(), "sk_live_123") ||
			strings.Contains(err.Error(), "curtoDemais123")) {
			t.Errorf("%s: mensagem expôs a chave", name)
		}
	}
	cfg, err = Load(append(append([]string{}, prodBase...), "FILA_ABACATEPAY_API_KEY="+prodKey, "FILA_ABACATEPAY_PRODUCTION_VALIDATED=true"))
	if err != nil || !cfg.Payments.ProductionValidated {
		t.Fatalf("produção validada deveria carregar: %v", err)
	}
}

func TestSecretFiles(t *testing.T) {
	files := map[string]string{"/run/secrets/db": devURL + "\n", "/run/secrets/vazio": "\n"}
	read := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
	env, err := secretFiles([]string{"FILA_ENV=production", "FILA_DATABASE_URL_FILE=/run/secrets/db"}, read)
	if err != nil {
		t.Fatal(err)
	}
	if toMap(env)[VarDatabaseURL] != devURL {
		t.Fatal("o conteúdo do arquivo (sem a quebra de linha) deve virar a variável")
	}
	_, err = secretFiles([]string{"FILA_DATABASE_URL=" + devURL, "FILA_DATABASE_URL_FILE=/run/secrets/db",
		"FILA_SECRET_KEY_FILE=/nao/existe", "FILA_ABACATEPAY_API_KEY_FILE=/run/secrets/vazio"}, read)
	var v *ValidationError
	if !errors.As(err, &v) || len(v.Problems) != 3 {
		t.Fatalf("esperava 3 problemas (as duas formas, arquivo ausente, vazio): %v", err)
	}
	if strings.Contains(err.Error(), "segredo-local-123") || strings.Contains(err.Error(), "/run/secrets") {
		t.Fatal("erro não pode mostrar conteúdo nem caminho")
	}
	// _FILE é variável conhecida (não "desconhecida").
	if _, err := Load(valid("FILA_SECRET_KEY_FILE=/x")); err != nil {
		t.Fatalf("FILA_SECRET_KEY_FILE deve ser aceita: %v", err)
	}
}

func TestPreviousKeysAndOps(t *testing.T) {
	k := func(b byte) string {
		return base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(string(rune('a'+b)), 32)))
	}
	cfg, err := Load(valid("FILA_SECRET_KEY="+k(1), "FILA_SECRET_KEY_PREVIOUS="+k(2)+", "+k(3), "FILA_METRICS_ADDR=127.0.0.1:9090"))
	if err != nil {
		t.Fatal(err)
	}
	cur, prev := cfg.Security.KeyMaterial()
	if len(cur) != 32 || len(prev) != 2 || cfg.Ops.MetricsAddr != "127.0.0.1:9090" {
		t.Fatalf("chaves/opções inesperadas: %d %d %q", len(cur), len(prev), cfg.Ops.MetricsAddr)
	}
	if cfg.Ops.RequestsPerMinute != 600 || cfg.Ops.SSEMaxConnections != 2000 || cfg.Ops.SSEPerIP != 100 {
		t.Fatalf("limites padrão inesperados: %+v", cfg.Ops)
	}
	_, err = Load(valid("FILA_SECRET_KEY_PREVIOUS=curta", "FILA_METRICS_ADDR=sem-porta", "FILA_HTTP_REQUESTS_PER_MINUTE=1"))
	var v *ValidationError
	if !errors.As(err, &v) || len(v.Problems) != 3 {
		t.Fatalf("esperava 3 problemas: %v", err)
	}
}

// Cenários: C-OPS-04.
func TestProductionPixOnlyOfficialHost(t *testing.T) {
	prod := []string{"FILA_ENV=production",
		"FILA_DATABASE_URL=postgres://fila:segredo-real-1@db:5432/fila?sslmode=require",
		"FILA_ALLOWED_ORIGINS=https://fila.exemplo.com.br", "FILA_PUBLIC_BASE_URL=https://fila.exemplo.com.br",
		"FILA_PIX_PROVIDER=abacatepay", "FILA_ABACATEPAY_API_KEY=abc_prod_xyz", "FILA_ABACATEPAY_PRODUCTION_VALIDATED=true"}
	if _, err := Load(prod); err != nil {
		t.Fatalf("produção com o endereço oficial deve passar: %v", err)
	}
	for _, base := range []string{"https://pixfake.interno/v2", "https://api.abacatepay.com.evil.example/v2", "http://api.abacatepay.com/v2"} {
		_, err := Load(append(append([]string(nil), prod...), "FILA_ABACATEPAY_BASE_URL="+base))
		if err == nil || !strings.Contains(err.Error(), VarAbacateBaseURL) {
			t.Fatalf("produção deveria recusar %s: %v", base, err)
		}
	}
	// Fora de produção, o simulador local continua permitido (testes).
	if _, err := Load(valid("FILA_PIX_PROVIDER=abacatepay", "FILA_ABACATEPAY_API_KEY=abc_dev_x", "FILA_ABACATEPAY_BASE_URL=http://127.0.0.1:18090/v2")); err != nil {
		t.Fatalf("simulador fora de produção deve passar: %v", err)
	}
}

// Homologação: travas de produção (https, cookies Secure, banco com TLS), mas Pix só com chave de
// teste e simulador/sandbox permitidos; e-mail em arquivo permitido (sem provedor de e-mail).
// Cenários: C-OPS-04.
func TestStagingIsHardenedButNeverCharges(t *testing.T) {
	base := []string{"FILA_ENV=homologacao",
		"FILA_DATABASE_URL=postgres://fila:segredo-real-1@db:5432/fila?sslmode=require",
		"FILA_ALLOWED_ORIGINS=https://homolog.exemplo.com.br", "FILA_PUBLIC_BASE_URL=https://homolog.exemplo.com.br",
		"FILA_MAIL_MODE=file", "FILA_PIX_PROVIDER=abacatepay", "FILA_ABACATEPAY_API_KEY=abc_dev_x",
		"FILA_ABACATEPAY_BASE_URL=http://pixfake:18090/v2"}
	cfg, err := Load(base)
	if err != nil || !cfg.Security.CookieSecure || !cfg.Env.Hardened() {
		t.Fatalf("homologação válida: %v", err)
	}
	for _, bad := range [][]string{
		{"FILA_ABACATEPAY_API_KEY=abc_prod_x"},
		{"FILA_DATABASE_URL=postgres://fila:segredo-real-1@db:5432/fila?sslmode=disable"},
		{"FILA_ALLOWED_ORIGINS=http://homolog.exemplo.com.br"},
		{"FILA_COOKIE_SECURE=false"},
	} {
		env := append(append([]string(nil), base...), bad...)
		if _, err := Load(env); err == nil {
			t.Errorf("homologação deveria recusar %v", bad)
		}
	}
	if environ, _, _ := effectiveEnviron([]string{"FILA_ENV=homologacao"}, func(string) ([]byte, error) {
		return []byte("FILA_SECRET_KEY=do-arquivo"), nil
	}); len(environ) != 1 {
		t.Fatal("homologação ignora .env")
	}
}

// Variáveis da implantação (terminal do operador) não são erro; as da aplicação continuam vigiadas.
func TestDeployVarsAreIgnored(t *testing.T) {
	if _, err := Load(valid("FILA_IMAGEM_BACKEND=fila-backend:v2", "FILA_SEGREDOS=/etc/fila/segredos")); err != nil {
		t.Fatalf("variáveis de implantação deveriam ser ignoradas: %v", err)
	}
	if _, err := Load(valid("FILA_IMAGEM_BACKNED=x")); err == nil {
		t.Fatal("erro de digitação continua sendo acusado")
	}
}
