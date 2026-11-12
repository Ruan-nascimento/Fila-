# ADR-0014 — Identificadores, dinheiro e tempo

- Situação: Aceita (etapa 00)
- Data: 2026-11-12

## Contexto

Contrato e banco precisam de representações sem ambiguidade para IDs, valores e datas, compatíveis entre Go, PostgreSQL e TypeScript.

## Decisão

- IDs: UUIDv7 gerados pelo servidor, opacos para o cliente; nunca usados como prova de acesso.
- Senha da fila: letra da fila + 3 dígitos, sequência diária por fila no fuso da loja (única por fila e dia).
- Dinheiro: `Money { amount_cents: int64, currency: "BRL" }`; `bigint` no banco; sem ponto flutuante em nenhum lugar.
- Tempo: RFC 3339 UTC com `Z`; `timestamptz` no banco; o relógio do servidor/banco é a referência; relógio injetável nos testes (expirações, tolerância, reserva).
- Fuso IANA por loja só para dia operacional e exibição.

## Alternativas consideradas

- IDs sequenciais expostos: facilitam enumeração e vazam volume.
- UUIDv4: funciona, mas é pior para índices; UUIDv7 é ordenável.
- Decimal em string: exige parsing cuidadoso em todo cliente; centavos inteiros são mais simples.

## Consequências

- Formatação em reais e horário local é responsabilidade da interface.
- `number` do JavaScript é exato até 2^53 centavos; os limites de esquema ficam muito abaixo.
