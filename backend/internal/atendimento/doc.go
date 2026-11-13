// Package atendimento é o módulo de comanda: itens com snapshot, solicitações de alteração, conclusão, interrupção e ajustes.
//
// Etapa 04: comanda criada na entrada da fila (T-O-01) e encerrada sem cobrança em desistência,
// cancelamento ou ausência (T-O-05). Etapa 05: edição de itens, solicitações, conclusão e
// interrupção. Regras: R-CMD-*. Transições: T-O-*, T-CR-* (ver docs/regras.md e
// docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001, ADR-0020): dono das tabelas service_order, order_item, change_request,
// change_request_addition e order_adjustment; outros módulos usam suas funções exportadas, nunca
// suas tabelas diretamente. Participação e fila são pedidas ao módulo fila pela interface Visits,
// na ordem de travas fila → participação → comanda. A plataforma não importa módulos.
package atendimento
