package objstore

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Vetores do exemplo oficial "Signature Calculations for the Authorization Header" (AWS S3,
// Signature Version 4): mesma credencial de exemplo, mesmo instante, mesmos pedidos.
func TestSignV4MatchesAWSExamples(t *testing.T) {
	const ak, sk = "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	at := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

	get, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	get.Header.Set("Range", "bytes=0-9")
	signV4(get, nil, ak, sk, "us-east-1", at)
	want := "Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if a := get.Header.Get("Authorization"); !strings.HasSuffix(a, want) ||
		!strings.Contains(a, "SignedHeaders=host;range;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("GET Object: assinatura diferente do exemplo AWS: %s", a)
	}

	list, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/", nil)
	list.URL.RawQuery = "max-keys=2&prefix=J"
	signV4(list, nil, ak, sk, "us-east-1", at)
	want = "Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"
	if a := list.Header.Get("Authorization"); !strings.HasSuffix(a, want) {
		t.Fatalf("ListObjects: assinatura diferente do exemplo AWS: %s", a)
	}
}
