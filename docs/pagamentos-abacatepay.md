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
| Estorno | `POST /transparents/refund {id, reason}`, somente integral (fica para a etapa 09) | [refund](https://docs.abacatepay.com/pages/transparents/refund) |
| Webhook | Eventos `transparent.completed/refunded/disputed/lost`; segredo e assinatura HMAC; reenvios e duplicatas (fica para a etapa 09) | [webhooks](https://docs.abacatepay.com/pages/notificacoes/webhooks), [segurança](https://docs.abacatepay.com/pages/notificacoes/seguranca) |

**O que não está documentado e por isso não foi inventado:**

- cancelamento de Pix;
- estorno parcial;
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
  - duplo clique e concorrência.

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
7. Concluir a etapa 09 (webhook com assinatura, pendências e estorno).
8. Só então usar a chave de produção, com `FILA_ABACATEPAY_PRODUCTION_VALIDATED=true`, registrando quem validou e quando.
