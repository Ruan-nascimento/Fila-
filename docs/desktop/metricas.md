# Dicionário de métricas do Dashboard (programa Fila Gestão)

Fonte de verdade: `backend/internal/analitico/` (consultas) e ADR-0027. Todas as somas são feitas no banco, em **centavos** (inteiro) e **segundos**. O programa só formata (R$ em pt-BR, horários no fuso da loja).

## Regras comuns

| Tema | Regra |
|---|---|
| Período | O dono escolhe datas **locais** da loja (`from`, `to`, inclusivas). O intervalo consultado é `[from 00:00, to+1 00:00)` no fuso da loja (`establishment.timezone`), convertido em instantes antes de ir ao banco. Período em andamento: até o instante da leitura (inclusive). Máximo de 366 dias. |
| Buckets | `date_trunc(granularidade, instante AT TIME ZONE fuso)`: hora, dia, semana (começa na segunda) ou mês. Padrão: hora até 2 dias, dia até 62, semana acima disso. |
| Comparação | `compare=previous`. Mês civil inteiro → mês anterior. Outro intervalo de N dias → os N dias anteriores. Nos dois casos com o **mesmo tempo transcorrido** (hoje até 14h × ontem até 14h). Base anterior zero: mostra os dois valores absolutos, sem %. |
| Ausência | Cada valor tem `status`. `ok`: número (0 = zero real). `empty`: sem observações ("Sem dados para este período"). `not_collected`: antes da criação da loja no sistema (`meta.collection_started_at`). `not_applicable`: o filtro ativo não tem atribuição confiável nesta métrica. Divisão por zero → `empty`. |
| Filtros | `professional_id`, `service_id`, `method` (pix, cash, card). Parâmetro desconhecido é erro 400. Filtro sem atribuição confiável **não é ignorado em silêncio**: os valores saem `not_applicable` e `meta.ignored_filters` lista o filtro. |
| Profissional | Atribuição ao profissional da **fila final**. A transferência move a participação e a comanda para o destino (`participation.queue_id`, `service_order.professional_id`). |
| Duplicação | Recebimentos e estornos vêm das próprias tabelas (uma linha = um registro financeiro). A comanda só entra por `EXISTS` 1:1 quando há filtro de profissional. Itens nunca entram no mesmo JOIN que recebimentos. |
| Instante | `meta.generated_at` (leitura) e, para fotografias, `meta.snapshot_at`. Cada resposta lê num único `REPEATABLE READ` somente leitura. |

## Indicadores da primeira dobra

| Indicador | Fonte | Fórmula | Unidade | Data de referência | Filtros | Limitações |
|---|---|---|---|---|---|---|
| Recebimentos líquidos | `receipt`, `refund` | Σ `receipt.amount_cents` − Σ `refund.amount_cents` (status `confirmed`) | centavos | registro do recebimento / efetivação do estorno | profissional, meio. Serviço: inaplicável | Inclui recebimentos `principal` e `excess` (excesso é dinheiro que entrou; a devolução dele aparece como estorno quando efetivada) |
| Atendimentos concluídos | `participation` | contagem com `status = completed` | quantidade | `finished_at` | profissional, serviço. Meio: inaplicável | — |
| Espera mediana até a chamada | `participation` | mediana (`percentile_cont 0.5`) de `called_at − created_at`; também p90 e n | segundos | `called_at` | profissional, serviço. Meio: inaplicável | Sem chamada → fora. Transferência não reinicia o relógio (conta desde a entrada original). Ausentes depois de chamados entram (houve chamada) |
| Pendências financeiras **agora** | `financial_issue`, `service_order`, `pix_charge` | abertas + comandas aguardando pagamento + aguardando conferência + Pix com resultado desconhecido | quantidade (valor quando existe) | fotografia em `snapshot_at` | nenhum (inaplicável) | Não depende do período escolhido |

## Séries (`/series/{metric}`)

| Métrica | Séries | Fórmula | Referência | Filtros | Visual |
|---|---|---|---|---|---|
| `cash` | recebido, estornado, líquido | como acima, por bucket. Pix só vira recebimento depois de pago (Pix pendente não existe em `receipt`); estorno solicitado, em análise ou falho não entra. `amount_cents` é o valor devido recebido: no dinheiro, `cash_tendered_cents` e `change_cents` não entram | registro / efetivação | profissional, meio; serviço → `not_applicable` (sem rateio de estorno entre serviços) | colunas + legenda clicável |
| `ticket` | ticket médio, valor cobrado, comandas liquidadas | Σ `service_order.total_cents` ÷ nº de comandas `settled` (numerador e denominador expostos) | `closed_at` | profissional, serviço (comandas que contêm o item cobrado), meio (`payment_method`) | linha |
| `movement` | entradas, concluídos | entradas por `created_at`; concluídos por `finished_at`. Não são a mesma coorte, e o gráfico diz isso | entrada / conclusão | profissional, serviço; meio → `not_applicable` | colunas |
| `wait` | mediana, p90 (com n) | `called_at − created_at` | `called_at` | profissional, serviço | linha + faixas |
| `duration` | mediana dos concluídos, interrompidos | `finished_at − started_at` só de `completed`; `interrupted` contados à parte | `finished_at` | profissional, serviço | linha |
| `loyalty` | creditados, utilizados, devolvidos, ajustes, reservas, entregas | `loyalty_ledger.kind`: `credit` (+), `consume` (\|Δ\|), `consume_reversal` (+), ajustes = `credit_reversal` + `account_closure` (\|Δ\|). `hold`/`hold_release` só reservam pontos e não entram. Reservas por `created_at`; entregas (`delivered`, `delivery_reverted`) por `resolved_at` | `occurred_at` | nenhum (inaplicável) | linha |

**Correções posteriores (ticket).** `total_cents` é o valor cobrado **atual**: uma correção do dono depois do fechamento (`order_adjustment`) muda o total na data **original** da liquidação. O registro da comanda mostra `adjusted_after_close`.

## Detalhamentos (`/breakdowns/{dimension}`)

| Dimensão | Conteúdo | Regra |
|---|---|---|
| `payment_methods` | por meio: recebido, quantidade, estornado, líquido | mesmos critérios do caixa; serviço inaplicável |
| `outcomes` | concluídos, interrompidos, ausências, cancelamentos; esperando/chamados/em atendimento agora | coorte das participações que **entraram** no período. Taxa = quantidade ÷ participações já encerradas (denominador explícito). As abertas aparecem à parte, sem taxa |
| `professionals` | concluídos, espera mediana (n), duração mediana (n), serviços realizados, transferências recebidas | fila final; inclui profissionais inativos (histórico) |
| `services` | solicitados, realizados, valor dos itens | solicitados = itens incluídos (exceto removidos) por `added_at`; realizados = `performed` por `performed_at`; valor = `unit_price_cents` dos itens cobrados (preço registrado na comanda). `received_cents_attributed` é sempre `null`: dinheiro recebido não é rateado por serviço |
| `peak_hours` | entradas por dia ISO (1 = segunda) × hora local | `created_at AT TIME ZONE fuso` |
| `wait_bands` | 0–5, 5–15, 15–30, 30–60, 60+ minutos | participações chamadas no período |
| `pending` | abertas agora por tipo (com valor quando existe), idade agora (<1 h, 1–24 h, 1–7 d, >7 d), abertas e resolvidas no período | idade da comanda a receber = desde o fechamento do valor (`total_frozen_at`) |
| `rewards` | por prêmio: reservas, entregas, cancelamentos, expirações, pontos usados; fidelidade ligada agora? | o histórico continua visível com a fidelidade desligada (`program_enabled_now = false`) |
| `recurrence` | contas identificadas, contas com 2+ visitas, taxa (n), visitas anônimas excluídas | janela = período escolhido; só `participation.account_id`. Visitante e apelido nunca identificam ninguém |

## Registros (`/records/{kind}`): o que formou cada total

Paginação no servidor, 50 por página, cursor `(instante, id)` decrescente. O recorte é o mesmo do gráfico: período, bucket (`bucket_start`/`bucket_end`), dia/hora, faixa, situação e filtros. `total` e `total_cents` são do recorte inteiro, para conferir com o valor clicado.

| Tipo | Instante | Campos (sem dado de contato: o cliente aparece só pela senha da fila) | Abrir na gestão |
|---|---|---|---|
| `receipts` | `registered_at` | meio, valor, classificação, entregue/troco, comanda, senha | comanda |
| `refunds` | `resolved_at` (efetivados) | meio, valor, pedido em, comanda | comanda |
| `orders` | `closed_at` (liquidadas) | total, meio, profissional, itens cobrados, corrigida depois de fechar | comanda |
| `participations` | `ref=entered/called/finished` | situação, horários, espera, duração, profissional, transferida | comanda |
| `service_items` | `ref=performed/added` | serviço, situação, preço, cobrado | comanda |
| `financial_issues` | `state=open` (agora) / `opened` / `resolved` | tipo, situação, valor, comanda | pendências |
| `loyalty_entries` | `occurred_at` | tipo, Δ pontos, origem | — |

Filtro sem atribuição num tipo de registro → `422 analytics_filter_not_applicable` (ex.: recebimentos filtrados por serviço).

## O que ainda depende de coleta inexistente

| Métrica | Por que não existe | Dado necessário |
|---|---|---|
| Lucro / margem | Não há custos registrados | Custos por serviço (insumos, comissão) e despesas da loja |
| Ocupação / ociosidade da cadeira | Não há jornada registrada | Turnos de trabalho por profissional (início, fim, pausas) |
| Tempo de espera desde a entrada até a desistência | A saída do cliente é registrada, mas não o motivo nem quando ele desistiu de fato | Motivo e instante da desistência informados pelo cliente |
| Espera por trecho antes e depois de uma transferência | Só a última fila fica na participação; `participation_transfer` guarda o instante da troca, mas não a posição | Posição e horário de chamada por trecho |
| Comissão / repasse por profissional | Não há regra de repasse | Tabela de comissões por serviço e profissional |
| Recorrência de visitantes anônimos | Por regra de privacidade, o apelido não identifica pessoa | Nada a coletar: fica fora de propósito |
| Atendimento antes do cadastro da loja no sistema | Não havia coleta | Nenhum: esses intervalos aparecem como `not_collected` |
