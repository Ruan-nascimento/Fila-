// Package migrations embute os arquivos SQL de migração no executável.
//
// Regras para novas migrações (ver docs/adr/0016-fundacao-ferramentas-e-configuracao.md):
//   - Nome: NNNNN_descricao_curta.sql, número sequencial com 5 dígitos.
//   - Formato goose: blocos "-- +goose Up" e "-- +goose Down".
//   - Migração aplicada nunca é editada; correções entram como nova migração.
//   - Cada migração roda numa transação (padrão do goose), salvo indicação explícita.
package migrations

import "embed"

// FS contém todos os arquivos .sql desta pasta.
//
//go:embed *.sql
var FS embed.FS
