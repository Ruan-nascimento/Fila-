package midia

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/objstore"
)

// s3FromEnv devolve um S3Store de teste (FILA_TEST_S3_*: servidor descartável compatível com S3,
// ex.: SeaweedFS ou MinIO local) ou pula. Com FILA_REQUIRE_S3_TESTS=1 a ausência é falha.
func s3FromEnv(t *testing.T, prefix string) *S3Store {
	t.Helper()
	ep := os.Getenv("FILA_TEST_S3_ENDPOINT")
	if ep == "" {
		if os.Getenv("FILA_REQUIRE_S3_TESTS") == "1" {
			t.Fatal("FILA_TEST_S3_ENDPOINT não definida e FILA_REQUIRE_S3_TESTS=1")
		}
		t.Skip("sem servidor S3 de teste (FILA_TEST_S3_ENDPOINT)")
	}
	// Bucket descartável já criado (FILA_TEST_S3_BUCKET); cada teste usa um prefixo aleatório.
	run := "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "/"
	return NewS3Store(&objstore.Client{Endpoint: ep, Region: "auto", Bucket: os.Getenv("FILA_TEST_S3_BUCKET"),
		AccessKey: os.Getenv("FILA_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("FILA_TEST_S3_SECRET_KEY")}, run+prefix)
}

func TestS3StoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := s3FromEnv(t, "homologacao/")
	id := uuid.NewString()
	staging := "staging/" + id
	data := bytes.Repeat([]byte{0xAB}, 300_000)
	if err := s.Put(ctx, staging, data); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, staging)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("leitura diferente da gravação: err=%v len=%d", err, len(got))
	}
	v := variantKey(id, 256, "jpg")
	if err := s.Put(ctx, v, []byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, staging); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, staging); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("depois de apagar deveria ser ErrObjectNotFound: %v", err)
	}
	if err := s.Delete(ctx, staging); err != nil {
		t.Fatalf("apagar inexistente não é erro: %v", err)
	}
	if b, err := s.Get(ctx, v); err != nil || string(b) != "jpeg" {
		t.Fatalf("variante: %v", err)
	}
	for _, bad := range []string{"../etc/passwd", "staging/../x", "v/" + id + "/zz.jpg", ""} {
		if err := s.Put(ctx, bad, []byte("x")); err == nil {
			t.Fatalf("chave inválida aceita: %q", bad)
		}
	}
}

func TestS3StoreStagingListingPaginatesAndIsolatesPrefix(t *testing.T) {
	ctx := context.Background()
	s := s3FromEnv(t, "producao/")
	s.Client.PageSize = 2
	other := *s
	other.Prefix = strings.TrimSuffix(s.Prefix, "producao/") + "homologacao/"
	var keys []string
	for range 5 {
		k := "staging/" + uuid.NewString()
		keys = append(keys, k)
		if err := s.Put(ctx, k, []byte("o")); err != nil {
			t.Fatal(err)
		}
	}
	_ = other.Put(ctx, "staging/"+uuid.NewString(), []byte("o"))
	_ = s.Put(ctx, variantKey(uuid.NewString(), 64, "png"), []byte("p")) // publicada: nunca listada

	got, err := s.StagingOlderThan(ctx, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	slices.Sort(keys)
	if !slices.Equal(got, keys) {
		t.Fatalf("listagem paginada incorreta:\n got=%v\nwant=%v", got, keys)
	}
	if old, _ := s.StagingOlderThan(ctx, time.Now().Add(-time.Hour)); len(old) != 0 {
		t.Fatalf("objetos recentes não podem ser considerados órfãos: %v", old)
	}
}

func TestS3StoreWrongCredentialsFailWithoutLeaking(t *testing.T) {
	s := s3FromEnv(t, "")
	cl := *s.Client
	cl.SecretKey = "segredo-errado-nao-pode-aparecer"
	bad := S3Store{Client: &cl, Prefix: s.Prefix}
	err := bad.Put(context.Background(), "staging/"+uuid.NewString(), []byte("x"))
	if err == nil {
		t.Fatal("credencial errada deveria falhar")
	}
	if strings.Contains(err.Error(), "segredo-errado") || strings.Contains(err.Error(), s.Client.AccessKey) {
		t.Fatalf("erro expõe credencial: %v", err)
	}
}
