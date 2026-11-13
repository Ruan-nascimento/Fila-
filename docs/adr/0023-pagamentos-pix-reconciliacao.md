# ADR-0023 — Pagamentos: Pix em duas fases, reconciliação por externalId e recebimento presencial

- Situação: Aceita (etapa 08)
- Data: 2026-11-13

## Contexto

O ADR-0007 fixou os princípios:

- Pix só por evidência autenticada;
- uma tentativa ativa por comanda;
- resultado desconhecido em vez de nova cobrança;
- conferência separada.

Na etapa 08 a documentação oficial da AbacatePay v2 foi consultada (docs/pagamentos-abacatepay.md). Ela mostrou:

- não há cabeçalho de idempotência;
- não há cancelamento de Pix;
- a única garantia remota é "cobrança pendente com o mesmo `externalId` é devolvida em vez de criar outra".

O webhook fica para a etapa 09.

## Decisão

1. **Módulo `pagamento`.** É dono de `pix_charge`, `receipt` e `receipt_conferral`. Lê e muda a comanda só por funções do módulo `atendimento` (`LockForPayment`, `MarkAwaitingConfirmation`, `Settle`, `SetPaymentMethod`), na ordem de travas comanda → cobrança.
2. **Criação em duas fases.**
   - Primeira transação: grava a tentativa `creating` (com o hash da `Idempotency-Key`) e confirma.
   - Depois chama o provedor fora da transação, com contexto que não depende do navegador.
   - Segunda transação: grava o resultado, só se a tentativa ainda estiver `creating`.
   - Por isso o comando não usa o executor genérico de idempotência: a repetição é ancorada na própria tentativa (`UNIQUE(order_id, idem_key_hash)`).
3. **`externalId` = id da tentativa.**
   - Tempo esgotado ou 5xx resulta em `unknown`.
   - O worker procura a cobrança por `externalId`. Se acha, adota. Se não acha, repete a criação **com o mesmo `externalId`**.
   - Nunca se cria uma cobrança nova às cegas.
4. **Evidência de Pix pago = consulta autenticada pelo backend** (`list?id=`).
   - A resposta precisa conferir o ambiente da chave (`devMode`), o valor e o vínculo (`metadata.charge_id`). Se não conferir, a resposta não é aceita como verdade.
   - Nesta etapa não há webhook: ele chega na etapa 09 como segunda fonte, também autenticada.
5. **Cancelamento.** Como não existe, `cancelPixCharge` e a troca de meio com ciência só registram `cancel_requested_at`.
   - A cobrança segue pendente e deixa de impedir o recebimento presencial.
   - Se for paga, vira recebimento `excess`.
6. **Classificação do recebimento.**
   - `principal`: cobre exatamente o devido de uma comanda aguardando pagamento e ainda sem recebimento principal.
   - `excess`: todo o resto (tardio, duplicado, valor diferente).
   - O banco garante, no COMMIT, que comanda `awaiting_confirmation`/`settled` tem recebimentos principais iguais ao total, e que `settled` tem todos conferidos.
   - Recebimento e conferência não podem ser alterados nem apagados.
7. **Presencial.** Dinheiro e cartão gravam o recebimento e a conferência na mesma transação, em registros distintos. O valor enviado precisa ser igual ao devido calculado pelo servidor (`payment_amount_mismatch` se não for).
8. **Configuração.** O Pix vem desligado por padrão.
   - A chave de teste é recusada em produção.
   - A chave de produção exige `FILA_ABACATEPAY_PRODUCTION_VALIDATED=true`.
   - O QR é desenhado no navegador a partir do `br_code` (sem guardar a imagem do provedor).

## Alternativas consideradas

- **Chamar o provedor dentro da transação:** rejeitado. A transação ficaria aberta durante a rede e, se caísse depois de o provedor criar a cobrança, não sobraria registro da tentativa.
- **Nova tentativa automática depois de timeout com outro `externalId`:** rejeitado, porque poderia cobrar duas vezes.
- **Usar `/transparents/check` na reconciliação:** rejeitado. Ele não traz valor nem metadados, e sem eles não dá para conferir a evidência.
- **Fingir cancelamento (marcar `cancelled` localmente):** rejeitado. O Pix continuaria pagável, e a tela mentiria.

## Consequências

- O worker precisa da mesma configuração de Pix que a API. Sem worker, uma tentativa `unknown` fica "verificando" indefinidamente (nunca vira nova cobrança).
- Excesso e resultado não resolvido ficam registrados e logados. A pendência visível, a devolução e o webhook são da etapa 09.
- A compatibilidade com a AbacatePay real depende do checklist de docs/pagamentos-abacatepay.md. Os testes usam um simulador local que segue a documentação.
