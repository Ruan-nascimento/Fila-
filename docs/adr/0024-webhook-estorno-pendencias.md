# ADR-0024 — Webhook autenticado, estorno em três momentos e pendências financeiras

- Situação: Aceita (etapa 09)
- Data: 2026-11-13

## Contexto

Na etapa 08 o Pix passou a ser confirmado só pela consulta autenticada do worker (ADR-0023). Na etapa 09 entram o aviso da AbacatePay (webhook), o estorno e o tratamento de problemas financeiros (pagamento tardio, duplicado, divergente).

A documentação oficial (docs/pagamentos-abacatepay.md, consultada em 2026-11-13) diz:

- **Origem e integridade são coisas separadas.** O secret do webhook (no cabeçalho `X-Webhook-Secret` e na query) prova a origem. A assinatura `X-Webhook-Signature` é base64(HMAC-SHA256) do corpo bruto com uma chave **pública**, igual para todas as contas, e prova só que o corpo chegou intacto.
- **Os avisos podem vir repetidos e fora de ordem.** As retentativas usam o mesmo `id`; o reenvio manual usa um `id` novo.
- **O aviso não traz moeda nem identificação da loja recebedora**, mas traz dados pessoais do pagador.
- **O estorno é sempre integral**, e repetir o pedido para uma cobrança já estornada é recusado sem criar outro estorno.

## Decisão

1. **Webhook = evidência só se autenticado.** O sistema confere o secret (comparação em tempo constante) **e** a assinatura sobre os bytes recebidos. Se algum dos dois falhar, responde 401 (a AbacatePay não repete) e não reconhece nada. Se o aviso cita uma cobrança nossa em aberto, a consulta autenticada do worker é antecipada; ela registra só o que comprovar. Sem secret configurado, o webhook fica desligado (404) e o sistema funciona só com a consulta.
2. **Aviso autenticado: confere antes de aplicar.** O aviso precisa bater em cinco pontos; se algum não bater, fica registrado como `mismatch` e não muda nada:
   - ambiente (`devMode` igual ao da chave);
   - cobrança (nossa);
   - pedido e loja (os metadados que enviamos na criação);
   - método PIX;
   - versão da API.

   Valor diferente do cobrado é diferente: o recebimento é registrado como `excess`, com a pendência `amount_mismatch`, e nunca é descartado.
3. **Deduplicação e persistência antes do 200.** Na mesma transação, o aviso é gravado em `provider_event` (único por `id` autenticado) e o efeito é aplicado. Se a transação falhar, a resposta é 5xx e a AbacatePay tenta de novo.

   O efeito de negócio também é idempotente:
   - no máximo um recebimento por cobrança;
   - estados finais não voltam atrás;
   - estorno confirmado não muda.

   Por isso o reenvio manual (`id` novo) também não duplica nada. Do corpo, só o hash é guardado; nome, e-mail e documento do pagador não são gravados.
4. **Aviso fora de ordem.** Se `transparent.refunded` chega antes de `transparent.completed`, o pagamento é registrado primeiro, com a evidência do mesmo aviso autenticado, e só depois o estorno.
5. **Estorno em três momentos**, sem depender da aba do cliente:
   - **intenção:** só o dono, reautenticado e com motivo;
   - **processamento:** o worker chama `POST /transparents/refund`;
   - **confirmação:** só quando a cobrança aparece `REFUNDED` na consulta autenticada ou no webhook.

   Os casos difíceis seguem estas regras:
   - Resposta ambígua → `unknown`, e o worker consulta antes de qualquer coisa.
   - Depois de reinício, o worker consulta e repete o pedido se a cobrança ainda estiver paga. Isso é seguro pela regra documentada.
   - Recusa, disputa ou 12 consultas sem confirmação → `under_review`, com pendência; o dono confirma com evidência documentada ou registra que não ocorreu.
   - Dinheiro e cartão vão direto para revisão.

   O banco garante as regras centrais:
   - um estorno não-falho por recebimento (integral);
   - estorno resolvido não muda;
   - nada é apagado.
6. **Corrida entre conferência e estorno**, serializada pela trava da comanda:
   - um recebimento principal só é estornado depois de conferido;
   - um recebimento com estorno ativo ou confirmado não é conferido.
7. **Pendências financeiras** ficam na tabela `financial_issue`, com no máximo uma por (tipo, origem). São abertas pelo sistema nestes casos:
   - Pix tardio;
   - Pix duplicado;
   - valor divergente;
   - troca de meio com Pix ativo;
   - cobrança desconhecida depois do limite de consultas;
   - estorno em revisão.

   Só o dono resolve, sempre com nota, e resolver não altera recebimentos. A equipe vê os tipos abertos na comanda (`OrderPayment.open_issue_kinds`, campo novo do contrato).
8. **Painel do dono** (`listPendencies`, `pendencies.updated` e o snapshot SSE). Fica no módulo `painel`, que não tem tabelas: só soma contadores exportados pelos módulos donos dos dados. O evento `pendencies.updated` é gravado nas mudanças financeiras. As outras mudanças (por exemplo, uma comanda nova a receber) chegam por `order.updated`, e a tela relê o painel.

## Alternativas consideradas

- **Tratar só a assinatura HMAC como autenticação:** rejeitado. A chave é pública, então qualquer um consegue assinar.
- **Responder 200 e processar depois (fila):** rejeitado nesta etapa. O efeito é pequeno e cabe na mesma transação, e "persistir antes de responder" fica garantido sem uma fila a mais.
- **Confirmar o estorno pela resposta `COMPLETE` do pedido:** rejeitado. A confirmação vem da situação da cobrança (`REFUNDED`), que é a mesma evidência usada para o pagamento.
- **Marcar `failed` em qualquer recusa do provedor:** rejeitado. Recusas transitórias ("outro reembolso em andamento") virariam falha falsa; vão para a revisão do dono.
- **Guardar o corpo bruto do aviso:** rejeitado, porque contém dados pessoais do pagador. Fica só o hash.

## Consequências

- Para usar o webhook, a API precisa de uma URL **https** pública (implantação, etapa 12). Sem ela, o worker continua confirmando em segundos.
- A reversão de pontos por estorno do recebimento principal (T-LG-02) chega na etapa 10. A transição de confirmação acontece uma única vez por estorno (estado final imutável no banco), o que dá o ponto único para ligá-la.
- Disputas (MED) não têm tratamento automático: o aviso é registrado como `ignored` e um estorno pela API é recusado, indo para a revisão do dono.
- A compatibilidade real (assinatura, formato dos avisos, estorno) depende do checklist de docs/pagamentos-abacatepay.md. Os testes usam o simulador local, que segue a documentação.
