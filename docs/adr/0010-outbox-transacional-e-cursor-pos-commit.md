# ADR-0010 — Outbox transacional e cursor de eventos pós-commit

- Situação: Aceita (etapa 00)
- Data: 2026-11-12

## Contexto

Eventos só podem confirmar dados já persistidos, e a reconexão SSE não pode perder mudanças. Sequências do PostgreSQL são alocadas antes do commit, então "id crescente" não equivale a "ordem de visibilidade".

## Decisão

- O comando grava evento(s) por audiência na tabela `outbox` **na mesma transação**, com `pg_current_xact_id()`.
- Um publicador único (advisory lock no worker) move para `event_log` apenas linhas de transações já terminadas (`xact_id < pg_snapshot_xmin(pg_current_snapshot())`), atribuindo `position` densa nessa hora. Lease e retry: se o publicador cair, outro assume o lock; reprocessar é seguro (chave única por linha de outbox).
- Cursor SSE = época + posição. A época muda após restauração de backup.
- A API escuta `LISTEN/NOTIFY` e lê do `event_log`, então vários processos de API veem a mesma sequência.
- Snapshot em `REPEATABLE READ` junto com `max(position)`; duplicatas são inofensivas porque eventos carregam estado completo + versão.
- Retenção do `event_log`: 24 h; cursor mais antigo → snapshot.
- Monitorar atraso da outbox (idade da linha mais antiga não publicada).
- Consumidores internos (ex.: crédito de pontos) também são idempotentes por chave de origem.

## Alternativas consideradas

- Publicar direto do processo da API após commit: perde eventos em queda entre commit e envio.
- Cursor = `id` da outbox: perde eventos de transações longas (descrito em `eventos-sse.md` §5).
- Broker externo: complexidade não justificada no piloto.

## Consequências

- Latência do evento = intervalo do publicador + NOTIFY (alvo inicial < 1 s, a medir na etapa 06).
- Transações longas atrasam a publicação de todos (o horizonte `xmin` não avança): manter transações curtas e monitorar.
