package identidade

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"

	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
)

// Prova de posse da chave da instalação desktop: DPoP (RFC 9449), verificada com go-jose.
//
// Cada pedido do programa leva no cabeçalho DPoP um JWT assinado (ES256) pela chave privada da
// instalação, com a chave pública no cabeçalho "jwk". O servidor confere: assinatura, tipo
// dpop+jwt, método e URL (htm/htu), instante (iat) numa janela curta, identificador único (jti)
// nunca visto antes e, quando há token de acesso, o hash dele (ath). A impressão digital da chave
// (RFC 7638, "jkt") é o que liga prova, instalação e token.

const (
	// dpopMaxAge: prova mais velha (ou mais adiantada) que isso é recusada. O programa corrige a
	// diferença de relógio pelo cabeçalho Date das respostas do servidor.
	dpopMaxAge = 2 * time.Minute
	dpopType   = "dpop+jwt"
)

type dpopProof struct {
	JKT string
	JWK *jose.JSONWebKey
	JTI string
	IAT time.Time
}

type dpopClaims struct {
	JTI string   `json:"jti"`
	HTM string   `json:"htm"`
	HTU string   `json:"htu"`
	IAT *float64 `json:"iat"`
	ATH string   `json:"ath"`
}

func errProof(detail string) *httpserver.Error {
	return &httpserver.Error{Status: 401, Code: "desktop_proof_invalid", Title: "Programa não reconhecido",
		Detail: detail}
}

// verifyDPoP confere a prova para o método e a URL do pedido. accessToken vazio = prova sem token
// (ativação e vínculo); com token, exige ath = base64url(SHA-256(token)).
func verifyDPoP(raw, method, htu, accessToken string, now time.Time) (*dpopProof, error) {
	if raw == "" || len(raw) > 4096 {
		return nil, errProof("Este pedido não veio de uma instalação autorizada do Fila Gestão.")
	}
	jws, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(jws.Signatures) != 1 {
		return nil, errProof("Prova de instalação ilegível.")
	}
	h := jws.Signatures[0].Protected
	if typ, _ := h.ExtraHeaders[jose.HeaderType].(string); typ != dpopType {
		return nil, errProof("Prova de instalação com tipo inválido.")
	}
	jwk := h.JSONWebKey
	if jwk == nil || !jwk.IsPublic() || !jwk.Valid() {
		return nil, errProof("Prova de instalação sem chave pública.")
	}
	pub, ok := jwk.Key.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errProof("A chave da instalação precisa ser P-256.")
	}
	payload, err := jws.Verify(pub)
	if err != nil {
		return nil, errProof("Assinatura da instalação inválida.")
	}
	var c dpopClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, errProof("Prova de instalação ilegível.")
	}
	if len(c.JTI) < 16 || len(c.JTI) > 100 || c.IAT == nil {
		return nil, errProof("Prova de instalação incompleta.")
	}
	if c.HTM != method || !sameHTU(c.HTU, htu) {
		return nil, errProof("Prova de instalação feita para outro pedido.")
	}
	iat := time.Unix(int64(*c.IAT), 0)
	if iat.Before(now.Add(-dpopMaxAge)) || iat.After(now.Add(dpopMaxAge)) {
		return nil, errProof("Prova de instalação vencida. Confira a data e a hora deste computador.")
	}
	if accessToken != "" {
		sum := sha256.Sum256([]byte(accessToken))
		want := base64.RawURLEncoding.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(c.ATH), []byte(want)) != 1 {
			return nil, errProof("Prova de instalação não corresponde ao acesso apresentado.")
		}
	}
	tp, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return nil, errProof("Chave da instalação inválida.")
	}
	return &dpopProof{JKT: base64.RawURLEncoding.EncodeToString(tp), JWK: jwk, JTI: c.JTI, IAT: iat}, nil
}

// sameHTU compara as URLs sem consulta nem fragmento (RFC 9449 §4.3).
func sameHTU(got, want string) bool {
	g, err1 := url.Parse(got)
	w, err2 := url.Parse(want)
	if err1 != nil || err2 != nil {
		return false
	}
	norm := func(u *url.URL) string {
		return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.EscapedPath()
	}
	return norm(g) == norm(w)
}

// rememberProof grava o jti; uma prova repetida (mesma chave e jti) é recusada.
func rememberProof(ctx context.Context, q database.Querier, p *dpopProof, now time.Time) error {
	// Limpeza oportunista das provas vencidas desta chave (o índice por chave é a PK).
	if _, err := q.Exec(ctx, `DELETE FROM desktop_proof_seen WHERE jkt = $1 AND expires_at < $2`, p.JKT, now); err != nil {
		return fmt.Errorf("limpar provas: %w", err)
	}
	tag, err := q.Exec(ctx, `INSERT INTO desktop_proof_seen (jkt, jti, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, p.JKT, p.JTI, p.IAT.Add(2*dpopMaxAge))
	if err != nil {
		return fmt.Errorf("registrar prova: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errProof("Esta prova de instalação já foi usada.")
	}
	return nil
}

func publicJWK(p *dpopProof) ([]byte, error) {
	return json.Marshal(p.JWK.Public())
}
