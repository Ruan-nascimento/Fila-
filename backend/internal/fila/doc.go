// Package fila é o módulo de participação na fila: entrada, ordem FIFO, chamada, tolerância, ausência, desistência e transferência.
//
// Etapa 04. Regras: R-FILA-*. Transições: T-P-01…07 (ver docs/regras.md,
// docs/matriz-transicoes.md e ADR-0019). Conclusão e interrupção (T-P-08/09) chegam na etapa 05.
//
// Concorrência: índices únicos no banco (uma participação ativa por identidade e loja; uma
// cadeira por fila), trava da fila antes da participação e trava consultiva por identidade na
// entrada. Nada depende de mutex em memória.
//
// Fronteiras (ADR-0001): dono das tabelas participation, participation_transfer e
// queue_counter; usa estabelecimento, catalogo e atendimento só pelas funções exportadas.
package fila
