# ADR-0020 — Comanda, atendimento e outbox de eventos

- Situação: Aceita (etapa 05)
- Data: 2026-11-13

## Contexto

A etapa 05 muda a comanda depois da entrada na fila:

- o cliente troca serviços enquanto aguarda;
- depois da chamada, o cliente pede alterações e a equipe aprova ou recusa;
- a equipe conclui ou interrompe o atendimento;
- o dono revisa interrupções e corrige comandas com valor fechado.

Tudo isso disputa com outros comandos (a cliente e a dona editando ao mesmo tempo, a conclusão correndo junto com uma solicitação). O valor cobrado precisa ser explicável item a item, e a cadeira precisa ficar livre mesmo com o pagamento pendente. A etapa também pede que os eventos de mudança sejam gravados na outbox, na mesma transação da mudança.

## Decisão

1. **Módulos e travas.**
   - `atendimento` é dono de `service_order`, `order_item`, `change_request`, `change_request_addition` e `order_adjustment`, e expõe as rotas `/orders/...`.
   - Ele não lê a tabela de participação: pede ao módulo `fila` pela interface `atendimento.Visits` (`LockVisit`, `QueueChanged`).
   - A ordem de travas continua **fila → participação → comanda** (ADR-0019). Comandos sobre comanda já fora da fila (revisão e correção) travam só a comanda.
   - Concluir e interromper são rotas de participação, no módulo `fila`, que chama `atendimento.Complete` ou `atendimento.Interrupt` na mesma transação.
2. **Profissional do atendimento na comanda.**
   - `service_order.professional_id` define o escopo `own_attendance`. É copiado da fila na entrada e muda junto na transferência.
   - Profissional de outra fila recebe `404` (C-CMD-07).
3. **Elegibilidade de pontos.** `loyalty_eligible` é copiado da participação para a comanda (snapshot). Comanda interrompida (`interrupted_at`) nunca gera pontos.
4. **Total explicável, garantido pelo banco.**
   - Um gatilho de restrição adiado (`check_order_total`, conferido no COMMIT) exige:
     - comanda aberta ou em revisão: total = soma dos itens não retirados, sem nenhum item cobrado;
     - valor fechado: total = soma dos itens com `charged = true`.
   - Item cobrado é sempre item realizado (`CHECK`).
   - Valor fechado (`total_frozen_at`) existe exatamente fora de `open` e `pending_review`.
5. **Versão esperada em tudo.**
   - Todo comando de comanda usa `expected_version` da comanda. Conflito devolve `409 version_conflict` com a comanda atual e não sobrescreve nada.
   - Concluir e interromper exigem as duas versões (participação e comanda).
   - Criar ou decidir uma solicitação sobe a versão da comanda (a visão muda), mas não o total.
6. **Solicitações.**
   - Índice único parcial: uma pendente por comanda.
   - A inclusão guarda o snapshot de preço visto pelo cliente, e é esse preço que vale na aprovação. Na aprovação, o serviço é conferido de novo (ativo e feito pelo profissional).
   - Cancelamento, ausência e interrupção anulam a pendente (`voided`, ator "system").
7. **Interrupção.**
   - O profissional informa o resultado de cada item (`reported_outcome`). O dono decide no fechamento: só `fully_performed` é cobrado.
   - Soma zero encerra com `interruption_zero_total`, sem pagamento.
   - A participação continua `interrupted`.
8. **Correção depois de fechado (T-O-11).**
   - Só o dono, reautenticado, com motivo, e só por item:
     - retirar a cobrança de um item;
     - incluir um serviço feito, pelo preço atual do catálogo.
   - Cada correção grava uma linha imutável em `order_adjustment` e auditoria `T-O-11`.
   - Corrigir até zero encerra com o novo motivo `adjustment_zero_total`.
   - O bloqueio por cobrança Pix ativa ou desconhecida passa pela interface `ChargeGuard`. Como as cobranças só existem a partir da etapa 08, até lá não há cobrança; os testes simulam uma para provar o bloqueio.
   - Por não existirem recebimentos ainda, a correção vale só para `awaiting_payment`.
9. **Auditoria com versão.** `from_state`/`to_state` levam estado e versão (`open@2` → `open@3`), sem nick nem e-mail.
10. **Outbox (ADR-0010).**
    - Tabela `outbox`, dona `internal/platform/outbox`. Cada mudança grava um registro por audiência, com `pg_current_xact_id()` e a visão já projetada:
      - `visit`, com a conta ou o visitante dono da visita;
      - `professional`, com o profissional;
      - `owner`;
      - `public`.
    - A visão pública da fila é montada pela projeção pública, nunca pela da equipe com campos apagados.
    - Gravam eventos todos os comandos de participação e comanda, incluindo os da etapa 04, porque agora mexem também em comanda e solicitação.
    - A versão do evento de fila é `versão da fila + queue_counter.activity_seq`: as duas só crescem, então a soma também.
11. **Conflito com a etapa 04.**
    - Antes, uma comanda encerrada sem cobrança guardava o total dos serviços aceitos. Agora o valor de uma comanda encerrada é o cobrado (zero), com `total_frozen_at`.
    - A migração 00010 corrige as linhas existentes sem apagar itens.

## Alternativas consideradas

- **Rotas de comanda no módulo `fila`:** menos interfaces, mas a fila passaria a conhecer as regras de preço e de itens.
- **Conferir o total só no código:** mais simples, mas uma correção manual no banco ou um erro futuro deixaria um valor inexplicável sem ninguém perceber.
- **Uma linha de evento só, filtrada na entrega:** a entrega teria de decidir o que cada audiência vê. Projetar ao gravar segue o contrato (eventos-sse.md §3) e evita vazar dados.
- **Versão do evento de fila = versão da fila:** a versão não muda quando alguém entra ou sai, e o cliente descartaria o evento.

## Consequências

- A etapa 06 lê a outbox por `xact_id`, publica no `event_log` e apaga as linhas publicadas. Até lá as linhas só se acumulam.
- Ainda sem eventos: abrir, pausar e fechar a fila e o sinal de indisponibilidade (módulo `estabelecimento`) ficam para a etapa 06. `pendencies.updated` e `listPendencies` ficam para a etapa 09, junto com as pendências financeiras. Até lá, o dono vê as interrupções em revisão em `listOrders?status=pending_review`.
- A etapa 08 implementa `ChargeGuard` com as cobranças reais. A etapa 09 estende a correção para comandas com recebimento: excesso vira pendência, falta volta a aguardar pagamento.
- A etapa 10 credita pontos só de comanda com `loyalty_eligible` e sem `interrupted_at`.
