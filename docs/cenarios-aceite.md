# Cenários de aceite

Cenários em linguagem simples ("Dado / Quando / Então"). Cada um cita as regras (`R-…`), transições (`T-…`) e operações/eventos do contrato que comprova.
Eles viram testes nas etapas seguintes; a coluna **Tipo** indica o nível esperado:

- **unidade** — regra pura no Go ou no TypeScript;
- **integração** — API real + PostgreSQL real (sem mock do banco);
- **concorrência** — integração com requisições paralelas reais (e `go test -race`);
- **E2E** — navegador contra API real;
- **manual** — verificação humana registrada em `docs/evidencias.md` (etapa 13).

Estes cenários **ainda não foram executados**: na etapa 00 não há código. Nenhum deve ser marcado como aprovado sem evidência.

Dados de exemplo usados em vários cenários: loja **Barbearia Teste** (fuso `America/Sao_Paulo`), dona **Dona Ana**, profissionais **Beto** (fila `A`) e **Caio** (fila `B`), serviços **Corte** (R$ 45,00 = 4500 centavos, 30 min, 10 pontos) e **Barba** (R$ 30,00 = 3000, 20 min, 5 pontos), ambos habilitados para Beto; Caio só faz Corte.

---

## Identidade e acesso

### C-ID-01 — Cadastro só ativa após verificar o e-mail
- Regras: R-CONTA-01 · Transições: T-AC-01, T-AC-02, T-SS-01 · Contrato: `registerAccount`, `verifyEmail`, `login` · Tipo: integração

Dado que Maria se cadastra com nome, e-mail e senha
Quando tenta fazer login antes de verificar o e-mail
Então recebe `403 email_not_verified` e nenhuma sessão é criada
Quando abre o link de verificação (token válido, < 24 h)
Então a conta fica `active`, o login funciona e o mesmo link usado de novo responde `token_invalid`.

### C-ID-02 — Respostas não revelam se o e-mail existe
- Regras: R-CONTA-02 · Contrato: `registerAccount`, `resendVerification`, `requestPasswordReset` · Tipo: integração

Dado um e-mail cadastrado e outro inexistente
Quando cada um é usado em cadastro, reenvio de verificação e pedido de redefinição
Então as respostas têm mesmo status (`202`), mesmo corpo e tempo de resposta sem diferença sistemática (envio de e-mail feito de forma assíncrona).

### C-ID-03 — Redefinição de senha: uso único, 30 minutos, revoga sessões
- Regras: R-CONTA-03, R-CONTA-09 · Transições: T-PW-01, T-PW-02, T-SS-04 · Contrato: `requestPasswordReset`, `confirmPasswordReset` · Tipo: integração (relógio controlável)

Dado que Maria tem duas sessões abertas e pediu redefinição
Quando usa o link após 31 minutos (relógio de teste avançado)
Então recebe `token_invalid`
Quando pede outro link e o usa em 5 minutos
Então a senha muda, **todas** as sessões são revogadas (próximas requisições → `401`), as conexões SSE recebem `stream.session_ended`
E usar o mesmo link de novo responde `token_invalid`
E o banco só contém o hash do token.

### C-ID-04 — Cadastro público não vira dono; dono inicial por processo controlado
- Regras: R-CONTA-04, R-PERM-06 · Transições: T-OP-01, T-IN-02 · Contrato: `registerAccount`, `login`, `listPendencies` · Tipo: integração

Dado que João se cadastra pela rota pública e envia no corpo `"role": "owner"`
Então recebe `400 validation_failed` (`unknown_field`)
Quando se cadastra corretamente e faz login com contexto `staff`
Então recebe `403` (não há vínculo de equipe)
Quando o operador executa `fila-admin bootstrap` para a loja com o e-mail de Dona Ana
Então Dona Ana recebe convite de dono, aceita e passa a acessar `listPendencies` da loja.

### C-ID-05 — Convite de profissional: uso único e 24 horas
- Regras: R-CONTA-05, R-CONTA-10 · Transições: T-IN-01, T-IN-02, T-IN-03, T-IN-04 · Contrato: `createInvite`, `acceptInvite`, `revokeInvite` · Tipo: integração (relógio controlável)

Dado que Dona Ana se reautenticou há 2 minutos e convida Beto
Quando Beto aceita o convite
Então o vínculo de profissional fica ativo
Quando alguém tenta aceitar o mesmo convite de novo
Então recebe `invite_invalid`
Dado outro convite, para Caio, com 25 horas
Então aceitar responde `invite_invalid` (mensagem igual à de convite usado)
Dado Dona Ana sem reautenticação recente
Quando tenta criar convite
Então recebe `403 reauthentication_required`.

### C-ID-06 — Expiração de sessões de cliente e de equipe
- Regras: R-CONTA-06, R-CONTA-07 · Transições: T-SS-05 · Contrato: `login`, `getSession` · Tipo: integração (relógio controlável)

Dado Maria logada como cliente
Quando passam 6 dias sem uso e ela usa o sistema
Então a sessão continua (inatividade reiniciada)
Quando passam mais 7 dias sem uso
Então a sessão expira (`401`)
Dado Maria usando todos os dias
Quando completam 30 dias desde o login
Então a sessão expira mesmo com uso recente
Dado Beto logado com contexto `staff`
Quando completam 12 horas desde o login
Então a sessão expira mesmo com uso contínuo.

### C-ID-07 — Logout e troca de senha revogam no servidor
- Regras: R-CONTA-08, R-CONTA-09 · Transições: T-SS-02, T-SS-03 · Contrato: `logout`, `changePassword`, `stream.session_ended` · Tipo: integração

Dado Maria com sessões no celular e no computador
Quando faz logout no computador
Então o cookie antigo do computador, reenviado manualmente, responde `401`
Quando troca a senha no celular
Então a sessão do celular continua e todas as outras são revogadas.

### C-ID-08 — Ações sensíveis exigem reautenticação recente
- Regras: R-CONTA-10, R-EXC-01 · Transições: T-SS-06 · Contrato: `reauthenticate`, `requestAccountDeletion`, `deactivateProfessional`, `requestRefund`, `adjustClosedOrder` · Tipo: integração (relógio controlável)

Dado Dona Ana logada há 1 hora sem reautenticar
Quando tenta desativar um profissional, estornar ou corrigir comanda
Então recebe `403 reauthentication_required`
Quando reautentica e tenta em até 10 minutos
Então a ação é avaliada normalmente
Quando tenta 11 minutos depois
Então volta a exigir reautenticação.

### C-ID-09 — Força bruta limitada sem depender só do IP
- Regras: R-CONTA-11, R-SEG-04 · Contrato: `login`, `reauthenticate`, `recoverVisitorSession` · Tipo: integração

Dado tentativas de senha errada para a conta de Maria vindas de 20 endereços IP diferentes
Quando o limite por conta é atingido
Então novas tentativas para essa conta recebem `429 rate_limited` com `Retry-After`, mesmo com a senha certa, até o fim da janela
E tentativas para outras contas não são afetadas
E nenhum log contém a senha tentada.

## Visitante

### C-VIS-01 — Visitante entra com nick e recebe código uma vez
- Regras: R-VIS-01, R-VIS-04, R-VIS-06 · Transições: T-VS-01, T-P-01 · Contrato: `createVisitorSession`, `createParticipation` · Tipo: integração + E2E

Dado Lia sem conta abrindo o link da loja
Quando informa o nick "Lia" e entra na fila de Beto com Corte
Então recebe cookie HttpOnly de visitante, código de recuperação (resposta com `Cache-Control: no-store`), senha `A001`, posição e estimativa identificada como estimativa
E o código não aparece em URL, no histórico do navegador nem em logs do servidor
E o banco guarda só o HMAC do código.

### C-VIS-02 — Credencial do visitante expira no prazo certo
- Regras: R-VIS-02, R-VIS-03, R-VIS-08 · Transições: T-VS-04, T-VS-03 · Contrato: `getCurrentVisit`, `endVisitorSession` · Tipo: integração (relógio controlável)

Dado Lia com comanda paga e conferida (`settled`) há 23 horas
Então ainda vê a visita encerrada
Quando passam 24 horas do encerramento
Então a credencial deixa de valer (`401`)
Dado outra visitante com comanda `awaiting_payment` e 8 dias sem usar o navegador
Então a credencial expirou por inatividade, mas a comanda continua `awaiting_payment` e visível para a equipe
Dado Lia aguardando que encerra a credencial no navegador ("sair deste aparelho")
Então o cookie deixa de valer, mas a participação continua `waiting` na fila.

### C-VIS-03 — Concluir o corte não encerra o acesso ao pagamento
- Regras: R-VIS-03, R-ARQ-07 · Transições: T-P-08, T-O-04 · Contrato: `completeService`, `getCurrentVisit`, `createPixCharge` · Tipo: E2E

Dado Lia em atendimento
Quando Beto conclui o corte
Então a participação fica `completed`, a cadeira de Beto fica livre e a comanda fica `awaiting_payment`
E Lia, no mesmo navegador, ainda vê a comanda e consegue escolher Pix.

### C-VIS-04 — Recuperação com código em outro navegador
- Regras: R-VIS-04, R-VIS-06 · Transições: T-VS-02 · Contrato: `recoverVisitorSession` · Tipo: integração + E2E

Dado Lia aguardando, com o código guardado, e o celular sem bateria
Quando abre o link da loja em outro aparelho e informa o código (no corpo, nunca na URL)
Então recupera a visita, recebe **novo** código exibido uma vez, e o cookie antigo deixa de valer
Dado um código de visita encerrada há 25 horas
Então a recuperação responde `recovery_code_invalid` (mesma mensagem de código errado).

### C-VIS-05 — Código de recuperação não pode ser reutilizado
- Regras: R-VIS-05 · Transições: T-VS-02 · Contrato: `recoverVisitorSession`, `stream.session_ended` · Tipo: integração

Dado que o código `K1` de Lia foi usado e gerou `K2`
Quando alguém tenta `K1` de novo
Então recebe `recovery_code_invalid`
E a conexão SSE aberta com a credencial antiga recebe `stream.session_ended` e é fechada.

### C-VIS-06 — Sem prova de acesso, só o balcão resolve
- Regras: R-VIS-01, R-VIS-07, R-VIS-08 · Contrato: `createVisitorSession`, `listStaffQueues`, `registerInPersonReceipt` · Tipo: integração

Dado Lia sem cookie e sem código, com comanda `awaiting_payment`
Quando outra pessoa cria um visitante com o nick "Lia"
Então essa nova identidade não vê a visita de Lia (`getCurrentVisit` → `data: null`)
E Beto localiza a comanda pela senha na tela da equipe e registra o pagamento em dinheiro no balcão.

### C-VIS-07 — Visitante não ganha histórico nem pontos ao criar conta
- Regras: R-VIS-09, R-FID-05 · Contrato: `registerAccount`, `listMyHistory`, `getMyLoyalty` · Tipo: integração

Dado Lia com visita de visitante encerrada e paga
Quando cria uma conta com e-mail e entra na mesma loja
Então o histórico da conta está vazio e o saldo de pontos é zero
E não existe operação no contrato para associar visita de visitante a conta.

### C-VIS-08 — Limites de abuso para identidades anônimas
- Regras: R-VIS-10, R-SEG-04 · Contrato: `createVisitorSession`, `createParticipation` · Tipo: integração

Dado um mesmo navegador criando muitos visitantes na loja em pouco tempo
Quando ultrapassa o limite (por cookie de navegador, por loja e por origem combinados)
Então recebe `429 rate_limited`
E a documentação registra que isso **reduz** abuso, mas não prova que sessões anônimas diferentes são pessoas diferentes.

## Loja, equipe e catálogo

### C-EST-01 — Cada profissional tem uma fila
- Regras: R-FILA-01 · Transições: T-PR-01 · Contrato: `createProfessional`, `listStaffQueues` · Tipo: integração

Dado Dona Ana cadastrando Beto com letra `A`
Então existe exatamente uma fila de Beto, inicialmente `closed`
E cadastrar outro profissional com a letra `A` responde `400 validation_failed`.

### C-EST-02 — Catálogo valida preço, duração e pontos
- Regras: R-CMD-01, R-ESC-03 · Transições: T-SV-01, T-SV-05 · Contrato: `createService`, `setServiceProfessionals`, `listPublicServices` · Tipo: integração

Dado Dona Ana criando serviço
Quando informa preço 0, preço `45.5`, duração 0, pontos −1 ou campo `discount`
Então cada caso responde `400 validation_failed` com o campo indicado
Quando cria "Corte" com 4500 centavos, 30 min, 10 pontos, habilitado para Beto
Então o catálogo público mostra nome, preço, duração e profissionais habilitados, sem pontos, versão ou outros dados internos.

### C-EST-03 — Só a dona abre, pausa e fecha
- Regras: R-FILA-06, R-FILA-07 · Transições: T-Q-01, T-Q-02, T-Q-03, T-Q-05 · Contrato: `openQueue`, `pauseQueue`, `signalAvailability` · Tipo: integração

Dado Beto logado como profissional
Quando tenta abrir ou pausar a própria fila
Então recebe `403 forbidden`
Quando sinaliza indisponibilidade
Então a fila **não** muda de estado e o painel de Dona Ana mostra o sinal
Quando Dona Ana abre a fila
Então ela fica `open` e o evento `queue.public.updated` chega às telas públicas.

### C-EST-04 — Pausa e fechamento seguem o anexo
- Regras: R-FILA-06, R-FILA-17 · Transições: T-Q-02, T-Q-04, T-P-03, T-P-05 · Contrato: `pauseQueue`, `closeQueue`, `startService`, `cancelParticipation` · Tipo: integração

Dado a fila de Beto com Lia chamada e Maria aguardando
Quando Dona Ana pausa
Então novas entradas e `callNext` respondem `409 queue_not_open`, e Beto ainda consegue iniciar Lia
Quando Dona Ana tenta fechar
Então recebe `409 queue_has_pending_participants` (Maria aguardando)
Quando cancela Maria com motivo e Lia já está em atendimento
Então o fechamento funciona e Beto ainda conclui o atendimento de Lia.

### C-EST-05 — Não desativar profissional com atendimento ativo
- Regras: R-FILA-08, R-PERM-03 · Transições: T-PR-02 · Contrato: `deactivateProfessional`, `listOrders` · Tipo: integração

Dado Beto com Lia em atendimento
Quando Dona Ana (reautenticada) tenta desativá-lo
Então recebe `409 professional_has_active_participations`
Dado a fila vazia e uma comanda antiga de Beto `awaiting_payment`
Quando desativa
Então Beto perde o acesso, e Dona Ana continua vendo e recebendo a comanda antiga.

### C-EST-06 — Alterações concorrentes no catálogo
- Regras: R-CMD-10, R-CMD-05 · Transições: T-SV-02 · Contrato: `updateService` · Tipo: concorrência

Dado duas abas de Dona Ana com "Corte" versão 3
Quando ambas alteram o preço ao mesmo tempo
Então exatamente uma recebe `200` (versão 4) e a outra `409 version_conflict` com o serviço atual
E comandas já abertas continuam com o preço antigo.

### C-EST-07 — Configuração da loja e da equipe
- Regras: R-FILA-07, R-CONTA-10, R-PERF-01 · Transições: T-ES-01, T-PR-03, T-PR-04 · Contrato: `updateEstablishment`, `updateProfessional`, `reactivateProfessional`, `getPublicEstablishmentBySlug` · Tipo: integração

Dado Dona Ana alterando o nome e o slug da loja
Quando o slug já é usado por outra loja
Então recebe `409 slug_taken`
Quando muda o nome exibido de Beto
Então a fila pública passa a mostrar o novo nome (sem outros dados de Beto)
Dado Beto desativado
Quando Dona Ana, reautenticada, o reativa
Então o vínculo volta e a fila de Beto continua `closed` até a dona abrir
Quando Beto tenta mudar o próprio nome ou a loja
Então recebe `403`.

## Fila

### C-FILA-01 — Entrada válida
- Regras: R-FILA-04, R-FILA-16, R-CMD-02 · Transições: T-P-01, T-O-01 · Contrato: `createParticipation`, `participation.updated`, `queue.public.updated` · Tipo: integração

Dado a fila de Beto aberta
Quando Maria (conta) entra com Corte e Barba
Então recebe `201` com senha, posição, estimativa (`kind: estimate`) e comanda `open` de 7500 centavos calculados no servidor
E a tela pública recebe `queue.public.updated` só com senhas, quantidade e estimativa.

### C-FILA-02 — Mesma identidade em duas abas: só uma participação
- Regras: R-FILA-02 · Transições: T-P-01 · Contrato: `createParticipation` · Tipo: concorrência

Dado Maria com duas abas abertas
Quando envia ao mesmo tempo duas entradas (chaves de idempotência diferentes), uma para Beto e outra para Caio
Então exatamente uma é criada
E a outra recebe `409 active_participation_exists` com a participação atual em `current`
E repetir a requisição vencedora com a mesma chave devolve a mesma resposta com `Idempotent-Replayed: true`.

### C-FILA-03 — Dois clientes disputam a entrada enquanto a fila é pausada
- Regras: R-FILA-05, R-FILA-06, R-FILA-18 · Transições: T-P-01, T-Q-02 · Contrato: `createParticipation`, `pauseQueue` · Tipo: concorrência

Dado a fila de Beto aberta com 3 pessoas
Quando Maria e João enviam entrada ao mesmo tempo que Dona Ana pausa a fila
Então cada entrada ou entra **completa** (senha, posição, comanda, evento) ou é recusada com `409 queue_not_open`, nunca pela metade
E quem entrou tem posições distintas e consecutivas na ordem em que o servidor confirmou
E não existem duas senhas iguais no dia.

### C-FILA-04 — Comanda pendente bloqueia nova entrada
- Regras: R-FILA-03 · Transições: T-P-01 · Contrato: `createParticipation` · Tipo: integração

Dado Maria com atendimento concluído e comanda `awaiting_payment`
Quando tenta entrar de novo na mesma loja
Então recebe `409 pending_order_exists`
Quando paga em dinheiro (comanda `settled`)
Então consegue entrar
Dado Lia com Pix recebido aguardando só a conferência (`awaiting_confirmation`)
Então Lia consegue entrar de novo.

### C-FILA-05 — Serviço inválido ou fila fechada
- Regras: R-FILA-04, R-CMD-03 · Transições: T-P-01 · Contrato: `createParticipation` · Tipo: integração

Dado Caio, que só faz Corte
Quando Maria tenta entrar na fila de Caio com Barba
Então recebe `409 service_not_eligible`
Quando tenta com serviço inativo
Então recebe `409 service_inactive`
Quando tenta sem serviços
Então recebe `400 validation_failed`
Quando a fila está fechada
Então recebe `409 queue_not_open`.

### C-FILA-06 — Ordem de chegada determinística
- Regras: R-FILA-05 · Transições: T-P-01, T-P-02 · Contrato: `callNext` · Tipo: integração

Dado Maria, João e Lia entrando nessa ordem na fila de Beto
Quando Beto chama três vezes (concluindo cada atendimento entre as chamadas)
Então a ordem das chamadas é Maria, João, Lia
E não existe operação para reordenar ou furar a fila.

### C-FILA-07 — Duas chamadas simultâneas não ocupam a cadeira duas vezes
- Regras: R-FILA-14, R-FILA-05 · Transições: T-P-02 · Contrato: `callNext` · Tipo: concorrência

Dado a fila de Beto com Maria e João aguardando, e Beto e Dona Ana olhando a mesma fila
Quando os dois clicam "Chamar próximo" ao mesmo tempo
Então exatamente Maria é chamada
E a outra requisição recebe `409 professional_busy` com a fila atual
E João continua `waiting`.

### C-FILA-08 — Fila pública não vaza dados
- Regras: R-PERF-01, R-FILA-16 · Contrato: `listPublicQueues`, `queue.public.updated`, `openEventStream` · Tipo: integração + E2E

Dado Maria (nick "Maria") e Lia na fila
Quando uma pessoa sem sessão abre a fila pública e assina o canal `public`
Então vê senhas, status, quantidade e "estimativa de ~N min"
E a resposta e os eventos não contêm nick, serviços, valores, e-mail ou identificadores de identidade
E pedir o canal `owner` sem sessão resulta em `denied_channels: ["owner"]` no snapshot.

### C-FILA-09 — Tolerância de 10 minutos e ausência
- Regras: R-FILA-09, R-FILA-10, R-FILA-11 · Transições: T-P-02, T-P-03, T-P-06 · Contrato: `callNext`, `markNoShow`, `startService` · Tipo: integração (relógio controlável)

Dado Lia chamada às 10:00:00 (`tolerance_ends_at` 10:10:00)
Quando Lia recarrega a página às 10:05
Então o prazo continua 10:10:00
Quando Beto tenta marcar ausência às 10:09:59
Então recebe `409 tolerance_not_elapsed`
Quando nada acontece até 10:30
Então Lia continua `called` (sem cancelamento automático)
Quando Lia chega às 10:31 e Beto inicia
Então o atendimento começa normalmente
Dado João chamado e ausente após 10 minutos
Quando Beto marca ausência
Então João fica `no_show`, comanda `closed_no_charge`, sem perda de pontos, e para voltar entra no fim.

### C-FILA-10 — Desistência e cancelamento pela dona
- Regras: R-FILA-12, R-FILA-17, R-CMD-14, R-FID-06 · Transições: T-P-04, T-P-05, T-O-05, T-CR-04 · Contrato: `cancelParticipation` · Tipo: integração

Dado Maria chamada, com uma solicitação de alteração pendente
Quando ela desiste
Então a participação fica `cancelled`, a comanda `closed_no_charge`, a solicitação `voided`, a cadeira livre, e o saldo de pontos inalterado
Dado João em atendimento
Quando tenta desistir pelo aplicativo
Então recebe `409 invalid_transition` (só interrupção pela equipe)
Dado Lia aguardando
Quando Dona Ana cancela sem motivo
Então recebe `400 validation_failed`; com motivo, o cancelamento é auditado com motivo.

### C-FILA-11 — Transferência válida
- Regras: R-FILA-13 · Transições: T-P-07 · Contrato: `transferParticipation` · Tipo: integração

Dado Maria aguardando na fila de Beto só com Corte, e a fila de Caio aberta com 2 pessoas
Quando Dona Ana transfere com consentimento verbal registrado e motivo
Então Maria fica em 3º na fila de Caio com nova senha `B…`, preço de 4500 mantido e estimativa recalculada
Quando Dona Ana tenta transferir João, que pediu Barba, para Caio
Então recebe `409 transfer_target_incompatible`
Quando Beto (profissional) tenta transferir
Então recebe `403`
Quando falta o consentimento
Então recebe `400 validation_failed` / `transfer_consent_required`.

### C-FILA-12 — Transferência disputando com chamada
- Regras: R-FILA-13, R-FILA-15 · Transições: T-P-07, T-P-02 · Contrato: `transferParticipation`, `callNext` · Tipo: concorrência

Dado Maria primeira da fila de Beto
Quando Dona Ana transfere Maria e Beto chama o próximo ao mesmo tempo
Então ou Maria foi chamada por Beto e a transferência recebe `409 invalid_transition` com `current`
Ou Maria foi transferida e Beto chama a pessoa seguinte (ou recebe `no_waiting_participant`)
E nunca Maria fica chamada por Beto e posicionada na fila de Caio ao mesmo tempo.

### C-FILA-13 — Tela antiga recebe conflito com dados atuais
- Regras: R-FILA-15, R-CMD-10 · Transições: T-P-03, T-P-04 · Contrato: `startService`, `cancelParticipation`, `Problem.current` · Tipo: concorrência

Dado Lia chamada (versão 3)
Quando Lia desiste e, ao mesmo tempo, Beto clica "Iniciar" com versão 3
Então só uma transição vence
E a outra recebe `409` com `current` mostrando o estado vencedor (cancelado ou em atendimento)
E a tela de quem perdeu é atualizada sem aplicar a ação.

## Comanda e atendimento

### C-CMD-01 — Preço adulterado pelo navegador é rejeitado
- Regras: R-CMD-02, R-ESC-03, R-PERM-06 · Contrato: `createParticipation`, `replaceOrderItems`, `registerInPersonReceipt` · Tipo: integração

Dado Maria entrando na fila
Quando envia `"price_cents": 100` ou `"total": 100` junto com os serviços
Então recebe `400 validation_failed` com `unknown_field`
Quando, já no pagamento, Beto registra dinheiro com `amount_cents` 4000 para comanda de 4500
Então recebe `409 payment_amount_mismatch` e nada é registrado.

### C-CMD-02 — Serviço desativado ou alterado depois da inclusão
- Regras: R-CMD-03, R-CMD-05 · Transições: T-SV-02, T-SV-03, T-SV-04 · Contrato: `deactivateService`, `reactivateService`, `updateService`, `getOrder` · Tipo: integração

Dado Maria aguardando com Corte a 4500
Quando Dona Ana muda o Corte para 5000 e depois o desativa
Então a comanda de Maria continua com "Corte — 4500"
E novas inclusões de Corte respondem `409 service_inactive`
E o histórico mostra o nome e preço aceitos
Quando Dona Ana reativa o Corte
Então novas inclusões usam o preço atual (5000), e a comanda de Maria continua com 4500.

### C-CMD-03 — Edição enquanto aguarda
- Regras: R-CMD-04, R-CMD-06 · Transições: T-O-02 · Contrato: `replaceOrderItems` · Tipo: integração

Dado Maria aguardando com Corte
Quando troca para Corte + Barba
Então o total vira 7500 e a estimativa da fila é recalculada
Quando tenta lista vazia
Então recebe `400 validation_failed` (mínimo 1)
Quando envia Corte duas vezes
Então recebe `400 validation_failed` (`duplicate`)
Quando é chamada e tenta editar diretamente
Então recebe `409 order_not_editable` e é orientada a fazer uma solicitação.

### C-CMD-04 — Solicitação após a chamada
- Regras: R-CMD-07, R-CMD-08 · Transições: T-CR-01, T-CR-02, T-CR-03 · Contrato: `createChangeRequest`, `approveChangeRequest`, `rejectChangeRequest`, `change_request.updated` · Tipo: integração

Dado Maria em atendimento com Corte (4500)
Quando pede para incluir Barba
Então a solicitação fica `pending` e o total continua 4500
Quando pede outra alteração
Então recebe `409 change_request_pending`
Quando Beto aprova
Então o total vira 7500, com autor "profissional", instante e nova versão registrados, e Maria recebe o evento
Dado outra solicitação recusada
Então a comanda permanece igual e Maria é avisada.

### C-CMD-05 — Item realizado não é removido pelo cliente
- Regras: R-CMD-09 · Transições: T-O-13, T-CR-01 · Contrato: `markItemPerformed`, `createChangeRequest` · Tipo: integração

Dado Maria em atendimento com Corte marcado como realizado e Barba aceita
Quando pede para remover o Corte
Então recebe `409 item_already_performed`
Quando pede para remover a Barba
Então a solicitação é criada.

### C-CMD-06 — Edição concorrente não sobrescreve
- Regras: R-CMD-10 · Transições: T-O-02, T-O-03 · Contrato: `replaceOrderItems` · Tipo: concorrência

Dado a comanda de Maria na versão 2 (aguardando)
Quando Maria e Dona Ana alteram ao mesmo tempo com `expected_version: 2`
Então uma vence (versão 3) e a outra recebe `409 version_conflict` com a comanda atual.

### C-CMD-07 — Equipe altera serviços com auditoria
- Regras: R-CMD-15, R-SEG-10 · Transições: T-O-03 · Contrato: `replaceOrderItems`, `listAuditEvents` · Tipo: integração

Dado Maria em atendimento com Beto
Quando Beto inclui Barba com motivo "cliente pediu na cadeira"
Então a comanda muda e a auditoria registra ator (profissional), transição T-O-03, versão e motivo, sem nick nem e-mail
Quando Caio (outra fila) tenta alterar essa comanda
Então recebe `404`.

### C-ATD-01 — Concluir libera a cadeira; financeiro fica pendente
- Regras: R-CMD-11, R-ARQ-07 · Transições: T-P-08, T-O-04, T-P-02 · Contrato: `completeService`, `callNext` · Tipo: integração

Dado Maria em atendimento com Corte + Barba e João aguardando
Quando Beto conclui marcando os dois como realizados
Então a participação fica `completed`, a comanda `awaiting_payment` com 7500 congelado
E Beto chama João imediatamente, sem esperar o pagamento de Maria.

### C-ATD-02 — Concluir com solicitação pendente falha
- Regras: R-CMD-11 · Transições: T-P-08 · Contrato: `completeService` · Tipo: integração

Dado Maria em atendimento com solicitação pendente
Quando Beto tenta concluir
Então recebe `409 change_request_pending`
Quando Beto conclui marcando todos os itens como `not_performed`
Então recebe `409 performed_item_required` (isso é interrupção, não conclusão).

### C-ATD-03 — Interrupção com valor positivo
- Regras: R-CMD-13, R-FID-05 · Transições: T-P-09, T-O-06, T-O-07 · Contrato: `interruptService`, `closeInterruptedOrder`, `listPendencies` · Tipo: integração

Dado Maria em atendimento com Corte (feito inteiro) e Barba (pela metade)
Quando Beto interrompe com motivo "cliente precisou sair"
Então a cadeira fica livre, a participação `interrupted`, a comanda `pending_review` e o painel da dona mostra a revisão
Quando Dona Ana revisa: Corte `fully_performed`, Barba `partially_performed`
Então a comanda vai para `awaiting_payment` com 4500 (Barba = 0)
E após pagamento e conferência **nenhum** ponto é creditado
E a participação continua `interrupted`.

### C-ATD-04 — Interrupção com valor zero
- Regras: R-CMD-13, R-CMD-14, R-PAG-14 · Transições: T-O-08 · Contrato: `closeInterruptedOrder`, `createPixCharge` · Tipo: integração

Dado Maria interrompida sem nenhum serviço integralmente feito
Quando Dona Ana revisa com nota
Então a comanda fica `closed_no_charge` com motivo `interruption_zero_total`
E tentar criar Pix responde `409 order_not_payable`
E não existe recebimento, cobrança ou ponto.

### C-ATD-05 — Correção após fechamento com Pix pendente
- Regras: R-CMD-12 · Transições: T-O-11, T-PX-07 · Contrato: `adjustClosedOrder`, `cancelPixCharge` · Tipo: integração

Dado a comanda de Maria `awaiting_payment` (7500) com Pix `pending`
Quando Dona Ana (reautenticada) tenta retirar a Barba
Então recebe `409 pix_charge_active`
Quando cancela o Pix e o provedor confirma (`cancelled`)
Então a correção funciona, o devido vira 4500, e a auditoria registra motivo e versão
Dado uma cobrança `unknown`
Então a correção responde `409 pix_charge_outcome_unknown`.

## Pagamento

### C-PAG-01 — Pix criado pelo servidor com o valor da comanda
- Regras: R-PAG-01, R-PAG-02, R-PAG-18 · Transições: T-PX-01, T-PX-02 · Contrato: `choosePaymentMethod`, `createPixCharge` · Tipo: integração (provedor de teste)

Dado a comanda de Maria `awaiting_payment` com 7500
Quando Maria escolhe Pix e pede a cobrança
Então o servidor cria a cobrança de 7500 vinculada à loja e à comanda e devolve `br_code` e validade
E o corpo da requisição não aceita valor algum.

### C-PAG-02 — Pix confirmado pelo provedor e conferido pela equipe
- Regras: R-PAG-03, R-PAG-04, R-PAG-13, R-FID-04 · Transições: T-PX-05, T-RC-02, T-O-09, T-RC-03, T-O-10, T-LG-01 · Contrato: `receiveAbacatePayWebhook`, `conferReceipt`, `payment.updated` · Tipo: integração (fixture do provedor)

Dado o Pix de 7500 `pending`
Quando chega notificação autenticada de pagamento com cobrança, valor e recebedor corretos
Então o recebimento é registrado (`registered`) e a comanda fica `awaiting_confirmation`
E a tela de Maria mostra "pago, aguardando conferência" e **nunca** volta a "não pago"
Quando Beto confere
Então a comanda fica `settled` e, sendo conta elegível, 15 pontos são creditados uma vez.

### C-PAG-03 — "Já paguei" não confirma nada
- Regras: R-PAG-03 · Contrato: `createPixCharge`, `getPixCharge` · Tipo: integração + E2E

Dado o Pix `pending`
Quando Maria volta da página do banco, envia comprovante por fora ou clica "já paguei"
Então o estado continua `pending` e a comanda `awaiting_payment`
E não existe operação no contrato para o cliente marcar pagamento.

### C-PAG-04 — Duplo clique na criação do Pix
- Regras: R-PAG-08, R-PAG-10 · Transições: T-PX-01 · Contrato: `createPixCharge` · Tipo: concorrência

Dado a comanda `awaiting_payment`
Quando Maria clica duas vezes (mesma chave) e, em outra aba, mais uma vez (outra chave)
Então existe **uma** cobrança ativa
E as repetições com a mesma chave devolvem a mesma resposta
E a requisição com outra chave recebe `409 pix_charge_active` com a cobrança atual.

### C-PAG-05 — Dinheiro com troco
- Regras: R-PAG-05, R-PAG-06, R-PAG-13, R-PAG-01 · Transições: T-RC-01, T-O-10 · Contrato: `registerInPersonReceipt` · Tipo: integração

Dado a comanda de João `awaiting_payment` com 4500
Quando Beto registra dinheiro com 4500 devido e 5000 entregues
Então há um recebimento de 4500 e um registro separado de conferência
E o troco exibido é 500 e a receita registrada é 4500
E a comanda fica `settled`
Quando Caio (não atendeu João) tenta registrar
Então recebe `404`.

### C-PAG-06 — Timeout ambíguo na criação do Pix
- Regras: R-PAG-09, R-PAG-16, R-PAG-08 · Transições: T-PX-03, T-PX-04, T-PX-08 · Contrato: `createPixCharge`, `OrderPayment.verifying` · Tipo: integração (provedor simulado com atraso)

Dado o provedor demorando além do timeout
Quando Maria pede o Pix
Então recebe `202` com status `unknown` e a tela mostra "verificando pagamento"
E nova tentativa responde `409 pix_charge_outcome_unknown`
Quando Maria fecha o navegador
Então o worker consulta o provedor com backoff e resolve (`pending`, `paid`, `expired` ou `failed`)
E se não resolver dentro do limite, abre pendência `charge_outcome_unresolved` para a dona
E em nenhum caso duas cobranças ficam ativas por falha de rede
Dado o provedor recusando a criação de forma definitiva (sem cobrança criada)
Então a tentativa fica `failed` e uma nova tentativa é permitida.

### C-PAG-07 — Webhook repetido, fora de ordem ou não autenticado
- Regras: R-PAG-17, R-PAG-03, R-PAG-08 · Transições: T-PX-05, T-RC-02 · Contrato: `receiveAbacatePayWebhook` · Tipo: integração

Dado a mesma notificação de pagamento entregue três vezes
Então existe um único recebimento e cada entrega responde 200
Dado uma notificação de "expirado" chegando depois de "pago"
Então a cobrança continua `paid`
Dado uma notificação com autenticação inválida
Então nada é reconhecido como pago, o sistema faz consulta autenticada ao provedor e registra só o que a consulta comprovar
E a notificação é persistida antes de responder sucesso.

### C-PAG-08 — Valor divergente
- Regras: R-PAG-07 · Transições: T-RC-04, T-FI-01 · Contrato: `receiveAbacatePayWebhook`, `listFinancialIssues` · Tipo: integração

Dado a cobrança de 7500
Quando o provedor confirma 7000
Então o recebimento é registrado, a comanda **não** encerra, e abre pendência `amount_mismatch` para a dona.

### C-PAG-09 — Repetição de conferência e de recebimento presencial
- Regras: R-PAG-08, R-SEG-05 · Transições: T-RC-03, T-RC-01 · Contrato: `conferReceipt`, `registerInPersonReceipt` · Tipo: concorrência

Dado um Pix `registered`
Quando Beto e Dona Ana conferem ao mesmo tempo
Então existe uma conferência, uma mudança para `settled`, um crédito de pontos e um evento por audiência
Dado uma comanda `awaiting_payment`
Quando Beto registra dinheiro duas vezes (rede instável, mesma chave)
Então existe um recebimento.

### C-PAG-10 — Troca de Pix para dinheiro e Pix tardio
- Regras: R-PAG-11, R-PAG-12 · Transições: T-O-12, T-PX-07, T-RC-01, T-RC-04, T-FI-01, T-RF-01, T-FI-02 · Contrato: `choosePaymentMethod`, `cancelPixCharge`, `registerInPersonReceipt`, `receiveAbacatePayWebhook`, `requestRefund`, `resolveFinancialIssue` · Tipo: integração

Dado o Pix de Maria `pending`
Quando ela escolhe dinheiro sem `acknowledge_active_pix`
Então recebe `409 pix_charge_active` com aviso
Quando confirma a ciência
Então o sistema tenta cancelar o Pix e abre pendência `active_pix_on_method_switch`
Dado que o provedor não confirmou o cancelamento e Beto recebeu em dinheiro
Quando o Pix chega depois
Então os **dois** recebimentos ficam registrados, o Pix como `excess`, e abre pendência `late_receipt` para devolução
E nenhum recebimento é apagado
Quando Dona Ana estorna o recebimento excedente e o estorno é confirmado
Então ela resolve a pendência com nota; os dois recebimentos continuam no histórico e os pontos da compra não são revertidos.

### C-PAG-11 — Estorno integral de Pix
- Regras: R-PAG-12, R-FID-08 · Transições: T-RF-01, T-RF-02, T-RF-03, T-LG-02 · Contrato: `requestRefund`, `receiveAbacatePayWebhook` · Tipo: integração (provedor de teste)

Dado a comanda de Maria `settled` por Pix de 7500, com 15 pontos creditados
Quando Beto tenta estornar
Então recebe `403`
Quando Dona Ana (reautenticada) estorna com motivo
Então o estorno fica `processing` e só vira `confirmed` com evidência do provedor
E então o crédito de 15 pontos é revertido uma vez
E o recebimento original continua registrado, com valor devolvido 7500.

### C-PAG-12 — Estorno desconhecido, manual e corrida com conferência
- Regras: R-PAG-12, R-PAG-16 · Transições: T-RF-04, T-RF-05, T-RF-06, T-RF-07 · Contrato: `requestRefund`, `confirmRefundManually`, `markRefundFailed` · Tipo: integração + concorrência

Dado um estorno cujo pedido ao provedor dá timeout
Então ele fica `unknown`, o worker consulta, e sem resolução vai para `under_review` com pendência
Dado um recebimento em dinheiro
Quando Dona Ana pede estorno
Então ele vai direto para `under_review` e só é `confirmed` quando ela registra evidência documentada
Dado um Pix `registered`
Quando a conferência e um pedido de estorno chegam ao mesmo tempo
Então as duas operações são serializadas e o estado final é coerente (sem receita contada após devolução confirmada).

### C-PAG-13 — Cobrança expirada permite nova cobrança
- Regras: R-PAG-10, R-PAG-11 · Transições: T-PX-06, T-PX-01, T-RC-04 · Contrato: `createPixCharge`, `receiveAbacatePayWebhook` · Tipo: integração

Dado o Pix de Maria `expired` confirmado pelo provedor
Quando ela pede novo Pix
Então uma nova cobrança é criada
E se a cobrança antiga for paga depois, o pagamento é registrado como `excess` com pendência `late_receipt`.

## Pontos e prêmios

### C-FID-01 — Programa começa desligado
- Regras: R-FID-01 · Transições: T-LP-01 · Contrato: `getLoyaltyProgram`, `updateLoyaltyProgram`, `createRewardReservation` · Tipo: integração

Dado uma loja nova
Então o programa está `enabled: false` e reservar prêmio responde `409 loyalty_program_disabled`
Quando Dona Ana liga o programa
Então novas visitas de contas passam a ser elegíveis.

### C-FID-02 — Crédito após conclusão, pagamento integral e conferência
- Regras: R-FID-02, R-FID-04 · Transições: T-LG-01 · Contrato: `conferReceipt`, `registerInPersonReceipt`, `getMyLoyalty` · Tipo: integração

Dado Maria (conta) com programa ligado, Corte (10) e Barba (5), Barba não realizada
Quando a comanda é concluída, paga e conferida
Então Maria recebe 10 pontos, uma vez
E repetir a conferência ou reprocessar o evento não credita de novo (chave única por comanda).

### C-FID-03 — Desligar depois da entrada mantém o crédito prometido
- Regras: R-FID-03, R-FID-13 · Transições: T-LP-02, T-LG-01 · Contrato: `updateLoyaltyProgram` · Tipo: integração

Dado Maria que entrou com programa ligado
Quando Dona Ana desliga o programa durante o atendimento
Então ao encerrar a comanda Maria recebe os pontos prometidos
E João, que entrou depois do desligamento, não recebe.

### C-FID-04 — Sem pontos para visitante, cancelado, ausente, interrompido ou gratuito
- Regras: R-FID-05, R-FID-06, R-VIS-09 · Transições: T-LG-01 · Contrato: `getMyLoyalty` · Tipo: integração

Dado programa ligado
Quando visitas terminam como visitante pago, cancelada, ausente, interrompida (mesmo com valor) e interrompida com zero
Então nenhuma gera lançamento `credit`
E nenhuma gera lançamento negativo (sem punição).

### C-FID-05 — Extrato explica o saldo
- Regras: R-FID-07 · Contrato: `listMyLoyaltyLedger`, `getMyLoyalty` · Tipo: integração

Dado Maria com créditos, uma reserva, uma liberação e uma entrega
Então `saldo = Σ balance_delta`, `retido = Σ held_delta` e `disponível = saldo − retido`
E cada lançamento aponta a origem (comanda, reserva, conta)
E lançamentos nunca são alterados nem apagados.

### C-FID-06 — Estorno reverte; excesso não; saldo negativo bloqueia resgate
- Regras: R-FID-08 · Transições: T-LG-02, T-RW-01 · Contrato: `requestRefund`, `createRewardReservation` · Tipo: integração

Dado Maria com 15 pontos creditados da comanda X e um Pix duplicado (excesso) na mesma comanda
Quando o excesso é estornado
Então os pontos continuam
Quando Maria usa 15 pontos num prêmio e depois o recebimento principal da comanda X é estornado
Então o saldo fica −15, sem cobrança em dinheiro
E nova reserva responde `409 insufficient_points`.

### C-FID-07 — Prêmio é cortesia separada
- Regras: R-FID-09, R-ESC-03 · Transições: T-RD-01, T-RD-02, T-RD-03 · Contrato: `createReward`, `updateReward`, `deactivateReward`, `listCustomerRewards` · Tipo: integração

Dado Dona Ana criando "Lavagem de cabelo" com custo 50, estoque limitado 3 e instruções "não garante prioridade na fila"
Então o prêmio aparece para contas
E não existe campo para aplicá-lo como desconto em comanda
Quando ela inativa o prêmio
Então não aceita novas reservas, mas reservas ativas continuam válidas.

### C-FID-08 — Reserva de 24 horas libera exatamente uma vez
- Regras: R-FID-10, R-SEG-11 · Transições: T-RW-01, T-RW-02, T-RW-03 · Contrato: `createRewardReservation`, `cancelRewardReservation` · Tipo: integração (relógio controlável)

Dado Maria com 60 pontos reservando o prêmio de 50
Então o disponível vira 10, o estoque cai 1 e `expires_at` = +24 h
Quando tenta outra reserva na mesma loja
Então recebe `409 reservation_already_active`
Quando passam 24 horas e o job de expiração roda duas vezes (inclusive após reinício)
Então existe um único `hold_release`, o estoque volta 1 e a reserva fica `expired`.

### C-FID-09 — Dois clientes disputam a última unidade (e gasto duplo)
- Regras: R-FID-14, R-FID-10 · Transições: T-RW-01 · Contrato: `createRewardReservation` · Tipo: concorrência

Dado o prêmio com **1 unidade** e Maria e João com pontos suficientes
Quando os dois reservam ao mesmo tempo
Então exatamente um recebe `201`
E o outro recebe `409 reward_out_of_stock`
E o estoque termina em 0, nunca −1, e só o vencedor tem pontos retidos
Dado Maria com 60 pontos e dois prêmios de 50 em abas diferentes
Quando reserva os dois ao mesmo tempo
Então só um é reservado (`reservation_already_active` ou `insufficient_points`) e o disponível nunca fica negativo por reserva.

### C-FID-10 — Entrega disputando com vencimento; correção da dona
- Regras: R-FID-11, R-FID-12 · Transições: T-RW-03, T-RW-04, T-RW-05 · Contrato: `deliverRewardReservation`, `revertRewardDelivery` · Tipo: concorrência (relógio controlável)

Dado a reserva de Maria vencendo às 18:00:00
Quando Beto confirma a entrega às 17:59:59 e o job de expiração roda ao mesmo tempo
Então só um efeito acontece: entregue (com `consume`) ou vencida (com `hold_release`), nunca os dois
Quando Beto tenta entregar às 18:00:01
Então recebe `409 reservation_expired`
Dado uma entrega registrada por engano
Quando Dona Ana corrige com motivo
Então a reserva fica `delivery_reverted`, os pontos voltam uma vez e o estoque +1
E a reserva mostra as condições aceitas originais mesmo após o prêmio ter sido editado.

### C-FID-11 — Desligar o programa com reservas ativas
- Regras: R-FID-13 · Transições: T-LP-02, T-RW-02, T-RW-04 · Contrato: `updateLoyaltyProgram`, `cancelRewardReservation`, `deliverRewardReservation`, `getMyLoyalty` · Tipo: integração

Dado Maria com reserva ativa e saldo 40
Quando Dona Ana desliga o programa
Então Maria ainda vê saldo 40, pode cancelar a reserva, e a equipe pode entregá-la
E novas reservas respondem `409 loyalty_program_disabled`
E nenhum ponto expira.

## Exclusão de conta

### C-EXC-01 — Exclusão simples
- Regras: R-EXC-01, R-EXC-02, R-EXC-08 · Transições: T-AC-03, T-P-10, T-RW-06, T-LG-03, T-AC-04 · Contrato: `requestAccountDeletion`, `getDeletionRequestStatus` · Tipo: integração

Dado Maria aguardando na fila, com reserva ativa e 40 pontos, reautenticada
Quando confirma a exclusão vendo os efeitos
Então todas as sessões são revogadas, a participação é cancelada, a reserva liberada, o saldo encerrado (`account_closure`) e ela recebe um protocolo
E a resposta mostra `completed` só depois da anonimização; repetir a solicitação não duplica efeitos.

### C-EXC-02 — Exclusão com atendimento ou pagamento pendente
- Regras: R-EXC-03 · Transições: T-AC-03 · Contrato: `requestAccountDeletion`, `getDeletionRequestStatus`, `listDeletionRequests`, `receiveAbacatePayWebhook` · Tipo: integração

Dado João em atendimento e com outra comanda `awaiting_payment` com Pix pendente
Quando solicita exclusão
Então recebe `processing` com bloqueios `service_in_progress` e `financial_pending`, nunca "concluída"
E a equipe continua podendo concluir, receber e conferir
E um Pix que chegue depois é registrado normalmente
Quando as pendências são resolvidas
Então o worker conclui a anonimização e a consulta pelo protocolo mostra `completed`.

### C-EXC-03 — Anonimização repetível e retomável
- Regras: R-EXC-04, R-EXC-08, R-SEG-11 · Transições: T-AC-04 · Contrato: `listAuditEvents` · Tipo: integração

Dado uma anonimização interrompida no meio (processo morto)
Quando o worker reinicia
Então retoma e conclui sem erro e sem duplicar efeitos
E nome, e-mail e credenciais não existem mais no perfil
E comandas e recebimentos permanecem com identificador pseudônimo, valores e datas
E a evidência registrada não contém dados pessoais.

### C-EXC-04 — Meta de 30 dias gera alerta
- Regras: R-EXC-05 · Contrato: `listDeletionRequests`, `listPendencies` · Tipo: integração (relógio controlável)

Dado uma exclusão em processamento há 25 dias
Então o painel da dona e o alerta operacional destacam a pendência
E nenhum texto da interface chama os 30 dias de prazo legal.

### C-EXC-05 — Dona única não pode excluir a conta
- Regras: R-EXC-06 · Transições: T-AC-03, T-OP-02 · Contrato: `requestAccountDeletion` · Tipo: integração

Dado Dona Ana como única dona de loja ativa
Quando solicita exclusão
Então recebe `409 sole_owner` com orientação de transferir ou encerrar a loja
E depois do processo controlado de transferência, a exclusão é aceita.

### C-EXC-06 — Profissional exclui a própria conta
- Regras: R-EXC-07, R-PERM-03 · Transições: T-AC-03 · Contrato: `requestAccountDeletion`, `listDeletionRequests` · Tipo: integração

Dado Beto com 2 pessoas aguardando na fila
Quando solicita exclusão
Então perde o acesso na hora, a solicitação fica `processing` com bloqueio `professional_queue_has_participants`
E Dona Ana vê a pendência e transfere/cancela essas participações; então o processo conclui.

## Atualizações SSE

### C-SSE-01 — Comando gera atualização sem polling
- Regras: R-ARQ-04, R-ARQ-06 · Contrato: `openEventStream`, `callNext`, `participation.updated` · Tipo: E2E

Dado Lia com a tela aberta e uma conexão SSE
Quando Beto a chama
Então a tela de Lia mostra "você foi chamada" em segundos, sem recarregar e sem requisições periódicas
E o evento só é enviado depois do commit (se a transação falha, nenhum evento sai).

### C-SSE-02 — Evento durante o snapshot não se perde
- Regras: R-SEG-05, R-SEG-06 · Contrato: `openEventStream`, `stream.snapshot` · Tipo: concorrência

Dado uma conexão sendo aberta
Quando uma entrada na fila é confirmada exatamente entre a leitura do snapshot e o início da assinatura
Então o cliente termina com o estado que inclui a entrada (por snapshot ou por evento)
E se recebeu os dois, aplica uma vez só (versão).

### C-SSE-03 — Recarregar a página reconstrói o estado do banco
- Regras: R-ARQ-02 · Contrato: `getCurrentVisit`, `openEventStream` · Tipo: E2E

Dado Maria aguardando
Quando fecha a aba e abre de novo
Então vê a mesma participação, posição e comanda, lidas do banco.

### C-SSE-04 — Rede cai e volta
- Regras: R-WEB-03 · Contrato: `openEventStream` · Tipo: E2E

Dado Maria com a tela aberta
Quando a rede cai, Beto a chama, e a rede volta
Então durante a queda a tela mostra "dados podem estar desatualizados" e desabilita ações de pagamento
E ao voltar reconecta com o cursor, recebe a chamada e reabilita as ações só depois de sincronizar
E nenhum comando financeiro é reenviado automaticamente.

### C-SSE-05 — Audiência indevida não recebe nada
- Regras: R-SEG-07, R-PERM-01, R-PERF-02 · Contrato: `openEventStream` · Tipo: integração

Dado Lia (visitante) conectada pedindo os canais `visit`, `owner` e `professional`
Então o snapshot informa `denied_channels: ["owner","professional"]`
E ela nunca recebe eventos de outra visita, nem com cursor copiado de outra pessoa
Dado Beto conectado como profissional
Então não recebe eventos da fila de Caio nem pendências financeiras da dona.

### C-SSE-06 — Sessão revogada encerra a conexão
- Regras: R-SEG-07, R-PERM-03 · Contrato: `openEventStream`, `stream.session_ended`, `deactivateProfessional` · Tipo: integração

Dado Beto conectado
Quando Dona Ana o desativa (ou Beto faz logout em outro aparelho)
Então a conexão recebe `stream.session_ended` e fecha em até 30 s
E a reconexão com o mesmo cookie é recusada.

### C-SSE-07 — Cursor velho ou banco restaurado
- Regras: R-SEG-06, R-SEG-09 · Contrato: `openEventStream`, `stream.snapshot` · Tipo: integração

Dado um cursor de 2 dias atrás
Quando o cliente reconecta
Então recebe `stream.snapshot` com `reason: cursor_expired`
Dado um banco restaurado de backup (nova época)
Então cursores antigos recebem `reason: epoch_changed`.

### C-SSE-08 — Dois processos de API entregam a mesma sequência
- Regras: R-SEG-05, R-SEG-06 · Contrato: `openEventStream` · Tipo: integração

Dado duas instâncias da API e clientes conectados a cada uma
Quando várias mudanças acontecem
Então todos os clientes recebem os mesmos eventos autorizados na mesma ordem de posição
E escala horizontal só é declarada depois deste teste passar.

## Segurança e autorização

### C-SEG-01 — Negação por padrão
- Regras: R-PERM-01 · Contrato: todas as operações (`x-authz`) · Tipo: integração (teste gerado a partir do contrato)

Para cada operação do OpenAPI
Quando chamada sem sessão, com visitante, com cliente, com profissional e com dono de **outra** loja
Então só os pares ator/escopo listados em `x-authz` são aceitos; todos os outros recebem `401`, `403` ou `404`.

### C-SEG-02 — Dono não acessa outra loja
- Regras: R-PERM-02, R-PERM-05, R-ESC-02 · Contrato: `getOrder`, `listOrders`, `listMyHistory` · Tipo: integração

Dado Dona Ana dona da Barbearia Teste e uma comanda da Barbearia Outra
Quando troca o `establishmentId` ou o `orderId` na URL
Então recebe `404` (sem revelar existência)
E consultas indiretas (lista, histórico, eventos) também não retornam dados da outra loja.

### C-SEG-03 — Cliente não vê a comanda de outro cliente
- Regras: R-PERF-02, R-PERM-05 · Contrato: `getOrder`, `getParticipation`, `getPixCharge` · Tipo: integração

Dado Maria e Lia na mesma loja
Quando Maria usa o `orderId` de Lia
Então recebe `404`.

### C-SEG-04 — Profissional fora da própria fila
- Regras: R-PERM-04, R-FILA-07 · Contrato: `callNext`, `getOrder`, `registerInPersonReceipt` · Tipo: integração

Dado Beto e Caio na mesma loja
Quando Beto chama a fila de Caio, lê comanda de atendimento de Caio ou registra recebimento nela
Então recebe `404`/`403` e nada muda.

### C-SEG-05 — Papel usado é o da sessão, não o declarado
- Regras: R-PERF-03, R-PERF-04, R-PERM-06 · Contrato: `login`, `createParticipation`, `callNext` · Tipo: integração

Dado Beto logado com contexto `customer`
Quando tenta chamar o próximo
Então recebe `403` (vínculo de equipe só vale com sessão `staff`)
Quando entra na fila de Caio como cliente
Então participa como qualquer cliente
E enviar `role`/`professional_id` no corpo resulta em `400 unknown_field`.

### C-SEG-06 — Profissional desativado perde acesso imediatamente
- Regras: R-PERM-03 · Contrato: `deactivateProfessional`, `listStaffQueues` · Tipo: integração

Dado Beto com sessão `staff` válida
Quando é desativado
Então a próxima requisição de equipe responde `401`/`403`, e a conexão SSE é encerrada.

### C-SEG-07 — Nick com HTML/script é só texto
- Regras: R-SEG-01 · Contrato: `createVisitorSession`, `listStaffQueues` · Tipo: E2E

Dado o nick `<img src=x onerror=alert(1)>`
Quando aparece na tela da equipe e na tela do visitante
Então é exibido literalmente como texto e nenhum script executa
E nick com 31 caracteres ou caracteres de controle responde `400 validation_failed`.

### C-SEG-08 — Segredos fora de logs, URLs e bundle
- Regras: R-SEG-03, R-VIS-06, R-PAG-15 · Tipo: integração + verificação do build

Dado um fluxo completo (login, visitante, recuperação, Pix)
Então os logs não contêm senha, token de sessão, código de recuperação, token de convite/reset, cookie ou chave do provedor
E o bundle do frontend não contém a chave do provedor nem variáveis secretas
E nenhuma URL carrega segredo.

### C-SEG-09 — CSRF e origem
- Regras: R-SEG-02 · Contrato: `csrfToken`, `origin_not_allowed`, `csrf_failed` · Tipo: integração

Dado Maria logada
Quando um site de outra origem envia um `POST` com o cookie dela
Então recebe `403 origin_not_allowed`
Quando a origem está certa mas falta `X-CSRF-Token`
Então recebe `403 csrf_failed`
E os cookies são `Secure; HttpOnly; SameSite=Lax` e nenhum token é guardado em `localStorage`.

### C-SEG-12 — Varredura de autorização em todas as rotas
- Regras: R-PERM-01, R-PERM-02 · Contrato: `x-authz` · Tipo: integração (etapa 13)

Dado recursos reais da loja A (visita, comanda paga, recebimento, cobrança, reserva, convite)
Quando cada rota do contrato é chamada por anônimo, dona de outra loja (com a loja A e com a própria loja na URL), cliente sem relação, visitante sem relação e outro profissional da mesma loja
Então toda chamada que o `x-authz` não permite responde 401, 403 ou 404 — inclusive com corpo válido
E as leituras permitidas (dados próprios, listas filtradas) não trazem recurso alheio
E nada muda nos recursos da loja A.

### C-SEG-10 — Limites de corpo e campos
- Regras: R-SEG-04 · Contrato: `payload_too_large`, `validation_failed` · Tipo: integração

Dado um corpo de 1 MiB ou um motivo com 10.000 caracteres
Então o servidor responde `413` ou `400` sem processar.

### C-SEG-11 — Auditoria sem dados pessoais
- Regras: R-SEG-10 · Contrato: `listAuditEvents`, `AuditEvent` · Tipo: integração

Dado as transições auditadas (A, A+M, A+E) da matriz
Quando executadas
Então cada uma gera um registro com ator pseudônimo, transição, estados, motivo quando exigido e `request_id`
E nenhum registro contém e-mail, nick, telefone ou segredo.

## Web responsiva

### C-WEB-01 — Larguras previstas sem rolagem horizontal
- Regras: R-WEB-01, R-WEB-05, R-ESC-01 · Tipo: E2E + manual

Para cada jornada de cliente, profissional e dona
Nas larguras 360, 390, 768, 1024 e 1440 px
Então a página não tem rolagem horizontal; tabelas viram cartões ou têm rolagem local identificada; todas as ações principais estão acessíveis.

### C-WEB-02 — Acessibilidade básica
- Regras: R-WEB-02 · Tipo: E2E (verificação automatizada) + manual

Então campos têm rótulos, erros ficam junto ao campo e são anunciados, o foco é visível, tudo funciona por teclado, nada depende de hover, botões principais têm 44×44 px, estados não dependem só de cor e a página funciona com zoom de 200%.

### C-WEB-03 — Offline não gera falsa confirmação
- Regras: R-WEB-03 · Contrato: `createParticipation`, `createPixCharge` · Tipo: E2E

Dado Maria sem internet clicando "Entrar na fila" ou "Pagar com Pix"
Então a tela mostra "não foi possível confirmar" (nunca "você entrou"/"pago")
E ao reconectar, a interface consulta o estado real antes de oferecer nova tentativa, reutilizando a mesma chave de idempotência da intenção.

### C-WEB-04 — Navegadores registrados
- Regras: R-WEB-04 · Tipo: manual

Então `docs/evidencias.md` lista navegador, versão, sistema e aparelho de cada teste, e lista explicitamente o que **não** foi testado.

## Operação

### C-OPS-01 — Restauração reconcilia pagamentos antes de religar o worker
- Regras: R-SEG-09, R-SEG-08 · Transições: T-OP-03, T-OP-04 · Contrato: `stream.snapshot` · Tipo: integração (TestRestoreReconcilesBeforeWorkerResumes, TestGateDetectsCopiedDatabaseAndRequiresReconciliation, TestOperatorMaintenanceAndRestoreCommands) + exercício real em homologação local (docs/operacao/evidencias.md)

Dado um backup restaurado em ambiente isolado
Quando a aplicação sobe
Então o worker fica parado até a reconciliação com o provedor terminar
E cobranças recebidas depois do backup são registradas a partir do provedor (o banco restaurado não "desfaz" um pagamento)
E a época do fluxo SSE muda.

### C-OPS-02 — Implantação não perde fila
- Regras: R-SEG-05, R-SEG-06, R-SEG-11 · Transições: T-OP-03 · Contrato: `listPublicQueues`, `openEventStream` · Tipo: exercício real em homologação (frontend/e2e/operacao.spec.ts)

Dado pessoas aguardando na fila e uma tela de cliente aberta no celular
Quando uma versão nova é implantada com tráfego contínuo (deploy/implantar.sh)
Então nenhuma leitura falha (o proxy segura as requisições durante a troca da API)
E ninguém some da fila e o comando feito durante a troca vale uma vez
E a tela do cliente atualiza a posição sozinha depois da troca.

### C-OPS-03 — Worker retoma depois de cair
- Regras: R-SEG-11 · Transições: T-OP-03 · Contrato: `createPixCharge`, `createRewardReservation`, `requestAccountDeletion` · Tipo: exercício real em homologação + integração (TestWorkerHeldDoesNothingUntilReleased)

Dado o worker morto sem encerramento (SIGKILL) com Pix pago, reserva vencida, exclusão e e-mail pendentes
Quando o worker volta
Então tudo é feito uma única vez (um recebimento, uma liberação, exclusão concluída, e-mail entregue).

### C-OPS-04 — Produção não ativa com integração financeira simulada
- Regras: R-PAG-15, R-SEG-08 · Tipo: unidade (TestProductionPixOnlyOfficialHost, TestStagingIsHardenedButNeverCharges) + CI (imagem de produção recusa subir)

Dado FILA_ENV=production
Quando a chave do Pix é de teste, ou o endereço não é o oficial da AbacatePay
Então o processo não sobe (configuração inválida, saída 2)
E a homologação nunca aceita chave de produção.

---

## Critérios de pronto do anexo (§15) → cenários

| Critério do anexo | Cenários |
|---|---|
| Visita completa: seleção, entrada, chamada, serviço, pagamento e conferência | C-FILA-01, C-FILA-06, C-ATD-01, C-PAG-02, C-PAG-05 |
| Visitante e conta funcionam; equipe opera pelo celular e computador | C-VIS-01, C-ID-01, C-WEB-01 |
| Ninguém acessa visita ou loja alheia alterando identificadores | C-SEG-01, C-SEG-02, C-SEG-03, C-SEG-04 |
| Duas chamadas simultâneas não atendem duas pessoas na mesma cadeira | C-FILA-07 |
| Cancelar após chamada libera a fila sem penalidade | C-FILA-10 |
| Ausência respeita dez minutos | C-FILA-09 |
| Interrupção fecha valor por regra definida | C-ATD-03, C-ATD-04 |
| Concluir corte não encerra acesso de pagamento do visitante | C-VIS-03 |
| Pix duplicado/tardio registrado e tratado | C-PAG-07, C-PAG-10, C-PAG-13 |
| Dois resgates concorrentes não gastam o mesmo saldo ou estoque | C-FID-09 |
| Estado após reconexão corresponde ao banco | C-SSE-02, C-SSE-03, C-SSE-04, C-SSE-07 |
| Sessão revogada não continua recebendo dados | C-SSE-06, C-VIS-05 |
| Layout passa pelas larguras previstas | C-WEB-01 |
| Sandbox financeiro e restauração têm evidências | C-PAG-02 (sandbox na etapa 08/09; **AbacatePay real pendente**), C-OPS-01, C-OPS-02, C-OPS-03, C-OPS-04 |
