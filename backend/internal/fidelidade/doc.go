// Package fidelidade é o módulo de programa de pontos, razão de lançamentos imutáveis, prêmios e reservas.
//
// Implementação prevista na etapa 10. Regras: R-FID-*. Transições: T-LP-*, T-LG-*, T-RW-*, T-RD-*
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package fidelidade
