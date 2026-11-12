# Matriz de permissões

Princípio: **negar por padrão**. O servidor decide pelo ator da sessão (nunca por papel enviado no corpo), pela loja da rota e pela propriedade do recurso. Recurso fora do escopo responde `404`.
Atores e escopos estão definidos em [contrato/convencoes.md §12](contrato/convencoes.md).

## 1. Resumo por papel (anexo §11, detalhado)

| Ação | Visitante | Cliente (conta) | Profissional | Dono |
|---|---|---|---|---|
| Ver fila pública e catálogo | Sim | Sim | Sim | Sim |
| Entrar / desistir | Própria visita | Própria visita | Como cliente (sessão `customer`) | Como cliente (sessão `customer`) |
| Ver comanda | Visita atual (até 24 h após encerrar) | Própria | Atendimentos da própria fila | Toda a loja |
| Alterar serviços | Aguardando: direto; após chamada: solicitação | Idem | Atendimento próprio (com motivo) | Toda a loja (com motivo) |
| Aprovar/recusar solicitação | Não | Não | Atendimento próprio | Toda a loja |
| Chamar / iniciar / ausência / concluir / interromper | Não | Não | Própria fila | Toda a loja |
| Cancelar participação de outra pessoa | Não | Não | Não | Toda a loja (com motivo) |
| Escolher meio de pagamento / gerar Pix | Própria visita | Própria visita | Atendimento próprio | Toda a loja |
| Registrar dinheiro/maquininha, conferir Pix, cancelar Pix | Não | Não | Atendimento próprio | Toda a loja |
| Estornar, confirmar/negar estorno manual, corrigir comanda fechada, fechar interrupção | Não | Não | Não | Toda a loja (reautenticado, exceto fechar interrupção) |
| Pendências financeiras e painel de pendências | Não | Não | Não | Toda a loja |
| Abrir / pausar / fechar / transferir | Não | Não | Não (só sinaliza indisponibilidade) | Toda a loja |
| Preços, serviços, equipe, convites, loja | Não | Não | Lê catálogo e loja | Toda a loja (equipe: reautenticado) |
| Histórico pessoal | Não | Próprio | Como cliente | Como cliente |
| Consultar saldo / reservar / cancelar reserva | Não | Próprio | Como cliente | Como cliente |
| Confirmar entrega de prêmio | Não | Não | Na própria loja | Na própria loja |
| Corrigir entrega de prêmio | Não | Não | Não | Na própria loja |
| Configurar programa e prêmios | Não | Não | Não | Própria loja |
| Auditoria da loja | Não | Não | Não | Própria loja |
| Solicitar exclusão | Não tem conta | Própria conta | Própria conta | Própria conta (bloqueado se dono único) |
| Exclusões pendentes da loja | Não | Não | Não | Própria loja |
| Criar loja / dono inicial / transferir administração | Não | Não | Não | Não — processo controlado do operador (ADR-0013) |

Regras gerais:

- **Profissional desativado** perde acesso e conexões SSE imediatamente (R-PERM-03).
- **Dono não acessa outra loja** nem histórico do cliente em outras lojas (R-PERM-02).
- **Mesma pessoa, dois papéis:** papéis de equipe só com sessão de contexto `staff`; agir como cliente com sessão `customer` (R-PERF-03).
- **Eventos SSE** seguem a mesma matriz por audiência (ver [contrato/eventos-sse.md §3](contrato/eventos-sse.md)).

## 2. Por operação (gerado do OpenAPI)

Não edite à mão: rode `python docs/tools/verificar_contrato.py --write`. Células mostram o escopo exigido; `—` = negado; `sim` = permitido sem condição adicional.

<!-- gerado:permissoes:inicio -->
| Operação | Método e rota | Público | Visitante | Cliente | Profissional | Dono | Provedor | Reauth | Transições |
|---|---|---|---|---|---|---|---|---|---|
| `getLiveness` | `GET /healthz/live` | sim | — | — | — | — | — | — | — |
| `getReadiness` | `GET /healthz/ready` | sim | — | — | — | — | — | — | — |
| `verifyEmail` | `POST /v1/auth/email-verification/confirm` | sim | — | — | — | — | — | — | T-AC-02 |
| `resendVerification` | `POST /v1/auth/email-verification/resend` | sim | — | — | — | — | — | — | — |
| `acceptInvite` | `POST /v1/auth/invites/accept` | sim | — | — | — | — | — | — | T-IN-02 |
| `login` | `POST /v1/auth/login` | sim | — | — | — | — | — | — | T-SS-01 |
| `logout` | `POST /v1/auth/logout` | — | — | self | self | self | — | — | T-SS-02 |
| `confirmPasswordReset` | `POST /v1/auth/password-reset/confirm` | sim | — | — | — | — | — | — | T-PW-02, T-SS-04 |
| `requestPasswordReset` | `POST /v1/auth/password-reset/request` | sim | — | — | — | — | — | — | T-PW-01 |
| `changePassword` | `POST /v1/auth/password/change` | — | — | self | self | self | — | — | T-SS-03 |
| `reauthenticate` | `POST /v1/auth/reauthenticate` | — | — | self | self | self | — | — | T-SS-06 |
| `registerAccount` | `POST /v1/auth/register` | sim | — | — | — | — | — | — | T-AC-01 |
| `getDeletionRequestStatus` | `POST /v1/deletion-requests/status` | protocol_holder | — | — | — | — | — | — | — |
| `getEstablishment` | `GET /v1/establishments/{establishmentId}` | — | — | — | establishment_member | establishment | — | — | — |
| `updateEstablishment` | `PATCH /v1/establishments/{establishmentId}` | — | — | — | — | establishment | — | — | T-ES-01 |
| `listAuditEvents` | `GET /v1/establishments/{establishmentId}/audit-events` | — | — | — | — | establishment | — | — | — |
| `listDeletionRequests` | `GET /v1/establishments/{establishmentId}/deletion-requests` | — | — | — | — | establishment | — | — | — |
| `listFinancialIssues` | `GET /v1/establishments/{establishmentId}/financial-issues` | — | — | — | — | establishment | — | — | — |
| `resolveFinancialIssue` | `POST /v1/establishments/{establishmentId}/financial-issues/{issueId}/resolve` | — | — | — | — | establishment | — | — | T-FI-02 |
| `revokeInvite` | `POST /v1/establishments/{establishmentId}/invites/{inviteId}/revoke` | — | — | — | — | establishment | — | — | T-IN-03 |
| `getLoyaltyProgram` | `GET /v1/establishments/{establishmentId}/loyalty-program` | — | — | — | — | establishment | — | — | — |
| `updateLoyaltyProgram` | `PUT /v1/establishments/{establishmentId}/loyalty-program` | — | — | — | — | establishment | — | — | T-LP-01, T-LP-02 |
| `getCurrentVisit` | `GET /v1/establishments/{establishmentId}/my-visit` | — | visit_owner | visit_owner | — | — | — | — | — |
| `listOrders` | `GET /v1/establishments/{establishmentId}/orders` | — | — | — | own_attendance | establishment | — | — | — |
| `getOrder` | `GET /v1/establishments/{establishmentId}/orders/{orderId}` | — | visit_owner | visit_owner | own_attendance | establishment | — | — | — |
| `adjustClosedOrder` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/adjustments` | — | — | — | — | establishment | — | sim | T-O-11 |
| `createChangeRequest` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/change-requests` | — | visit_owner | visit_owner | — | — | — | — | T-CR-01 |
| `approveChangeRequest` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/change-requests/{changeRequestId}/approve` | — | — | — | own_attendance | establishment | — | — | T-CR-02 |
| `rejectChangeRequest` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/change-requests/{changeRequestId}/reject` | — | — | — | own_attendance | establishment | — | — | T-CR-03 |
| `closeInterruptedOrder` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/close-interruption` | — | — | — | — | establishment | — | — | T-O-07, T-O-08 |
| `replaceOrderItems` | `PUT /v1/establishments/{establishmentId}/orders/{orderId}/items` | — | visit_owner | visit_owner | own_attendance | establishment | — | — | T-O-02, T-O-03 |
| `markItemPerformed` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/items/{itemId}/mark-performed` | — | — | — | own_attendance | establishment | — | — | T-O-13 |
| `choosePaymentMethod` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/payment-method` | — | visit_owner | visit_owner | own_attendance | establishment | — | — | T-O-12 |
| `createPixCharge` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/pix-charges` | — | visit_owner | visit_owner | own_attendance | establishment | — | — | T-PX-01, T-PX-02, T-PX-03, T-PX-08 |
| `getPixCharge` | `GET /v1/establishments/{establishmentId}/orders/{orderId}/pix-charges/{chargeId}` | — | visit_owner | visit_owner | own_attendance | establishment | — | — | — |
| `cancelPixCharge` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/pix-charges/{chargeId}/cancel` | — | — | — | own_attendance | establishment | — | — | T-PX-07 |
| `registerInPersonReceipt` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/receipts` | — | — | — | own_attendance | establishment | — | — | T-RC-01, T-O-10, T-LG-01 |
| `conferReceipt` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/receipts/{receiptId}/confer` | — | — | — | own_attendance | establishment | — | — | T-RC-03, T-O-10, T-LG-01 |
| `requestRefund` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/receipts/{receiptId}/refunds` | — | — | — | — | establishment | — | sim | T-RF-01, T-RF-02, T-RF-05 |
| `confirmRefundManually` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/refunds/{refundId}/confirm` | — | — | — | — | establishment | — | sim | T-RF-06, T-LG-02 |
| `markRefundFailed` | `POST /v1/establishments/{establishmentId}/orders/{orderId}/refunds/{refundId}/mark-failed` | — | — | — | — | establishment | — | sim | T-RF-07 |
| `createParticipation` | `POST /v1/establishments/{establishmentId}/participations` | — | self | self | — | — | — | — | T-P-01, T-O-01 |
| `getParticipation` | `GET /v1/establishments/{establishmentId}/participations/{participationId}` | — | visit_owner | visit_owner | own_queue | establishment | — | — | — |
| `cancelParticipation` | `POST /v1/establishments/{establishmentId}/participations/{participationId}/cancel` | — | visit_owner | visit_owner | — | establishment | — | — | T-P-04, T-P-05, T-O-05, T-CR-04 |
| `completeService` | `POST /v1/establishments/{establishmentId}/participations/{participationId}/complete` | — | — | — | own_queue | establishment | — | — | T-P-08, T-O-04 |
| `interruptService` | `POST /v1/establishments/{establishmentId}/participations/{participationId}/interrupt` | — | — | — | own_queue | establishment | — | — | T-P-09, T-O-06, T-CR-04 |
| `markNoShow` | `POST /v1/establishments/{establishmentId}/participations/{participationId}/no-show` | — | — | — | own_queue | establishment | — | — | T-P-06, T-O-05, T-CR-04 |
| `startService` | `POST /v1/establishments/{establishmentId}/participations/{participationId}/start` | — | — | — | own_queue | establishment | — | — | T-P-03 |
| `transferParticipation` | `POST /v1/establishments/{establishmentId}/participations/{participationId}/transfer` | — | — | — | — | establishment | — | — | T-P-07 |
| `listPendencies` | `GET /v1/establishments/{establishmentId}/pendencies` | — | — | — | — | establishment | — | — | — |
| `listProfessionals` | `GET /v1/establishments/{establishmentId}/professionals` | — | — | — | — | establishment | — | — | — |
| `createProfessional` | `POST /v1/establishments/{establishmentId}/professionals` | — | — | — | — | establishment | — | — | T-PR-01 |
| `updateProfessional` | `PATCH /v1/establishments/{establishmentId}/professionals/{professionalId}` | — | — | — | — | establishment | — | — | T-PR-04 |
| `deactivateProfessional` | `POST /v1/establishments/{establishmentId}/professionals/{professionalId}/deactivate` | — | — | — | — | establishment | — | sim | T-PR-02 |
| `createInvite` | `POST /v1/establishments/{establishmentId}/professionals/{professionalId}/invites` | — | — | — | — | establishment | — | sim | T-IN-01 |
| `reactivateProfessional` | `POST /v1/establishments/{establishmentId}/professionals/{professionalId}/reactivate` | — | — | — | — | establishment | — | sim | T-PR-03 |
| `listStaffQueues` | `GET /v1/establishments/{establishmentId}/queues` | — | — | — | own_queue | establishment | — | — | — |
| `signalAvailability` | `POST /v1/establishments/{establishmentId}/queues/{queueId}/availability-signal` | — | — | — | own_queue | — | — | — | T-Q-05 |
| `callNext` | `POST /v1/establishments/{establishmentId}/queues/{queueId}/call-next` | — | — | — | own_queue | establishment | — | — | T-P-02 |
| `closeQueue` | `POST /v1/establishments/{establishmentId}/queues/{queueId}/close` | — | — | — | — | establishment | — | — | T-Q-04 |
| `openQueue` | `POST /v1/establishments/{establishmentId}/queues/{queueId}/open` | — | — | — | — | establishment | — | — | T-Q-01, T-Q-03 |
| `pauseQueue` | `POST /v1/establishments/{establishmentId}/queues/{queueId}/pause` | — | — | — | — | establishment | — | — | T-Q-02 |
| `listCustomerRewards` | `GET /v1/establishments/{establishmentId}/reward-catalog` | — | — | self | — | — | — | — | — |
| `listRewardReservations` | `GET /v1/establishments/{establishmentId}/reward-reservations` | — | — | — | establishment_member | establishment | — | — | — |
| `createRewardReservation` | `POST /v1/establishments/{establishmentId}/reward-reservations` | — | — | self | — | — | — | — | T-RW-01 |
| `cancelRewardReservation` | `POST /v1/establishments/{establishmentId}/reward-reservations/{reservationId}/cancel` | — | — | self | — | — | — | — | T-RW-02 |
| `deliverRewardReservation` | `POST /v1/establishments/{establishmentId}/reward-reservations/{reservationId}/deliver` | — | — | — | establishment_member | establishment | — | — | T-RW-04 |
| `revertRewardDelivery` | `POST /v1/establishments/{establishmentId}/reward-reservations/{reservationId}/revert-delivery` | — | — | — | — | establishment | — | — | T-RW-05 |
| `listRewards` | `GET /v1/establishments/{establishmentId}/rewards` | — | — | — | — | establishment | — | — | — |
| `createReward` | `POST /v1/establishments/{establishmentId}/rewards` | — | — | — | — | establishment | — | — | T-RD-01 |
| `updateReward` | `PATCH /v1/establishments/{establishmentId}/rewards/{rewardId}` | — | — | — | — | establishment | — | — | T-RD-02 |
| `deactivateReward` | `POST /v1/establishments/{establishmentId}/rewards/{rewardId}/deactivate` | — | — | — | — | establishment | — | — | T-RD-03 |
| `listServices` | `GET /v1/establishments/{establishmentId}/services` | — | — | — | establishment_member | establishment | — | — | — |
| `createService` | `POST /v1/establishments/{establishmentId}/services` | — | — | — | — | establishment | — | — | T-SV-01 |
| `updateService` | `PATCH /v1/establishments/{establishmentId}/services/{serviceId}` | — | — | — | — | establishment | — | — | T-SV-02 |
| `deactivateService` | `POST /v1/establishments/{establishmentId}/services/{serviceId}/deactivate` | — | — | — | — | establishment | — | — | T-SV-03 |
| `setServiceProfessionals` | `PUT /v1/establishments/{establishmentId}/services/{serviceId}/professionals` | — | — | — | — | establishment | — | — | T-SV-05 |
| `reactivateService` | `POST /v1/establishments/{establishmentId}/services/{serviceId}/reactivate` | — | — | — | — | establishment | — | — | T-SV-04 |
| `createVisitorSession` | `POST /v1/establishments/{establishmentId}/visitor-sessions` | sim | — | — | — | — | — | — | T-VS-01 |
| `recoverVisitorSession` | `POST /v1/establishments/{establishmentId}/visitor-sessions/recover` | sim | — | — | — | — | — | — | T-VS-02 |
| `updateMyProfile` | `PATCH /v1/me` | — | — | self | self | self | — | — | — |
| `requestAccountDeletion` | `POST /v1/me/deletion-request` | — | — | self | self | self | — | sim | T-AC-03, T-P-10, T-O-05, T-RW-06, T-LG-03 |
| `listMyHistory` | `GET /v1/me/history` | — | — | self | — | — | — | — | — |
| `getMyLoyalty` | `GET /v1/me/loyalty/{establishmentId}` | — | — | self | — | — | — | — | — |
| `listMyLoyaltyLedger` | `GET /v1/me/loyalty/{establishmentId}/ledger` | — | — | self | — | — | — | — | — |
| `listPublicQueues` | `GET /v1/public/establishments/{establishmentId}/queues` | sim | — | — | — | — | — | — | — |
| `listPublicServices` | `GET /v1/public/establishments/{establishmentId}/services` | sim | — | — | — | — | — | — | — |
| `getPublicEstablishmentBySlug` | `GET /v1/public/slugs/{slug}` | sim | — | — | — | — | — | — | — |
| `getSession` | `GET /v1/session` | sim | — | — | — | — | — | — | — |
| `openEventStream` | `GET /v1/stream` | sim | visit_owner | self | own_queue | establishment | — | — | — |
| `endVisitorSession` | `POST /v1/visitor-sessions/current/end` | — | self | — | — | — | — | — | T-VS-03 |
| `receiveAbacatePayWebhook` | `POST /v1/webhooks/abacatepay` | — | — | — | — | — | provider_signature | — | T-PX-05, T-PX-06, T-RC-02, T-RC-04, T-O-09, T-RF-03, T-RF-04, T-LG-02 |
<!-- gerado:permissoes:fim -->
