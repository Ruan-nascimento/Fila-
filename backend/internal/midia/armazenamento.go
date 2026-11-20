package midia

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Storage é o armazenamento de objetos das imagens. A interface é pequena de propósito: um
// adaptador de nuvem (S3 compatível) pode ser acrescentado sem mudar o resto do módulo.
type Storage interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	// StagingOlderThan lista originais em staging gravados antes de t (limpeza de órfãos: o
	// arquivo foi gravado, mas a transação que criaria o registro não confirmou).
	StagingOlderThan(ctx context.Context, t time.Time) ([]string, error)
}

// ErrObjectNotFound indica chave inexistente no armazenamento.
var ErrObjectNotFound = errors.New("objeto não encontrado no armazenamento")

// Chaves válidas: sempre geradas aqui (aleatórias), nunca com nome, e-mail ou nome de arquivo
// enviado pela pessoa. A expressão também impede caminho relativo ("..") ou absoluto.
var keyPattern = regexp.MustCompile(`^(staging/[0-9a-f-]{36}|v/[0-9a-f-]{36}/[0-9a-f]{16}-[0-9]{2,3}\.(jpg|png))$`)

// maxObjectBytes limita a leitura de um objeto (o maior é o original em staging).
const maxObjectBytes = MaxUploadBytes + 1

// FileStore guarda os objetos numa pasta persistente e não pública (desenvolvimento ou volume
// persistente em produção, compartilhado por API e worker e incluído no backup).
type FileStore struct {
	Root string
}

func (f FileStore) path(key string) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", fmt.Errorf("chave de armazenamento inválida")
	}
	return filepath.Join(f.Root, filepath.FromSlash(key)), nil
}

// Put grava de forma atômica (arquivo temporário + rename): leitor nunca vê arquivo pela metade.
func (f FileStore) Put(_ context.Context, key string, data []byte) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if serr := tmp.Sync(); werr == nil {
		werr = serr
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(name)
		return werr
	}
	if err := os.Chmod(name, 0o640); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, p)
}

// Get lê o objeto inteiro (com limite de tamanho).
func (f FileStore) Get(_ context.Context, key string) ([]byte, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	fh, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, maxObjectBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxObjectBytes {
		return nil, fmt.Errorf("objeto maior que o limite")
	}
	return b, nil
}

// Delete apaga o objeto; inexistente não é erro (limpeza repetível).
func (f FileStore) Delete(_ context.Context, key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.HasPrefix(key, "v/") {
		_ = os.Remove(filepath.Dir(p)) // pasta da imagem vazia (falha se ainda houver variantes)
	}
	return nil
}

// StagingOlderThan lista os originais em staging modificados antes de t.
func (f FileStore) StagingOlderThan(_ context.Context, t time.Time) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(f.Root, "staging"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		key := "staging/" + e.Name()
		if e.IsDir() || !keyPattern.MatchString(key) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(t) {
			out = append(out, key)
		}
	}
	return out, nil
}

// variantKey gera a chave de uma variante: pasta do registro + sufixo aleatório (uma nova
// tentativa nunca reaproveita a chave de uma tentativa anterior que falhou no meio).
func variantKey(assetID string, size int, ext string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("v/%s/%s-%d.%s", assetID, hex.EncodeToString(b[:]), size, ext)
}
