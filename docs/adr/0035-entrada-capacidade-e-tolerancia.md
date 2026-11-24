# ADR-0035 — Entrada na fila separada da operação, capacidade e tolerância configurável

- Situação: Aceita
- Data: 2026-11-21
- Substitui: R-FILA-09 e R-FILA-10 (tolerância fixa de 10 min), R-FILA-18 (sem limite de vagas); altera R-FILA-07 (profissional controla a entrada da própria fila).

## Contexto

Hoje `queue.status` (aberta, pausada, fechada) controla ao mesmo tempo novas entradas, chamadas e transições. Fechar exige a fila vazia. A equipe precisa bloquear novas entradas sem parar de atender, limitar o tamanho da fila e ajustar a tolerância.

## Decisão

1. Três conceitos (migration 00027):
   - **operação** = `queue.status` (sem mudança de significado);
   - **política de entrada** = `queue.admission` (`open` | `closed`, fechada manualmente);
   - **situação calculada** = `entry_state`: `operation_paused`, `operation_closed`, `closed_manual`, `full`, `accepting` — nesta ordem de precedência. A lotação nunca é gravada.
2. **Fechar entrada / Reabrir entrada** (T-Q-06/07): bloqueia só novas participações. Quem está continua, a ordem não muda, a equipe chama, inicia, conclui e recebe. Uma desistência **não** reabre. O dono controla qualquer fila; o profissional, a própria, se `professionals_control_admission` estiver ligada (**padrão ligada**, mudança documentada). Essa permissão não libera capacidade, transferência, preços, outras filas nem encerrar a operação. Abrir uma operação **encerrada** (novo expediente) começa com a entrada aberta; pausar e retomar não mexem na entrada.
3. **Capacidade**: limite opcional de participações **pendentes** (aguardando + chamado; em atendimento não ocupa vaga). Padrão da loja `default_queue_capacity` (desligado = sem limite, como antes; 1–200) e limite próprio por fila (`queue.capacity`, T-Q-08, só o dono). Três cortes = **uma** vaga (e 90 minutos a mais na estimativa). Reduzir o limite não tira ninguém.
4. **Concorrência**: entrada e transferência travam a fila (`FOR UPDATE`, ordem por id) e contam as pendentes **na mesma transação**: duas pessoas pela última vaga entram uma de cada vez, e só uma passa. A transferência para fila lotada ou com entrada fechada é recusada antes de qualquer mudança: a pessoa fica no mesmo lugar.
5. **Mensagens**: lotada — "A fila está cheia no momento. Tente novamente quando uma vaga for liberada."; entrada fechada — "A entrada na fila foi encerrada por enquanto. Quem já está na fila será atendido normalmente."; reaberta e ainda lotada — "Entrada reaberta. Novas participações serão aceitas quando houver vaga." Liberar vaga reabre a entrada **sozinha** quando não há bloqueio manual nem operacional.
6. **Tolerância configurável** `call_tolerance_minutes` (padrão 10, 1–60). Cada chamada grava `tolerance_minutes` e o prazo calculado pelo servidor; mudar a configuração vale para chamadas futuras. Atualizar a página não reinicia; o aviso de atraso não aumenta o prazo; a ausência continua sendo ação explícita da equipe depois do prazo. Uma nova chamada legítima (incluindo trocar de lugar com quem estava chamado, T-P-11) usa a tolerância vigente. Chamadas existentes na migração ficam com 10 minutos.
7. **Tempo real**: abrir/fechar entrada, capacidade (da fila ou padrão da loja) e mudanças de quantidade emitem `queue.updated` e `queue.public.updated`. O público vê só `entry_state` (sem número de vagas nem dados internos).

## Alternativas

- Novo estado "aberta sem entrada" em `queue.status`: misturaria eixos independentes e multiplicaria as transições.
- Contar a capacidade por unidades de serviço: penalizaria o grupo sem refletir a cadeira; a duração já aparece na estimativa.

## Consequências

- Telas usam os rótulos "Fechar entrada", "Reabrir entrada", "Pausar operação", "Retomar operação", "Encerrar operação", mostrando só o que cabe no estado atual.
