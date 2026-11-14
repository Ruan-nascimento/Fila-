// Package keys deriva chaves de uso específico a partir da chave secreta do servidor
// (FILA_SECRET_KEY) e oferece HMAC e criptografia autenticada com elas.
//
// Uma chave mestra atual é configurada; cada finalidade recebe uma subchave própria via
// HKDF-SHA256 (RFC 5869), para que o vazamento ou mau uso de uma finalidade não afete as outras.
// Ver ADR-0017.
//
// Troca de chave (ADR-0026): chaves ANTERIORES podem ser informadas. Tudo o que é novo usa só a
// chave atual; as anteriores servem apenas para reconhecer o que já foi emitido (códigos de
// visitante, protocolos de exclusão, e-mails na fila, CSRF de sessões abertas) até expirar.
package keys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// MinMasterLen é o tamanho mínimo da chave mestra (256 bits).
const MinMasterLen = 32

// Finalidades conhecidas. Mudar o texto invalida o que foi protegido com a subchave antiga.
const (
	PurposeCSRF         = "csrf/v1"
	PurposeRecoveryCode = "visitante-codigo/v1"
	PurposeRateLimit    = "limite-tentativas/v1"
	PurposeMail         = "correio/v1"
)

// Ring guarda a chave mestra e as subchaves já derivadas.
type Ring struct {
	master   []byte
	mu       sync.Mutex
	cache    map[string][]byte
	previous []*Ring
}

// New valida a chave mestra atual e as anteriores (opcionais, só para reconhecer o já emitido).
func New(master []byte, previous ...[]byte) (*Ring, error) {
	r, err := single(master)
	if err != nil {
		return nil, err
	}
	for _, p := range previous {
		old, err := single(p)
		if err != nil {
			return nil, fmt.Errorf("chave anterior: %w", err)
		}
		r.previous = append(r.previous, old)
	}
	return r, nil
}

// NewRotating é New com as chaves anteriores numa lista (config.SecurityConfig.KeyMaterial).
func NewRotating(master []byte, previous [][]byte) (*Ring, error) { return New(master, previous...) }

func single(master []byte) (*Ring, error) {
	if len(master) < MinMasterLen {
		return nil, fmt.Errorf("chave secreta curta: são necessários pelo menos %d bytes", MinMasterLen)
	}
	m := make([]byte, len(master))
	copy(m, master)
	return &Ring{master: m, cache: map[string][]byte{}}, nil
}

// MACs devolve o MAC com a chave atual seguido dos MACs com as chaves anteriores. Use para
// PROCURAR algo emitido antes de uma troca de chave (ex.: WHERE hmac = ANY($1)); para gravar,
// use sempre MAC (só a chave atual).
func (r *Ring) MACs(purpose string, parts ...[]byte) [][]byte {
	out := [][]byte{r.MAC(purpose, parts...)}
	for _, p := range r.previous {
		out = append(out, p.MAC(purpose, parts...))
	}
	return out
}

// Derive devolve a subchave de 32 bytes da finalidade.
func (r *Ring) Derive(purpose string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.cache[purpose]; ok {
		return k
	}
	k, err := hkdf.Key(sha256.New, r.master, nil, "fila/"+purpose, 32)
	if err != nil { // só acontece com parâmetros inválidos, que aqui são constantes
		panic("hkdf: " + err.Error())
	}
	r.cache[purpose] = k
	return k
}

// MAC calcula HMAC-SHA256 das partes com a subchave da finalidade. Cada parte é prefixada pelo
// seu tamanho, então ("ab","c") e ("a","bc") nunca colidem.
func (r *Ring) MAC(purpose string, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, r.Derive(purpose))
	var n [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		m.Write(n[:])
		m.Write(p)
	}
	return m.Sum(nil)
}

// ErrDecrypt indica conteúdo adulterado ou cifrado com outra chave.
var ErrDecrypt = errors.New("não foi possível decifrar: conteúdo alterado ou chave diferente")

// Seal cifra com AES-256-GCM (nonce aleatório no início). aad é autenticado, mas não cifrado.
func (r *Ring) Seal(purpose string, plaintext, aad []byte) ([]byte, error) {
	gcm, err := r.gcm(purpose)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize(), gcm.NonceSize()+len(plaintext)+gcm.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// Open decifra o que Seal produziu (com a chave atual ou, depois de uma troca, com uma anterior).
func (r *Ring) Open(purpose string, sealed, aad []byte) ([]byte, error) {
	pt, err := r.open(purpose, sealed, aad)
	for _, p := range r.previous {
		if err == nil {
			break
		}
		pt, err = p.open(purpose, sealed, aad)
	}
	return pt, err
}

func (r *Ring) open(purpose string, sealed, aad []byte) ([]byte, error) {
	gcm, err := r.gcm(purpose)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrDecrypt
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func (r *Ring) gcm(purpose string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(r.Derive(purpose))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
