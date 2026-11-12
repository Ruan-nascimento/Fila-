// Package auditoria é o módulo de registro de auditoria das transições relevantes, sem dados pessoais.
//
// Implementação prevista na etapa 02 em diante (transversal). Regras: R-SEG-10. Transições: coluna Auditoria da matriz
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package auditoria
