# ADR-0033 — Quantidades na comanda, execução parcial e grupos numa única participação

- Situação: Aceita
- Data: 2026-11-21
- Altera: R-CMD-04 (cada serviço aparece uma vez **por linha**, com quantidade), R-CMD-09, R-CMD-11, R-CMD-13, R-FID-04 (pontos por unidade).

## Contexto

Um responsável acompanha dois filhos e os três farão o mesmo corte; uma pessoa só responde pela comanda. Hoje cada serviço aparece uma vez por comanda (`order_item_service_uq`) e não há quantidade.

## Decisão

1. **Uma linha por serviço ou produto, com quantidade** (migration 00025). O índice único continua valendo: repetir o item é ajustar a quantidade da mesma linha. Itens antigos recebem `quantity = 1`; realizado vira `performed_quantity = 1`; cobrado vira `charged_quantity = 1`. Preço, duração e pontos registrados não mudam.
2. **Invariantes no banco**: `1 ≤ quantity ≤ 50`; `0 ≤ charged_quantity ≤ performed_quantity ≤ quantity`; `charged ⇔ charged_quantity > 0`; linha "aceita" ainda tem unidade a fazer; "realizada" tem ≥ 1 unidade; "não realizada" tem 0; produto sem duração e sem pontos. O gatilho de total passa a ser Σ preço × quantidade (aberta) e Σ preço × unidades cobradas (valor fechado).
3. **Cálculos (sempre no servidor)**: subtotal = unidades × preço unitário registrado; duração = unidades × duração registrada (só serviços); pontos = unidades de serviço cobradas × pontos registrados, creditados ao **titular com conta** depois da liquidação (R-FID-04). Nada é distribuído a acompanhantes; visitante continua sem pontos.
4. **Snapshot**: aumentar a quantidade de uma linha mantém o preço aceito para ela; linha nova copia o catálogo vigente.
5. **Mudanças por quantidade ABSOLUTA com versão esperada** (`items` em `replaceOrderItems`, `set_quantities` em solicitações): repetir o comando não soma de novo. A quantidade nunca fica abaixo das unidades já realizadas; linha com unidade realizada não é retirada. Toda mudança de quantidade (pedida, realizada, cobrada) gera uma linha imutável em `order_item_quantity_change` com origem, autor e versão da comanda.
6. **Limite por loja** `max_service_quantity` (padrão 10, 1–50, decisão nova de produto). Vale ao incluir ou **aumentar**; quantidade já aceita acima de um limite reduzido depois continua válida (sem efeito retroativo).
7. **Uma participação, vários serviços**: o pedido ocupa **uma posição** na fila, um profissional faz os serviços em sequência, o titular responde pelo pedido e pelo pagamento. Não há contas ou dados de acompanhantes, várias posições silenciosas, atendimento simultâneo por profissionais diferentes nem divisão de pagamento. Unidades de serviço **não são pessoas**: as telas mostram "participações" e "unidades", nunca "pessoas atendidas".
8. **Execução parcial**: `setItemPerformedQuantity` (T-O-14) registra "2 de 3" (valor absoluto, só cresce). A conclusão aceita menos unidades que as pedidas **com motivo**. A interrupção registra, por linha, as unidades inteiras feitas (`reported_quantity`) e o que houve com a seguinte (`partially_performed` = começada; `not_started` = não começada). O dono revisa e cobra **só unidades completas**; a unidade parcial vale zero. Não existe cobrança proporcional por minutos.
9. **Correção do dono (T-O-11)** ganha `set_charged_quantities` e `add_performed_items` (com quantidade), registrados em `order_adjustment.quantity_changes`. Continua bloqueada com Pix ativo ou desconhecido: uma cobrança antiga nunca vale por um total novo.
10. **Compatibilidade**: `service_ids` continua aceito em entrada, edição e solicitação (quantidade 1); o servidor tolera a janela de atualização entre web, API e desktop.

## Alternativas

- Uma linha por unidade (três linhas de Corte): exigiria remover o índice único e espalharia o histórico; quantidade numa linha é mais simples de explicar e auditar.
- Incremento (+1/−1) com idempotência: o valor absoluto com versão esperada resolve a repetição sem estado extra.

## Consequências

- A fila mostra "Corte ×3" e a carga em participações, unidades e minutos.
- Os indicadores contam unidades (3 cortes realizados) separadas de participações (1) e de contas únicas.
