// Package atendimento é o módulo de comanda: itens com snapshot, solicitações de alteração, conclusão, interrupção e ajustes.
//
// Implementação prevista na etapa 05. Regras: R-CMD-*. Transições: T-O-*, T-CR-*
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package atendimento
