# Pagamentos e AbacatePay (etapa 08)

Este documento registra o que a **documentação oficial da AbacatePay** confirma, o que o sistema faz com isso e o que falta validar antes de ligar cobrança real.

A documentação foi consultada em **2026-11-13** (API v2): <https://docs.abacatepay.com>.

## 1. O que a documentação oficial confirma

| Assunto | O que está documentado | Onde |
|---|---|---|
| Endereço | `https://api.abacatepay.com/v2` | [introdução](https://docs.abacatepay.com/pages/reference/introduction) |
| Autenticação | Cabeçalho `Authorization: Bearer <chave>` | [introdução](https://docs.abacatepay.com/pages/reference/introduction) |
| Ambiente (sandbox) | Vem da chave: `abc_dev_…` é teste e nada é cobrado; `abc_prod_…` é produção. As respostas trazem `devMode`. | [modo teste](https://docs.abacatepay.com/pages/comecar/modo-teste) |
| Resposta | Envelope `{data, success, error}` | [introdução](https://docs.abacatepay.com/pages/reference/introduction) |
| Limite | 100 req/s por loja; acima disso, `429` | [introdução](https://docs.abacatepay.com/pages/reference/introduction) |
| Criar Pix | `POST /transparents/create` com `{method: "PIX", data: {amount, expiresIn, description, externalId, metadata}}`. O valor `amount` é em centavos (mínimo 100) e `expiresIn` em segundos (mínimo 300). A resposta traz `id` (`pix_char_…`), `status`, `amount`, `brCode`, `brCodeBase64`, `expiresAt` e `devMode`. | [create](https://docs.abacatepay.com/pages/transparents/create), [Pix](https://docs.abacatepay.com/pages/pagamentos/pix) |
| Idempotência | **Não há** cabeçalho de idempotência. O documentado é: se já existe uma cobrança Pix **pendente** com o mesmo `externalId`, a API devolve essa em vez de criar outra. | [Pix](https://docs.abacatepay.com/pages/pagamentos/pix) |
| Consultar | `GET /transparents/check?id=` devolve só `status` e `expiresAt`. `GET /transparents/list` aceita filtros `id`, `externalId`, `status` e `method`, traz valor e metadados, e é paginado. | [check](https://docs.abacatepay.com/pages/transparents/check), [list](https://docs.abacatepay.com/pages/transparents/list) |
| Estados | `PENDING`, `EXPIRED`, `CANCELLED`, `PAID`, `UNDER_DISPUTE`, `REFUNDED`, `REDEEMED`, `APPROVED`, `FAILED` | [create](https://docs.abacatepay.com/pages/transparents/create) |
| Cancelar Pix | **Não existe** rota de cancelamento. O Pix só expira e, depois de expirado, não pode mais ser pago. | [Pix](https://docs.abacatepay.com/pages/pagamentos/pix) |
| Simular pagamento | `POST /transparents/simulate-payment?id=` funciona só em modo teste | [simulate-payment](https://docs.abacatepay.com/pages/transparents/simulate-payment) |
| Estorno | `POST /transparents/refund {id, reason}`: sempre integral (sem parcial), só de cobrança paga e fora de disputa; resposta `{id "tran_…", status "COMPLETE", amount}`. Repetir para cobrança já estornada responde 400 ("Esta cobrança já foi reembolsada.") **sem** criar outro estorno. A cobrança passa a `REFUNDED` e chega o aviso `transparent.refunded`. | [refund](https://docs.abacatepay.com/pages/transparents/refund), [reembolsos](https://docs.abacatepay.com/pages/vender/reembolsos) |
| Webhook: entrega | `POST` com envelope `{id, event, apiVersion: 2, devMode, data}`. 2xx em até 30 s = entregue; 5xx/408/429/rede = repete (até 7 tentativas em ~18 h, mesmo `id`); 401 e outros 4xx não repetem. Pode chegar fora de ordem e repetido; reenvio manual pelo painel tem `id` novo. HTTPS obrigatório (inclusive em teste). | [webhooks](https://docs.abacatepay.com/pages/notificacoes/webhooks) |
| Webhook: autenticidade | **Origem:** o secret cadastrado no webhook (32+ caracteres) chega em `X-Webhook-Secret` e na query `webhookSecret`; comparar em tempo constante. **Integridade:** `X-Webhook-Signature` = base64(HMAC-SHA256 do corpo bruto) com a **chave pública** da AbacatePay (a mesma para todas as contas). A documentação diz que "quem prova a origem é o secret". Sem carimbo de tempo nem lista de IPs. | [segurança](https://docs.abacatepay.com/pages/notificacoes/seguranca) |
| Webhook: eventos Pix | `transparent.completed` (`status: PAID`), `transparent.refunded` (`REFUNDED`), `transparent.disputed` (MED: `UNDER_DISPUTE`, depois `PAID` ou `REFUNDED`), `transparent.lost`. `data.transparent` traz `id`, `amount`, `status`, `devMode`, `metadata`; `data.customer` e `data.payerInformation` trazem nome, e-mail e documento mascarado do pagador. **Não há campo de moeda.** | [eventos](https://docs.abacatepay.com/pages/webhooks/events/transparent) |

**O que não está documentado e por isso não foi inventado:**

- cancelamento de Pix;
- estorno parcial;
- moeda no aviso (Pix na AbacatePay é sempre em reais; conferimos método PIX e valor);
- identificador da loja recebedora no aviso (o "destinatário" é provado pelo secret do webhook da nossa conta e pelo ambiente `devMode`);
- idempotência por cabeçalho;
- garantia de deduplicação de cobrança que **já foi paga** ou expirou com o mesmo `externalId`. A garantia documentada vale só para cobrança pendente.

## 2. Como o sistema usa isso

O código está em `backend/internal/pagamento/`:

- adaptador em `abacatepay.go`;
- regras em `pix.go` e `presencial.go`;
- worker em `reconciliar.go`.

**Criar Pix (`createPixCharge`)**

1. O servidor calcula o valor devido. O corpo da requisição não aceita valor.
2. A tentativa é gravada como `creating` e a transação é confirmada **antes** de chamar o provedor.
3. O sistema chama `POST /transparents/create` com `externalId` igual ao id da tentativa e `metadata` com `charge_id`, `order_id` e `establishment_id` (só identificadores, nenhum dado pessoal).
4. O resultado define o que acontece:

| Resultado da chamada | Estado da tentativa | Resposta |
|---|---|---|
| Resposta conferida (ambiente da chave, valor, vínculo) | `pending` | 201 com `br_code` e validade |
| Tempo esgotado, conexão caiu, 5xx, resposta ilegível ou que não confere | `unknown` | 202, "verificando pagamento" |
| Não conectou, 429 ou recusa 4xx | `failed` | 503 `provider_unavailable`; a comanda continua aguardando pagamento |

**Uma tentativa ativa por comanda.** Isso vale para tentativas `creating`, `pending` e `unknown`, e é garantido por um índice único no banco. Repetir com a mesma `Idempotency-Key` devolve a mesma tentativa. Outra aba recebe `409 pix_charge_active` com a cobrança atual.

**Reconciliação (worker, a cada 2 s)**

- Uma tentativa `unknown` ou parada em `creating` é procurada por `list?externalId=`.
  - Se for achada, é adotada.
  - Se não for achada, a criação é repetida com o **mesmo** `externalId`. A API devolve a pendente, se ela existir. Nunca se cria com outro `externalId`.
- Uma tentativa `pending` é consultada por `list?id=` até ficar paga, expirada, cancelada ou falhar.
- A espera entre consultas cresce de 5 s até 5 min.
- Depois de 12 consultas sem resolver, o log registra um erro `charge_outcome_unresolved`. A pendência visível para a dona fica para a etapa 09.

**Recebimento**

- Pix só é recebido quando a **consulta autenticada** ao provedor diz `PAID`. Retorno do navegador, imagem ou "já paguei" não confirmam nada: não existe operação para isso.
- Pix pago que cobre exatamente o devido de uma comanda aguardando pagamento é registrado como recebimento `principal`, e a comanda vai para `awaiting_confirmation`.
- Pix tardio, duplicado ou com valor diferente vira recebimento `excess`. Fica registrado e nada é apagado. A pendência e a devolução são da etapa 09.
- Conferência é um registro separado, feito pela equipe (`conferReceipt`). Com todos os recebimentos conferidos, a comanda fica `settled`.
- Dinheiro e cartão presencial (`registerInPersonReceipt`): o recebimento integral e a conferência são feitos na mesma ação, em dois registros. O troco é o entregue menos o devido. No cartão, só se guarda uma referência opcional, nunca dados do cartão.
- Trocar de Pix para outro meio com Pix em aberto exige ciência (`acknowledge_active_pix`). Como a AbacatePay não cancela Pix, só se registra o pedido (`cancel_requested_at`): o Pix continua válido até expirar.

**Webhook (etapa 09, ADR-0024)** — `POST /v1/webhooks/abacatepay`

1. Lê o corpo bruto (até 256 KB) e confere o secret (tempo constante) e a assinatura.
2. Falhou: responde 401 e nada é reconhecido. Se o aviso cita uma cobrança nossa em aberto, a consulta autenticada do worker é antecipada; só o que a consulta comprovar é registrado.
3. Autenticado: grava o aviso (deduplicação pelo `id`) e, **na mesma transação**, aplica o efeito; só então responde 200. Repetido → 200 sem efeito.
4. Confere ambiente (`devMode` igual ao da chave), cobrança (nossa, pelo id do provedor ou pelo `charge_id` dos metadados), pedido e loja (metadados), método PIX e valor (diferente → recebimento `excess` + pendência `amount_mismatch`).
5. Guarda só identificadores, estado, valor e o hash do corpo. **Nome, e-mail e documento do pagador não são gravados.**
6. Sem `FILA_ABACATEPAY_WEBHOOK_SECRET` o webhook fica desligado (404) e só a consulta do worker confirma.

**Estorno (etapa 09)** — intenção, processamento e confirmação separados

- O dono pede (motivo, senha confirmada) → `requested`. Recebimento principal precisa estar conferido; um estorno não-falho por recebimento (integral).
- O worker pede `POST /transparents/refund` → `processing`; tempo esgotado ou 5xx → `unknown`.
- Confirmado (`confirmed`, evidência `provider`) **só** quando a consulta autenticada (ou o webhook `transparent.refunded`) mostra a cobrança `REFUNDED`.
- Recusa do provedor, disputa ou 12 consultas sem confirmação → `under_review` com pendência; o dono confirma com evidência documentada ou registra que não ocorreu. Dinheiro e cartão vão direto para `under_review`.
- Depois de queda ou reinício, o worker retoma: consulta primeiro; se a cobrança ainda está paga, repete o pedido (seguro, pela regra documentada acima).

**Pendências financeiras** — uma por tipo e origem, nunca apagadas:

- Pix tardio (`late_receipt`);
- Pix duplicado (`duplicate_receipt`);
- valor divergente (`amount_mismatch`);
- troca de meio com Pix ativo (`active_pix_on_method_switch`);
- cobrança sem resposta depois de 12 consultas (`charge_outcome_unresolved`);
- estorno em revisão (`refund_under_review`).

O dono vê e resolve (com nota) em Gestão → Pendências; a equipe vê os tipos abertos na comanda.

**Segurança da chave**

- A chave fica só no backend (`FILA_ABACATEPAY_API_KEY`). Nunca aparece em log, erro, resposta ou bundle (há teste para isso).
- O sistema recusa iniciar se a configuração for perigosa:
  - chave de teste em produção;
  - chave de produção fora de produção;
  - produção sem `FILA_ABACATEPAY_PRODUCTION_VALIDATED=true`;
  - endereço sem https em produção.

## 3. Configuração

| Variável | Padrão | Uso |
|---|---|---|
| `FILA_PIX_PROVIDER` | `disabled` | `disabled` (só dinheiro e cartão) ou `abacatepay` |
| `FILA_ABACATEPAY_API_KEY` | — | Chave da API (`abc_dev_…` para testar) |
| `FILA_ABACATEPAY_BASE_URL` | `https://api.abacatepay.com/v2` | Endereço da API |
| `FILA_PIX_EXPIRES_IN` | `15m` | Validade do Pix (de 5m a 24h) |
| `FILA_ABACATEPAY_TIMEOUT` | `10s` | Tempo máximo de cada chamada |
| `FILA_ABACATEPAY_PRODUCTION_VALIDATED` | `false` | Libera a chave de produção (só depois do checklist abaixo) |
| `FILA_ABACATEPAY_WEBHOOK_SECRET` | vazio (webhook desligado) | O mesmo secret cadastrado no webhook da AbacatePay (32–256 caracteres) |

A API e o worker precisam da **mesma** configuração: o worker é quem reconcilia.

## 4. O que foi testado e o que não foi

**Testado (sem segredo):**

- testes de contrato do adaptador com respostas no formato da documentação;
- testes de integração com o adaptador real falando HTTP com um simulador local (`internal/pagamento/pagamentotest`). Cobrem:
  - tempo esgotado;
  - 5xx antes e depois de criar;
  - recusa;
  - resposta adulterada;
  - consulta fora do ar;
  - pagamento tardio;
  - duplo clique e concorrência;
  - webhook: repetido, simultâneo, reenvio com id novo, secret errado, assinatura inválida, corpo alterado, outro ambiente, pedido trocado, cobrança desconhecida, valor divergente, estorno fora de ordem;
  - estorno: confirmação só pela consulta, resposta ambígua (tempo esgotado e 5xx), recusa, sem confirmação até o limite, retomada depois de reinício, presencial com evidência, corrida com a conferência.

**Não testado:** a AbacatePay de verdade. Não há chave de teste disponível neste ambiente, então **não se afirma** compatibilidade com o serviço real.

O simulador segue a documentação, mas pode divergir dela em detalhes como:

- formato de datas;
- campos extras;
- comportamento de `list` com `externalId`.

## 5. Checklist antes de ligar cobrança real

1. Criar a chave de **teste** (`abc_dev_…`) no painel e configurar `FILA_PIX_PROVIDER=abacatepay` em desenvolvimento.
2. Criar um Pix pela interface e conferir no painel: valor, `externalId` e metadados.
3. Pagar com `simulate-payment` e conferir que o worker leva a comanda a "aguardando conferência".
4. Deixar um Pix expirar e conferir que a reconciliação o marca como `expired` e que um novo Pix pode ser criado.
5. Derrubar a rede durante a criação (ou usar um `FILA_ABACATEPAY_TIMEOUT` mínimo) e conferir:
   - o resultado fica `unknown`;
   - a reconciliação adota a cobrança;
   - o painel mostra uma única cobrança.
6. Conferir que `list?externalId=` filtra de verdade e que repetir `create` com o mesmo `externalId` devolve a pendente.
7. Webhook: cadastrar no painel (Webhook v2, eventos `transparent.*`) a URL **https** pública `…/v1/webhooks/abacatepay` com um secret novo de 32+ caracteres, e colocar o mesmo valor em `FILA_ABACATEPAY_WEBHOOK_SECRET`. Pagar com `simulate-payment` e conferir no log do painel que a entrega teve 200 e que a comanda mudou; reenviar pelo painel e conferir que nada duplica.
8. Estorno: estornar um Pix de teste pela interface (dono) e conferir que só vira "estornado" depois da confirmação; conferir no painel que houve um único reembolso.
9. Conferir no painel se a assinatura chega como documentado (a chave pública não muda sem aviso).
10. Só então usar a chave de produção, com `FILA_ABACATEPAY_PRODUCTION_VALIDATED=true`, registrando quem validou e quando.
