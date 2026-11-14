# ADR-0012 — Razão de pontos com lançamentos imutáveis

- Situação: Aceita (etapa 00)
- Data: 2026-11-12

## Contexto

O saldo precisa ser explicável, resistir a eventos repetidos e permitir saldo negativo após estorno sem criar dívida.

## Decisão

- Tabela de lançamentos por conta + loja, só inserção: `credit`, `credit_reversal`, `hold`, `hold_release`, `consume`, `consume_reversal`, `account_closure`.
- Cada lançamento tem `balance_delta` e `held_delta`. Saldo = Σ`balance_delta`; retido = Σ`held_delta`; disponível = saldo − retido.
- Chave única de origem (ex.: `(order_id, 'credit')`, `(reservation_id, 'hold_release')`) garante "uma única vez".
- Saldo materializado opcional para leitura rápida, sempre reconstruível pela soma (teste de consistência).
- Reserva trava com bloqueio de linha no prêmio (estoque) e na conta/loja (disponível) na mesma transação.

## Alternativas consideradas

- Só um campo "saldo": rejeitado (não explica, difícil de corrigir).

## Consequências

- Correções são novos lançamentos, nunca edição.
- Saldo negativo bloqueia apenas novas reservas.
- Atualização (etapa 10): implementado como no ADR-0025 (saldo materializado por gatilho, estoque por movimentos).
