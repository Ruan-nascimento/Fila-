package identidade

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"fila/internal/platform/httpserver"
)

// PasswordParams são os parâmetros do Argon2id (RFC 9106). Ver ADR-0017.
type PasswordParams struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
	SaltLen   uint32
	KeyLen    uint32
}

// DefaultPasswordParams segue a configuração mínima recomendada pela OWASP (Password Storage
// Cheat Sheet, consultada em 12/11/2026): 19 MiB de memória, 2 iterações, paralelismo 1.
var DefaultPasswordParams = PasswordParams{MemoryKiB: 19 * 1024, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}

// Limites de tamanho de senha do contrato (caracteres Unicode).
const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
)

// Hasher calcula e confere hashes de senha. Limita quantos hashes rodam ao mesmo tempo,
// porque cada um usa ~19 MiB de memória (uma avalanche de logins não esgota o servidor).
type Hasher struct {
	p     PasswordParams
	slots chan struct{}
	dummy string
}

// NewHasher cria o hasher. maxConcurrent < 1 vira 1.
func NewHasher(p PasswordParams, maxConcurrent int) *Hasher {
	h := &Hasher{p: p, slots: make(chan struct{}, max(maxConcurrent, 1))}
	// Hash de referência para equalizar o tempo quando a conta não existe.
	d, err := h.hashWithSalt("senha-que-ninguem-usa", make([]byte, p.SaltLen))
	if err != nil {
		panic(err)
	}
	h.dummy = d
	return h
}

// normalizePassword aplica NFKC: o mesmo texto digitado em teclados diferentes vira os mesmos bytes.
func normalizePassword(pw string) string { return norm.NFKC.String(pw) }

// ValidateNewPassword confere o tamanho da senha nova (sem regras de composição; ver ADR-0017).
func ValidateNewPassword(field, pw string) *httpserver.FieldError {
	if !utf8.ValidString(pw) {
		return &httpserver.FieldError{Field: field, Code: httpserver.FieldInvalidFormat}
	}
	n := utf8.RuneCountInString(normalizePassword(pw))
	switch {
	case n == 0:
		return &httpserver.FieldError{Field: field, Code: httpserver.FieldRequired}
	case n < MinPasswordLen:
		return &httpserver.FieldError{Field: field, Code: httpserver.FieldTooShort, Message: fmt.Sprintf("use pelo menos %d caracteres", MinPasswordLen)}
	case n > MaxPasswordLen:
		return &httpserver.FieldError{Field: field, Code: httpserver.FieldTooLong}
	case strings.TrimSpace(pw) == "":
		return &httpserver.FieldError{Field: field, Code: httpserver.FieldNotAllowed}
	}
	return nil
}

func (h *Hasher) acquire(ctx context.Context) (func(), error) {
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Hash devolve o hash no formato PHC: $argon2id$v=19$m=...,t=...,p=...$sal$hash.
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, h.p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return h.hashWithSalt(pw, salt)
}

func (h *Hasher) hashWithSalt(pw string, salt []byte) (string, error) {
	key := argon2.IDKey([]byte(normalizePassword(pw)), salt, h.p.Time, h.p.MemoryKiB, h.p.Threads, h.p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.p.MemoryKiB, h.p.Time, h.p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("hash de senha em formato desconhecido")

// Verify confere a senha. needsRehash indica que o hash usa parâmetros antigos e deve ser
// recalculado com os atuais (feito no login bem-sucedido).
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	p, salt, want, err := parsePHC(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(normalizePassword(pw)), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(want)))
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = p.MemoryKiB != h.p.MemoryKiB || p.Time != h.p.Time || p.Threads != h.p.Threads || uint32(len(want)) != h.p.KeyLen
	return ok, needsRehash, nil
}

// Burn gasta o mesmo tempo de uma verificação real, sem conta (não revela se o e-mail existe).
func (h *Hasher) Burn(ctx context.Context, pw string) {
	_, _, _ = h.Verify(ctx, pw, h.dummy)
}

func parsePHC(s string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, errBadHash
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return PasswordParams{}, nil, nil, errBadHash
	}
	var p PasswordParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Time, &p.Threads); err != nil ||
		p.MemoryKiB == 0 || p.Time == 0 || p.Threads == 0 {
		return PasswordParams{}, nil, nil, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, errBadHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return PasswordParams{}, nil, nil, errBadHash
	}
	return p, salt, key, nil
}
