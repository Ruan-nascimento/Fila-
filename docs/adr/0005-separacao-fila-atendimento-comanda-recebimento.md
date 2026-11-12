# ADR-0005 — Separar fila, atendimento, comanda, recebimento e conferência

- Situação: Aceita (decisão 05 do anexo)
- Data: 2026-11-12

## Contexto

Terminar o corte libera a cadeira, mas o dinheiro pode chegar depois, chegar duplicado, ou nem chegar. Misturar esses estados faria a cadeira ficar presa ou o pagamento parecer confirmado sem estar.

## Decisão

- Máquinas de estado independentes: fila do profissional, participação, comanda, cobrança Pix, recebimento/conferência, estorno, reserva de prêmio (ver `matriz-transicoes.md`).
- Participação `completed` + comanda `awaiting_payment` é um estado normal.
- Comanda pendente bloqueia nova entrada da identidade (R-FILA-03), mas não ocupa cadeira.
- Recebimento e conferência são registros distintos, mesmo quando criados na mesma ação (dinheiro/maquininha).

## Alternativas consideradas

- Status único da visita: rejeitado (não representa "concluído e não pago").

## Consequências

- Telas e eventos mostram as duas dimensões (cadeira e financeiro).
- Pendências financeiras ficam visíveis ao dono sem travar a operação da fila.
