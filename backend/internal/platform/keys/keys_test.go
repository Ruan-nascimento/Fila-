package keys

import (
	"bytes"
	"testing"
)

func ring(t *testing.T, b byte) *Ring {
	t.Helper()
	r, err := New(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestShortMasterRejected(t *testing.T) {
	if _, err := New(make([]byte, 31)); err == nil {
		t.Fatal("chave de 31 bytes deveria ser recusada")
	}
}

func TestSubkeysAreIndependent(t *testing.T) {
	r := ring(t, 1)
	if bytes.Equal(r.Derive(PurposeCSRF), r.Derive(PurposeMail)) {
		t.Fatal("finalidades diferentes devem gerar subchaves diferentes")
	}
	if !bytes.Equal(r.Derive(PurposeCSRF), ring(t, 1).Derive(PurposeCSRF)) {
		t.Fatal("mesma chave mestra deve gerar a mesma subchave")
	}
	if bytes.Equal(r.Derive(PurposeCSRF), ring(t, 2).Derive(PurposeCSRF)) {
		t.Fatal("chaves mestras diferentes devem gerar subchaves diferentes")
	}
}

func TestMACIsUnambiguous(t *testing.T) {
	r := ring(t, 1)
	if bytes.Equal(r.MAC(PurposeCSRF, []byte("ab"), []byte("c")), r.MAC(PurposeCSRF, []byte("a"), []byte("bc"))) {
		t.Fatal("partes diferentes não podem colidir")
	}
}

func TestSealOpen(t *testing.T) {
	r := ring(t, 1)
	sealed, err := r.Seal(PurposeMail, []byte("conteúdo"), []byte("id-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("conteúdo")) {
		t.Fatal("texto não pode aparecer cifrado")
	}
	pt, err := r.Open(PurposeMail, sealed, []byte("id-1"))
	if err != nil || string(pt) != "conteúdo" {
		t.Fatalf("Open = %q, %v", pt, err)
	}
	if _, err := r.Open(PurposeMail, sealed, []byte("id-2")); err == nil {
		t.Fatal("aad diferente deve falhar")
	}
	if _, err := ring(t, 2).Open(PurposeMail, sealed, []byte("id-1")); err == nil {
		t.Fatal("outra chave deve falhar")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := r.Open(PurposeMail, sealed, []byte("id-1")); err == nil {
		t.Fatal("conteúdo adulterado deve falhar")
	}
}
