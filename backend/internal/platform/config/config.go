// Package config carrega e valida a configuração da aplicação a partir de variáveis de ambiente.
//
// Regras:
//   - Toda variável da aplicação começa com FILA_. Variável FILA_ desconhecida é erro (pega erro de digitação).
//   - Configuração inválida devolve *ValidationError com TODOS os problemas, sem nunca incluir valores.
//   - Segredos ficam no tipo Secret, que não aparece em logs nem em fmt.
//   - O arquivo .env só é lido fora de produção (ver dotenv.go).
package config

import (
	"encoding/base64"
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
	Security  SecurityConfig
	Mail      MailConfig
	Payments  PaymentsConfig
	Ops       OpsConfig
}

// OpsConfig reúne opções de operação (etapa 12, ADR-0026).
type OpsConfig struct {
	// MetricsAddr: endereço do servidor de métricas (formato Prometheus). Vazio = desligado.
	// Nunca publicar esse endereço na internet: só a rede interna do monitoramento.
	MetricsAddr string
	// RequestsPerMinute: limite geral de requisições por IP por minuto, por processo da API
	// (primeira linha contra abuso; os limites de senha/código/cadastro ficam no banco).
	RequestsPerMinute int
	// SSEMaxConnections: conexões SSE simultâneas por processo; SSEPerIP: por IP.
	SSEMaxConnections int
	SSEPerIP          int
	// DeletionJournalDir: pasta do diário de exclusões (fora do banco), usado para reaplicar
	// exclusões depois de restaurar um backup. Vazio = diário desligado.
	DeletionJournalDir string
}

// OfficialAbacatePayHost é o único endereço aceito para o Pix em produção (nunca um simulador).
const OfficialAbacatePayHost = "api.abacatepay.com"

// PaymentsConfig configura o Pix pela AbacatePay (ADR-0007, ADR-0023). A chave só existe no
// backend. Fora de produção só chave de TESTE (abc_dev_: nada é cobrado de verdade); chave de
// produção (abc_prod_) só em produção e só depois da validação real (R-PAG-15).
type PaymentsConfig struct {
	// Pix: "disabled" (padrão: só dinheiro e cartão presencial) ou "abacatepay".
	Pix                 string
	APIKey              Secret
	BaseURL             string
	ProductionValidated bool
	// ExpiresIn é a validade de cada QR Pix (o provedor não cancela cobrança: só expira).
	ExpiresIn time.Duration
	// Timeout de cada chamada ao provedor; estourou depois de enviar = resultado desconhecido.
	Timeout time.Duration
	// WebhookSecret é o secret do webhook cadastrado no painel da AbacatePay (prova a origem do
	// aviso). Vazio = webhook desligado: só a consulta do worker confirma pagamentos (ADR-0024).
	WebhookSecret Secret
}

// Provedores de Pix.
const (
	PixDisabled   = "disabled"
	PixAbacatePay = "abacatepay"
)

// SecurityConfig reúne chave do servidor, origens permitidas e opções de cookie (ADR-0006, ADR-0017).
type SecurityConfig struct {
	// SecretKey é a chave mestra (bytes já decodificados). Vazia quando não configurada;
	// os executáveis que precisam dela chamam RequireSecretKey.
	SecretKey Secret
	// PreviousSecretKeys: chaves anteriores (troca de chave). Só RECONHECEM o que já foi emitido
	// (códigos de visitante, protocolos, e-mails na fila); nada novo é protegido com elas.
	PreviousSecretKeys []Secret
	// AllowedOrigins: origens aceitas no cabeçalho Origin de métodos não seguros.
	AllowedOrigins []string
	// PublicBaseURL: origem do frontend, usada nos links enviados por e-mail.
	PublicBaseURL string
	// CookieSecure: cookies com Secure e prefixo __Host-. Obrigatório em produção.
	CookieSecure bool
	// TrustForwardedFor: usar o último endereço de X-Forwarded-For como IP do cliente
	// (somente atrás de proxy reverso próprio que sobrescreve o cabeçalho).
	TrustForwardedFor bool
}

// MailConfig define como os e-mails são entregues.
type MailConfig struct {
	// Mode: "file" grava cada mensagem em arquivo (só desenvolvimento/teste);
	// "disabled" não envia nada (padrão em produção até haver provedor — limitação externa).
	Mode string
	Dir  string
}

// Modos de entrega de e-mail.
const (
	MailFile     = "file"
	MailDisabled = "disabled"
)

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
	VarSecretKey         = "FILA_SECRET_KEY"
	VarAllowedOrigins    = "FILA_ALLOWED_ORIGINS"
	VarPublicBaseURL     = "FILA_PUBLIC_BASE_URL"
	VarCookieSecure      = "FILA_COOKIE_SECURE"
	VarTrustForwardedFor = "FILA_TRUST_X_FORWARDED_FOR"
	VarMailMode          = "FILA_MAIL_MODE"
	VarMailDir           = "FILA_MAIL_DIR"
	VarPixProvider       = "FILA_PIX_PROVIDER"
	VarAbacateKey        = "FILA_ABACATEPAY_API_KEY"
	VarAbacateBaseURL    = "FILA_ABACATEPAY_BASE_URL"
	VarAbacateValidated  = "FILA_ABACATEPAY_PRODUCTION_VALIDATED"
	VarPixExpiresIn      = "FILA_PIX_EXPIRES_IN"
	VarAbacateTimeout    = "FILA_ABACATEPAY_TIMEOUT"
	VarAbacateWebhook    = "FILA_ABACATEPAY_WEBHOOK_SECRET"
	VarSecretKeyPrevious = "FILA_SECRET_KEY_PREVIOUS"
	VarMetricsAddr       = "FILA_METRICS_ADDR"
	VarRatePerMinute     = "FILA_HTTP_REQUESTS_PER_MINUTE"
	VarSSEMax            = "FILA_SSE_MAX_CONNECTIONS"
	VarSSEPerIP          = "FILA_SSE_MAX_PER_IP"
	VarDeletionJournal   = "FILA_DELETION_JOURNAL_DIR"
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
	VarSecretKey: true, VarAllowedOrigins: true, VarPublicBaseURL: true, VarCookieSecure: true,
	VarTrustForwardedFor: true, VarMailMode: true, VarMailDir: true,
	VarPixProvider: true, VarAbacateKey: true, VarAbacateBaseURL: true, VarAbacateValidated: true,
	VarPixExpiresIn: true, VarAbacateTimeout: true, VarAbacateWebhook: true,
	VarSecretKeyPrevious: true, VarMetricsAddr: true, VarRatePerMinute: true, VarSSEMax: true, VarSSEPerIP: true,
	VarDeletionJournal: true,
}

// FileVars são os segredos que também podem vir de arquivo: NOME_FILE=/caminho (segredos da
// plataforma montados como arquivo, ex.: Docker secrets). Nunca as duas formas juntas.
var FileVars = []string{VarDatabaseURL, VarSecretKey, VarSecretKeyPrevious, VarAbacateKey, VarAbacateWebhook}

// Origens padrão fora de produção: o servidor de desenvolvimento do Vite.
var devOrigins = []string{"http://127.0.0.1:5173", "http://localhost:5173"}

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
	prod := cfg.Env == EnvProduction
	cfg.Security = SecurityConfig{
		SecretKey:          l.secretKey(),
		PreviousSecretKeys: l.previousKeys(),
		AllowedOrigins:     l.origins(prod),
		PublicBaseURL:      l.publicBaseURL(prod),
		CookieSecure:       l.boolean(VarCookieSecure, prod),
		TrustForwardedFor:  l.boolean(VarTrustForwardedFor, false),
	}
	if prod && !cfg.Security.CookieSecure {
		l.fail(VarCookieSecure, "em produção os cookies precisam ser Secure (use true ou remova a variável)")
	}
	mailDefault := MailFile
	if prod {
		mailDefault = MailDisabled
	}
	cfg.Mail = MailConfig{
		Mode: l.oneOf(VarMailMode, mailDefault, MailFile, MailDisabled),
		Dir:  l.text(VarMailDir, ".correio"),
	}
	if prod && cfg.Mail.Mode == MailFile {
		l.fail(VarMailMode, "o modo file (arquivos locais) é só para desenvolvimento; em produção use disabled até configurar um provedor")
	}
	cfg.Payments = l.payments(prod)
	cfg.Ops = OpsConfig{
		RequestsPerMinute:  l.intRange(VarRatePerMinute, 600, 30, 100000),
		SSEMaxConnections:  l.intRange(VarSSEMax, 2000, 10, 100000),
		SSEPerIP:           l.intRange(VarSSEPerIP, 100, 1, 10000),
		DeletionJournalDir: l.text(VarDeletionJournal, ""),
	}
	if _, ok := l.get(VarMetricsAddr); ok {
		cfg.Ops.MetricsAddr = l.addr(VarMetricsAddr, "")
	}
	l.unknownVars()

	if len(l.problems) > 0 {
		sort.SliceStable(l.problems, func(i, j int) bool { return l.problems[i].Var < l.problems[j].Var })
		return nil, &ValidationError{Problems: l.problems}
	}
	return cfg, nil
}

// RequireSecretKey falha (com ValidationError) quando FILA_SECRET_KEY não foi definida.
// Usado pelos executáveis que protegem sessões, códigos e e-mails (api, worker, fila-admin).
func (c *Config) RequireSecretKey() error {
	if c.Security.SecretKey == "" {
		return &ValidationError{Problems: []Problem{{Var: VarSecretKey,
			Message: "obrigatória; gere uma com: go -C backend run ./cmd/fila-admin gerar-chave"}}}
	}
	return nil
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

func (l *loader) text(v, def string) string {
	if val, ok := l.get(v); ok {
		return val
	}
	return def
}

func (l *loader) boolean(v string, def bool) bool {
	val, ok := l.get(v)
	if !ok {
		return def
	}
	switch strings.ToLower(val) {
	case "true", "1", "sim":
		return true
	case "false", "0", "nao", "não":
		return false
	}
	l.fail(v, "use true ou false")
	return def
}

func (l *loader) secretKey() Secret {
	val, ok := l.get(VarSecretKey)
	if !ok {
		return ""
	}
	return l.decodeKey(VarSecretKey, val)
}

func (l *loader) decodeKey(v, val string) Secret {
	raw, err := decodeBase64(val)
	if err != nil {
		l.fail(v, "deve estar em base64 (gere com: go -C backend run ./cmd/fila-admin gerar-chave)")
		return ""
	}
	if len(raw) < 32 {
		l.fail(v, "curta demais: são necessários pelo menos 32 bytes aleatórios")
		return ""
	}
	return Secret(raw)
}

// previousKeys lê as chaves anteriores (separadas por vírgula, no máximo 3).
func (l *loader) previousKeys() []Secret {
	val, ok := l.get(VarSecretKeyPrevious)
	if !ok {
		return nil
	}
	var out []Secret
	for _, part := range strings.Split(val, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		if k := l.decodeKey(VarSecretKeyPrevious, part); k != "" {
			out = append(out, k)
		}
	}
	if len(out) > 3 {
		l.fail(VarSecretKeyPrevious, "no máximo 3 chaves anteriores; remova as mais antigas depois do prazo da troca")
	}
	return out
}

// KeyMaterial devolve a chave atual e as anteriores para keys.New.
func (s SecurityConfig) KeyMaterial() ([]byte, [][]byte) {
	prev := make([][]byte, 0, len(s.PreviousSecretKeys))
	for _, k := range s.PreviousSecretKeys {
		prev = append(prev, []byte(k.Reveal()))
	}
	return []byte(s.SecretKey.Reveal()), prev
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// origin valida "esquema://host[:porta]" sem caminho. Em produção exige https.
func (l *loader) origin(v, val string, prod bool) (string, bool) {
	u, err := url.Parse(val)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		l.fail(v, `cada origem deve ter o formato "https://dominio" (sem caminho no final)`)
		return "", false
	}
	if prod && u.Scheme != "https" {
		l.fail(v, "em produção use https")
		return "", false
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), true
}

func (l *loader) origins(prod bool) []string {
	val, ok := l.get(VarAllowedOrigins)
	if !ok {
		if prod {
			l.fail(VarAllowedOrigins, "obrigatória em produção (ex.: https://fila.exemplo.com.br)")
			return nil
		}
		return append([]string(nil), devOrigins...)
	}
	var out []string
	for _, part := range strings.Split(val, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		if o, ok := l.origin(VarAllowedOrigins, part, prod); ok {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		l.fail(VarAllowedOrigins, "informe pelo menos uma origem")
	}
	return out
}

func (l *loader) publicBaseURL(prod bool) string {
	val, ok := l.get(VarPublicBaseURL)
	if !ok {
		if prod {
			l.fail(VarPublicBaseURL, "obrigatória em produção (endereço público do site, usado nos links de e-mail)")
			return ""
		}
		return devOrigins[0]
	}
	o, _ := l.origin(VarPublicBaseURL, val, prod)
	return o
}

func (l *loader) unknownVars() {
	fileVar := map[string]bool{}
	for _, v := range FileVars {
		fileVar[v+"_FILE"] = true
	}
	for k := range l.env {
		if strings.HasPrefix(k, "FILA_") && !knownVars[k] && !fileVar[k] {
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

// payments lê a configuração do Pix e aplica as travas de ambiente (R-PAG-15).
func (l *loader) payments(prod bool) PaymentsConfig {
	c := PaymentsConfig{
		Pix:                 l.oneOf(VarPixProvider, PixDisabled, PixDisabled, PixAbacatePay),
		BaseURL:             l.text(VarAbacateBaseURL, "https://api.abacatepay.com/v2"),
		ProductionValidated: l.boolean(VarAbacateValidated, false),
		ExpiresIn:           l.duration(VarPixExpiresIn, 15*time.Minute, 5*time.Minute, 24*time.Hour),
		Timeout:             l.duration(VarAbacateTimeout, 10*time.Second, time.Second, time.Minute),
	}
	key, _ := l.get(VarAbacateKey)
	c.APIKey = Secret(strings.TrimSpace(key))
	hook, _ := l.get(VarAbacateWebhook)
	c.WebhookSecret = Secret(strings.TrimSpace(hook))
	if s := c.WebhookSecret.Reveal(); s != "" && (len(s) < 32 || len(s) > 256 || strings.ContainsAny(s, " \t&?#/")) {
		l.fail(VarAbacateWebhook, "use de 32 a 256 caracteres sem espaço nem &?#/ (o mesmo secret cadastrado no webhook da AbacatePay)")
	}
	if c.Pix != PixAbacatePay {
		return c
	}
	k := c.APIKey.Reveal()
	switch {
	case k == "":
		l.fail(VarAbacateKey, "obrigatória com FILA_PIX_PROVIDER=abacatepay (crie no painel da AbacatePay; nunca no código nem no frontend)")
	case strings.HasPrefix(k, "abc_dev_"):
		if prod {
			l.fail(VarAbacateKey, "em produção não se usa chave de teste (abc_dev_): os pagamentos seriam simulados")
		}
	case strings.HasPrefix(k, "abc_prod_"):
		if !prod {
			l.fail(VarAbacateKey, "chave de produção (abc_prod_) só no ambiente de produção; para testar use a chave de teste (abc_dev_)")
		} else if !c.ProductionValidated {
			l.fail(VarAbacateValidated, "a cobrança real está desligada até a validação da integração (ver docs/pagamentos-abacatepay.md); depois use true")
		}
	default:
		l.fail(VarAbacateKey, "formato não reconhecido (esperado abc_dev_... ou abc_prod_...)")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (prod && u.Scheme != "https") || (u.Scheme != "https" && u.Scheme != "http") {
		l.fail(VarAbacateBaseURL, "endereço inválido (em produção precisa ser https)")
	} else if prod && strings.ToLower(u.Hostname()) != OfficialAbacatePayHost {
		// Produção nunca fala com simulador nem com outro endereço: a integração financeira é a real.
		l.fail(VarAbacateBaseURL, "em produção só o endereço oficial da AbacatePay (https://"+OfficialAbacatePayHost+"/v2) é aceito")
	}
	return c
}
