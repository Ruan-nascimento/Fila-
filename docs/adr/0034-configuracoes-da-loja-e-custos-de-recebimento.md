# ADR-0034 — Configurações da loja por seção e custos de recebimento

- Situação: Aceita
- Data: 2026-11-21
- Altera: R-PAG-06 (cartão com modalidade), R-VIS (visitante pode ser desligado para novas entradas).

## Contexto

A loja precisa configurar catálogo, fila, pagamentos e custos internos de recebimento (débito, crédito, Pix integrado) sem uma tela única com tudo misturado, e o dono quer ver o resultado dos recebimentos depois desses custos.

## Decisão

### Configurações
1. `establishment_settings` (migration 00026), uma linha por loja, **criada só quando o dono salva** (sem linha valem os padrões, idênticos aos `DEFAULT`). Três seções com **versão própria**: Catálogo, Filas e atendimento, Pagamentos. `PATCH /settings/{seção}` altera só os campos enviados, com `expected_version` da seção; conflito devolve o estado atual. Auditoria T-ST-01/02/03. Tabela completa em [configuracoes-da-loja.md](../configuracoes-da-loja.md).
2. **Visitante desligado** (`visitors_enabled = false`): nenhuma sessão nova de visitante e nenhuma entrada nova de visitante. Visitas ativas, credenciais, código de recuperação e pagamento continuam.
3. **Meios de pagamento**: dinheiro, cartão presencial e Pix (Pix só é oferecido com a integração operacional). Não se salva configuração sem meio utilizável. Desligar um meio não invalida cobrança nem recebimento existentes. **Escolha ainda não cobrada**: se o meio foi escolhido antes de desligar, a equipe ainda registra o recebimento nele; nova escolha ou nova cobrança Pix no meio desligado é recusada (`payment_method_disabled`).
4. **Páginas públicas** recebem só `ordering` (limite de quantidade, produtos ligados, visitante ligado, meios aceitos). Nada financeiro interno.

### Custos de recebimento
5. `payment_fee_config`: por modalidade (`debit`, `credit`, `pix`), **versões imutáveis**; a de maior número vale a partir de `effective_from`. Percentual em **pontos-base** (1 pb = 0,01%; 0–3000) e componente **fixo por transação** (0–R$ 100,00). `cleared` volta a "não configurado". Zero explícito é configurado. Só o dono vê e altera (T-FE-01). São **estimativas cadastradas**: o sistema não assume taxa de provedor nenhuma e não usa valores atribuídos à AbacatePay.
6. **Regra única de arredondamento (backend)**: custo = ⌊(valor × pb + 5 000) ÷ 10 000⌋ + fixo (parte percentual ao centavo mais próximo, metade para cima). Exemplo de teste: R$ 100,00 × 2,5% + R$ 0,50 = R$ 3,00 → recebimento após o custo R$ 97,00. Três cortes numa transação = um componente fixo.
7. `receipt_fee`: custo gravado **no momento do recebimento** (o recebimento é imutável, então o custo fica ao lado): `not_applicable` (dinheiro), `estimated` (configuração vigente, com snapshot de pb e fixo), `not_configured`, `actual` (quando o provedor informar — hoje a integração não informa, então nada é marcado como efetivo). Sem linha = recebimento anterior à 1.0.0 ou cartão sem modalidade: **desconhecido**, nunca fabricado. Mudar a configuração não recalcula recebimentos antigos.
8. **Cartão**: `receipt.card_type` (`debit` | `credit`). Recebimentos antigos ficam "cartão não especificado". A web sempre envia a modalidade; a API aceita a ausência só durante a janela de atualização (sem custo estimado). Parcelamento e cartão online continuam fora (não existia parcelamento no código).
9. **Estornos**: a taxa do recebimento estornado continua contada uma vez só (não há dupla dedução); devolução de taxa só entra quando informada (`refund.fee_returned_cents`); senão o resumo conta os estornos com devolução desconhecida.
10. **Resultado**: `GET /receipt-costs` (dono) e o Dashboard mostram recebido, estornos, custos estimados, custos efetivos, recebido após custos e **cobertura** (`complete = false` quando há custo desconhecido ou não configurado). Nunca se chama isso de lucro. Custo nunca muda o valor cobrado do cliente nem é repassado.

## Alternativas

- Colunas de custo na tabela `receipt`: exigiria liberar UPDATE numa tabela imutável.
- Percentual em `numeric`: pontos-base inteiros evitam ponto flutuante e deixam a precisão explícita.

## Consequências

- Os indicadores separam recebido bruto, estornos, custos e o recebido após custos, com a cobertura.
- Quando o provedor passar a informar a taxa efetiva, basta preencher `actual_cents` sem migrar dados.
