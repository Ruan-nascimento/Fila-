// Package objstore é um cliente mínimo para armazenamento de objetos compatível com S3
// (Cloudflare R2 em produção): PUT, GET, DELETE e ListObjectsV2 com assinatura AWS SigV4,
// endereçamento por caminho (https://endpoint/bucket/chave), sem SDK.
//
// Todas as operações são idempotentes, então falhas transitórias (rede, 5xx, 429) são repetidas
// algumas vezes. Credenciais, assinaturas e corpos nunca aparecem em erro ou log.
package objstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ErrNotFound indica objeto inexistente.
var ErrNotFound = errors.New("objeto não encontrado")

// Client acessa um bucket.
type Client struct {
	Endpoint  string // ex.: https://<conta>.r2.cloudflarestorage.com
	Bucket    string
	Region    string // R2: "auto"
	AccessKey string
	SecretKey string
	HTTP      *http.Client
	// MaxBytes limita o tamanho lido de um objeto (0 = 32 MiB).
	MaxBytes int
	// Now e PageSize são trocáveis em testes.
	Now      func() time.Time
	PageSize int
}

// Error é um erro sem dados sensíveis (sem chave, assinatura nem corpo).
type Error struct {
	Op     string
	Status int
	Code   string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("armazenamento %s: HTTP %d (%s)", e.Op, e.Status, e.Code)
	}
	return fmt.Sprintf("armazenamento %s: HTTP %d", e.Op, e.Status)
}

// Object é um item de listagem.
type Object struct {
	Key          string
	LastModified time.Time
}

// Put grava o objeto (atômico no S3: leitor nunca vê objeto pela metade).
func (c *Client) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := c.do(ctx, "gravar", http.MethodPut, key, nil, data, map[string]string{"Content-Type": contentType}, http.StatusOK)
	return err
}

// Get lê o objeto inteiro (ErrNotFound se não existe).
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := c.do(ctx, "ler", http.MethodGet, key, nil, nil, nil, http.StatusOK)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	return b, err
}

// Delete apaga o objeto; inexistente não é erro.
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.do(ctx, "apagar", http.MethodDelete, key, nil, nil, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound)
	return err
}

type listResult struct {
	Contents []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

// List percorre (paginado) os objetos com o prefixo.
func (c *Client) List(ctx context.Context, prefix string, fn func(Object) error) error {
	token := ""
	size := 1000
	if c.PageSize > 0 {
		size = c.PageSize
	}
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}, "max-keys": {fmt.Sprint(size)}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		b, err := c.do(ctx, "listar", http.MethodGet, "", q, nil, nil, http.StatusOK)
		if err != nil {
			return err
		}
		var r listResult
		if err := xml.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("armazenamento listar: resposta inválida")
		}
		for _, it := range r.Contents {
			mod, _ := time.Parse(time.RFC3339, it.LastModified)
			if err := fn(Object{Key: it.Key, LastModified: mod}); err != nil {
				return err
			}
		}
		if !r.IsTruncated || r.NextContinuationToken == "" {
			return nil
		}
		token = r.NextContinuationToken
	}
}

func (c *Client) maxBytes() int {
	if c.MaxBytes > 0 {
		return c.MaxBytes
	}
	return 32 << 20
}

// do executa a operação com até 3 tentativas para falhas transitórias.
func (c *Client) do(ctx context.Context, op, method, key string, q url.Values, body []byte,
	headers map[string]string, okStatus ...int) ([]byte, error) {
	var last error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 300 * time.Millisecond):
			}
		}
		b, status, code, err := c.once(ctx, method, key, q, body, headers)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = fmt.Errorf("armazenamento %s: falha de rede", op)
			continue
		}
		for _, ok := range okStatus {
			if status == ok {
				return b, nil
			}
		}
		last = &Error{Op: op, Status: status, Code: code}
		if status != http.StatusTooManyRequests && status < 500 {
			return nil, last // 4xx (exceto 429) não melhora repetindo
		}
	}
	return nil, last
}

// Raw executa um pedido assinado sem repetição (só para preparar ambientes de teste, ex.: criar
// bucket descartável). Devolve status e código de erro do S3.
func (c *Client) Raw(ctx context.Context, method, key string) (int, string, error) {
	_, status, code, err := c.once(ctx, method, key, nil, nil, nil)
	return status, code, err
}

func (c *Client) once(ctx context.Context, method, key string, q url.Values, body []byte,
	headers map[string]string) ([]byte, int, string, error) {
	u, err := url.Parse(strings.TrimRight(c.Endpoint, "/"))
	if err != nil {
		return nil, 0, "", fmt.Errorf("endereço do armazenamento inválido")
	}
	u.Path = "/" + c.Bucket
	if key != "" {
		u.Path += "/" + key
	}
	u.RawQuery = canonicalQuery(q)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, "", fmt.Errorf("pedido inválido")
	}
	req.ContentLength = int64(len(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	signV4(req, body, c.AccessKey, c.SecretKey, c.Region, now().UTC())
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()
	limit := c.maxBytes()
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, 0, "", err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Code string `xml:"Code"`
		}
		_ = xml.Unmarshal(b, &e)
		code := e.Code
		if len(code) > 40 {
			code = code[:40]
		}
		return nil, resp.StatusCode, code, nil
	}
	if len(b) > limit {
		return nil, 0, "", fmt.Errorf("objeto maior que o limite")
	}
	return b, resp.StatusCode, "", nil
}

// ---- AWS Signature Version 4 ----

// uriEncode codifica conforme a especificação SigV4 (RFC 3986; "/" preservada quando pedido).
func uriEncode(s string, keepSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, false)+"="+uriEncode(v, false))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// signV4 assina req (todos os cabeçalhos presentes + host) com SigV4 para o serviço s3.
func signV4(req *http.Request, body []byte, accessKey, secretKey, region string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ",")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{req.Method, uriEncode(path, true), req.URL.RawQuery,
		canonHeaders.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	k := hmacSHA256([]byte("AWS4"+secretKey), day)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}
