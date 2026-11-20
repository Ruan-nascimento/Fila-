package config

import (
	"strings"
	"testing"
)

var prodBase = []string{"FILA_ENV=production",
	"FILA_DATABASE_URL=postgres://fila:Xk29-forte@db.interno:5432/fila?sslmode=verify-full",
	"FILA_ALLOWED_ORIGINS=https://fila.exemplo.com.br", "FILA_PUBLIC_BASE_URL=https://fila.exemplo.com.br"}

// Google e fotos ficam desligados sem configuração; o redirect vem da origem pública fixa.
func TestGoogleAndMediaDefaults(t *testing.T) {
	cfg, err := Load(valid())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Google.Enabled() || cfg.Media.Enabled() {
		t.Fatalf("Google/fotos ligados sem configuração: %+v %+v", cfg.Google, cfg.Media)
	}
	cfg, err = Load(valid("FILA_GOOGLE_CLIENT_ID=abc.apps.googleusercontent.com", "FILA_GOOGLE_CLIENT_SECRET=s3cr3t",
		"FILA_MEDIA_STORAGE=filesystem"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Google.Enabled() || cfg.Google.Issuer != GoogleIssuer ||
		cfg.Google.RedirectURL != "http://127.0.0.1:5173/v1/auth/google/callback" {
		t.Fatalf("Google: %+v", cfg.Google)
	}
	if !cfg.Media.Enabled() || cfg.Media.Dir != ".midia" || cfg.Media.UploadsPerMin != 5 || cfg.Media.UploadsPerDay != 20 {
		t.Fatalf("fotos: %+v", cfg.Media)
	}
	if strings.Contains(cfg.Google.ClientSecret.String(), "s3cr3t") {
		t.Fatal("secret exposto")
	}
}

func TestGoogleAndMediaValidation(t *testing.T) {
	cases := map[string]struct {
		env  []string
		want string
	}{
		"só o client id":             {valid("FILA_GOOGLE_CLIENT_ID=abc"), "FILA_GOOGLE_CLIENT_ID"},
		"redirect com outro path":    {valid("FILA_GOOGLE_CLIENT_ID=abc", "FILA_GOOGLE_CLIENT_SECRET=x", "FILA_GOOGLE_REDIRECT_URL=http://127.0.0.1:5173/outro"), "FILA_GOOGLE_REDIRECT_URL"},
		"redirect com parâmetros":    {valid("FILA_GOOGLE_CLIENT_ID=abc", "FILA_GOOGLE_CLIENT_SECRET=x", "FILA_GOOGLE_REDIRECT_URL=http://127.0.0.1:5173/v1/auth/google/callback?x=1"), "FILA_GOOGLE_REDIRECT_URL"},
		"emissor falso em produção":  {append(append([]string{}, prodBase...), "FILA_GOOGLE_ISSUER=https://falso.exemplo"), "FILA_GOOGLE_ISSUER"},
		"redirect http em produção":  {append(append([]string{}, prodBase...), "FILA_GOOGLE_CLIENT_ID=a", "FILA_GOOGLE_CLIENT_SECRET=b", "FILA_GOOGLE_REDIRECT_URL=http://fila.exemplo.com.br/v1/auth/google/callback"), "FILA_GOOGLE_REDIRECT_URL"},
		"pasta de fotos em produção": {append(append([]string{}, prodBase...), "FILA_MEDIA_STORAGE=filesystem"), "FILA_MEDIA_DIR"},
		"armazenamento desconhecido": {valid("FILA_MEDIA_STORAGE=s3"), "FILA_MEDIA_STORAGE"},
		"limite fora da faixa":       {valid("FILA_MEDIA_UPLOADS_PER_MINUTE=0"), "FILA_MEDIA_UPLOADS_PER_MINUTE"},
	}
	for name, c := range cases {
		_, err := Load(c.env)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: esperava erro em %s, veio %v", name, c.want, err)
		}
	}
	cfg, err := Load(append(append([]string{}, prodBase...), "FILA_GOOGLE_CLIENT_ID=a", "FILA_GOOGLE_CLIENT_SECRET=b",
		"FILA_MEDIA_STORAGE=filesystem", "FILA_MEDIA_DIR=/var/lib/fila/midia"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Google.RedirectURL != "https://fila.exemplo.com.br/v1/auth/google/callback" || cfg.Google.Issuer != GoogleIssuer {
		t.Fatalf("produção: %+v", cfg.Google)
	}
}
