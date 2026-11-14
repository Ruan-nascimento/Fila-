# Rastreabilidade: regra → cenário → teste

Gerado por `python docs/tools/rastrear_evidencias.py` (etapa 13). Um cenário conta como
**automatizado** quando algum teste o cita pelo identificador; os demais estão na seção 3 com o
motivo. Teste citado aqui não quer dizer aprovado: o resultado de cada execução está em
[evidencias.md](evidencias.md).

## 1. Regras

| Regra | Cenários | Situação |
|---|---|---|
| R-ESC-01 | C-WEB-01 | automatizado |
| R-ESC-02 | C-SEG-02 | automatizado |
| R-ESC-03 | C-CMD-01, C-EST-02, C-FID-07 | automatizado |
| R-ARQ-01 | — (backend/internal/arquitetura/arquitetura_test.go: TestDomainModulesExist, TestModulesOnlyQueryOwnTables) | automatizado (estrutural) |
| R-ARQ-02 | C-SSE-03 | automatizado |
| R-ARQ-03 | — (CI job Frontend (npm run build, lint, typecheck); frontend/package.json) | automatizado (estrutural) |
| R-ARQ-04 | C-SSE-01 | automatizado |
| R-ARQ-05 | — (backend/internal/api/contrato_test.go: TestRoutesMatchContract, TestResponsesMatchContractTypes; CI: tipos gerados em dia (go generate, npm run check:api)) | automatizado (estrutural) |
| R-ARQ-06 | C-SSE-01 | automatizado |
| R-ARQ-07 | C-ATD-01, C-VIS-03 | automatizado |
| R-PERF-01 | C-EST-07, C-FILA-08 | automatizado |
| R-PERF-02 | C-SEG-03, C-SSE-05 | automatizado |
| R-PERF-03 | C-SEG-05 | automatizado |
| R-PERF-04 | C-SEG-05 | automatizado |
| R-CONTA-01 | C-ID-01 | automatizado |
| R-CONTA-02 | C-ID-02 | automatizado |
| R-CONTA-03 | C-ID-03 | automatizado |
| R-CONTA-04 | C-ID-04 | automatizado |
| R-CONTA-05 | C-ID-05 | automatizado |
| R-CONTA-06 | C-ID-06 | automatizado |
| R-CONTA-07 | C-ID-06 | automatizado |
| R-CONTA-08 | C-ID-07 | automatizado |
| R-CONTA-09 | C-ID-03, C-ID-07 | automatizado |
| R-CONTA-10 | C-EST-07, C-ID-05, C-ID-08 | automatizado |
| R-CONTA-11 | C-ID-09 | automatizado |
| R-CONTA-12 | — (backend/internal/identidade/segredos_test.go: TestPasswordHashing, TestTokenIs256BitsAndHashed) | automatizado (estrutural) |
| R-VIS-01 | C-VIS-01, C-VIS-06 | automatizado |
| R-VIS-02 | C-VIS-02 | automatizado |
| R-VIS-03 | C-VIS-02, C-VIS-03 | automatizado |
| R-VIS-04 | C-VIS-01, C-VIS-04 | automatizado |
| R-VIS-05 | C-VIS-05 | automatizado |
| R-VIS-06 | C-SEG-08, C-VIS-01, C-VIS-04 | automatizado |
| R-VIS-07 | C-VIS-06 | automatizado |
| R-VIS-08 | C-VIS-02, C-VIS-06 | automatizado |
| R-VIS-09 | C-FID-04, C-VIS-07 | automatizado |
| R-VIS-10 | C-VIS-08 | automatizado |
| R-FILA-01 | C-EST-01 | automatizado |
| R-FILA-02 | C-FILA-02 | automatizado |
| R-FILA-03 | C-FILA-04 | automatizado |
| R-FILA-04 | C-FILA-01, C-FILA-05 | automatizado |
| R-FILA-05 | C-FILA-03, C-FILA-06, C-FILA-07 | automatizado |
| R-FILA-06 | C-EST-03, C-EST-04, C-FILA-03 | automatizado |
| R-FILA-07 | C-EST-03, C-EST-07, C-SEG-04 | automatizado |
| R-FILA-08 | C-EST-05 | automatizado |
| R-FILA-09 | C-FILA-09 | automatizado |
| R-FILA-10 | C-FILA-09 | automatizado |
| R-FILA-11 | C-FILA-09 | automatizado |
| R-FILA-12 | C-FILA-10 | automatizado |
| R-FILA-13 | C-FILA-11, C-FILA-12 | automatizado |
| R-FILA-14 | C-FILA-07 | automatizado |
| R-FILA-15 | C-FILA-12, C-FILA-13 | automatizado |
| R-FILA-16 | C-FILA-01, C-FILA-08 | automatizado |
| R-FILA-17 | C-EST-04, C-FILA-10 | automatizado |
| R-FILA-18 | C-FILA-03 | automatizado |
| R-CMD-01 | C-EST-02 | automatizado |
| R-CMD-02 | C-CMD-01, C-FILA-01 | automatizado |
| R-CMD-03 | C-CMD-02, C-FILA-05 | automatizado |
| R-CMD-04 | C-CMD-03 | automatizado |
| R-CMD-05 | C-CMD-02, C-EST-06 | automatizado |
| R-CMD-06 | C-CMD-03 | automatizado |
| R-CMD-07 | C-CMD-04 | automatizado |
| R-CMD-08 | C-CMD-04 | automatizado |
| R-CMD-09 | C-CMD-05 | automatizado |
| R-CMD-10 | C-CMD-06, C-EST-06, C-FILA-13 | automatizado |
| R-CMD-11 | C-ATD-01, C-ATD-02 | automatizado |
| R-CMD-12 | C-ATD-05 | automatizado |
| R-CMD-13 | C-ATD-03, C-ATD-04 | automatizado |
| R-CMD-14 | C-ATD-04, C-FILA-10 | automatizado |
| R-CMD-15 | C-CMD-07 | automatizado |
| R-PAG-01 | C-PAG-01, C-PAG-05 | automatizado |
| R-PAG-02 | C-PAG-01 | automatizado |
| R-PAG-03 | C-PAG-02, C-PAG-03, C-PAG-07 | automatizado |
| R-PAG-04 | C-PAG-02 | automatizado |
| R-PAG-05 | C-PAG-05 | automatizado |
| R-PAG-06 | C-PAG-05 | automatizado |
| R-PAG-07 | C-PAG-08 | automatizado |
| R-PAG-08 | C-PAG-04, C-PAG-06, C-PAG-07, C-PAG-09 | automatizado |
| R-PAG-09 | C-PAG-06 | automatizado |
| R-PAG-10 | C-PAG-04, C-PAG-13 | automatizado |
| R-PAG-11 | C-PAG-10, C-PAG-13 | automatizado |
| R-PAG-12 | C-PAG-10, C-PAG-11, C-PAG-12 | automatizado |
| R-PAG-13 | C-PAG-02, C-PAG-05 | automatizado |
| R-PAG-14 | C-ATD-04 | automatizado |
| R-PAG-15 | C-OPS-04, C-SEG-08 | automatizado |
| R-PAG-16 | C-PAG-06, C-PAG-12 | automatizado |
| R-PAG-17 | C-PAG-07 | automatizado |
| R-PAG-18 | C-PAG-01 | automatizado |
| R-FID-01 | C-FID-01 | automatizado |
| R-FID-02 | C-FID-02 | automatizado |
| R-FID-03 | C-FID-03 | automatizado |
| R-FID-04 | C-FID-02, C-PAG-02 | automatizado |
| R-FID-05 | C-ATD-03, C-FID-04, C-VIS-07 | automatizado |
| R-FID-06 | C-FID-04, C-FILA-10 | automatizado |
| R-FID-07 | C-FID-05 | automatizado |
| R-FID-08 | C-FID-06, C-PAG-11 | automatizado |
| R-FID-09 | C-FID-07 | automatizado |
| R-FID-10 | C-FID-08, C-FID-09 | automatizado |
| R-FID-11 | C-FID-10 | automatizado |
| R-FID-12 | C-FID-10 | automatizado |
| R-FID-13 | C-FID-03, C-FID-11 | automatizado |
| R-FID-14 | C-FID-09 | automatizado |
| R-PERM-01 | C-SEG-01, C-SEG-12, C-SSE-05 | automatizado |
| R-PERM-02 | C-SEG-02, C-SEG-12 | automatizado |
| R-PERM-03 | C-EST-05, C-EXC-06, C-SEG-06, C-SSE-06 | automatizado |
| R-PERM-04 | C-SEG-04 | automatizado |
| R-PERM-05 | C-SEG-02, C-SEG-03 | automatizado |
| R-PERM-06 | C-CMD-01, C-ID-04, C-SEG-05 | automatizado |
| R-EXC-01 | C-EXC-01, C-ID-08 | automatizado |
| R-EXC-02 | C-EXC-01 | automatizado |
| R-EXC-03 | C-EXC-02 | automatizado |
| R-EXC-04 | C-EXC-03 | automatizado |
| R-EXC-05 | C-EXC-04 | automatizado |
| R-EXC-06 | C-EXC-05 | automatizado |
| R-EXC-07 | C-EXC-06 | automatizado |
| R-EXC-08 | C-EXC-01, C-EXC-03 | automatizado |
| R-WEB-01 | C-WEB-01 | automatizado |
| R-WEB-02 | C-WEB-02 | automatizado |
| R-WEB-03 | C-SSE-04, C-WEB-03 | automatizado |
| R-WEB-04 | C-WEB-04 (sem teste) | sem teste |
| R-WEB-05 | C-WEB-01 | automatizado |
| R-SEG-01 | C-SEG-07 | automatizado |
| R-SEG-02 | C-SEG-09 | automatizado |
| R-SEG-03 | C-SEG-08 | automatizado |
| R-SEG-04 | C-ID-09, C-SEG-10, C-VIS-08 | automatizado |
| R-SEG-05 | C-OPS-02, C-PAG-09, C-SSE-02, C-SSE-08 | automatizado |
| R-SEG-06 | C-OPS-02, C-SSE-02, C-SSE-07, C-SSE-08 | automatizado |
| R-SEG-07 | C-SSE-05, C-SSE-06 | automatizado |
| R-SEG-08 | C-OPS-01, C-OPS-04 | automatizado |
| R-SEG-09 | C-OPS-01, C-SSE-07 | automatizado |
| R-SEG-10 | C-CMD-07, C-SEG-11 | automatizado |
| R-SEG-11 | C-EXC-03, C-FID-08, C-OPS-02, C-OPS-03 | automatizado |

Resumo: automatizado: 126 · parcial: 0 · sem teste: 1 · sem cenário: 0 · automatizado (estrutural): 4

## 2. Cenários e testes

| Cenário | Testes que o citam |
|---|---|
| C-ID-01 — Cadastro só ativa após verificar o e-mail | `backend/internal/api/identidade_test.go: TestRegisterVerifyAndLogin` |
| C-ID-02 — Respostas não revelam se o e-mail existe | `backend/internal/api/identidade_test.go: TestPublicResponsesDoNotRevealAccounts` |
| C-ID-03 — Redefinição de senha: uso único, 30 minutos, revoga sessões | `backend/internal/api/identidade_test.go: TestPasswordResetRevokesAllSessions` |
| C-ID-04 — Cadastro público não vira dono; dono inicial por processo controlado | `backend/internal/api/identidade_test.go: TestPublicSignupNeverGrantsStaffAccess` |
| C-ID-05 — Convite de profissional: uso único e 24 horas | `backend/internal/api/identidade_test.go: TestProfessionalInvite` |
| C-ID-06 — Expiração de sessões de cliente e de equipe | `backend/internal/api/identidade_test.go: TestSessionExpiry` |
| C-ID-07 — Logout e troca de senha revogam no servidor | `backend/internal/api/identidade_test.go: TestLogoutAndPasswordChangeRevoke` |
| C-ID-08 — Ações sensíveis exigem reautenticação recente | `backend/internal/api/identidade_test.go: TestReauthenticationWindow` |
| C-ID-09 — Força bruta limitada sem depender só do IP | `backend/internal/api/identidade_test.go: TestBruteForceLimitedPerAccount` |
| C-VIS-01 — Visitante entra com nick e recebe código uma vez | `backend/internal/api/visitante_test.go: TestVisitorSessionAndRecoveryCode`<br>`frontend/e2e/piloto.spec.ts: roteiro do piloto: do primeiro cliente às pendências da dona` |
| C-VIS-02 — Credencial do visitante expira no prazo certo | `backend/internal/api/visitante_test.go: TestVisitorCredentialExpiry` |
| C-VIS-03 — Concluir o corte não encerra o acesso ao pagamento | `frontend/e2e/jornadas.spec.ts: visitante no celular e Beto no computador: do link da loja ao pago, atualizando sozinho` |
| C-VIS-04 — Recuperação com código em outro navegador | `backend/internal/api/visitante_test.go: TestVisitorRecovery`<br>`frontend/e2e/piloto.spec.ts: roteiro do piloto: do primeiro cliente às pendências da dona` |
| C-VIS-05 — Código de recuperação não pode ser reutilizado | `backend/internal/api/visitante_test.go: TestVisitorRecovery` |
| C-VIS-06 — Sem prova de acesso, só o balcão resolve | `backend/internal/api/visitante_test.go: TestNickDoesNotAuthenticate` |
| C-VIS-07 — Visitante não ganha histórico nem pontos ao criar conta | `backend/internal/api/exclusao_test.go: TestHistoryPrivateAndPaginated` |
| C-VIS-08 — Limites de abuso para identidades anônimas | `backend/internal/api/visitante_test.go: TestVisitorCreationRateLimited` |
| C-EST-01 — Cada profissional tem uma fila | `backend/internal/api/equipe_test.go: TestCreateProfessionalCreatesClosedQueue` |
| C-EST-02 — Catálogo valida preço, duração e pontos | `backend/internal/api/catalogo_test.go: TestServiceValidationAndPublicCatalog` |
| C-EST-03 — Só a dona abre, pausa e fecha | `backend/internal/api/equipe_test.go: TestQueueStatesAndPermissions` |
| C-EST-04 — Pausa e fechamento seguem o anexo | `backend/internal/api/fila_test.go: TestConcurrentEntriesWhilePausing`<br>`backend/internal/api/fila_test.go: TestQueueRulesWithRealParticipations` |
| C-EST-05 — Não desativar profissional com atendimento ativo | `backend/internal/api/equipe_test.go: TestDeactivateAndReactivateProfessional` |
| C-EST-06 — Alterações concorrentes no catálogo | `backend/internal/api/catalogo_test.go: TestConcurrentServiceUpdate` |
| C-EST-07 — Configuração da loja e da equipe | `backend/internal/api/equipe_test.go: TestDeactivateAndReactivateProfessional`<br>`backend/internal/api/equipe_test.go: TestEstablishmentConfiguration` |
| C-FILA-01 — Entrada válida | `backend/internal/api/fila_test.go: TestQueueEntry` |
| C-FILA-02 — Mesma identidade em duas abas: só uma participação | `backend/internal/api/fila_test.go: TestSameIdentityTwoTabs` |
| C-FILA-03 — Dois clientes disputam a entrada enquanto a fila é pausada | `backend/internal/api/fila_test.go: TestConcurrentEntriesWhilePausing` |
| C-FILA-04 — Comanda pendente bloqueia nova entrada | `backend/internal/api/fila_test.go: TestPendingOrderBlocksEntry` |
| C-FILA-05 — Serviço inválido ou fila fechada | `backend/internal/api/fila_test.go: TestEntryValidation` |
| C-FILA-06 — Ordem de chegada determinística | `backend/internal/api/fila_test.go: TestFIFOAndDoubleCall` |
| C-FILA-07 — Duas chamadas simultâneas não ocupam a cadeira duas vezes | `backend/internal/api/fila_test.go: TestFIFOAndDoubleCall` |
| C-FILA-08 — Fila pública não vaza dados | `backend/internal/api/revisao_test.go: TestPublicQueueLeaksNothing` |
| C-FILA-09 — Tolerância de 10 minutos e ausência | `backend/internal/api/fila_test.go: TestToleranceAndNoShow`<br>`frontend/e2e/piloto.spec.ts: roteiro do piloto: do primeiro cliente às pendências da dona` |
| C-FILA-10 — Desistência e cancelamento pela dona | `backend/internal/api/atendimento_test.go: TestOrderEventsInOutbox`<br>`backend/internal/api/fila_test.go: TestCancelParticipation` |
| C-FILA-11 — Transferência válida | `backend/internal/api/fila_test.go: TestTransfer` |
| C-FILA-12 — Transferência disputando com chamada | `backend/internal/api/fila_test.go: TestTransferVersusCall` |
| C-FILA-13 — Tela antiga recebe conflito com dados atuais | `backend/internal/api/fila_test.go: TestCancelVersusStart` |
| C-CMD-01 — Preço adulterado pelo navegador é rejeitado | `backend/internal/api/fila_test.go: TestQueueEntry`<br>`backend/internal/api/pagamento_test.go: TestCashWithChange` |
| C-CMD-02 — Serviço desativado ou alterado depois da inclusão | `backend/internal/api/atendimento_test.go: TestCatalogChangesDoNotRewriteOrder` |
| C-CMD-03 — Edição enquanto aguarda | `backend/internal/api/atendimento_test.go: TestEditItemsWhileWaiting` |
| C-CMD-04 — Solicitação após a chamada | `backend/internal/api/atendimento_test.go: TestChangeRequests` |
| C-CMD-05 — Item realizado não é removido pelo cliente | `backend/internal/api/atendimento_test.go: TestCompleteFreesChair` |
| C-CMD-06 — Edição concorrente não sobrescreve | `backend/internal/api/atendimento_test.go: TestConcurrentOrderEdit` |
| C-CMD-07 — Equipe altera serviços com auditoria | `backend/internal/api/atendimento_test.go: TestOrderAccessAndList` |
| C-ATD-01 — Concluir libera a cadeira; financeiro fica pendente | `backend/internal/api/atendimento_test.go: TestCompleteFreesChair` |
| C-ATD-02 — Concluir com solicitação pendente falha | `backend/internal/api/atendimento_test.go: TestCompleteFreesChair`<br>`backend/internal/api/atendimento_test.go: TestInterruptionPositive` |
| C-ATD-03 — Interrupção com valor positivo | `backend/internal/api/atendimento_test.go: TestInterruptionPositive`<br>`frontend/e2e/piloto.spec.ts: roteiro do piloto: do primeiro cliente às pendências da dona` |
| C-ATD-04 — Interrupção com valor zero | `backend/internal/api/atendimento_test.go: TestInterruptionZero` |
| C-ATD-05 — Correção após fechamento com Pix pendente | `backend/internal/api/atendimento_test.go: TestAdjustClosedOrder`<br>`backend/internal/api/pagamento_test.go: TestAdjustBlockedByRealPix` |
| C-PAG-01 — Pix criado pelo servidor com o valor da comanda | `backend/internal/api/pagamento_test.go: TestPixChargeCreatedByServer` |
| C-PAG-02 — Pix confirmado pelo provedor e conferido pela equipe | `backend/internal/api/pagamento_test.go: TestPixPaidThenConferred` |
| C-PAG-03 — "Já paguei" não confirma nada | `backend/internal/api/pagamento_test.go: TestPixChargeCreatedByServer` |
| C-PAG-04 — Duplo clique na criação do Pix | `backend/internal/api/pagamento_test.go: TestPixDoubleClick` |
| C-PAG-05 — Dinheiro com troco | `backend/internal/api/pagamento_test.go: TestCashWithChange` |
| C-PAG-06 — Timeout ambíguo na criação do Pix | `backend/internal/api/pagamento_test.go: TestPixAmbiguousTimeout` |
| C-PAG-07 — Webhook repetido, fora de ordem ou não autenticado | `backend/internal/api/estorno_test.go: TestWebhookConfirmsPixOnceAndDeduplicates`<br>`backend/internal/api/estorno_test.go: TestWebhookUnauthenticatedTriggersAuthenticatedQuery` |
| C-PAG-08 — Valor divergente | `backend/internal/api/estorno_test.go: TestWebhookDivergences` |
| C-PAG-09 — Repetição de conferência e de recebimento presencial | `backend/internal/api/pagamento_test.go: TestCashWithChange`<br>`backend/internal/api/pagamento_test.go: TestPixPaidThenConferred` |
| C-PAG-10 — Troca de Pix para dinheiro e Pix tardio | `backend/internal/api/estorno_test.go: TestLatePixIssuesAndResolution`<br>`backend/internal/api/pagamento_test.go: TestSwitchFromPixAndLatePayment` |
| C-PAG-11 — Estorno integral de Pix | `backend/internal/api/estorno_test.go: TestRefundPixConfirmedByProvider` |
| C-PAG-12 — Estorno desconhecido, manual e corrida com conferência | `backend/internal/api/estorno_test.go: TestConferAndRefundRace`<br>`backend/internal/api/estorno_test.go: TestRefundAmbiguousResolvedByQuery`<br>`backend/internal/api/estorno_test.go: TestRefundCashNeedsDocumentedEvidence` |
| C-PAG-13 — Cobrança expirada permite nova cobrança | `backend/internal/api/estorno_test.go: TestLatePixIssuesAndResolution`<br>`backend/internal/api/pagamento_test.go: TestPixExpiredAllowsNewCharge` |
| C-FID-01 — Programa começa desligado | `backend/internal/api/fidelidade_test.go: TestLoyaltyProgramStartsDisabled` |
| C-FID-02 — Crédito após conclusão, pagamento integral e conferência | `backend/internal/api/fidelidade_test.go: TestCreditOnceAfterPaidAndConferred` |
| C-FID-03 — Desligar depois da entrada mantém o crédito prometido | `backend/internal/api/fidelidade_test.go: TestCreditOnceAfterPaidAndConferred` |
| C-FID-04 — Sem pontos para visitante, cancelado, ausente, interrompido ou gratuito | `backend/internal/api/fidelidade_test.go: TestCreditOnceAfterPaidAndConferred` |
| C-FID-05 — Extrato explica o saldo | `backend/internal/api/fidelidade_test.go: TestCreditOnceAfterPaidAndConferred`<br>`backend/internal/api/fidelidade_test.go: TestRewardReservationLifecycle` |
| C-FID-06 — Estorno reverte; excesso não; saldo negativo bloqueia resgate | `backend/internal/api/fidelidade_test.go: TestRefundAfterRedemption` |
| C-FID-07 — Prêmio é cortesia separada | `backend/internal/api/fidelidade_test.go: TestDisablingProgramKeepsRights`<br>`backend/internal/api/fidelidade_test.go: TestRewardReservationLifecycle` |
| C-FID-08 — Reserva de 24 horas libera exatamente uma vez | `backend/internal/api/fidelidade_test.go: TestDeliveryVersusExpiry`<br>`backend/internal/api/fidelidade_test.go: TestRewardReservationLifecycle` |
| C-FID-09 — Dois clientes disputam a última unidade (e gasto duplo) | `backend/internal/api/fidelidade_test.go: TestLastUnitConcurrency`<br>`backend/internal/api/fidelidade_test.go: TestNoDoubleSpend` |
| C-FID-10 — Entrega disputando com vencimento; correção da dona | `backend/internal/api/fidelidade_test.go: TestDeliveryVersusExpiry` |
| C-FID-11 — Desligar o programa com reservas ativas | `backend/internal/api/fidelidade_test.go: TestDisablingProgramKeepsRights` |
| C-EXC-01 — Exclusão simples | `backend/internal/api/exclusao_test.go: TestDeletionSimple`<br>`backend/internal/api/revisao_test.go: TestDeletionLeavesNoPersonalDataAnywhere` |
| C-EXC-02 — Exclusão com atendimento ou pagamento pendente | `backend/internal/api/exclusao_test.go: TestDeletionWithPendingWork` |
| C-EXC-03 — Anonimização repetível e retomável | `backend/internal/api/exclusao_test.go: TestDeletionJobResumes`<br>`backend/internal/api/revisao_test.go: TestDeletionLeavesNoPersonalDataAnywhere` |
| C-EXC-04 — Meta de 30 dias gera alerta | `frontend/src/pages/exclusao/exclusao.test.tsx: destaca o que está perto da meta de 30 dias, sem chamar de prazo legal` |
| C-EXC-05 — Dona única não pode excluir a conta | `backend/internal/api/exclusao_test.go: TestDeletionSoleOwner` |
| C-EXC-06 — Profissional exclui a própria conta | `backend/internal/api/exclusao_test.go: TestDeletionProfessional` |
| C-SSE-01 — Comando gera atualização sem polling | `backend/internal/api/stream_test.go: TestStreamSnapshotAndLiveEvents`<br>`frontend/e2e/jornadas.spec.ts: visitante no celular e Beto no computador: do link da loja ao pago, atualizando sozinho` |
| C-SSE-02 — Evento durante o snapshot não se perde | `backend/internal/api/stream_test.go: TestStreamEventDuringSnapshotConverges` |
| C-SSE-03 — Recarregar a página reconstrói o estado do banco | `frontend/e2e/piloto.spec.ts: roteiro do piloto: do primeiro cliente às pendências da dona` |
| C-SSE-04 — Rede cai e volta | `frontend/e2e/jornadas.spec.ts: cliente: cadastro, e-mail, Pix com rede caindo (sem cobrar duas vezes), pontos e prêmio`<br>`frontend/e2e/piloto.spec.ts: roteiro do piloto: do primeiro cliente às pendências da dona`<br>`frontend/src/stream/stream.test.tsx: aparelho sem internet: fecha a conexão e avisa na hora; volta com a rede, pelo cursor` |
| C-SSE-05 — Audiência indevida não recebe nada | `backend/internal/api/stream_test.go: TestStreamAudienceIsolation` |
| C-SSE-06 — Sessão revogada encerra a conexão | `backend/internal/api/stream_test.go: TestStreamSessionRevoked` |
| C-SSE-07 — Cursor velho ou banco restaurado | `backend/internal/api/stream_test.go: TestStreamResumeAndCursorFallbacks` |
| C-SSE-08 — Dois processos de API entregam a mesma sequência | `backend/internal/api/stream_test.go: TestStreamTwoAPIProcessesAndRestart` |
| C-SEG-01 — Negação por padrão | `backend/internal/api/identidade_test.go: TestRoleEscalationAndCrossEstablishment` |
| C-SEG-02 — Dono não acessa outra loja | `backend/internal/api/atendimento_test.go: TestOrderAccessAndList`<br>`backend/internal/api/equipe_test.go: TestCrossEstablishmentIsolation`<br>`backend/internal/api/identidade_test.go: TestRoleEscalationAndCrossEstablishment`<br>`backend/internal/api/revisao_test.go: TestAuthorizationSweepAllRoutes` |
| C-SEG-03 — Cliente não vê a comanda de outro cliente | `backend/internal/api/atendimento_test.go: TestOrderAccessAndList`<br>`backend/internal/api/revisao_test.go: TestAuthorizationSweepAllRoutes` |
| C-SEG-04 — Profissional fora da própria fila | `backend/internal/api/atendimento_test.go: TestOrderAccessAndList`<br>`backend/internal/api/equipe_test.go: TestQueueStatesAndPermissions`<br>`backend/internal/api/revisao_test.go: TestAuthorizationSweepAllRoutes` |
| C-SEG-05 — Papel usado é o da sessão, não o declarado | `backend/internal/api/identidade_test.go: TestRoleEscalationAndCrossEstablishment` |
| C-SEG-06 — Profissional desativado perde acesso imediatamente | `backend/internal/api/equipe_test.go: TestDeactivateAndReactivateProfessional` |
| C-SEG-07 — Nick com HTML/script é só texto | `backend/internal/api/revisao_test.go: TestInjectionStringsAreData`<br>`backend/internal/api/visitante_test.go: TestVisitorSessionAndRecoveryCode`<br>`frontend/e2e/piloto.spec.ts: roteiro do piloto: do primeiro cliente às pendências da dona` |
| C-SEG-08 — Segredos fora de logs, URLs e bundle | `backend/internal/api/contrato_test.go: TestNoSecretsInLogsAuditOrDatabase` |
| C-SEG-09 — CSRF e origem | `backend/internal/api/identidade_test.go: TestCSRFAndOrigin` |
| C-SEG-12 — Varredura de autorização em todas as rotas | `backend/internal/api/revisao_test.go: TestAuthorizationSweepAllRoutes` |
| C-SEG-10 — Limites de corpo e campos | `backend/internal/api/revisao_test.go: TestBodyAndFieldLimits` |
| C-SEG-11 — Auditoria sem dados pessoais | `backend/internal/api/contrato_test.go: TestNoSecretsInLogsAuditOrDatabase` |
| C-WEB-01 — Larguras previstas sem rolagem horizontal | `frontend/e2e/telas.spec.ts: telas públicas` |
| C-WEB-02 — Acessibilidade básica | `frontend/e2e/telas.spec.ts: só teclado: pular para o conteúdo, entrar e foco sempre visível`<br>`frontend/e2e/telas.spec.ts: telas públicas` |
| C-WEB-03 — Offline não gera falsa confirmação | `frontend/e2e/jornadas.spec.ts: cliente: cadastro, e-mail, Pix com rede caindo (sem cobrar duas vezes), pontos e prêmio`<br>`frontend/src/stream/stream.test.tsx: aparelho sem internet: fecha a conexão e avisa na hora; volta com a rede, pelo cursor` |
| C-WEB-04 — Navegadores registrados | **nenhum** |
| C-OPS-01 — Restauração reconcilia pagamentos antes de religar o worker | `backend/internal/api/restauracao_test.go: TestRestoreReconcilesBeforeWorkerResumes`<br>`frontend/e2e/operacao.spec.ts: restauração de backup: worker travado até conciliar; pagamento depois do backup não se perde` |
| C-OPS-02 — Implantação não perde fila | `frontend/e2e/operacao.spec.ts: implantação com gente na fila: nada se perde, ninguém vê erro e as telas voltam sozinhas` |
| C-OPS-03 — Worker retoma depois de cair | `backend/cmd/worker/main_test.go: TestWorkerHeldDoesNothingUntilReleased`<br>`frontend/e2e/operacao.spec.ts: worker morto no meio do trabalho: ao voltar, faz tudo o que ficou parado (uma vez)` |
| C-OPS-04 — Produção não ativa com integração financeira simulada | `backend/internal/platform/config/config_test.go: TestProductionPixOnlyOfficialHost`<br>`backend/internal/platform/config/config_test.go: TestStagingIsHardenedButNeverCharges` |

## 3. Cenários sem teste automatizado

- C-WEB-04 — Navegadores registrados (tipo declarado: manual)
