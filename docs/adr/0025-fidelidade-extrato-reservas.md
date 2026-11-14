# ADR-0025 — Fidelidade: extrato materializado pelo banco, crédito na liquidação e reservas de 24 h

- Situação: Aceita (etapa 10)
- Data: 2026-11-13

## Contexto

O ADR-0012 definiu o extrato de pontos só de inserção, com chave única de origem. A etapa 10 precisa garantir, mesmo com pedidos simultâneos, repetição e worker:

- crédito uma única vez e só depois de pago **e** conferido;
- reversão uma única vez quando o pagamento principal é estornado;
- reserva de prêmio que não gasta o mesmo saldo nem a mesma última unidade duas vezes;
- vencimento × entrega sem liberar e consumir a mesma reserva;
- saldo e estoque sempre explicáveis pelos movimentos.

## Decisão

1. **O banco mantém o saldo.** `loyalty_ledger` só aceita inserção, com `UNIQUE(loja, tipo de origem, origem, tipo)`. Um gatilho soma cada lançamento em `loyalty_account` (saldo, retido, versão). Ninguém edita o saldo direto. O banco ainda:
   - recusa retido negativo;
   - recusa um `hold` acima do disponível.

   O gatilho faz UPDATE e só insere se a linha não existe, porque num `INSERT … ON CONFLICT` o PostgreSQL confere o `CHECK` na linha proposta antes de ver o conflito.
2. **Estoque por movimentos.** `reward_stock_movement` também é só de inserção (dono define, reserva, libera, corrige entrega), e um gatilho atualiza `units_available`, que nunca fica negativo. Na disputa pela última unidade, a trava na linha do prêmio deixa uma só reserva passar, e o `CHECK` é a segunda barreira.
3. **Elegibilidade na entrada.** O módulo fila grava o snapshot: é conta (sessão de conta só existe com e-mail verificado) **e** o programa está ligado naquele momento. Visitante nunca é elegível, e criar conta depois não muda comandas antigas, que apontam para o visitante.
4. **Crédito na liquidação.** O módulo pagamento chama a fidelidade (interface `Loyalty`) na **mesma transação** em que a comanda vira `settled` (`Settle`). O crédito exige:
   - conta e snapshot elegível;
   - sem interrupção;
   - comanda paga e conferida;
   - soma de pontos dos itens realizados e cobrados maior que zero.

   Gratuidade nunca é paga, então nunca credita.
5. **Reversão.** Quando o estorno de um recebimento **principal** é confirmado (pelo provedor ou pelo dono com evidência), a fidelidade grava `credit_reversal` uma vez. O saldo pode ficar negativo, sem dívida em dinheiro; só bloqueia reservas. Devolver um recebimento `excess` não reverte nada.
6. **Ordem de travas do módulo:** saldo da conta → reserva → prêmio. A entrega confere `now < expires_at` com a reserva travada. O vencimento (worker, ou na hora de reservar de novo) muda o estado só se ela ainda está ativa. Assim, entregar e vencer nunca acontecem os dois.
7. **Programa desligado** só bloqueia novas reservas e novas elegibilidades. Consulta, cancelamento, entrega e créditos já prometidos continuam.
8. **Exclusão de conta** (`Benefits.CloseForDeletion`): cancela a reserva ativa, libera pontos e unidade, encerra o saldo de cada loja (`account_closure`) e troca por "Cliente" o nome guardado nas reservas (mostrado à equipe na entrega).
9. **Eventos:**
   - `loyalty.updated` vai só para a própria conta;
   - `reward_reservation.updated` vai para o titular **sem** o nome, e para o dono e cada profissional ativo **com** o nome;
   - o snapshot do SSE no canal `account` traz o resumo de pontos.

## Alternativas consideradas

- **Calcular o saldo somando o extrato a cada leitura:** rejeitado. Precisaria travar algo mesmo assim para impedir o gasto duplo; a linha materializada é essa trava.
- **Atualizar o saldo no código Go:** rejeitado. O gatilho garante que nenhum caminho esquece, e o teste "explicável" compara saldo e soma.
- **Creditar por evento assíncrono depois da liquidação:** rejeitado. A mesma transação garante "uma vez" sem fila nova e sem janela em que a comanda está paga sem crédito.
- **Ler o nome do titular na tabela de contas:** rejeitado, porque ela pertence a outro módulo. O nome é guardado na reserva e minimizado na exclusão.

## Consequências

- Correção de pontos é sempre um novo lançamento: não existe edição.
- Pontos por serviço continuam definidos no catálogo (etapa 03) e copiados para o item na entrada.
- O vencimento de reservas depende do worker. Sem ele, a reserva vencida não fica entregável (o prazo é conferido pelo horário) e é liberada na próxima reserva da mesma conta.
