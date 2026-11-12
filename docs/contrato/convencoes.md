# Convenções do contrato HTTP

Este documento explica as regras que valem para **todas** as rotas de [openapi.yaml](openapi.yaml).
O OpenAPI é a fonte única: o backend Go e o frontend TypeScript geram tipos a partir dele (ADR-0008).
Se este texto e o OpenAPI discordarem, o OpenAPI vence e este texto deve ser corrigido.

## 1. Versão e base

- Prefixo `/v1`. Mudança incompatível cria `/v2`; mudança compatível (campo novo opcional, rota nova) fica em `/v1`.
- Frontend e API são servidos **na mesma origem** (proxy reverso). Ver ADR-0006.
- Corpo sempre `application/json; charset=utf-8`, exceto erros (`application/problem+json`) e SSE (`text/event-stream`).
- Requisição com `Content-Type` diferente em método com corpo → `415 unsupported_media_type`.
- Tamanho máximo do corpo: 64 KiB (webhook: 256 KiB). Acima disso → `413 payload_too_large`.

## 2. Envelopes de sucesso

| Caso | Formato |
|---|---|
| Um recurso | `{ "data": { ... } }` |
| Lista paginada | `{ "data": [ ... ], "page": { "next_cursor": "…" \| null, "has_more": bool } }` |
| Lista curta não paginada | `{ "data": [ ... ] }` (catálogo, profissionais, prêmios — limitados por loja) |
| Leitura usada como snapshot SSE | inclui também `"cursor": "<StreamCursor>"` (ver [eventos-sse.md](eventos-sse.md)) |
| Sem conteúdo | `204` sem corpo |

Respostas nunca trazem campos que o ator não pode ver (ex.: `identity_kind` só para equipe; `points_expected` só para o dono da visita).

## 3. Envelope de erro (RFC 9457)

```json
{
  "type": "/problems/version_conflict",
  "title": "A comanda mudou",
  "status": 409,
  "detail": "Outra pessoa alterou esta comanda. Confira os dados atualizados.",
  "code": "version_conflict",
  "request_id": "01JD…",
  "errors": [],
  "current": { "...": "visão atual do recurso para este ator" }
}
```

- `code` é estável e é o que o frontend usa para decidir o que mostrar (lista em `ErrorCode`).
- `title`/`detail` em português, sem stack trace, SQL, IDs internos de terceiros, e-mail ou nick alheio.
- `request_id` também vai no cabeçalho `X-Request-Id` e nos logs, para diagnóstico.
- `errors[]` só em `400 validation_failed`: `field` em JSON Pointer (`/service_ids/0`) e `code` (`required`, `unknown_field`, …).
- `current` em `409`: a mesma visão que o GET daquele recurso devolveria **para este ator**. É assim que "tela antiga recebe conflito e os dados atuais".

### Mapa de status

| Status | Quando | Exemplos de `code` |
|---|---|---|
| 400 | Formato inválido, campo desconhecido, faltando chave de idempotência | `validation_failed`, `idempotency_key_missing` |
| 401 | Sem sessão válida / credencial incorreta | `unauthenticated`, `invalid_credentials`, `recovery_code_invalid` |
| 403 | Sessão válida sem permissão; CSRF/origem; reautenticação | `forbidden`, `csrf_failed`, `origin_not_allowed`, `reauthentication_required` |
| 404 | Não existe **ou** está fora do escopo do ator | `not_found` |
| 409 | Estado/versão incompatível, regra de negócio sobre o estado atual | `version_conflict`, `invalid_transition`, `queue_not_open`, … |
| 413 / 415 | Corpo grande / tipo errado | `payload_too_large`, `unsupported_media_type` |
| 422 | Mesma `Idempotency-Key` com conteúdo diferente | `idempotency_key_reused` |
| 429 | Limite de taxa/tentativas | `rate_limited` (com `Retry-After`) |
| 500 / 503 | Falha inesperada / dependência fora | `internal_error`, `service_unavailable`, `provider_unavailable` |

**Regra de 404 vs 403:** recurso de outra loja, ou de outro cliente, responde **404** (não revela que existe).
403 é usado quando o ator pode saber que o recurso existe mas não pode agir (ex.: profissional tentando pausar a própria fila).

## 4. Identificadores

- Todos os IDs são UUIDv7 gerados pelo servidor, tratados como texto opaco pelo frontend.
- UUIDv7 é ordenável por tempo (bom para índices); **não** é segredo e **não** prova acesso. Toda rota confere ator, loja e propriedade (ADR-0006, ADR-0014).
- Senha da fila (`ticket_code`) é para humanos: letra da fila + número diário de 3 dígitos (ex.: `A017`), sequencial por fila e por dia no fuso da loja. Não identifica a pessoa.
- Segredos (token de sessão, código de recuperação, tokens de e-mail/convite, protocolo de exclusão) nunca são IDs e nunca aparecem em URL.

## 5. Dinheiro

- Sempre `Money = { "amount_cents": <inteiro ≥ 0>, "currency": "BRL" }`. Nunca número decimal, nunca string formatada.
- No banco: `bigint` em centavos. Soma e comparação só com inteiros.
- O cliente **nunca envia preço ou total**. Os corpos de pedido não têm esses campos e `additionalProperties: false` faz o servidor rejeitar com `400 validation_failed` (`unknown_field`) qualquer tentativa.
- Exceção explícita: `registerInPersonReceipt.amount_cents` — a equipe **confirma** o valor recebido; o servidor compara com o devido e recusa diferença (`payment_amount_mismatch`). O valor não define o total.
- Formatação `R$ 45,00` é responsabilidade da interface.

## 6. Datas e horas

- Instantes em RFC 3339 **UTC com sufixo `Z`** (ex.: `2026-11-12T18:04:05.123Z`).
- O relógio que vale é o do servidor/banco. Prazos (tolerância de 10 min, 24 h da reserva, expirações de sessão) são calculados e gravados no servidor; o frontend só exibe contagem regressiva a partir de `tolerance_ends_at`/`expires_at`.
- Cada loja tem `timezone` IANA (padrão `America/Sao_Paulo`), usado apenas para "dia operacional" (numeração das senhas) e para exibição.
- Durações em minutos inteiros (`duration_minutes`).

## 7. Paginação

- Por cursor opaco: `?limit=1..100` (padrão 20) e `?cursor=<page.next_cursor>`.
- Ordem estável e documentada por rota (histórico: mais recente primeiro; extrato: mais recente primeiro; auditoria: mais recente primeiro).
- Cursor inválido → `400 validation_failed`. Cursor não carrega permissão: o filtro de escopo é reaplicado a cada página.

## 8. Idempotência de comandos

Todo `POST/PUT/PATCH/DELETE` sob `/v1`, **exceto** `/v1/auth/*`, `/v1/establishments/{id}/visitor-sessions*`, `/v1/visitor-sessions/*`, `/v1/deletion-requests/status` e `/v1/webhooks/*`, exige o cabeçalho `Idempotency-Key`.

1. O frontend gera a chave **uma vez por intenção** (ex.: ao clicar em "Chamar próximo") e reutiliza a mesma chave em reenvios daquela intenção (timeout, rede caiu). Nova intenção = nova chave.
2. Escopo da chave = **ator autenticado + operação (método + rota modelo)**. A mesma chave usada por outro ator não colide.
3. O servidor guarda: escopo, chave, impressão digital do pedido (SHA-256 do corpo canônico + parâmetros de rota), status e corpo da resposta, por **24 horas**.
4. Repetição com mesma impressão → devolve a **mesma resposta** com cabeçalho `Idempotent-Replayed: true`, sem novo efeito.
5. Mesma chave com impressão diferente → `422 idempotency_key_reused`.
6. Pedido ainda em execução com a mesma chave → `409 idempotency_in_progress` (cliente espera e tenta de novo).
7. Respostas com segredo exibido uma vez **não** são armazenadas em claro. As rotas de visitante (código de recuperação) ficam fora desta regra e têm proteção própria (ADR-0011). Em `requestAccountDeletion`, a repetição idempotente devolve o protocolo **mascarado** (só o sufixo); o protocolo completo aparece uma vez e é enviado ao e-mail verificado quando houver serviço de e-mail.
8. Idempotência local **não** garante nada no provedor de pagamento: a criação de cobrança tem regra própria (uma tentativa ativa por comanda, estado `unknown` e reconciliação — ADR-0007).

Além da chave, as **restrições do banco** (índices únicos parciais) garantem invariantes mesmo com chaves diferentes: uma participação ativa por identidade e loja, uma pessoa chamada/em atendimento por fila, uma cobrança Pix ativa por comanda, uma reserva ativa por conta e loja, um crédito de pontos por comanda.

## 9. Concorrência otimista (versão esperada)

- Agregados mutáveis têm `version` (inteiro que sobe a cada mudança): fila, participação, comanda, serviço, profissional, programa, prêmio, reserva, loja.
- Comandos sobre um agregado existente enviam `expected_version`. Se não bate → `409 version_conflict` com `current`.
- Nada é sobrescrito "por cima": quem perdeu a disputa vê o estado novo e decide de novo.
- `callNext` não exige versão: o servidor escolhe o próximo pelo FIFO com bloqueio de linha; a disputa é resolvida pelo banco (`professional_busy`, `no_waiting_participant`).
- Comandos que mexem em dois agregados (concluir/interromper: participação + comanda) enviam as duas versões.

## 10. Sessões, cookies, CSRF e origem

| Item | Valor |
|---|---|
| Cookie de conta | `__Host-fila_session` — `Secure; HttpOnly; SameSite=Lax; Path=/` |
| Cookie de visitante | `__Host-fila_visitor` — idem; um por loja guardado no servidor como lista vinculada ao cookie |
| Token no navegador | Nunca em `localStorage`/`sessionStorage`; o JavaScript não lê os cookies |
| CSRF | `X-CSRF-Token` obtido em `GET /v1/session`, vinculado (HMAC) às sessões presentes; obrigatório em método não seguro quando há sessão |
| Origem | `Origin` deve estar na lista de origens permitidas em **todo** método não seguro (exceto webhook). Ausente ou diferente → `403 origin_not_allowed` |
| Rotas sem sessão (login, cadastro, visitante, reset) | Protegidas por `Origin` + `Content-Type: application/json` obrigatório (força preflight em origem cruzada) + limites de taxa |
| Reautenticação | Operações com `x-reauth: true` exigem `reauthenticate` nos últimos 10 min → senão `403 reauthentication_required` |

Durações (regras R-CONTA-06/07 e R-VIS-02/03):

| Credencial | Inatividade | Absoluta | Outros fins |
|---|---|---|---|
| Sessão de cliente | 7 dias | 30 dias desde o login | logout, troca/reset de senha, exclusão |
| Sessão de equipe | — | 12 horas desde o login | logout, desativação do vínculo, troca/reset de senha |
| Visitante | 7 dias | 30 dias desde a criação | até 24 h após encerramento da comanda; recuperação revoga a anterior |
| Token de verificação de e-mail | — | 24 horas, uso único | — |
| Token de redefinição de senha | — | 30 minutos, uso único | — |
| Convite de profissional | — | 24 horas, uso único | revogação pelo dono |

## 11. Validação de texto

- Textos livres (`nick`, nomes, motivos, descrições) são normalizados em NFC, sem caracteres de controle, com tamanho máximo do esquema.
- São **dados**, nunca HTML: o frontend renderiza como texto (React escapa por padrão; proibido `dangerouslySetInnerHTML` com esses campos).
- Unicidade de e-mail: comparação case-insensitive após normalização.

## 12. Autorização declarada no contrato (`x-authz`)

Cada operação lista os pares `ator/escopo` permitidos. O backend implementa um verificador por escopo e **nega tudo que não estiver listado**.

| Ator | Quem é |
|---|---|
| `public` | Qualquer requisição (sem identidade) |
| `visitor` | Credencial de visitante válida para a loja da rota |
| `customer` | Sessão de conta ativa (contexto cliente ou equipe) agindo como cliente |
| `professional` | Sessão de contexto **staff** com vínculo ativo de profissional na loja da rota |
| `owner` | Sessão de contexto **staff** com vínculo ativo de dono na loja da rota |
| `provider` | Notificação do provedor de pagamento autenticada conforme contrato oficial |
| `system` / `operator` | Worker interno / processo controlado de linha de comando (não são rotas HTTP) |

| Escopo | Significado |
|---|---|
| `none` | Sem condição adicional |
| `self` | O próprio titular (conta ou credencial) |
| `visit_owner` | A identidade (visitante ou conta) que criou a visita, com credencial ainda válida |
| `own_queue` | Participação/fila do próprio profissional (após transferência, o destino) |
| `own_attendance` | Comanda de atendimento do próprio profissional |
| `establishment_member` | Qualquer vínculo ativo de equipe nesta loja |
| `establishment` | Dono desta loja |
| `provider_signature` | Autenticidade verificada; sem ela, só dispara consulta autenticada |
| `protocol_holder` | Quem possui o protocolo secreto de exclusão (só lê situação) |

O papel **nunca** vem do corpo da requisição. Um vínculo de equipe só concede papel de equipe quando a sessão é de contexto `staff` (ADR-0006).
