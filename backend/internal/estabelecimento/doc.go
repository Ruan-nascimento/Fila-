// Package estabelecimento é o módulo de loja, profissionais, vínculos de equipe e estado das filas (aberta, pausada, fechada).
//
// Implementação prevista na etapa 03. Regras: R-FILA-01, R-FILA-06…08, R-PERM-*. Transições: T-ES-*, T-PR-*, T-Q-*
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package estabelecimento
