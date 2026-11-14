# Eventos SSE — atualizações servidor → navegador

Comandos continuam sendo HTTP. SSE só **avisa** o navegador de mudanças que já estão gravadas no PostgreSQL (ADR-0004, ADR-0010).
Polling periódico não é o mecanismo normal de atualização; o frontend só relê um recurso via GET para se recuperar (snapshot).

## 1. Conexão

```
GET /v1/stream?establishment_id=<id>&channels=public,visit[,account|professional|owner][&cursor=<cursor>]
Last-Event-ID: <cursor>        (enviado automaticamente pelo navegador ao reconectar)
Cookie: __Host-fila_session / __Host-fila_visitor
```

- **Uma conexão por aba**, aberta por um único serviço do frontend que distribui eventos para as telas. Componentes não abrem conexões próprias.
- Credenciais só via cookie. Nada durável na URL; `cursor` é posição de leitura, não dá acesso.
- O servidor calcula quais canais o ator pode receber. Canais pedidos e não autorizados aparecem em `denied_channels` no snapshot (não é erro).
- Cabeçalhos de resposta: `Content-Type: text/event-stream`, `Cache-Control: no-cache, no-transform`, `X-Accel-Buffering: no`.

## 2. Formato de cada mensagem

```
id: <cursor opaco>
event: order.updated
data: {"schema_version":1,"event_id":"…","type":"order.updated","occurred_at":"2026-11-12T18:04:05.123Z",
data:  "establishment_id":"…","audience":"visit","resource":{"type":"order","id":"…","version":7},"data":{…}}
```

`data` segue `EventEnvelope` do OpenAPI:

| Campo | Significado |
|---|---|
| `schema_version` | Versão do envelope/payload. Hoje `1`. Mudança incompatível → nova versão, servidor envia a versão que o cliente declarar suportar (parâmetro futuro `v`), mantendo a anterior por um ciclo de deploy. |
| `event_id` | UUID do evento (deduplicação no cliente). |
| `type` | Tipo (tabela abaixo). |
| `audience` | Para quem o payload foi projetado. |
| `resource.version` | Versão do agregado **após** a mudança. Cliente aplica só se for maior do que a que já tem. |
| `data` | Visão **completa** do recurso para aquela audiência (mesmo esquema do GET). Não é um "diff". |

Como o evento carrega o estado completo e a versão, **receber o mesmo evento duas vezes, ou fora de ordem, não muda o resultado**: o cliente fica sempre com a maior versão.

## 3. Audiências

| Audiência | Quem recebe | Dados permitidos |
|---|---|---|
| `public` | Qualquer conexão da loja | `PublicQueue`: senhas, status, quantidade, estimativa. **Nunca** nick, serviços, valores, contatos. |
| `visit` | Credencial que é `visit_owner` da visita (visitante ou conta) | Participação, comanda e pagamento **da própria visita**. |
| `account` | Sessão da conta titular | Pontos, extrato e reservas **da própria conta** nesta loja. |
| `professional` | Profissional com vínculo ativo, só eventos com `professional_id` igual ao dele | Sua fila (com nick e serviços), suas comandas, reservas da loja para entrega. |
| `owner` | Dono com vínculo ativo da loja | Tudo da loja, incluindo pendências financeiras e exclusões. Também recebe eventos `professional`. |

Cada mudança grava **um registro de evento por audiência afetada**, já com o payload projetado e filtrado para ela, na mesma transação do comando (outbox). A entrega reconfere a audiência contra a sessão **no momento do envio**.

## 4. Catálogo de eventos (v1)

| Tipo | Audiências | Recurso | Gerado por (exemplos) |
|---|---|---|---|
| `stream.snapshot` | todas | `stream` | Início da conexão sem cursor, cursor expirado/inválido, mudança de época |
| `stream.session_ended` | a conexão afetada | `stream` | Logout, troca/reset de senha, desativação de profissional, recuperação de visitante, exclusão, expiração |
| `queue.public.updated` | public | queue | Entrada, saída, chamada, início, conclusão, transferência, abrir/pausar/fechar |
| `queue.updated` | professional, owner | queue | Idem + sinal de indisponibilidade |
| `participation.updated` | visit, professional, owner | participation | Todas as transições T-P-* |
| `order.updated` | visit, professional, owner | order | T-O-*, aprovação/recusa de solicitação |
| `change_request.updated` | visit, professional, owner | change_request | T-CR-* |
| `payment.updated` | visit, professional, owner | order | Cobrança Pix, recebimento, conferência, estorno |
| `financial_issue.updated` | owner | financial_issue | Abertura/resolução de pendência financeira |
| `pendencies.updated` | owner | establishment | Qualquer mudança nos contadores do painel |
| `loyalty.updated` | account | loyalty_account | Crédito, reversão, reserva, liberação, consumo |
| `reward_reservation.updated` | account, professional, owner | reward_reservation | T-RW-* |
| `deletion_request.updated` | owner | deletion_request | Exclusão com bloqueio na loja |

O catálogo de prêmios e o catálogo de serviços **não** são enviados por SSE: são lidos ao abrir a tela. Disputa de estoque é resolvida no comando (`409 reward_out_of_stock`).

## 5. Cursor e ordem (sem janela de perda)

Problema: uma sequência (`bigserial`) é reservada **antes** do commit. A transação A pode pegar 10, a B pegar 11 e B commitar primeiro. Um leitor que já entregou 11 e guardou "cursor = 11" perderia o 10 quando A commitasse.

Solução adotada (ADR-0010):

1. O comando grava na tabela `outbox` dentro da sua transação, junto com `pg_current_xact_id()`.
2. Um único **publicador** (no worker, com advisory lock para só haver um ativo) lê apenas linhas cujo `xact_id` é **menor que o `xmin` do snapshot atual** (`pg_snapshot_xmin(pg_current_snapshot())`) — ou seja, de transações que certamente já terminaram. Nenhuma transação mais antiga pode aparecer depois.
3. Ele copia essas linhas, em ordem `(xact_id, id)`, para `event_log`, atribuindo `position` densa e crescente. **A posição é atribuída depois do commit**, então a ordem de `position` é a ordem em que os eventos ficaram visíveis.
4. Cursor = `base64url(epoch || position)`. `epoch` muda quando o banco é restaurado de backup (posições podem se repetir), invalidando cursores antigos.
5. Processos da API recebem aviso de novos eventos por `LISTEN/NOTIFY` e leem do `event_log` (não da memória de outro processo). Por isso **dois ou mais processos de API** entregam a mesma sequência. Escala horizontal só será afirmada depois do teste C-SSE-08.

## 6. Snapshot + assinatura sem perda

Ao abrir a conexão:

1. O processo da API **começa a ouvir** notificações e a guardar eventos novos num buffer daquela conexão.
2. Em uma transação `REPEATABLE READ`, lê o estado autorizado (filas, visita, pendências…) **e** `max(position)` do `event_log` = `P`.
3. Envia `stream.snapshot` com `id: cursor(P)`.
4. Envia do `event_log` tudo com `position > P` autorizado, depois o buffer, descartando posições já enviadas.

Um evento cuja mudança já estava no snapshot pode chegar de novo com posição > P (porque a posição é atribuída depois do commit). Não há problema: a versão do recurso é igual ou menor, e o cliente ignora. **Perda é impossível; duplicata é inofensiva.**

## 7. Reconexão e retomada

| Situação | Comportamento |
|---|---|
| Reconexão com cursor válido e retido | Reenvia eventos autorizados com `position >` cursor, depois segue ao vivo |
| Cursor mais antigo que a retenção (24 h) | `stream.snapshot` com `reason: cursor_expired` |
| Cursor ilegível | `stream.snapshot` com `reason: cursor_invalid` |
| Época diferente (banco restaurado) | `stream.snapshot` com `reason: epoch_changed` |
| Sem cursor | `stream.snapshot` com `reason: initial` |

Cliente:

- Reconecta com **backoff exponencial com jitter total**: espera aleatória entre 0 e `min(30 s, 1 s × 2^tentativa)`.
- Ao voltar à aba (`visibilitychange`) ou recuperar rede (`online`), reconecta **imediatamente** e trata o snapshot/replay **antes de habilitar botões** de ação.
- Enquanto desconectado, marca a tela como "dados podem estar desatualizados" e desabilita ações financeiras; comandos já enviados não são reenviados automaticamente se forem financeiros (R-WEB-03).
- Não há promessa de entrega com a aba fechada ou suspensa: ao voltar, sincroniza.

## 8. Heartbeat, limites e proxy

| Item | Valor inicial (ajustável na etapa 06/12) |
|---|---|
| Heartbeat | Comentário `: ping` a cada 15 s |
| `retry:` sugerido ao navegador | 3000 ms (o cliente aplica seu próprio backoff com jitter) |
| Revalidação da sessão | A cada 30 s, e imediatamente ao receber aviso interno de revogação |
| Vida máxima da conexão | 30 min; servidor encerra e o cliente reconecta com cursor |
| Buffer por conexão | 256 eventos; estourou → conexão encerrada (cliente lento reconecta com cursor) |
| Conexões por credencial | Máximo de 6 simultâneas; acima disso → 429 |
| Timeout de escrita | Por mensagem (5 s); o `WriteTimeout` global do servidor HTTP não se aplica a esta rota |
| Proxy reverso | `proxy_buffering off`, `proxy_read_timeout` > 2 × heartbeat, HTTP/2 recomendado |
| Retenção do `event_log` | 24 h (limpeza pelo worker) |

## 9. Segurança

- Autorização na abertura **e** a cada envio (audiência × sessão atual).
- Ao revogar a sessão (logout, senha, desativação, recuperação de visitante, exclusão), o servidor envia `stream.session_ended` e fecha a conexão em até 30 s (imediato quando o aviso interno chega).
- Replay também é filtrado por audiência: um cursor "emprestado" de outra pessoa não revela nada, porque cada registro tem audiência e a sessão é conferida.
- Payloads `public` são gerados a partir de uma projeção pública dedicada; nunca a partir da visão de equipe com campos removidos no último momento.

## 10. Rastreabilidade

Cada operação do OpenAPI lista em `x-events` os tipos que pode gerar. O script [`docs/tools/verificar_contrato.py`](../tools/verificar_contrato.py) confere que todos os tipos citados existem em `EventType` e que todo `EventType` está neste catálogo.

## 11. Implementação (etapa 06)

Detalhes de como o desenho acima roda; as decisões estão no [ADR-0021](../adr/0021-sse-publicador-hub-e-cursor.md).

| Peça | Onde | O que faz |
|---|---|---|
| Outbox | `backend/internal/platform/outbox` (`Record`) | Comando grava um evento por audiência na própria transação e manda o aviso `fila_outbox` (só chega no COMMIT) |
| Publicador | worker (`Publisher`) | Trava `event_stream_state`, lê da outbox só `xact_id < xmin`, grava no `event_log` com posição densa, apaga da outbox, avisa `fila_eventos` — tudo numa transação |
| Distribuidor | API (`Hub`, um por processo) | `LISTEN fila_eventos, fila_revogacao`; lê o `event_log` e entrega às conexões da loja; buffer de 256 por conexão |
| Rota | `backend/internal/fluxo` | `GET /v1/stream`: autorização, snapshot + cursor, replay, ao vivo, heartbeat, reconferência |
| Navegador | `frontend/src/stream` | `StreamClient` (reconexão) e `StreamProvider` (uma conexão por página) |

Mensagens do próprio fluxo:

- `stream.snapshot`: `id` = cursor do instante do snapshot; `audience` = a mais específica concedida (owner > professional > account > visit > public); `resource` = `{type: "stream", id: <id da conexão>, version: <posição>}`; `data` segue `StreamSnapshot` (`pendencies` vem preenchido para o dono desde a etapa 09; `loyalty` vem preenchido para o canal `account` desde a etapa 10).
- `stream.session_ended`: sem `id` (não muda o cursor); `data` = `{"reason": "session_revoked"}`. O servidor fecha a conexão logo depois; o navegador relê a sessão e abre outro fluxo com os canais que ainda tiver.

Versão de recurso nos eventos de fila: `queue.version` + contador de atividade da fila (as duas só crescem). Depois de um snapshot, o cliente recomeça as versões conhecidas.

Limites deste processo: as 6 conexões por credencial são contadas por processo da API (com N processos, até 6 × N).

Atraso: `xmin` é do **servidor** PostgreSQL inteiro. Uma transação longa em qualquer banco do mesmo servidor segura a publicação até terminar. Monitorar com o aviso do worker (> 10 s) ou `go -C backend run ./cmd/fila-admin eventos`.

Restauração de backup: depois de restaurar, rode `go -C backend run ./cmd/fila-admin nova-epoca` — todo navegador recebe um snapshot novo (`epoch_changed`) em vez de retomar por um cursor que pode apontar para eventos que não existem mais.

Proxy reverso (exemplo nginx):

```nginx
location /v1/stream {
    proxy_pass http://api;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;          # mensagens chegam na hora
    proxy_cache off;
    proxy_read_timeout 60s;       # > 2 × heartbeat (15 s)
    gzip off;
}
```

Em desenvolvimento o proxy do Vite já repassa `/v1/stream` sem segurar as mensagens (testado no E2E da etapa 06).
