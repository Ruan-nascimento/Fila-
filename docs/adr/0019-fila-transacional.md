# ADR-0019 — Fila transacional: travas, ordem FIFO, senhas e estimativa

- Situação: Aceita (etapa 04)
- Data: 2026-11-12

## Contexto

A fila recebe comandos concorrentes: duas abas da mesma pessoa, chamada simultânea pelo profissional e pelo dono, transferência disputando com chamada, entrada durante uma pausa. Não pode haver cliente duplicado, atendimento duplo nem transição aplicada pela metade (aceite da etapa 04). A API pode rodar em mais de um processo, então uma trava em memória (mutex local) não serve.

## Decisão

1. **O banco é a última barreira.**
   - Um índice único parcial garante uma participação ativa (aguardando, chamada ou em atendimento) por identidade e loja.
   - Outro índice único parcial garante uma pessoa chamada ou em atendimento por fila.
   - Também são únicas a posição por fila e a senha por fila e dia.
   - Uma restrição garante que a tolerância é exatamente `called_at + 10 min`.
2. **Ordem fixa de travas, para não haver impasse (deadlock).**
   - Todo comando que muda uma fila trava primeiro a linha da fila. Se travar duas (transferência), a ordem é por id. Só depois trava a participação.
   - Entrada, pausa, chamada, transferência e fechamento na mesma fila ficam assim em fila indiana.
   - Se a participação mudou de fila entre a leitura e a trava, o comando responde `409 version_conflict` com os dados atuais.
3. **Entradas da mesma identidade.**
   - Uma trava consultiva da transação (`pg_advisory_xact_lock`) por loja e identidade faz a segunda aba esperar a primeira.
   - A segunda aba então vê a participação criada e recebe `409 active_participation_exists`, com a participação atual em `current`.
   - O índice único continua valendo como garantia final.
4. **FIFO e senha definidos pelo servidor.**
   - `queue_counter` guarda a última posição da fila e a sequência da senha do dia. Ambas são atualizadas com a fila travada.
   - A senha é a letra da fila + número do dia no fuso da loja (`A001`), e reinicia a cada dia.
   - A transferência pega nova posição e nova senha no fim da fila de destino. Não existe operação de reordenar.
5. **Identidade em uso.** O ator vem só dos cookies:
   - com sessão de conta (cliente ou equipe "agindo como cliente"), a visita é da conta;
   - sem ela, é do visitante desta loja.
6. **Estimativa (sempre "estimativa").**
   - A espera de quem aguarda é a soma das durações de quem está à frente. Para quem está em atendimento conta só o que falta: duração menos o tempo decorrido, nunca abaixo de zero.
   - A estimativa pública é a espera de quem chegasse agora.
   - É simples de propósito; pode ser refinada sem mudar o contrato.
7. **Comanda criada na entrada (T-O-01, módulo `atendimento`).**
   - Os itens guardam uma cópia de nome, preço, duração e pontos, e o total é calculado no servidor.
   - Desistência, cancelamento pelo dono e ausência encerram a comanda sem cobrança (T-O-05).
   - Enquanto aberta, nada é devido (`amounts.due = 0`). O valor passa a ser devido na conclusão (etapa 05).
   - Pagamentos ainda não existem: listas vazias, sem método escolhido.
8. **Fidelidade.** O programa de pontos começa desligado (R-FID-01) e só existe na etapa 10. Por isso `loyalty_eligible` é sempre `false` até lá.
9. **Visitante.**
   - Entrar na fila zera `visit_closed_at`.
   - Encerrar a visita (desistência, cancelamento ou ausência) grava `visit_closed_at`, e a credencial e o código passam a valer só por mais 24 h (R-VIS-03).
   - Visitante com participação ativa ou comanda pendente não cria outro visitante no mesmo navegador (`visitor_visit_in_progress`).
10. **Limites de entrada.**
    - Até 10 entradas por hora por identidade e loja, e até 60 por hora por IP e loja.
    - Ficam somados aos limites de criação de visitante da etapa 02, e nunca dependem só do IP.
11. **Cursor da visita atual.** `getCurrentVisit` devolve `cursor` nulo até a etapa 06 (`SnapshotCursor`, ADR-0018).

## Alternativas consideradas

- **Isolamento `SERIALIZABLE` com novas tentativas automáticas:** funciona, mas espalha erros de serialização. As travas explícitas deixam claro quem espera quem e dão mensagens específicas (`professional_busy`, `queue_not_open`).
- **Posição calculada por `max(position) + 1` sem contador:** exigiria travar a fila do mesmo jeito e complica a numeração diária da senha.
- **Mutex na memória da API:** proibido pela etapa (vários processos).
- **Cancelar automaticamente após a tolerância:** proibido pelo anexo (R-FILA-10).

## Consequências

- Conclusão, interrupção, edição de itens e solicitações de alteração (etapa 05) devem seguir a mesma ordem de travas.
- Alterar preço ou desativar um serviço espera as entradas em andamento: o catálogo é lido com `FOR SHARE`.
- Os eventos SSE (`participation.updated`, `queue.public.updated`) entram na etapa 06, gravados na outbox na mesma transação destes comandos.
