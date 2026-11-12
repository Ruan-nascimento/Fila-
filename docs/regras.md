# Regras do MVP de filas — versão 2 consolidada

Base: anexo "MVP web — Regras e decisões do sistema de filas", versão 2 (12/11/2026), copiado em [`REGRAS.md`](../REGRAS.md).
Este arquivo **não cria regras novas de produto**: numera as regras adotadas para que cada uma seja rastreável até cenário de aceite, transição e contrato.
Detalhes técnicos reversíveis escolhidos na etapa 00 estão marcados como _(detalhe da etapa 00)_ e justificados nos ADRs.

## Como ler

| Documento | Para quê |
|---|---|
| [regras.md](regras.md) (este) | O que o sistema deve fazer, com IDs `R-…` |
| [matriz-transicoes.md](matriz-transicoes.md) | Cada mudança de estado `T-…`: ator, pré-condição, efeito, idempotência, auditoria, erro |
| [matriz-permissoes.md](matriz-permissoes.md) | Quem pode o quê, por operação |
| [cenarios-aceite.md](cenarios-aceite.md) | Cenários `C-…` em linguagem simples, cada um citando regras |
| [contrato/openapi.yaml](contrato/openapi.yaml) | Contrato HTTP único para backend e frontend |
| [contrato/convencoes.md](contrato/convencoes.md) | Erros, paginação, IDs, dinheiro, datas, idempotência, CSRF |
| [contrato/eventos-sse.md](contrato/eventos-sse.md) | Eventos SSE: versão, audiência, cursor, recuperação |
| [adr/](adr/README.md) | Decisões de arquitetura |

A coluna **Referências** cita operações do OpenAPI (`operationId`), eventos, transições e ADRs. O script [`tools/verificar_contrato.py`](tools/verificar_contrato.py) confere que tudo citado existe e que toda regra tem cenário ou ADR.

## Glossário

| Termo | Significado |
|---|---|
| Loja / estabelecimento | A barbearia. Todos os dados privados pertencem a uma loja. |
| Identidade | Quem age como cliente: uma **conta** ou um **visitante** (credencial anônima de uma loja). |
| Participação | A passagem da pessoa pela fila: aguardando → chamado → em atendimento → fim. |
| Comanda / pedido | Serviços e valor da visita. Tem máquina de estados própria (financeira). |
| Visita | Participação + comanda de uma identidade numa loja. |
| Cobrança Pix | Pedido de pagamento criado no provedor (AbacatePay). Pode ter várias tentativas, uma ativa por vez. |
| Recebimento | Dinheiro que entrou (Pix confirmado pelo provedor, dinheiro, maquininha). |
| Conferência | Registro de que a equipe revisou o recebimento. |
| Pendência financeira | Situação que exige o dono: duplicidade, divergência, tardio, desconhecido, estorno em revisão. |
| Reserva | Prêmio travado por 24 h (pontos + unidade) até entrega, cancelamento ou vencimento. |

---

## 1. Escopo

| ID | Regra | Referências |
|---|---|---|
| R-ESC-01 | A primeira entrega é **somente web responsiva** (celular, tablet, computador). Desktop instalável, apps Android/iOS, PWA e push ficam para depois. | ADR-0003 |
| R-ESC-02 | Dados separados por estabelecimento desde o início: toda rota privada carrega `establishmentId` e o servidor confere vínculo. | `x-authz`, ADR-0006 |
| R-ESC-03 | Fora do MVP: agendamento, venda de produtos/estoque comercial, cartão online, pagamento dividido, fiado, descontos livres, cupons, promoções, sorteios. O contrato **não tem campos** para isso. | `AdjustOrderRequest`, `CreateParticipationRequest` |

## 2. Arquitetura (decisões 01–07 do anexo)

| ID | Regra | Referências |
|---|---|---|
| R-ARQ-01 | Backend Go em monólito modular (identidade, estabelecimento, catálogo, fila, atendimento, pagamento, fidelidade, auditoria); API e worker podem ser executáveis separados do mesmo projeto. Sem microsserviços. | ADR-0001 |
| R-ARQ-02 | PostgreSQL é o registro oficial da fila e do dinheiro. Fechar ou atualizar a página nunca apaga uma visita. | ADR-0002 |
| R-ARQ-03 | Frontend React + TypeScript + Vite em `frontend/`, sem componentes nativos. | ADR-0003 |
| R-ARQ-04 | Comandos são HTTP; atualizações chegam por SSE. Polling não é o mecanismo normal. | ADR-0004, `openEventStream` |
| R-ARQ-05 | Contrato OpenAPI único; backend e frontend geram tipos dele. | ADR-0008 |
| R-ARQ-06 | Eventos de mudança só depois de persistidos, via outbox na mesma transação. | ADR-0010 |
| R-ARQ-07 | Fila, atendimento, comanda, recebimento e conferência são separados: terminar o corte libera a cadeira antes de encerrar o financeiro. | ADR-0005, [matriz-transicoes.md](matriz-transicoes.md) |

## 3. Perfis e dados visíveis

| ID | Regra | Referências |
|---|---|---|
| R-PERF-01 | A fila pública mostra profissional, quantidade, senhas e previsão aproximada. **Nunca** nick de terceiros, telefone, e-mail, serviços ou valores. | `listPublicQueues`, `queue.public.updated`, `PublicQueue` |
| R-PERF-02 | Cada cliente vê só o próprio nick e a própria comanda; a equipe autorizada vê o necessário ao atendimento. | `getCurrentVisit`, `getOrder`, `getParticipation` |
| R-PERF-03 | Uma pessoa pode ser profissional e cliente; cada ação respeita o papel usado (sessão de contexto `staff` para agir como equipe). | `login`, ADR-0006 |
| R-PERF-04 | Papéis do MVP: dono e profissional. Gerente e recepcionista ficam para depois. | `StaffRole` |

## 4. Conta e sessões

| ID | Regra | Referências |
|---|---|---|
| R-CONTA-01 | Cadastro com nome, e-mail e senha; a conta só fica ativa após verificar o e-mail. | `registerAccount`, `verifyEmail`, T-AC-01, T-AC-02 |
| R-CONTA-02 | Respostas públicas não revelam se um e-mail existe (cadastro, reenvio, recuperação). | `registerAccount`, `resendVerification`, `requestPasswordReset` |
| R-CONTA-03 | Recuperação de senha por link de uso único válido por 30 minutos; token guardado só em hash. | `requestPasswordReset`, `confirmPasswordReset`, T-PW-01, T-PW-02 |
| R-CONTA-04 | Cadastro público nunca concede acesso administrativo. Dono inicial só por processo controlado. | T-OP-01, ADR-0013 |
| R-CONTA-05 | Profissionais são cadastrados pelo dono e ativam acesso por convite de uso único que expira em 24 horas. | `createInvite`, `acceptInvite`, `revokeInvite`, T-IN-01…04 |
| R-CONTA-06 | Sessão de cliente expira após 7 dias sem uso ou 30 dias desde o login (o que vier primeiro). | `login`, T-SS-05 |
| R-CONTA-07 | Sessão da equipe expira 12 horas após o login. | `login`, T-SS-05 |
| R-CONTA-08 | Logout revoga a sessão no servidor. | `logout`, T-SS-02 |
| R-CONTA-09 | Alterar a senha revoga as demais sessões; redefinir por link revoga todas. | `changePassword`, `confirmPasswordReset`, T-SS-03, T-SS-04 |
| R-CONTA-10 | Excluir conta, alterar acesso da equipe, estornar e corrigir comanda exigem confirmação recente de identidade (10 min). _(estorno/correção: detalhe da etapa 00)_ | `reauthenticate`, `x-reauth` |
| R-CONTA-11 | Tentativas de login, reautenticação, recuperação e código de visitante são limitadas por conta/alvo **e** por origem, não só por IP. | `login`, `recoverVisitorSession`, `rate_limited` |
| R-CONTA-12 | Senhas com hash adequado (Argon2id) e parâmetros documentados; tokens de sessão aleatórios de 256 bits guardados em hash. Parâmetros: m=19 MiB, t=2, p=1. | ADR-0006, ADR-0017 |

## 5. Visitante

| ID | Regra | Referências |
|---|---|---|
| R-VIS-01 | O visitante informa um nick. **Nick não autentica**: não serve para recuperar nem localizar a visita online. | `createVisitorSession`, `Nick` |
| R-VIS-02 | O navegador guarda uma credencial protegida (cookie HttpOnly), não uma cópia oficial da comanda. Ela expira após 7 dias sem uso ou 30 dias desde a criação. | `createVisitorSession`, T-VS-04, ADR-0011 |
| R-VIS-03 | Quando a comanda é encerrada (paga, sem cobrança ou ausência), a credencial termina em até 24 horas, sem passar do limite absoluto. Concluir só o trabalho **não** encerra o acesso ao pagamento. | T-VS-04, `getCurrentVisit` |
| R-VIS-04 | Ao entrar, o visitante recebe um código secreto de recuperação, exibido uma vez. Vale por 30 dias da criação e até 24 horas após o encerramento. Guardado protegido (HMAC) e com tentativas limitadas. | `createVisitorSession`, `recoverVisitorSession`, T-VS-02 |
| R-VIS-05 | Cada uso do código troca o código e revoga credenciais anteriores (replay falha). | `recoverVisitorSession`, `stream.session_ended` |
| R-VIS-06 | O código nunca aparece em links, URLs, registros de tela ou logs. | ADR-0011, `NoStore` |
| R-VIS-07 | Sem credencial e sem código, a equipe localiza a visita e resolve atendimento/recebimento no balcão, sem entregar a comanda online. | `listStaffQueues`, `listOrders`, `registerInPersonReceipt` |
| R-VIS-08 | Expirar a credencial não cancela a visita nem apaga a dívida. | T-VS-04 |
| R-VIS-09 | Não existe associação de visita de visitante a conta. Depois do encerramento a pessoa pode criar conta para as próximas visitas, sem histórico nem pontos retroativos. | `registerAccount`, `listMyHistory` |
| R-VIS-10 | Não se promete impedir a mesma pessoa de usar várias identidades anônimas; aplicam-se limites de abuso que não dependem só do IP. | `createVisitorSession`, `createParticipation`, `rate_limited` |

## 6. Fila

| ID | Regra | Referências |
|---|---|---|
| R-FILA-01 | Cada profissional tem uma fila, criada com ele (fechada). | `createProfessional`, T-PR-01 |
| R-FILA-02 | Uma identidade tem no máximo **uma participação ativa** (aguardando, chamado, em atendimento) por loja. | `createParticipation`, T-P-01 |
| R-FILA-03 | Comanda pendente (aberta, aguardando revisão ou aguardando pagamento) impede a mesma identidade de entrar de novo na loja até ser resolvida. Aguardando apenas conferência da equipe não bloqueia. _(detalhe da etapa 00)_ | `createParticipation`, `pending_order_exists` |
| R-FILA-04 | Entrar exige fila aberta e pelo menos um serviço ativo habilitado para o profissional. | `createParticipation`, T-P-01 |
| R-FILA-05 | A ordem é a de chegada registrada pelo servidor (FIFO determinístico). Não há furar fila nem reordenação arbitrária. | `callNext`, T-P-02 |
| R-FILA-06 | Aberta: aceita entrada e chamada. Pausada: bloqueia novas entradas e chamadas, permite iniciar quem já foi chamado e concluir. Fechada: bloqueia entradas e chamadas; para fechar, ninguém pode estar aguardando ou chamado; atendimento iniciado pode concluir. | `openQueue`, `pauseQueue`, `closeQueue`, T-Q-01…04 |
| R-FILA-07 | Só o dono abre, pausa, fecha e transfere. O profissional opera a própria fila e pode sinalizar indisponibilidade ao dono. | `x-authz`, `signalAvailability`, T-Q-05 |
| R-FILA-08 | Não se desativa profissional com cliente aguardando, chamado ou em atendimento. Comandas anteriores continuam disponíveis ao dono. | `deactivateProfessional`, T-PR-02 |
| R-FILA-09 | Ao chamar, começa uma tolerância fixa de 10 minutos, contada pelo servidor; atualizar a página não reinicia. | `callNext`, `tolerance_ends_at` |
| R-FILA-10 | Ausência só depois dos 10 minutos, por ação do profissional/dono; não há cancelamento automático silencioso. Voltar depois de ausente = nova entrada no fim. | `markNoShow`, T-P-06 |
| R-FILA-11 | Antes de marcar ausência, o profissional pode iniciar normalmente se o cliente chegou (mesmo após os 10 minutos). | `startService`, T-P-03 |
| R-FILA-12 | O cliente pode desistir aguardando ou chamado, sem multa nem perda de pontos. Depois de iniciado, só interrupção pela equipe. | `cancelParticipation`, T-P-04 |
| R-FILA-13 | Transferência: só dono, com concordância do cliente registrada, estado aguardando, destino aberto e compatível com todos os serviços; entra no fim do destino; mantém preços; motivo auditado; estimativa recalculada. | `transferParticipation`, T-P-07 |
| R-FILA-14 | Um profissional nunca tem duas pessoas chamadas/em atendimento ao mesmo tempo. | `callNext`, T-P-02 |
| R-FILA-15 | Quem age com tela antiga recebe conflito com os dados atuais; mudança inválida nunca é aplicada. | `Problem.current`, `version_conflict` |
| R-FILA-16 | Previsão de espera é sempre identificada como estimativa. | `Estimate` |
| R-FILA-17 | O dono pode cancelar participação aguardando/chamada com motivo (ex.: antes de fechar a fila). | `cancelParticipation`, T-P-05 |
| R-FILA-18 | O MVP não tem limite de vagas por fila; a disputa concorrente é pela posição, pela cadeira e pelo estado da fila. _(detalhe da etapa 00 — o anexo não define capacidade)_ | `createParticipation` |

## 7. Serviços, comanda e atendimento

| ID | Regra | Referências |
|---|---|---|
| R-CMD-01 | Serviço tem nome, preço positivo em centavos, duração estimada, pontos inteiros ≥ 0 e profissionais habilitados. | `createService`, `updateService`, `setServiceProfessionals` |
| R-CMD-02 | Preço e total são calculados no servidor. Não há item livre nem preço digitado pelo cliente; campos não previstos são rejeitados. | `CreateParticipationRequest`, `validation_failed` |
| R-CMD-03 | Serviço inativado não aceita nova inclusão; itens já aceitos permanecem e são resolvidos pela equipe. | `deactivateService`, T-SV-03 |
| R-CMD-04 | Cada serviço aparece no máximo uma vez por comanda. | `duplicate_service` |
| R-CMD-05 | Cada item guarda nome, preço, duração e pontos aceitos na inclusão; mudanças futuras do catálogo não reescrevem a visita. | `OrderItem`, T-SV-02 |
| R-CMD-06 | Aguardando: o cliente inclui/remove serviços compatíveis e vê novo total e estimativa; mantém ≥ 1 item (para tirar todos, desiste). | `replaceOrderItems`, T-O-02 |
| R-CMD-07 | Depois da chamada: o cliente cria uma solicitação; só uma pendente por comanda; o total anterior vale até a decisão. | `createChangeRequest`, T-CR-01 |
| R-CMD-08 | Profissional do atendimento ou dono aprova ou recusa; mudança aprovada registra autor, instante e versão. | `approveChangeRequest`, `rejectChangeRequest` |
| R-CMD-09 | Item marcado como realizado não é removido pelo cliente. | `markItemPerformed`, `item_already_performed` |
| R-CMD-10 | Alterações usam versão esperada; conflito não sobrescreve alteração alheia. | `expected_version`, `version_conflict` |
| R-CMD-11 | Concluir exige solicitações resolvidas e itens revisados; fecha uma versão do valor e libera a cadeira; o financeiro segue separado. | `completeService`, T-P-08, T-O-04 |
| R-CMD-12 | Valor fechado não admite edição direta; o dono corrige com motivo e efeito financeiro rastreável. Com cobrança pendente/desconhecida, só depois de cancelamento/expiração confirmados; nunca cria outra cobrança às cegas. | `adjustClosedOrder`, T-O-11 |
| R-CMD-13 | Interrupção: profissional registra motivo e o que foi feito; dono revisa; só serviços integralmente feitos são cobrados pelo preço aceito; parciais/não iniciados valem zero; estado continua interrompido; sem pontos. | `interruptService`, `closeInterruptedOrder`, T-P-09, T-O-06…08 |
| R-CMD-14 | Total zero encerra com motivo, sem Pix fictício nem lançamento de dinheiro. Cancelado/ausente antes do trabalho encerra sem cobrança e sem pontos. | T-O-05, T-O-08 |
| R-CMD-15 | A equipe (atendimento próprio / dono) pode alterar serviços da comanda, com auditoria. | `replaceOrderItems`, T-O-03 |

## 8. Pagamento

| ID | Regra | Referências |
|---|---|---|
| R-PAG-01 | Um único meio cobre o total: Pix integrado, dinheiro ou cartão na maquininha. Sem parcelamento, divisão, fiado ou dados de cartão. | `choosePaymentMethod`, `PaymentMethod` |
| R-PAG-02 | Só o servidor cria cobrança Pix, apenas da comanda com valor fechado, pelo valor devido calculado por ele. | `createPixCharge`, T-PX-01 |
| R-PAG-03 | Pix só é recebido com evidência autenticada do provedor que confira cobrança, valor e recebedor. "Já paguei", comprovante, imagem ou retorno do navegador não confirmam. | `receiveAbacatePayWebhook`, T-PX-05, ADR-0007 |
| R-PAG-04 | Pix recebido aguarda conferência, mas nunca volta a aparecer como não pago. | T-RC-02, T-RC-03, `conferReceipt` |
| R-PAG-05 | Dinheiro/maquininha: profissional do atendimento ou dono registra recebimento integral e conferência na mesma ação, mantendo os dois registros. | `registerInPersonReceipt`, T-RC-01 |
| R-PAG-06 | Troco = valor entregue − total; a receita registrada é só o total devido. Na maquininha guarda-se só meio, valor, responsável e referência opcional sem dados sensíveis. | `RegisterReceiptRequest` |
| R-PAG-07 | Recebimento incompleto ou divergente não encerra automaticamente; vai para revisão do dono. | T-RC-04, `amount_mismatch` |
| R-PAG-08 | Repetir clique ou receber aviso duplicado não cria outro recebimento, cobrança ou crédito de pontos. | IK, NAT, ÚNICA-VEZ |
| R-PAG-09 | Resposta desconhecida do provedor → "verificando pagamento"; consulta antes de permitir nova tentativa; a consulta continua com o navegador fechado. | T-PX-03, T-PX-04, `OrderPayment.verifying` |
| R-PAG-10 | No máximo uma tentativa Pix ativa por comanda. | T-PX-01, `pix_charge_active` |
| R-PAG-11 | Trocar de Pix para dinheiro/maquininha com Pix válido exige alerta à equipe e tentativa de cancelar quando suportado. Se pagar os dois, registra ambos, avisa duplicidade e abre devolução do excesso. Nada é apagado. | `choosePaymentMethod`, `cancelPixCharge`, T-O-12, T-RC-04 |
| R-PAG-12 | Estorno só pelo dono, com motivo, limitado ao recebido não devolvido; automático apenas integral por recebimento; parcial ou desconhecido fica em revisão; sucesso só com evidência; a transação original não é apagada. | `requestRefund`, `confirmRefundManually`, `markRefundFailed`, T-RF-* |
| R-PAG-13 | A comanda encerra com recebimento integral **e** conferência, ou por fechamento autorizado sem valor. | T-O-10, T-O-05, T-O-08 |
| R-PAG-14 | Comanda zero não cria cobrança. | `order_not_payable` |
| R-PAG-15 | Chave do provedor só no backend; integração de produção desligada até validação em sandbox. Produção não ativa com integração simulada. | ADR-0007 |
| R-PAG-16 | Reconciliação e estorno não dependem da aba do cliente: o worker reconsulta com limites/backoff e alerta casos não resolvidos. | T-PX-04, `charge_outcome_unresolved` |
| R-PAG-17 | Webhook: autenticidade conforme contrato oficial do provedor; deduplicação; tolera ordem trocada e repetição; persiste antes de responder sucesso; não autenticável → consulta autenticada antes de reconhecer pagamento. | `receiveAbacatePayWebhook` |
| R-PAG-18 | Todas as transações ficam vinculadas à loja e à comanda corretas. | `x-authz`, `PixCharge.order_id` |

## 9. Pontos e prêmios

| ID | Regra | Referências |
|---|---|---|
| R-FID-01 | Programa desligado por padrão; o dono liga e define pontos inteiros por serviço (inicialmente zero). | `updateLoyaltyProgram`, `CreateServiceRequest.points` |
| R-FID-02 | Só contas verificadas acumulam pontos, separados por loja. | T-LG-01 |
| R-FID-03 | Elegibilidade e identidade ficam registradas ao entrar na fila; desligar o programa depois não retira crédito prometido; mudanças valem para novas visitas. | `Participation.loyalty_eligible`, T-LP-02 |
| R-FID-04 | Crédito = soma dos pontos aceitos dos serviços efetivamente realizados, uma única vez, após conclusão normal + recebimento integral + conferência. | T-LG-01 |
| R-FID-05 | Visitas canceladas, ausentes, interrompidas, gratuitas ou iniciadas como visitante não geram pontos. | T-LG-01 |
| R-FID-06 | Pontos não expiram e não há punição por ausência ou desistência. | `LedgerEntryKind` |
| R-FID-07 | Saldo explicado por extrato imutável: créditos, reservas, consumos, liberações, reversões. | `listMyLoyaltyLedger`, ADR-0012 |
| R-FID-08 | Estorno integral do recebimento principal reverte o crédito daquela comanda uma vez; devolver só um recebimento duplicado não reverte. Saldo pode ficar negativo; novos resgates ficam bloqueados sem gerar dívida em dinheiro. | T-LG-02, `insufficient_points` |
| R-FID-09 | Prêmio é cortesia separada: sem desconto na comanda, sem horário, sem gerar pontos. Tem nome, descrição, custo inteiro, disponibilidade limitada ou ilimitada e instruções. Se exigir atendimento, avisa que não dá prioridade na fila. | `createReward`, `updateReward`, `deactivateReward` |
| R-FID-10 | Uma reserva ativa por conta e loja; trava pontos e uma unidade por 24 horas; vencimento ou cancelamento liberam ambos exatamente uma vez. | `createRewardReservation`, `cancelRewardReservation`, T-RW-01…03 |
| R-FID-11 | Profissional da loja ou dono confirma entrega presencial; consumo é definitivo, salvo correção do dono com motivo. | `deliverRewardReservation`, `revertRewardDelivery` |
| R-FID-12 | A reserva guarda as condições aceitas, mesmo que o catálogo mude. | `RewardReservation.accepted_terms` |
| R-FID-13 | Desligar o programa impede novos acúmulos (novas visitas) e novas reservas, mas mantém saldo, consulta e reservas existentes; sem expiração retroativa. | T-LP-02 |
| R-FID-14 | Dois resgates concorrentes não gastam o mesmo saldo nem a mesma unidade. | T-RW-01 |

## 10. Permissões

| ID | Regra | Referências |
|---|---|---|
| R-PERM-01 | Negar por padrão: toda ação e consulta é conferida no servidor (pessoa, loja, papel, propriedade), inclusive SSE e retomada. | `x-authz`, [matriz-permissoes.md](matriz-permissoes.md) |
| R-PERM-02 | O dono não acessa outras lojas nem o histórico do cliente em outras lojas. | `listMyHistory`, `x-authz` |
| R-PERM-03 | Profissional desativado perde imediatamente acesso operacional e conexões privadas. | `deactivateProfessional`, `stream.session_ended` |
| R-PERM-04 | Profissional atua na própria fila e nas comandas dos atendimentos que executou. | `own_queue`, `own_attendance` |
| R-PERM-05 | Identificador difícil de adivinhar e botão escondido não substituem autorização; recurso fora do escopo responde 404. | [contrato/convencoes.md](contrato/convencoes.md) §3 |
| R-PERM-06 | Papel, preço e identidade nunca vêm do corpo da requisição. | ADR-0006 |

## 11. Exclusão de conta e dados

| ID | Regra | Referências |
|---|---|---|
| R-EXC-01 | Exclusão exige confirmação recente de identidade e mostra os efeitos antes. | `requestAccountDeletion`, `RequestDeletionRequest` |
| R-EXC-02 | Ao solicitar: revoga sessões, bloqueia ações, cancela visitas aguardando/chamadas, libera reservas, encerra saldo sem conversão em dinheiro. | T-AC-03, T-P-10, T-RW-06, T-LG-03 |
| R-EXC-03 | Com atendimento em curso ou recebimento pendente, fica "em processamento"; a equipe resolve; nunca mostra "concluída" antes; não bloqueia recebimento tardio nem conciliação. | `DeletionRequest.blockers`, `getDeletionRequestStatus` |
| R-EXC-04 | Perfil, credenciais e identificadores pessoais sem necessidade são removidos/anonimizados; registros financeiros/operacionais necessários ficam separados, minimizados e com acesso restrito. | T-AC-04, ADR-0015 |
| R-EXC-05 | Meta operacional de 30 dias com alerta ao dono/operador. Não é prazo legal; retenção por categoria (inclusive backups) definida antes de produção. | `DeletionRequest.target_days`, ADR-0015 |
| R-EXC-06 | Dono único não exclui a conta sem transferir a administração ou encerrar a loja; loja nunca fica sem responsável. | `sole_owner`, T-OP-02 |
| R-EXC-07 | Profissional pode excluir a conta pessoal; o dono redistribui a operação. | `professional_queue_has_participants` |
| R-EXC-08 | Solicitação repetida não duplica efeitos; o processo de anonimização é retomável após falha. | T-AC-03, T-AC-04 |

## 12. Web responsiva e acessível

| ID | Regra | Referências |
|---|---|---|
| R-WEB-01 | Toda função funciona a partir de 360 px; validar 360, 390, 768, 1024 e 1440 px; sem rolagem horizontal da página (tabelas viram cartões ou têm rolagem local identificada). | — |
| R-WEB-02 | Rótulos, erros junto aos campos, foco visível, teclado, leitor de tela, nada depende de hover, alvos de 44×44 px, estado não só por cor, zoom 200%. | — |
| R-WEB-03 | Toda tela tem carregamento, vazio, erro e reconexão. Offline: dados marcados como desatualizados, sem falsa confirmação, sem reenviar comando financeiro antigo; ao voltar à aba, sincroniza antes de permitir ação. | [contrato/eventos-sse.md](contrato/eventos-sse.md) §7 |
| R-WEB-04 | Testar Chrome, Firefox, Edge e Safari atuais disponíveis e navegador móvel real quando possível; registrar versões e o que não foi testado. | — |
| R-WEB-05 | Jornadas por papel — cliente: acesso, seleção, confirmação, posição/chamada, comanda, pagamento, histórico, pontos, perfil; profissional: fila, chamada, serviços, conclusão, recebimentos; dono: catálogo/equipe, operação, pendências, estornos, fidelidade, exclusões. | `listPendencies` |

## 13. Segurança, qualidade e recuperação

| ID | Regra | Referências |
|---|---|---|
| R-SEG-01 | Nick e textos livres são texto, nunca HTML executável; tamanho limitado. | `Nick`, [contrato/convencoes.md](contrato/convencoes.md) §11 |
| R-SEG-02 | Sessões em cookies Secure/HttpOnly/SameSite, proteção CSRF e origens autorizadas; nada de token em localStorage. | `accountSession`, `visitorSession`, `csrfToken` |
| R-SEG-03 | Senhas, tokens, códigos e chaves nunca aparecem em logs, URLs ou no bundle web. | ADR-0006, ADR-0007 |
| R-SEG-04 | Limites de tentativas, tamanho de campos/corpo e volume de requisições. | `rate_limited`, `payload_too_large` |
| R-SEG-05 | Comandos e eventos gravados juntos antes de notificar; repetições sem efeito duplicado. | ADR-0010, ADR-0009 |
| R-SEG-06 | Reconexão sem perder mudança entre a leitura inicial e o início dos eventos. | [contrato/eventos-sse.md](contrato/eventos-sse.md) §6 |
| R-SEG-07 | SSE e retomada autorizados por audiência; sessão revogada para de receber. | `openEventStream`, `stream.session_ended` |
| R-SEG-08 | Ambientes separados (dados e credenciais); dados fictícios nos testes. | ADR-0002 |
| R-SEG-09 | Backup com restauração testada em ambiente isolado; antes de religar workers, conciliar pagamentos externos. | ADR-0010 (época do fluxo), ADR-0002 |
| R-SEG-10 | Auditoria mínima das ações relevantes, sem dados pessoais expostos. | `listAuditEvents`, `AuditEvent` |
| R-SEG-11 | O worker retoma tarefas após falha (reconciliação, expirações, outbox, exclusão). | ADR-0010 |

---

## Decisões já tomadas (não são dúvidas)

As decisões abaixo vêm do anexo e **não devem ser reabertas** nas próximas etapas, salvo nova versão do documento de regras:

1. Web responsiva primeiro; desktop e apps depois (R-ESC-01).
2. Go monólito modular + PostgreSQL + React/TS/Vite (ADR-0001…0003).
3. HTTP para comandos, SSE para atualizações (ADR-0004).
4. Fila, atendimento, comanda, recebimento e conferência separados (ADR-0005).
5. Sem penalidade de pontos por desistência ou ausência; tolerância fixa de 10 minutos (R-FILA-09/10/12).
6. Recuperação do visitante por código secreto com os prazos definidos (R-VIS-*).
7. Interrupção cobra só serviços integralmente feitos; parcial vale zero (R-CMD-13).
8. Pix confirmado só pelo provedor; conferência humana separada (ADR-0007).
9. Fidelidade simples: programa desligado por padrão, reserva de 24 h, uma por conta/loja (R-FID-*).
10. Exclusão é diferente de desativação; meta de 30 dias não é prazo legal (R-EXC-*).

## Limitações externas reais

São dependências fora do nosso controle. O produto já define o comportamento seguro enquanto não forem confirmadas; elas **não** são dúvidas de produto.

| Limitação | O que falta confirmar | Comportamento até confirmar | Onde será tratado |
|---|---|---|---|
| Capacidades da AbacatePay | Versão da API, autenticação, sandbox, idempotência remota, consulta de status, expiração, cancelamento de cobrança, estorno via API (integral/parcial), autenticação do webhook, política de reenvio do webhook, dados obrigatórios | Integração de produção desligada; sem cancelamento confirmado a cobrança segue `pending` até expirar e pagamento tardio vira excesso; sem estorno por API o estorno vai para revisão manual documentada; webhook não autenticável só dispara consulta | Etapas 08/09 (`x-external-pending` no OpenAPI) |
| Várias contas recebedoras / multi-loja | Modelo financeiro do provedor para várias lojas | Piloto com uma conta recebedora | Antes de expandir |
| Retenção legal e fiscal | Prazos por categoria de dado (financeiro, operacional, auditoria, backups) | Não se inventa prazo; registros financeiros não são destruídos; meta de 30 dias é só operacional | Etapa 07 (matriz de retenção) e antes de produção |
| Serviço de e-mail | Provedor, domínio, entregabilidade | Desenvolvimento usa captura local; sem e-mail, verificação/convite/reset não chegam e a interface não afirma envio garantido | Etapas 01/02/12 |
| Domínio e TLS | Domínio real, certificado | Cookies `__Host-` exigem HTTPS e mesma origem; desenvolvimento local usa `localhost` | Etapa 12 |
| Navegadores e dispositivos | Quais navegadores/aparelhos físicos estarão disponíveis para teste | Registrar o que foi e o que não foi testado; emulação não prova compatibilidade física | Etapa 11/13 |
| Navegador suspende abas | Abas em segundo plano/fechadas não recebem SSE | Sem promessa de aviso com aba fechada; sincroniza ao voltar | Etapa 06/11 |
| Identidades anônimas | Impossível provar que duas sessões anônimas são pessoas diferentes | Limites de abuso sem promessa de bloqueio perfeito | Etapa 04 |
