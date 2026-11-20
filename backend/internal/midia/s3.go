package midia

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fila/internal/platform/objstore"
)

// S3Store guarda os objetos num bucket PRIVADO compatível com S3 (Cloudflare R2 em produção).
// API e worker do mesmo ambiente apontam para o mesmo bucket/prefixo; nenhum objeto é público:
// a leitura continua passando pelas rotas da API, que conferem a autorização (ADR-0028).
type S3Store struct {
	Client *objstore.Client
	// Prefix separa ambientes/finalidades dentro de um bucket (ex.: "producao/"). Vazio = raiz.
	Prefix string
}

// NewS3Store cria o armazenamento com o limite de leitura das imagens.
func NewS3Store(c *objstore.Client, prefix string) *S3Store {
	c.MaxBytes = maxObjectBytes
	return &S3Store{Client: c, Prefix: prefix}
}

func (s *S3Store) objectKey(key string) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", fmt.Errorf("chave de armazenamento inválida")
	}
	return s.Prefix + key, nil
}

// Put grava o objeto.
func (s *S3Store) Put(ctx context.Context, key string, data []byte) error {
	k, err := s.objectKey(key)
	if err != nil {
		return err
	}
	ct := "application/octet-stream"
	switch {
	case strings.HasSuffix(k, ".jpg"):
		ct = "image/jpeg"
	case strings.HasSuffix(k, ".png"):
		ct = "image/png"
	}
	return s.Client.Put(ctx, k, data, ct)
}

// Get lê o objeto inteiro (com limite de tamanho).
func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	k, err := s.objectKey(key)
	if err != nil {
		return nil, err
	}
	b, err := s.Client.Get(ctx, k)
	if errors.Is(err, objstore.ErrNotFound) {
		return nil, ErrObjectNotFound
	}
	return b, err
}

// Delete apaga o objeto; inexistente não é erro (limpeza repetível).
func (s *S3Store) Delete(ctx context.Context, key string) error {
	k, err := s.objectKey(key)
	if err != nil {
		return err
	}
	return s.Client.Delete(ctx, k)
}

// StagingOlderThan lista (paginado) os originais em staging gravados antes de t. Só olha
// staging/: imagens publicadas nunca são apagadas por idade, só quando o registro deixa de usá-las.
func (s *S3Store) StagingOlderThan(ctx context.Context, t time.Time) ([]string, error) {
	var out []string
	err := s.Client.List(ctx, s.Prefix+"staging/", func(o objstore.Object) error {
		key := strings.TrimPrefix(o.Key, s.Prefix)
		if keyPattern.MatchString(key) && strings.HasPrefix(key, "staging/") && !o.LastModified.IsZero() && o.LastModified.Before(t) {
			out = append(out, key)
		}
		return nil
	})
	return out, err
}
