package identidade

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// newToken gera um segredo aleatório de 256 bits (sessão, verificação de e-mail, redefinição de
// senha, convite) e devolve o texto para o cliente e o SHA-256 para o banco. SHA-256 sem sal é
// suficiente porque o segredo tem 256 bits de entropia (não dá para adivinhar por dicionário).
func newToken() (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error()) // nunca acontece em sistemas suportados
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token)
}

// hashToken calcula o SHA-256 do texto recebido.
func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Alfabeto base32 de Crockford: sem I, L, O e U (evita confusão ao ler e digitar).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRecoveryCode gera o código de recuperação do visitante: 80 bits em 16 caracteres,
// no formato XXXX-XXXX-XXXX-XXXX (ADR-0011).
func newRecoveryCode() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	var out strings.Builder
	var acc uint64
	bits := 0
	n := 0
	for _, c := range b {
		acc = acc<<8 | uint64(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			if n > 0 && n%4 == 0 {
				out.WriteByte('-')
			}
			out.WriteByte(crockford[(acc>>uint(bits))&31])
			n++
		}
	}
	return out.String()
}

// normalizeRecoveryCode aceita o código com ou sem hífens/espaços, em maiúsculas ou minúsculas,
// e corrige as trocas comuns de leitura (O→0, I/L→1). Devolve os 16 caracteres ou false.
func normalizeRecoveryCode(s string) (string, bool) {
	var out strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case '-', ' ':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
		out.WriteRune(r)
	}
	if out.Len() != 16 {
		return "", false
	}
	return out.String(), true
}
