package api_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	"fila/internal/api"
	"fila/internal/midia"
	"fila/internal/platform/config"
	"fila/internal/platform/objstore"
)

// fakeGoogle é um provedor OpenID Connect de TESTE (só nos testes automatizados; produção recusa
// emissor diferente do oficial). Ele assina id_tokens com uma chave RSA própria, publica discovery
// e JWKS e confere o PKCE na troca do código, como o Google faz.
type fakeGoogle struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	codes  map[string]fakeGrant
	now    func() time.Time
	tokens []string // id_tokens emitidos (para conferir que não vão para log/banco)
}

type fakeGrant struct {
	challenge string
	claims    map[string]any
	// tamper: muda o id_token emitido (assinatura de outra chave, audiência errada etc.).
	tamper func(claims map[string]any) *rsa.PrivateKey
}

const googleClientID = "cliente-teste.apps.googleusercontent.com"
const googleSecret = "segredo-do-cliente-de-teste" // gitleaks:allow (não é segredo real)

func newFakeGoogle(t *testing.T, now func() time.Time) *fakeGoogle {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGoogle{t: t, key: key, codes: map[string]fakeGrant{}, now: now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss := f.srv.URL
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": iss, "authorization_endpoint": iss + "/auth",
			"token_endpoint": iss + "/token", "jwks_uri": iss + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, secret, _ := r.BasicAuth()
		if id == "" {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		f.mu.Lock()
		g, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code")) // código de uso único
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || id != googleClientID || secret != googleSecret ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		tok := f.sign(g)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "acesso-de-teste", "token_type": "Bearer", "expires_in": 3600, "id_token": tok})
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) sign(g fakeGrant) string {
	key := f.key
	claims := g.claims
	if g.tamper != nil {
		if k := g.tamper(claims); k != nil {
			key = k
		}
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(claims)
	obj, err := sig.Sign(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	s, _ := obj.CompactSerialize()
	f.mu.Lock()
	f.tokens = append(f.tokens, s)
	f.mu.Unlock()
	return s
}

// authorize simula a pessoa escolhendo a conta no Google: devolve o código que o navegador
// leva ao callback, preso ao desafio PKCE e ao nonce da URL de autorização.
func (f *fakeGoogle) authorize(authURL, sub, email string, extra map[string]any, tamper func(map[string]any) *rsa.PrivateKey) (state, code string) {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != googleClientID || q.Get("scope") != "openid email profile" {
		f.t.Fatalf("URL de autorização sem PKCE S256 ou com escopo errado: %s", authURL)
	}
	now := f.now()
	claims := map[string]any{"iss": f.srv.URL, "sub": sub, "aud": googleClientID, "azp": googleClientID,
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": q.Get("nonce"),
		"email": email, "email_verified": true, "name": "Pessoa Google"}
	for k, v := range extra {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	code = base64.RawURLEncoding.EncodeToString(b)
	f.mu.Lock()
	f.codes[code] = fakeGrant{challenge: q.Get("code_challenge"), claims: claims, tamper: tamper}
	f.mu.Unlock()
	return q.Get("state"), code
}

// newEnvFull liga Google (provedor de teste) e fotos (pasta temporária) na API.
func newEnvFull(t *testing.T) (*env, *fakeGoogle) {
	t.Helper()
	var g *fakeGoogle
	dir := t.TempDir()
	e := newEnvDeps(t, func(d *api.Deps) {
		g = newFakeGoogle(t, time.Now)
		d.Config.Google = config.GoogleConfig{ClientID: googleClientID, ClientSecret: config.Secret(googleSecret),
			RedirectURL: origin + "/v1/auth/google/callback", Issuer: g.srv.URL}
		d.GoogleHTTPClient = g.srv.Client()
		d.MediaStore = midia.FileStore{Root: dir}
		d.Config.Media = config.MediaConfig{Storage: config.MediaFilesystem, Dir: dir, UploadsPerMin: 5, UploadsPerDay: 20, Workers: 1}
		// Com um servidor S3 de teste (FILA_TEST_S3_*), as fotos usam o adaptador S3 de verdade
		// (bucket descartável, prefixo aleatório por teste) em vez da pasta.
		if ep := os.Getenv("FILA_TEST_S3_ENDPOINT"); ep != "" {
			d.MediaStore = midia.NewS3Store(&objstore.Client{Endpoint: ep, Region: "auto", Bucket: os.Getenv("FILA_TEST_S3_BUCKET"),
				AccessKey: os.Getenv("FILA_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("FILA_TEST_S3_SECRET_KEY")},
				"api-"+strings.ReplaceAll(uuid.NewString(), "-", "")+"/")
			d.Config.Media.Storage = config.MediaS3
		}
	})
	// O id_token usa o relógio real (a biblioteca confere exp com o relógio do processo).
	g.now = time.Now
	return e, g
}

// noRedirect faz o navegador simulado parar no 303 do callback (para ler o Location).
func (b *browser) noRedirect() *browser {
	b.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return b
}

// googleLogin faz o fluxo completo: start → Google (simulado) → callback. Devolve a resposta do callback.
func (b *browser) googleLogin(g *fakeGoogle, body map[string]any, sub, email string, extra map[string]any) resp {
	b.e.t.Helper()
	b.noRedirect()
	b.session()
	r := b.mustStatus(b.do("POST", "/v1/auth/google/start", body), 200)
	state, code := g.authorize(r.data()["authorization_url"].(string), sub, email, extra, nil)
	return b.do("GET", "/v1/auth/google/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil)
}

// testObjectStore devolve o cliente do bucket S3 de teste (FILA_TEST_S3_*) ou nil.
func testObjectStore() *objstore.Client {
	ep := os.Getenv("FILA_TEST_S3_ENDPOINT")
	if ep == "" {
		return nil
	}
	return &objstore.Client{Endpoint: ep, Region: "auto", Bucket: os.Getenv("FILA_TEST_S3_BUCKET"),
		AccessKey: os.Getenv("FILA_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("FILA_TEST_S3_SECRET_KEY")}
}
