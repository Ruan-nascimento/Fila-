// Package config carrega e valida a configuração da aplicação a partir de variáveis de ambiente.
//
// Regras:
//   - Toda variável da aplicação começa com FILA_. Variável FILA_ desconhecida é erro (pega erro de digitação).
//   - Configuração inválida devolve *ValidationError com TODOS os problemas, sem nunca incluir valores.
//   - Segredos ficam no tipo Secret, que não aparece em logs nem em fmt.
//   - O arquivo .env só é lido fora de produção (ver dotenv.go).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Environment identifica o ambiente de execução.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvTest        Environment = "test"
	EnvProduction  Environment = "production"
)

// Config é a configuração completa e já validada.
type Config struct {
	Env       Environment
	LogLevel  slog.Level
	LogFormat string // "json" ou "text"
	HTTP      HTTPConfig
	Database  DatabaseConfig
}

// HTTPConfig reúne endereço e limites de tempo do servidor HTTP.
type HTTPConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// DatabaseConfig reúne a conexão com o PostgreSQL.
type DatabaseConfig struct {
	URL            Secret
	MaxConns       int32
	ConnectTimeout time.Duration
}

// Secret guarda um valor sensível. String, GoString e LogValue nunca revelam o conteúdo.
type Secret string

const redacted = "[OCULTO]"

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Reveal devolve o valor real. Use apenas para entregar a uma biblioteca (ex.: driver do banco).
func (s Secret) Reveal() string { return string(s) }

// Problem descreve um problema em uma variável, sem expor o valor.
type Problem struct {
	Var     string
	Message string
}

// ValidationError lista todos os problemas encontrados.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString("configuração inválida (nenhum valor é exibido por segurança):")
	for _, p := range e.Problems {
		fmt.Fprintf(&b, "\n  - %s: %s", p.Var, p.Message)
	}
	b.WriteString("\nConfira o arquivo .env (modelo em .env.example) ou as variáveis de ambiente.")
	return b.String()
}

// Nomes das variáveis aceitas.
const (
	VarEnv               = "FILA_ENV"
	VarEnvFile           = "FILA_ENV_FILE"
	VarLogLevel          = "FILA_LOG_LEVEL"
	VarLogFormat         = "FILA_LOG_FORMAT"
	VarHTTPAddr          = "FILA_HTTP_ADDR"
	VarReadHeaderTimeout = "FILA_HTTP_READ_HEADER_TIMEOUT"
	VarReadTimeout       = "FILA_HTTP_READ_TIMEOUT"
	VarWriteTimeout      = "FILA_HTTP_WRITE_TIMEOUT"
	VarIdleTimeout       = "FILA_HTTP_IDLE_TIMEOUT"
	VarShutdownTimeout   = "FILA_SHUTDOWN_TIMEOUT"
	VarDatabaseURL       = "FILA_DATABASE_URL"
	VarDBMaxConns        = "FILA_DB_MAX_CONNS"
	VarDBConnectTimeout  = "FILA_DB_CONNECT_TIMEOUT"
	// VarTestDatabaseURL é lida só pelos testes de integração; aceita aqui para não ser "desconhecida".
	VarTestDatabaseURL = "FILA_TEST_DATABASE_URL"
	VarRequireDBTests  = "FILA_REQUIRE_DB_TESTS"
	// VarAPIProxyTarget é lida pelo Vite (frontend/vite.config.ts), não pelo backend.
	VarAPIProxyTarget = "FILA_API_PROXY_TARGET"
)

var knownVars = map[string]bool{
	VarEnv: true, VarEnvFile: true, VarLogLevel: true, VarLogFormat: true, VarHTTPAddr: true,
	VarReadHeaderTimeout: true, VarReadTimeout: true, VarWriteTimeout: true, VarIdleTimeout: true,
	VarShutdownTimeout: true, VarDatabaseURL: true, VarDBMaxConns: true, VarDBConnectTimeout: true,
	VarTestDatabaseURL: true, VarRequireDBTests: true, VarAPIProxyTarget: true,
}

// placeholderMarkers identifica senhas de exemplo copiadas do .env.example.
var placeholderMarkers = []string{"troque", "change-me", "changeme"}

// Load valida as variáveis em environ (formato "CHAVE=valor", como os.Environ()).
func Load(environ []string) (*Config, error) {
	env := toMap(environ)
	l := loader{env: env}

	cfg := &Config{}
	cfg.Env = l.environment()
	cfg.LogLevel = l.logLevel()
	cfg.LogFormat = l.oneOf(VarLogFormat, "json", "json", "text")
	cfg.HTTP = HTTPConfig{
		Addr:              l.addr(VarHTTPAddr, "127.0.0.1:8080"),
		ReadHeaderTimeout: l.duration(VarReadHeaderTimeout, 5*time.Second, time.Second, time.Minute),
		ReadTimeout:       l.duration(VarReadTimeout, 15*time.Second, time.Second, 5*time.Minute),
		WriteTimeout:      l.duration(VarWriteTimeout, 30*time.Second, time.Second, 5*time.Minute),
		IdleTimeout:       l.duration(VarIdleTimeout, 60*time.Second, time.Second, 10*time.Minute),
		ShutdownTimeout:   l.duration(VarShutdownTimeout, 20*time.Second, time.Second, 2*time.Minute),
	}
	cfg.Database = DatabaseConfig{
		URL:            l.databaseURL(cfg.Env),
		MaxConns:       int32(l.intRange(VarDBMaxConns, 10, 1, 200)),
		ConnectTimeout: l.duration(VarDBConnectTimeout, 5*time.Second, time.Second, time.Minute),
	}
	l.unknownVars()

	if len(l.problems) > 0 {
		sort.SliceStable(l.problems, func(i, j int) bool { return l.problems[i].Var < l.problems[j].Var })
		return nil, &ValidationError{Problems: l.problems}
	}
	return cfg, nil
}

// IsValidationError informa se err é erro de configuração (útil para escolher o código de saída).
func IsValidationError(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

type loader struct {
	env      map[string]string
	problems []Problem
}

func (l *loader) fail(v, msg string) { l.problems = append(l.problems, Problem{Var: v, Message: msg}) }

func (l *loader) get(v string) (string, bool) {
	val, ok := l.env[v]
	val = strings.TrimSpace(val)
	return val, ok && val != ""
}

func (l *loader) environment() Environment {
	v, ok := l.get(VarEnv)
	if !ok {
		l.fail(VarEnv, "obrigatória; use development, test ou production")
		return ""
	}
	switch e := Environment(v); e {
	case EnvDevelopment, EnvTest, EnvProduction:
		return e
	default:
		l.fail(VarEnv, "valor não reconhecido; use development, test ou production")
		return ""
	}
}

func (l *loader) logLevel() slog.Level {
	switch l.oneOf(VarLogLevel, "info", "debug", "info", "warn", "error") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (l *loader) oneOf(v, def string, allowed ...string) string {
	val, ok := l.get(v)
	if !ok {
		return def
	}
	for _, a := range allowed {
		if val == a {
			return val
		}
	}
	l.fail(v, "valor não reconhecido; use "+strings.Join(allowed, ", "))
	return def
}

func (l *loader) addr(v, def string) string {
	val, ok := l.get(v)
	if !ok {
		return def
	}
	_, port, err := net.SplitHostPort(val)
	if err != nil {
		l.fail(v, `formato esperado "host:porta" (ex.: 127.0.0.1:8080 ou :8080)`)
		return def
	}
	// Porta 0 = o sistema escolhe uma porta livre (usado em testes).
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		l.fail(v, "porta deve ser um número entre 0 e 65535")
		return def
	}
	return val
}

func (l *loader) duration(v string, def, min, max time.Duration) time.Duration {
	val, ok := l.get(v)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		l.fail(v, "duração inválida; use por exemplo 5s, 30s ou 2m")
		return def
	}
	if d < min || d > max {
		l.fail(v, fmt.Sprintf("deve estar entre %s e %s", min, max))
		return def
	}
	return d
}

func (l *loader) intRange(v string, def, min, max int) int {
	val, ok := l.get(v)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < min || n > max {
		l.fail(v, fmt.Sprintf("deve ser um número inteiro entre %d e %d", min, max))
		return def
	}
	return n
}

func (l *loader) databaseURL(env Environment) Secret {
	val, ok := l.get(VarDatabaseURL)
	if !ok {
		l.fail(VarDatabaseURL, "obrigatória; formato postgres://usuario:senha@host:porta/banco?sslmode=...")
		return ""
	}
	u, err := url.Parse(val)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		l.fail(VarDatabaseURL, "deve ser uma URL postgres://usuario:senha@host:porta/banco")
		return ""
	}
	if u.Hostname() == "" {
		l.fail(VarDatabaseURL, "falta o host do banco")
	}
	if strings.Trim(u.Path, "/") == "" {
		l.fail(VarDatabaseURL, "falta o nome do banco no final da URL")
	}
	if u.User == nil || u.User.Username() == "" {
		l.fail(VarDatabaseURL, "falta o usuário do banco")
	}
	sslmode := u.Query().Get("sslmode")
	if env == EnvProduction {
		switch sslmode {
		case "require", "verify-ca", "verify-full":
		default:
			l.fail(VarDatabaseURL, "em produção use sslmode=require, verify-ca ou verify-full")
		}
		if pw, _ := u.User.Password(); isPlaceholder(pw) {
			l.fail(VarDatabaseURL, "senha de exemplo não é permitida em produção")
		}
	}
	return Secret(val)
}

func (l *loader) unknownVars() {
	for k := range l.env {
		if strings.HasPrefix(k, "FILA_") && !knownVars[k] {
			l.fail(k, "variável desconhecida (erro de digitação?)")
		}
	}
}

func isPlaceholder(s string) bool {
	s = strings.ToLower(s)
	for _, m := range placeholderMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func toMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" {
			m[k] = v
		}
	}
	return m
}
