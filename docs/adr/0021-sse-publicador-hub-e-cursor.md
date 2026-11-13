# ADR-0021 — SSE: publicador, distribuidor por processo e cursor

- Situação: Aceita (etapa 06)
- Data: 2026-11-13

## Contexto

O ADR-0010 definiu o caminho dos eventos: a outbox gravada na transação do comando, um publicador que move para o `event_log` só o que já terminou, e o cursor época + posição. Faltava decidir como isso roda de fato:

- quem garante um publicador por vez;
- como a API entrega ao vivo para muitas conexões sem perder nada entre o snapshot e a assinatura;
- como lidar com cliente lento, revogação de sessão e prazos do servidor HTTP;
- como o navegador reconecta.

## Decisão

1. **Publicador (worker), serializado por uma linha do banco.**
   - Cada publicação trava `event_stream_state` (`FOR UPDATE`). Só então lê da outbox as linhas com `xact_id < pg_snapshot_xmin(pg_current_snapshot())`, na ordem `(xact_id, id)`.
   - Na mesma transação: grava no `event_log` com posição densa, apaga da outbox e avança `last_position`.
   - Pode haver vários workers: o segundo espera o bloqueio e depois enxerga o que o primeiro já apagou.
   - "Lease": `statement_timeout` de 10 s por publicação. Se o worker cair, o banco solta o bloqueio, desfaz tudo e a próxima tentativa refaz o lote inteiro.
   - `event_id` único protege contra publicar duas vezes.
   - O worker acorda com o aviso `fila_outbox` (enviado no COMMIT do comando) e também a cada 1 s.
   - Em caso de erro, tenta de novo com espera crescente até 30 s.
2. **Distribuidor (Hub) por processo da API.**
   - Uma conexão dedicada com `LISTEN fila_eventos, fila_revogacao`. A cada aviso, e também a cada 2 s como rede de segurança, lê o `event_log` a partir da última posição que viu e entrega às conexões daquela loja.
   - O Hub não guarda estado de verdade: dois processos de API entregam a mesma sequência, e reiniciar um processo não perde nada.
   - Cada conexão tem um buffer de 256 eventos. Se encher (cliente lento), a conexão é encerrada e o cliente volta pelo cursor.
3. **Conexão sem janela de perda.** A ordem dos passos é:
   1. registrar no Hub;
   2. ler, numa transação `REPEATABLE READ`, o snapshot autorizado e a posição P;
   3. reenviar do `event_log` as posições depois de P (ou depois do cursor válido do cliente), já filtradas por audiência no próprio SQL;
   4. seguir com o que o Hub entrega, descartando posições já enviadas.

   Como só um publicador grava por vez, a ordem das posições é a ordem de commit no `event_log`.
4. **Autorização por conexão.**
   - Os canais concedidos vêm da sessão (cookies), nunca do pedido. Os não autorizados aparecem em `denied_channels`.
   - Cada evento é conferido de novo, no envio, contra as permissões atuais da conexão.
   - A sessão é reconferida a cada 30 s e logo após qualquer aviso `fila_revogacao`. Esse aviso não leva dado nenhum e é enviado por logout, troca ou redefinição de senha, recuperação de visitante em outro aparelho e fim de acesso da equipe. Quando o Hub reconecta a escuta, também pede reconferência a todos, porque um aviso pode ter se perdido no intervalo.
   - Se a sessão perdeu algo que tinha, o servidor envia `stream.session_ended` e fecha.
5. **Prazos e limites.**
   - Cada mensagem tem prazo próprio de 5 s, via `http.ResponseController`. O `WriteTimeout` global da API não derruba o fluxo, e o prazo de leitura é zerado.
   - Heartbeat a cada 15 s; vida máxima de 30 min.
   - No máximo 6 conexões por credencial, contadas **em cada processo**: com N processos, o máximo real é 6 × N.
   - Ao desligar, a API encerra os fluxos abertos (`RegisterOnShutdown`) para os clientes reconectarem em outro processo.
6. **Cursor e retenção.**
   - O cursor é `base64url(época 16 bytes + posição 8 bytes)`, com até 128 caracteres. Não é credencial: pode ir no endereço ou em `Last-Event-ID`.
   - Ilegível ou de posição futura → `cursor_invalid`. Época diferente → `epoch_changed`. Anterior ao evento mais antigo guardado → `cursor_expired`.
   - O worker apaga eventos com mais de 24 h, sempre cortando por posição.
   - Depois de restaurar um backup, rode `fila-admin nova-epoca`.
7. **Versão dos recursos.**
   - Cada evento carrega o recurso inteiro e a versão dele. O cliente só aplica uma versão maior.
   - Para filas, a versão é `queue.version` (abrir, pausar, fechar) + `queue_counter.activity_seq` (gente entrando e saindo). As duas só crescem.
   - Depois de um snapshot, as versões conhecidas recomeçam do zero.
8. **Navegador.**
   - Uma conexão por página (loja ou gestão), aberta por `StreamProvider`. Componentes não abrem conexão própria.
   - Ao perder a conexão, o cliente espera um tempo aleatório entre 0 e min(30 s, 1 s × 2ⁿ) ("jitter total") e reconecta com o cursor.
   - Ao voltar para a aba ou recuperar a rede, reconecta na hora.
   - Enquanto desconectado, a tela avisa que os dados podem estar desatualizados.
   - Nada é prometido com o navegador fechado.
9. **Monitoramento.**
   - O worker registra um aviso quando o evento mais antigo da outbox espera mais de 10 s.
   - `fila-admin eventos` mostra quantos eventos esperam, há quanto tempo, a última posição e a época.

## Alternativas consideradas

- **Advisory lock de sessão (como dizia o ADR-0010):** também funciona, mas exige uma conexão presa ao "líder" e cuidado ao trocar de líder. O bloqueio da linha por transação faz o mesmo papel, sem estado fora da transação.
- **Cada conexão consultando o `event_log` a cada aviso:** mais simples, mas multiplica as consultas pelo número de conexões. O Hub lê uma vez por processo.
- **Guardar os eventos na memória da API:** perderia eventos em reinício e não funcionaria com dois processos.
- **Reconexão nativa do `EventSource`:** usa um intervalo fixo, sem jitter, e nada de reconexão imediata ao voltar para a aba. Por isso o cliente fecha a conexão e aplica o próprio backoff.

## Consequências

- **Transações longas atrasam a publicação de todos.** O horizonte `xmin` é do servidor PostgreSQL inteiro, não só deste banco: uma transação aberta em **outro** banco do mesmo servidor também segura a publicação. Em produção, o banco do sistema não deve dividir o servidor com cargas de transações longas; o atraso é monitorado.
- Latência medida no E2E local: comando → navegador em cerca de 50 ms, passando por worker, aviso do banco e proxy do Vite.
- O proxy reverso precisa deixar o fluxo passar sem segurar mensagens (`proxy_buffering off`, `proxy_read_timeout` maior que 2 × heartbeat). Veja `docs/contrato/eventos-sse.md` §8.
- Ficam para depois:
  - `pendencies.updated` e o painel de pendências (etapa 09);
  - eventos `account` de pontos (etapa 10);
  - `deletion_request.updated` (etapa 07).
