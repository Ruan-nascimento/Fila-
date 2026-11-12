# ADR-0009 — Idempotência por ator/operação e concorrência otimista

- Situação: Aceita (etapa 00)
- Data: 2026-11-12

## Contexto

Rede móvel falha, botões recebem duplo clique, duas pessoas olham a mesma fila. O sistema não pode presumir entrega única nem tela atualizada.

## Decisão

- Cabeçalho `Idempotency-Key` obrigatório nos comandos (exceções em `contrato/convencoes.md` §8). Escopo = ator + operação; guarda impressão digital do pedido e a resposta por 24 h; mesma chave com corpo diferente → 422; em execução → 409.
- A chave é gravada **na mesma transação** do efeito (não há janela entre efeito e registro da chave).
- Agregados têm `version`; comandos enviam `expected_version`; diferença → `409 version_conflict` com `current`.
- Invariantes que não podem depender do cliente usar a chave certa ficam em restrições do banco (ADR-0002).
- Escolhas feitas pelo servidor (próximo da fila, unidade de prêmio) usam bloqueio de linha.
- Efeitos derivados (crédito de pontos, reversão, liberação de reserva) têm chave única de origem (ÚNICA-VEZ).

## Alternativas consideradas

- Mutex local: não funciona com vários processos.
- Última escrita vence: proibido pelo anexo (sobrescreve alteração alheia).

## Consequências

- O frontend gera a chave por intenção e a reutiliza nos reenvios.
- Respostas com segredo exibido uma vez não são armazenadas (rotas fora da regra).
