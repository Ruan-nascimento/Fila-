// Package fila é o módulo de participação na fila: entrada, ordem FIFO, chamada, tolerância, ausência, desistência e transferência.
//
// Implementação prevista na etapa 04. Regras: R-FILA-*. Transições: T-P-*
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package fila
