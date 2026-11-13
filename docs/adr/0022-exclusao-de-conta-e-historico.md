# ADR-0022 — Exclusão de conta e histórico

- Situação: Aceita (etapa 07)
- Data: 2026-11-13

## Contexto

A pessoa precisa poder excluir a conta, mas a exclusão não pode:

- apagar obrigação financeira pendente;
- inventar pagamento;
- deixar uma loja sem dono;
- deixar efeitos pela metade se o processo cair.

A interface tem de dizer a verdade: "em processamento" com o que falta, e "concluída" só depois de concluída.

O histórico do cliente é privado, por loja, e visitante não ganha histórico retroativo.

## Decisão

1. **Módulo `exclusao`.** É dono de `deletion_request` e `deletion_request_block`. Orquestra funções dos módulos donos de cada dado (identidade, estabelecimento, fila, atendimento), sem consultar as tabelas deles diretamente.
2. **Pedido numa transação** (reautenticação recente e confirmação dos efeitos):
   - conta em `deletion_processing`;
   - todas as sessões e tokens revogados (o aviso de revogação encerra SSE abertos);
   - vínculos de equipe encerrados;
   - visitas aguardando ou chamadas canceladas (T-P-10), com a comanda encerrada sem cobrança;
   - benefícios encerrados (interface `Benefits`; pontos e reservas chegam na etapa 10);
   - solicitação gravada, auditoria e e-mail com o protocolo.

   Se qualquer passo falhar, nada muda.
3. **Dono único.**
   - Antes de aceitar, os vínculos de dono das lojas da conta são travados (`FOR UPDATE`). Se não restar outro dono ativo, a resposta é `409 sole_owner` e a transação é desfeita.
   - Dois donos pedindo ao mesmo tempo: o segundo espera e então se vê como único, então a loja nunca fica sem dono (teste determinístico).
   - A saída é o processo controlado do operador: `fila-admin transfer-owner` (convite de novo dono) ou `fila-admin close-establishment` (só sem ninguém na fila e sem comanda pendente).
4. **Protocolo.**
   - São 32 caracteres aleatórios, mostrados uma única vez na resposta e enviados por e-mail.
   - O banco guarda só o HMAC e um sufixo de 6 caracteres.
   - A idempotência ganhou `Result.Stored`: a resposta guardada para repetição leva o protocolo mascarado.
   - A consulta é pública por POST (o protocolo nunca vai no endereço) e tem limite por IP.
5. **Bloqueios reais**, recalculados a cada consulta e a cada volta do worker:
   - `service_in_progress`: atendimento em curso;
   - `financial_pending`: comanda em revisão, aguardando pagamento ou conferência;
   - `professional_queue_has_participants`: fila do profissional que excluiu a conta ainda tem gente.

   Enquanto houver algum, a situação é `processing`. A equipe continua concluindo, recebendo e conferindo. A dona vê as solicitações que dependem da loja dela, sem nome nem e-mail.
6. **Anonimização (worker), uma transação por solicitação.**
   - Sobrescreve o perfil, troca o nick nas participações por "Cliente", tira o e-mail de convites e apaga os eventos da própria pessoa.
   - A conta continua como pseudônimo para comandas, itens, correções e auditoria.
   - A evidência registra só categorias, contagens e data.
   - Queda no meio desfaz tudo e a próxima volta refaz. `FOR UPDATE SKIP LOCKED` impede dois workers na mesma solicitação.
   - O worker também cancela visitas que tenham escapado de uma corrida com o pedido.
7. **Meta de 30 dias.** É **operacional**, nunca chamada de prazo legal. Com 25 dias, o worker registra um alerta e a aba "Exclusões" destaca a solicitação.
8. **Histórico** (`GET /v1/me/history?establishment_id=`).
   - Só a sessão de conta, só as comandas dessa conta naquela loja, paginado por cursor.
   - Visitante recebe 401. Uma conta criada depois não herda visitas de visitante: as comandas de visitante apontam para o visitante, nunca para a conta.
   - `points_credited` fica 0 até a etapa 10.
9. **Minimização do visitante.**
   - O nick sai da participação 30 dias depois de a visita terminar com a comanda encerrada.
   - O nick sai do cadastro de visitante quando o acesso expira.
   - O prazo é operacional e reversível (`docs/retencao.md`).

## Alternativas consideradas

- **Apagar as linhas da conta:** perderia a ligação das comandas e da auditoria, e as tabelas proíbem apagar (`forbid_delete`).
- **Anonimizar em vários passos com marca de progresso:** permite lotes grandes, mas uma conta tem poucos registros. Uma transação dá "tudo ou nada" de graça.
- **Guardar o protocolo em claro para reenviar:** um vazamento do banco permitiria consultar as solicitações. HMAC + e-mail + sufixo bastam.
- **Recusar a exclusão enquanto houver pendência:** o anexo pede o contrário (aceitar, mostrar "em processamento" e a equipe resolver).

## Consequências

- Pagamento (etapas 08 e 09): recebimentos tardios continuam permitidos em conta excluída, e a pendência financeira de Pix entra no mesmo bloqueio.
- Pontos (etapa 10): implementar `Benefits` (saldo `account_closure`, reservas `cancelled`) e creditar só a contas ativas.
- Restauração de backup (etapa 12): reaplicar as exclusões concluídas e trocar a época do fluxo. Os prazos de backup e os prazos legais seguem pendentes (`docs/retencao.md`).
