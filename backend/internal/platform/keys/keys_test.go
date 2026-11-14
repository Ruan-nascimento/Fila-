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

func TestPreviousKeysOnlyRecognize(t *testing.T) {
	old := ring(t, 1)
	rotated, err := New(bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	// Gravar usa só a chave nova.
	if !bytes.Equal(rotated.MAC(PurposeCSRF, []byte("x")), ring(t, 2).MAC(PurposeCSRF, []byte("x"))) {
		t.Fatal("MAC deve usar a chave atual")
	}
	// Procurar reconhece o que a chave antiga emitiu.
	macs := rotated.MACs(PurposeRecoveryCode, []byte("codigo"))
	if len(macs) != 2 || !bytes.Equal(macs[1], old.MAC(PurposeRecoveryCode, []byte("codigo"))) {
		t.Fatal("MACs deve incluir o MAC da chave anterior")
	}
	sealed, err := old.Seal(PurposeMail, []byte("oi"), []byte("id"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := rotated.Open(PurposeMail, sealed, []byte("id"))
	if err != nil || string(pt) != "oi" {
		t.Fatalf("conteúdo cifrado com a chave anterior deve abrir: %v", err)
	}
	// Sem a chave anterior, não abre.
	if _, err := ring(t, 2).Open(PurposeMail, sealed, []byte("id")); err == nil {
		t.Fatal("sem a chave anterior não deveria abrir")
	}
	if _, err := New(bytes.Repeat([]byte{2}, 32), make([]byte, 10)); err == nil {
		t.Fatal("chave anterior curta deve ser recusada")
	}
}
