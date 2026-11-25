# Matriz de transições de estado

Cada linha é uma mudança de estado permitida. **Tudo que não está aqui é proibido** e responde `409 invalid_transition` (ou código mais específico) sem alterar nada.
Nomes de estado são os códigos da API (inglês); a tradução está em cada seção. Regras em [regras.md](regras.md); contrato em [contrato/openapi.yaml](contrato/openapi.yaml).

## Por que há várias máquinas separadas

Terminar o corte **não** significa que o dinheiro entrou. Por isso:

| Máquina | Responde à pergunta | Exemplo |
|---|---|---|
| Fila (`QueueStatus`) | A fila do profissional aceita gente/chamadas? | `paused` |
| Participação (`ParticipationStatus`) | Onde a pessoa está na jornada da cadeira? | `completed` libera a cadeira |
| Comanda (`OrderStatus`) | Qual a situação financeira da visita? | `awaiting_payment` mesmo com participação `completed` |
| Cobrança Pix (`PixChargeStatus`) | O que aconteceu com o pedido ao provedor? | `unknown` após timeout |
| Recebimento (`ReceiptStatus`) | Dinheiro que entrou foi conferido pela equipe? | Pix `registered` → `conferred` |
| Estorno (`RefundStatus`) | Devolução foi confirmada com evidência? | `under_review` |
| Reserva de prêmio (`ReservationStatus`) | Prêmio está travado, entregue, vencido? | `active` por 24 h |

## Legenda

**Idempotência**
- **IK** — `Idempotency-Key` por ator + operação (24 h): repetição devolve a mesma resposta, sem novo efeito; mesma chave com corpo diferente → `422 idempotency_key_reused`.
- **VE** — versão esperada (`expected_version`): versão diferente → `409 version_conflict` + `current`.
- **UQ(…)** — restrição única no banco que impede duplicata mesmo com chaves diferentes.
- **NAT** — idempotência natural: aplicar de novo não cria registro novo (ex.: evento de provedor deduplicado pelo ID externo).
- **ÚNICA-VEZ(…)** — efeito colateral com chave de origem única (ex.: crédito de pontos por comanda).

**Auditoria** (tabela `audit_event`, sem e-mail/nick/telefone; ator por pseudônimo interno)
- **A** — registra ator, transição, estados de/para, `request_id`.
- **A+M** — idem, com motivo obrigatório.
- **A+E** — idem, com evidência (referência externa ou nota) obrigatória.
- **—** — sem auditoria de negócio (apenas log técnico).

**Ator** — `visitante`, `cliente` (conta), `profissional` (contexto staff, vínculo ativo), `dono` (contexto staff, vínculo ativo), `provedor` (notificação autenticada), `sistema` (worker), `operador` (linha de comando controlada). Escopos como em [contrato/convencoes.md §12](contrato/convencoes.md).

---

## 1. Fila do profissional (`QueueStatus`)

Estados: `closed` (fechada, inicial), `open` (aberta), `paused` (pausada).

```mermaid
stateDiagram-v2
  [*] --> closed
  closed --> open: T-Q-01
  open --> paused: T-Q-02
  paused --> open: T-Q-03
  open --> closed: T-Q-04
  paused --> closed: T-Q-04
```

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-Q-01 | closed → open | dono (establishment) | Profissional `active` | Aceita entradas e chamadas | IK + VE | A | `professional_inactive`, `version_conflict` | `openQueue` |
| T-Q-02 | open → paused | dono | — | Bloqueia entradas e chamadas; permite iniciar quem já foi chamado e concluir atendimento | IK + VE | A | `invalid_transition`, `version_conflict` | `pauseQueue` |
| T-Q-03 | paused → open | dono | Profissional `active` | Volta a aceitar | IK + VE | A | `professional_inactive`, `version_conflict` | `openQueue` |
| T-Q-04 | open/paused → closed | dono | **Zero** participações `waiting` ou `called` na fila (transferir ou cancelar antes) | Bloqueia entradas e chamadas; `in_service` pode ser concluído/interrompido | IK + VE | A | `queue_has_pending_participants`, `version_conflict` | `closeQueue` |
| T-Q-05 | (sem mudança) sinal de indisponibilidade | profissional (own_queue) | — | Marca/desmarca sinal para o dono; **não** muda estado da fila | IK | A | — | `signalAvailability` |
| T-Q-06 | entrada open → closed (operação não muda) | dono / profissional (own_queue, se `professionals_control_admission`) | Entrada aberta | Bloqueia só NOVAS participações; quem está continua, chamadas e atendimentos seguem; desistência não reabre (ADR-0035) | IK + VE | A | `invalid_transition`, `version_conflict`, 403 sem permissão | `closeAdmission` |
| T-Q-07 | entrada closed → open | dono / profissional (own_queue, se `professionals_control_admission`) | Entrada fechada | Remove o bloqueio manual; **não** ignora a capacidade (lotada continua `full`) | IK + VE | A | `invalid_transition`, `version_conflict` | `reopenAdmission` |
| T-Q-08 | capacidade da fila | dono | 1–200 ou null (padrão da loja) | Vale para novas entradas e transferências; reduzir não tira ninguém | IK + VE | A | `validation_failed`, `version_conflict` | `setQueueCapacity` |

Invariantes: abrir/pausar/fechar só o dono (R-FILA-07). Entrar exige `open`; `callNext` exige `open`; `startService` aceita `open`, `paused` ou `closed`.

---

## 2. Participação (`ParticipationStatus`)

Estados: `waiting` (aguardando), `called` (chamado), `in_service` (em atendimento), `completed` (concluído), `cancelled` (cancelado), `no_show` (ausente), `interrupted` (interrompido). Ativos = `waiting`, `called`, `in_service`.

```mermaid
stateDiagram-v2
  [*] --> waiting: T-P-01
  waiting --> waiting: T-P-07 transferência / T-P-11 mudar de lugar
  called --> waiting: T-P-11 descer (troca com o próximo)
  waiting --> waiting: T-P-12 aviso de atraso
  called --> called: T-P-12 aviso de atraso
  waiting --> called: T-P-02
  called --> in_service: T-P-03
  waiting --> cancelled: T-P-04/05/10
  called --> cancelled: T-P-04/05/10
  called --> no_show: T-P-06
  in_service --> completed: T-P-08
  in_service --> interrupted: T-P-09
```

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-P-01 | ∅ → waiting | visitante / cliente (self) | Fila `open`; profissional `active`; 1–10 serviços **ativos**, distintos e **habilitados** para o profissional; identidade sem participação ativa na loja; identidade sem comanda `open`/`pending_review`/`awaiting_payment` na loja; limites de abuso | Cria participação (ordem FIFO = sequência atribuída sob bloqueio da fila), senha do dia, comanda `open` com snapshots dos itens, **snapshot de elegibilidade de pontos** (conta + programa ligado) | IK + UQ(uma participação ativa por identidade/loja) | A | `queue_not_open`, `service_inactive`, `service_not_eligible`, `active_participation_exists`, `pending_order_exists`, `rate_limited` | `createParticipation` |
| T-P-02 | waiting → called | profissional (own_queue) / dono | Fila `open`; **nenhuma** participação `called`/`in_service` na mesma fila; existe `waiting` | Servidor escolhe o primeiro FIFO; grava `called_at` e `tolerance_ends_at = called_at + 10 min` | IK + UQ(uma chamada/atendimento por fila) | A | `queue_not_open`, `professional_busy`, `no_waiting_participant` | `callNext`, `callAppointment` |
| T-P-03 | called → in_service | profissional (own_queue) / dono | Participação `called` (mesmo após os 10 min, enquanto não marcada ausente); fila em qualquer estado | Grava `started_at` | IK + VE | A | `invalid_transition`, `version_conflict` | `startService`, `callNext` |
| T-P-04 | waiting/called → cancelled | visitante / cliente (visit_owner) | Trabalho não iniciado | Libera posição/cadeira; comanda → `closed_no_charge` (T-O-05); solicitação pendente → `voided`; sem perda de pontos | IK + VE | A | `invalid_transition` (ex.: já `in_service`), `version_conflict` | `cancelParticipation` |
| T-P-05 | waiting/called → cancelled | dono (establishment) | Trabalho não iniciado; **motivo obrigatório** | Igual a T-P-04, `closure_reason = cancelled_by_owner` | IK + VE | A+M | `validation_failed` (sem motivo), `invalid_transition`, `version_conflict` | `cancelParticipation` |
| T-P-06 | called → no_show | profissional (own_queue) / dono | `now ≥ tolerance_ends_at` (relógio do servidor) | Libera cadeira; comanda → `closed_no_charge`; sem punição; retorno exige nova entrada (T-P-01) | IK + VE | A | `tolerance_not_elapsed`, `invalid_transition`, `version_conflict` | `markNoShow` |
| T-P-07 | waiting → waiting (outra fila) | dono | Consentimento do cliente registrado (`customer_consent.given = true`); motivo; fila destino `open`, profissional destino `active`; **todos** os serviços aceitos habilitados no destino; destino ≠ origem | Nova posição no **fim** da fila destino; nova senha do destino; preços aceitos mantidos; estimativa recalculada | IK + VE | A+M | `transfer_consent_required`, `transfer_target_incompatible`, `queue_not_open`, `invalid_transition`, `version_conflict` | `transferParticipation` |
| T-P-08 | in_service → completed | profissional (own_queue) / dono | Sem solicitação `pending`; cada item aceito com resultado (`performed`/`not_performed`+motivo); ≥1 `performed` | Libera cadeira; comanda fecha valor (T-O-04) | IK + VE (participação e comanda) | A | `change_request_pending`, `performed_item_required`, `version_conflict` | `completeService` |
| T-P-09 | in_service → interrupted | profissional (own_queue) / dono | Motivo; resultado de cada item (`fully_performed`/`partially_performed`/`not_started`) | Libera cadeira; comanda → `pending_review` (T-O-06); solicitação pendente → `voided`; **sem pontos** | IK + VE (ambas) | A+M | `validation_failed`, `version_conflict` | `interruptService` |
| T-P-10 | waiting/called → cancelled | sistema (exclusão de conta) | Exclusão solicitada pelo titular (T-AC-03) | Igual a T-P-04 com `closure_reason = cancelled_by_account_deletion` | ÚNICA-VEZ(exclusão, participação) | A | — | `requestAccountDeletion` |
| T-P-11 | waiting ↔ waiting; called ↔ waiting (mesma fila) | profissional (own_queue) / dono | Participação `waiting` ou `called`; existe vizinha na direção pedida (`up`: a que aguarda logo à frente ou, para a primeira que aguarda, a `called`; `down`: a que aguarda logo atrás); troca com a `called` exige fila `open`; `in_service` nunca se move | As duas trocam de posição; se uma estava `called`, ela volta a `waiting` (sem `called_at`) e a outra passa a `called` com nova tolerância de 10 min | IK + VE (da pessoa movida; a vizinha é travada pelo bloqueio da fila) | A (as duas) | `invalid_transition`, `queue_not_open`, `version_conflict` | `moveParticipation` |
| T-P-12 | waiting → waiting; called → called (aviso de atraso) | visitante / cliente (visit_owner) | Participação `waiting` ou `called`; sem aviso ativo (`late_notice_at` nulo) | Grava `late_notice_at`; estado, posição e tolerância não mudam; a equipe vê o aviso e é notificada; nova chamada (T-P-02 ou T-P-11) limpa o aviso | IK + VE | A | `invalid_transition`, `version_conflict` | `noticeLate` |

Invariantes no banco:
- Índice único parcial: `(establishment_id, identity_id) WHERE status IN ('waiting','called','in_service')`.
- Índice único parcial: `(queue_id) WHERE status IN ('called','in_service')` — uma cadeira, uma pessoa.
- Ordem FIFO: `(queue_id, queue_seq)` com `queue_seq` atribuído com a linha da fila bloqueada (`SELECT … FOR UPDATE`); desempate determinístico.
- Estados finais (`completed`, `cancelled`, `no_show`, `interrupted`) não mudam mais. `interrupted` continua `interrupted` mesmo após acerto financeiro.

---

## 3. Comanda (`OrderStatus`) e itens (`OrderItemStatus`)

Estados da comanda: `open` (aberta — serviços em definição), `pending_review` (aguardando revisão do dono após interrupção), `awaiting_payment` (valor fechado > 0, falta receber), `awaiting_confirmation` (recebido por Pix, falta conferência), `settled` (encerrada: recebida integralmente e conferida), `closed_no_charge` (encerrada sem valor).
Estados do item: `accepted`, `performed`, `not_performed`, `removed`.

```mermaid
stateDiagram-v2
  [*] --> open: T-O-01
  open --> awaiting_payment: T-O-04 concluir
  open --> closed_no_charge: T-O-05 cancelar/ausente
  open --> pending_review: T-O-06 interromper
  pending_review --> awaiting_payment: T-O-07
  pending_review --> closed_no_charge: T-O-08
  awaiting_payment --> awaiting_confirmation: T-O-09 Pix recebido
  awaiting_payment --> settled: T-O-10 dinheiro/cartão
  awaiting_confirmation --> settled: T-O-10 conferência
```

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-O-01 | ∅ → open | (junto com T-P-01) | Ver T-P-01 | Itens `accepted` com snapshot de nome, preço, duração, pontos; total calculado no servidor | (de T-P-01) | A | (de T-P-01) | `createParticipation`, `callAppointment` |
| T-O-02 | open → open (nova versão) | visitante / cliente (visit_owner) | Participação `waiting`; ≥1 serviço; sem duplicado; serviços novos ativos e habilitados; itens existentes mantidos preservam snapshot | Substitui conjunto; item retirado → `removed`; recalcula total e estimativa | IK + VE | A | `order_not_editable` (já chamado), `order_requires_item`, `duplicate_service`, `service_inactive`, `service_not_eligible`, `version_conflict` | `replaceOrderItems` |
| T-O-03 | open → open (nova versão) | profissional (own_attendance) / dono | Participação `waiting`/`called`/`in_service`; sem solicitação `pending`; item `performed` não pode ser retirado; motivo quando ator é equipe | Igual a T-O-02, autor = equipe | IK + VE | A+M | `change_request_pending`, `item_already_performed`, `order_requires_item`, `version_conflict` | `replaceOrderItems` |
| T-O-04 | open → awaiting_payment | (junto com T-P-08) | Ver T-P-08 | Itens `performed` → cobrados; `not_performed` → zero; **total congelado** (`total_frozen_at`); pagamento começa separado | (de T-P-08) | A | (de T-P-08) | `completeService` |
| T-O-05 | open → closed_no_charge | (junto com T-P-04/05/06/10) | Trabalho não iniciado | `closure_reason` preenchido; sem cobrança, sem recebimento fictício, sem pontos | (da origem) | A | — | `cancelParticipation`, `markNoShow`, `requestAccountDeletion` |
| T-O-06 | open → pending_review | (junto com T-P-09) | Ver T-P-09 | Itens recebem resultado informado pelo profissional; aguarda dono | (de T-P-09) | A+M | — | `interruptService` |
| T-O-07 | pending_review → awaiting_payment | dono | Revisão com nota; resultado final por item; soma dos `fully_performed` > 0 | Só `fully_performed` cobrado pelo preço aceito; parcial/não iniciado = 0; valor congelado | IK + VE | A+M | `validation_failed`, `version_conflict` | `closeInterruptedOrder` |
| T-O-08 | pending_review → closed_no_charge | dono | Revisão com nota; soma = 0 | `closure_reason = interruption_zero_total`; sem cobrança | IK + VE | A+M | `version_conflict` | `closeInterruptedOrder` |
| T-O-09 | awaiting_payment → awaiting_confirmation | sistema/provedor (via T-RC-02) | Recebimentos `principal` cobrem o devido e algum Pix não conferido | — | NAT | A | — | `receiveAbacatePayWebhook` |
| T-O-10 | awaiting_payment / awaiting_confirmation → settled | profissional (own_attendance) / dono (via T-RC-01 ou T-RC-03) | Recebimentos `principal` = devido e **todos** conferidos | Encerra comanda; dispara crédito de pontos se elegível (T-LG-01) | IK + NAT | A | `order_not_payable` | `registerInPersonReceipt`, `conferReceipt` |
| T-O-11 | awaiting_payment / awaiting_confirmation / settled → recalculado | dono | Motivo; reautenticação; **nenhuma** cobrança Pix `creating`/`pending`/`unknown` (cancelar/expirar antes); só por item (retirar cobrança de item, incluir serviço feito com preço atual) | Nova versão do total; se recebido > novo devido → pendência `adjustment_excess`; se recebido < novo devido → volta a `awaiting_payment` | IK + VE | A+M | `pix_charge_active`, `pix_charge_outcome_unknown`, `reauthentication_required`, `version_conflict` | `adjustClosedOrder` |
| T-O-12 | (sem mudança) escolha do meio | visitante / cliente (visit_owner), profissional / dono | Comanda `awaiting_payment`; sair de Pix com cobrança ativa exige `acknowledge_active_pix` | Registra meio; se havia Pix ativo → tentativa de cancelamento (T-PX-07) + pendência `active_pix_on_method_switch` | IK + VE | A | `order_not_payable`, `pix_charge_active` (sem ciência), `version_conflict` | `choosePaymentMethod` |
| T-O-13 | item accepted → performed (todas as unidades) | profissional (own_attendance) / dono | Participação `in_service` | Todas as unidades da linha realizadas (serviço) ou entregues (produto); não podem mais ser retiradas | IK + VE | A | `invalid_transition`, `version_conflict` | `markItemPerformed` |
| T-O-14 | unidades realizadas/entregues n → m (m > n) | profissional (own_attendance) / dono | Participação `in_service`; m ≤ quantidade | Registra "2 de 3" em valor absoluto (repetir não soma); m = quantidade → item `performed`; histórico em `order_item_quantity_change` (ADR-0033) | IK + VE | A | `item_already_performed` (m < n), `validation_failed`, `version_conflict` | `setItemPerformedQuantity` |

Invariantes: cada `service_id` no máximo uma vez entre itens não removidos (índice único parcial); total = soma de `unit_price` dos itens cobráveis (verificado em teste e recalculado no servidor); mudanças do catálogo não tocam itens existentes.

---

## 4. Solicitação de alteração (`ChangeRequestStatus`)

Estados: `pending`, `approved`, `rejected`, `voided`.

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-CR-01 | ∅ → pending | visitante / cliente (visit_owner) | Participação `called`/`in_service`; nenhuma outra `pending` na comanda; remoções só de itens `accepted` (não `performed`); inclusões ativas, habilitadas, sem duplicar; resultado mantém ≥1 item | Guarda pedido; **total aceito não muda** | IK + VE + UQ(uma pendente por comanda) | A | `change_request_pending`, `item_already_performed`, `service_not_eligible`, `order_requires_item`, `version_conflict` | `createChangeRequest` |
| T-CR-02 | pending → approved | profissional (own_attendance) / dono | Itens a remover ainda não `performed` | Aplica nova versão da comanda com autor, instante e versão | IK + VE | A | `item_already_performed`, `version_conflict` | `approveChangeRequest` |
| T-CR-03 | pending → rejected | profissional (own_attendance) / dono | — | Comanda fica na versão anterior; cliente é avisado (evento) | IK + VE | A | `version_conflict` | `rejectChangeRequest` |
| T-CR-04 | pending → voided | sistema | Participação cancelada, ausente ou interrompida | Pedido deixa de valer | ÚNICA-VEZ | A | — | `cancelParticipation`, `markNoShow`, `interruptService` |

---

## 5. Cobrança Pix (`PixChargeStatus`)

Estados: `creating`, `pending` (QR emitido), `unknown` (resultado da criação desconhecido), `paid`, `expired`, `cancelled`, `failed`. Ativos = `creating`, `pending`, `unknown`.

```mermaid
stateDiagram-v2
  [*] --> creating: T-PX-01
  creating --> pending: T-PX-02
  creating --> unknown: T-PX-03
  creating --> failed: T-PX-08
  unknown --> pending: T-PX-04
  unknown --> paid: T-PX-04
  unknown --> expired: T-PX-04
  unknown --> failed: T-PX-04
  pending --> paid: T-PX-05
  pending --> expired: T-PX-06
  pending --> cancelled: T-PX-07
```

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-PX-01 | ∅ → creating | visitante / cliente (visit_owner), profissional / dono | Comanda `awaiting_payment`, devido > 0; **nenhuma** cobrança ativa; integração habilitada no ambiente | Grava tentativa **antes** de chamar o provedor (valor = devido do servidor) | IK + UQ(uma ativa por comanda) | A | `order_not_payable`, `pix_charge_active`, `pix_charge_outcome_unknown`, `provider_unavailable` | `createPixCharge`, `createAppointmentPixCharge` |
| T-PX-02 | creating → pending | sistema | Provedor respondeu com cobrança válida (valor confere) | Guarda ID externo, `br_code`, `expires_at` | NAT | A | — | `createPixCharge` (201) |
| T-PX-03 | creating → unknown | sistema | Timeout/erro ambíguo na criação | Interface mostra "verificando pagamento"; worker reconcilia; **não** permite nova tentativa | NAT | A | — | `createPixCharge` (202) |
| T-PX-04 | unknown → pending/paid/expired/failed | sistema (worker) | Consulta autenticada ao provedor (backoff com limite); sem resposta após limite → pendência `charge_outcome_unresolved` | Resolve situação real | NAT | A | — | (worker) |
| T-PX-05 | pending → paid | provedor (provider_signature) / sistema (consulta) | Evidência autenticada; cobrança, valor, moeda e recebedor conferem | Cria recebimento (T-RC-02) | NAT(ID do evento externo) | A+E | `webhook_unauthenticated` (dispara consulta) | `receiveAbacatePayWebhook` |
| T-PX-06 | pending → expired | sistema / provedor | Prazo do provedor vencido e confirmado | Permite nova cobrança; pagamento tardio vira T-RC-04 | NAT | A | — | `receiveAbacatePayWebhook` |
| T-PX-07 | pending → cancelled | profissional (own_attendance) / dono, ou sistema após T-O-12 | Provedor suporta e **confirma** cancelamento; sem suporte/sem confirmação → continua `pending` com `cancel_requested_at` | Libera nova cobrança/meio | IK + NAT | A+M | `invalid_transition`, `provider_unavailable` | `cancelPixCharge` |
| T-PX-08 | creating → failed | sistema | Provedor recusou de forma definitiva (sem cobrança criada) | Permite nova tentativa | NAT | A | — | `createPixCharge` |

Nunca: botão "já paguei", comprovante, imagem ou retorno do navegador mudam o estado de uma cobrança.

---

## 6. Recebimento e conferência (`ReceiptStatus`, `ReceiptClassification`)

Estados: `registered` (registrado, aguarda conferência) → `conferred` (conferido). Classificação: `principal` (cobre o devido) ou `excess` (duplicado, tardio ou divergente). Recebimentos nunca são apagados.

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-RC-01 | ∅ → conferred (dinheiro/maquininha) | profissional (own_attendance) / dono | Comanda `awaiting_payment`; `amount_cents` = devido; sem Pix ativo; dinheiro: `cash_tendered ≥ devido` | Cria **dois registros**: recebimento e conferência; troco = entregue − devido; receita = devido; comanda → `settled` | IK + VE | A | `payment_amount_mismatch`, `pix_charge_active`, `order_not_payable`, `version_conflict` | `registerInPersonReceipt` |
| T-RC-02 | ∅ → registered (Pix) | provedor / sistema | Ver T-PX-05 | Recebimento `principal` se cobre o devido de comanda aberta a pagamento; comanda → `awaiting_confirmation` (T-O-09); **não volta a "não pago"** | NAT(ID externo) | A+E | — | `receiveAbacatePayWebhook` |
| T-RC-03 | registered → conferred | profissional (own_attendance) / dono | Recebimento Pix `registered` | Registra conferência; se tudo coberto e conferido → comanda `settled` (T-O-10) | IK + NAT | A | `invalid_transition` | `conferReceipt` |
| T-RC-04 | ∅ → registered como `excess` | provedor / sistema | Pagamento duplicado, tardio (cobrança `expired`/`cancelled` ou comanda já coberta) ou valor divergente | Registra mesmo assim; abre pendência (`duplicate_receipt`/`late_receipt`/`amount_mismatch`); valor divergente não encerra comanda | NAT(ID externo) | A+E | — | `receiveAbacatePayWebhook` |

---

## 7. Estorno (`RefundStatus`)

Estados: `requested`, `processing`, `unknown`, `under_review`, `confirmed`, `failed`. No MVP: **integral por recebimento**.

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-RF-01 | ∅ → requested | dono | Reautenticação; motivo; recebimento sem estorno ativo/confirmado; valor = recebido ainda não devolvido | Registra intenção (não altera saldo) | IK + UQ(um estorno ativo por recebimento) | A+M | `refund_not_allowed`, `refund_exceeds_available`, `reauthentication_required` | `requestRefund`, `cancelAppointment` |
| T-RF-02 | requested → processing | sistema | Recebimento Pix e provedor suporta estorno via API | Chama provedor | NAT | A | `provider_unavailable` | `requestRefund` |
| T-RF-03 | processing → confirmed | provedor / sistema (consulta) | Evidência do provedor | Atualiza devolvido; se recebimento `principal` → reversão de pontos (T-LG-02) | NAT | A+E | — | `receiveAbacatePayWebhook` |
| T-RF-04 | processing → unknown → under_review / failed | sistema | Timeout/ambíguo → `unknown` → consulta; sem resolução → `under_review`; recusa confirmada → `failed` | Pendência `refund_under_review` | NAT | A | — | `receiveAbacatePayWebhook` |
| T-RF-05 | requested → under_review | sistema | Dinheiro/maquininha, ou provedor sem estorno via API, ou caso parcial | Pendência para o dono documentar | NAT | A | — | `requestRefund` |
| T-RF-06 | under_review → confirmed | dono | Reautenticação; evidência documentada | Igual a T-RF-03 (`evidence = owner_documented`) | IK | A+E | `invalid_transition` | `confirmRefundManually`, `confirmBookingRefund` |
| T-RF-07 | under_review → failed | dono | Reautenticação; evidência | Nada é devolvido; recebimento intacto | IK | A+E | `invalid_transition` | `markRefundFailed`, `markBookingRefundFailed` |

Corrida confirmação × estorno: estorno exige recebimento existente e conferido ou registrado; bloqueio de linha no recebimento serializa as duas operações.

---

## 8. Pendência financeira (`FinancialIssueStatus`)

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-FI-01 | ∅ → open | sistema | Duplicidade, tardio, divergência, cobrança não resolvida, estorno em revisão, troca de Pix ativo, excesso por ajuste | Aparece no painel do dono | UQ(tipo, origem) | A | — | (vários) |
| T-FI-02 | open → resolved | dono | Nota de resolução; para excesso: estorno do recebimento excedente confirmado **ou** nota explicando devolução presencial documentada | Sai do painel; **não** altera nem apaga recebimentos | IK | A+M | `invalid_transition` | `resolveFinancialIssue`, `confirmBookingRefund`, `markBookingRefundFailed` |

---

## 9. Fidelidade

### 9.1 Programa

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-LP-01 | disabled → enabled | dono | — (padrão é desligado) | Novas visitas de conta passam a ser elegíveis | IK + VE | A | `version_conflict` | `updateLoyaltyProgram` |
| T-LP-02 | enabled → disabled | dono | — | Sem novos acúmulos em **novas** visitas e sem novas reservas; saldo, consulta, reservas ativas e créditos já prometidos preservados | IK + VE | A | `version_conflict` | `updateLoyaltyProgram` |

### 9.2 Razão de pontos (lançamentos imutáveis)

Saldo = soma de `balance_delta`; retido = soma de `held_delta`; disponível = saldo − retido.

| ID | Lançamento | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-LG-01 | `credit` (+saldo) | sistema | Comanda `settled`; participação `completed`; identidade = conta; snapshot elegível na entrada | Soma dos pontos snapshot dos itens `performed` | ÚNICA-VEZ(comanda, credit) | A | — | `registerInPersonReceipt`, `conferReceipt` |
| T-LG-02 | `credit_reversal` (−saldo) | sistema | Estorno confirmado do recebimento `principal` de comanda creditada | Reverte o crédito daquela comanda; saldo pode ficar negativo | ÚNICA-VEZ(comanda, reversal) | A | — | `confirmRefundManually`, `receiveAbacatePayWebhook` |
| T-LG-03 | `account_closure` (−saldo restante) | sistema | Exclusão de conta | Saldo encerrado sem conversão em dinheiro | ÚNICA-VEZ(conta, loja) | A | — | `requestAccountDeletion` |

Estorno de recebimento `excess` **não** gera `credit_reversal`.

### 9.3 Reserva de prêmio (`ReservationStatus`)

Estados: `active`, `delivered`, `expired`, `cancelled`, `delivery_reverted`.

```mermaid
stateDiagram-v2
  [*] --> active: T-RW-01
  active --> cancelled: T-RW-02 / T-RW-06
  active --> expired: T-RW-03
  active --> delivered: T-RW-04
  delivered --> delivery_reverted: T-RW-05
```

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-RW-01 | ∅ → active | cliente (self) | Programa ligado; prêmio ativo na versão aceita; disponível ≥ custo; estoque > 0 (se limitado); sem reserva ativa na loja | Lançamento `hold` (+retido); estoque −1; guarda condições aceitas; `expires_at = +24 h` | IK + UQ(uma ativa por conta/loja) + bloqueio de linha no prêmio | A | `loyalty_program_disabled`, `insufficient_points`, `reward_out_of_stock`, `reward_inactive`, `reservation_already_active`, `version_conflict` | `createRewardReservation` |
| T-RW-02 | active → cancelled | cliente (self) | `active` (também com programa desligado) | `hold_release` (−retido); estoque +1 | IK + VE + ÚNICA-VEZ | A | `invalid_transition` | `cancelRewardReservation` |
| T-RW-03 | active → expired | sistema | `now ≥ expires_at` | `hold_release`; estoque +1 | ÚNICA-VEZ (UPDATE condicional `WHERE status='active'`) | A | — | (worker) |
| T-RW-04 | active → delivered | profissional (establishment_member) / dono | `now < expires_at` (no banco, mesma transação) | `consume` (−saldo, −retido); estoque segue consumido | IK + VE | A | `reservation_expired`, `invalid_transition` | `deliverRewardReservation` |
| T-RW-05 | delivered → delivery_reverted | dono | Motivo | `consume_reversal` (+saldo); estoque +1 | IK + VE + ÚNICA-VEZ | A+M | `invalid_transition` | `revertRewardDelivery` |
| T-RW-06 | active → cancelled | sistema (exclusão de conta) | Exclusão solicitada | Igual a T-RW-02 | ÚNICA-VEZ | A | — | `requestAccountDeletion` |

---

## 10. Catálogo, equipe e loja

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-SV-01 | ∅ → active | dono | Preço > 0 em centavos; duração > 0; pontos inteiros ≥ 0; profissionais da mesma loja | Serviço disponível | IK | A | `validation_failed` | `createService` |
| T-SV-02 | active/inactive → nova versão | dono | Mesmas validações | Vale só para inclusões futuras | IK + VE | A | `version_conflict` | `updateService` |
| T-SV-03 | active → inactive | dono | — | Sem novas inclusões; itens aceitos permanecem | IK + VE | A | `version_conflict` | `deactivateService` |
| T-SV-04 | inactive → active | dono | — | Volta ao catálogo | IK + VE | A | `version_conflict` | `reactivateService` |
| T-SV-05 | habilitações | dono | Profissionais da mesma loja | Afeta só novas inclusões/transferências | IK + VE | A | `not_found`, `version_conflict` | `setServiceProfessionals` |
| T-CT-01 | ∅ → categoria active | dono | Tipo serviço/produto; nome único por tipo; até 50 por tipo | Organiza o catálogo (ADR-0032) | IK | A | `validation_failed`, `category_limit_reached` | `createCategory` |
| T-CT-02 | categoria → nova versão | dono | Nome único | Itens não mudam | IK + VE | A | `validation_failed`, `version_conflict` | `updateCategory` |
| T-CT-03 | categoria active → inactive | dono | — | Sai dos filtros públicos; itens continuam (sem categoria para o cliente) | IK + VE | A | `invalid_transition`, `version_conflict` | `deactivateCategory` |
| T-CT-04 | categoria inactive → active | dono | — | Volta aos filtros | IK + VE | A | `invalid_transition`, `version_conflict` | `reactivateCategory` |
| T-PD-01 | ∅ → produto active | dono | Preço > 0; até 500 por loja; categoria de produto da loja | Disponível como complemento (sem estoque) | IK | A | `validation_failed`, `product_limit_reached` | `createProduct` |
| T-PD-02 | produto → nova versão | dono | Mesmas validações; foto `product_photo` desta loja | Vale só para novas inclusões (snapshot na comanda) | IK + VE | A | `validation_failed`, `version_conflict` | `updateProduct` |
| T-PD-03 | produto active → inactive | dono | — | Sem novas inclusões; comandas com o produto continuam | IK + VE | A | `invalid_transition`, `version_conflict` | `deactivateProduct` |
| T-PD-04 | produto inactive → active | dono | — | Volta ao catálogo | IK + VE | A | `invalid_transition`, `version_conflict` | `reactivateProduct` |
| T-ST-01 | configurações: seção Catálogo | dono | Versão da seção; limite 1–50 | Novas inclusões/aumentos; não retroativo (ADR-0034) | IK + VE | A | `validation_failed`, `version_conflict` | `updateCatalogSettings` |
| T-ST-02 | configurações: seção Filas e atendimento | dono | Versão da seção; tolerância 1–60; capacidade 1–200 ou null | Chamadas e entradas futuras; emite eventos das filas | IK + VE | A | `validation_failed`, `version_conflict` | `updateQueueSettings` |
| T-ST-03 | configurações: seção Pagamentos | dono | Versão da seção; ao menos um meio utilizável | Novas escolhas e cobranças; nada existente é invalidado | IK + VE | A | `no_payment_method`, `version_conflict` | `updatePaymentSettings` |
| T-ST-04 | configurações: seção Agenda (forma de atendimento e regras das reservas) | dono | Versão da seção; limites da agenda; sair de "fila" só com `FILA_AGENDA` | Fecha só a admissão correspondente; regras novas valem para reservas futuras; emite eventos das filas (ADR-0038) | IK + VE | A | `agenda_unavailable`, `version_conflict` | `updateAgendaSettings` |
| T-FE-01 | custo de recebimento: nova versão | dono | Versão vigente esperada; 0–3000 pb; fixo 0–R$ 100; ou `clear` | Vale para recebimentos novos; anteriores guardam o custo calculado na hora | IK + VE | A | `validation_failed`, `version_conflict` | `setPaymentFee` |
| T-PR-01 | ∅ → active (+fila closed) | dono | Letra de fila única na loja | Cria profissional e sua fila | IK | A | `validation_failed` | `createProfessional` |
| T-PR-02 | active → inactive | dono | Reautenticação; **zero** participações ativas na fila | Vínculo desativado; sessões staff revogadas; SSE encerrado; fila `closed`; comandas antigas seguem com o dono | IK + VE | A+M | `professional_has_active_participations`, `reauthentication_required` | `deactivateProfessional` |
| T-PR-03 | inactive → active | dono | Reautenticação | Vínculo volta; fila continua `closed` | IK + VE | A | `reauthentication_required` | `reactivateProfessional` |
| T-PR-04 | nova versão (nome) | dono | — | Nome exibido muda | IK + VE | A | `version_conflict` | `updateProfessional` |
| T-PR-05 | foto pública unpublished ↔ published | o próprio profissional (self) | Envio próprio com finalidade `professional_photo` desta loja, ou cópia explícita da foto pessoal | Foto aparece/deixa de aparecer no cartão público; o avatar pessoal não muda (ADR-0028) | IK + VE | A | `media_not_attachable`, `version_conflict` | `setMyProfessionalPhoto`, `removeMyProfessionalPhoto` |
| T-PR-06 | foto pública published → unpublished | dono | Motivo (3–500) | Retira a publicação de foto inadequada; não toca o avatar pessoal da pessoa | IK + VE | A | `version_conflict` | `removeProfessionalPhoto` |
| T-RD-01 | ∅ → active (prêmio) | dono | Custo inteiro > 0; descrição; instruções; estoque limitado/ilimitado | Prêmio disponível | IK | A | `validation_failed` | `createReward` |
| T-RD-02 | nova versão (prêmio) | dono | — | Reservas existentes mantêm condições aceitas | IK + VE | A | `version_conflict` | `updateReward` |
| T-RD-03 | active → inactive (prêmio) | dono | — | Sem novas reservas; ativas honradas | IK + VE | A | `version_conflict` | `deactivateReward` |
| T-ES-01 | nova versão (loja) | dono | Slug único; paleta do catálogo; logo enviada pelo próprio dono para esta loja | Nome/slug/fuso, paleta, descrição/endereço/telefone públicos e logo (ADR-0028) | IK + VE | A (nomes dos campos alterados, nunca valores) | `slug_taken`, `version_conflict`, `media_not_attachable` | `updateEstablishment`, `setEstablishmentLogo`, `removeEstablishmentLogo` |

---

## 11. Identidade, sessões, convites e visitante

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-AC-01 | ∅ → pending_verification | público | E-mail não usado (resposta igual se usado); senha conforme política | Cria conta sem papel; envia verificação | NAT (resposta genérica) | A | `validation_failed`, `rate_limited` | `registerAccount` |
| T-AC-02 | pending_verification → active | público (token) | Token válido, não usado, ≤ 24 h | Conta ativa; token consumido | ÚNICA-VEZ(token) | A | `token_invalid` | `verifyEmail` |
| T-AC-03 | active → deletion_processing | cliente/profissional/dono (self) | Reautenticação; `confirm_effects`; não ser **dono único** de loja ativa | Revoga sessões; cancela `waiting`/`called` (T-P-10); libera reservas (T-RW-06); encerra saldo (T-LG-03); profissional: vínculos desativados; bloqueios listados | IK + ÚNICA-VEZ(conta) | A | `sole_owner`, `reauthentication_required`, `deletion_in_progress` | `requestAccountDeletion` |
| T-AC-04 | deletion_processing → deleted | sistema | Sem bloqueios (`service_in_progress`, `financial_pending`, `professional_queue_has_participants`) | Anonimiza perfil/credenciais; mantém registros operacionais/financeiros minimizados; evidência sem dados pessoais | ÚNICA-VEZ + retomável | A | — | (worker) |
| T-SS-01 | ∅ → active (sessão) | público | Credenciais válidas; e-mail verificado; contexto `staff` exige vínculo ativo; limite de tentativas por conta e por origem | Cookie HttpOnly; validade conforme contexto | — | A | `invalid_credentials`, `email_not_verified`, `rate_limited` | `login` |
| T-SS-02 | active → revoked | titular | — | Revoga no servidor; SSE encerrado | NAT | A | — | `logout` |
| T-SS-03 | outras sessões → revoked | titular | Senha atual correta | Troca senha; revoga todas as outras sessões | — | A | `invalid_credentials` | `changePassword` |
| T-SS-04 | todas → revoked | público (token de reset) | Token válido | Revoga todas as sessões | ÚNICA-VEZ(token) | A | `token_invalid` | `confirmPasswordReset` |
| T-SS-05 | active → expired | sistema | Inatividade/limite absoluto | Sessão inválida; SSE encerrado | NAT | — | — | (verificação a cada uso) |
| T-SS-06 | reautenticação | titular | Senha correta, ou código de 8 dígitos enviado ao e-mail (conta sem senha; 10 min, só nesta sessão, 5 tentativas/15 min) | `reauth_valid_until = now + 10 min` | — | A | `invalid_credentials`, `reauth_code_invalid`, `no_password`, `rate_limited` | `reauthenticate`, `requestReauthCode` |
| T-AC-05 | ∅ → active ou pending_verification (Google) | público (callback do Google) | Transação de uso único do mesmo navegador; id_token válido (assinatura, emissor, audiência, expiração, nonce, azp); identidade (provedor+emissor+sub) inexistente; e-mail sem conta local | Cria conta só de CLIENTE, sem senha; ativa se o Google for autoridade do e-mail (Gmail ou Workspace), senão exige confirmação local | ÚNICA-VEZ(state) + UQ(provedor, emissor, sub) | A | (redireciona com `google=conta_existente`, `verificar_email`, `falhou`) | `googleAuthCallback` |
| T-AC-06 | Google desconectado → conectado | titular | Sessão que iniciou + reautenticação recente; identidade não pertence a outra conta | Vínculo explícito (nunca por e-mail igual) | ÚNICA-VEZ(state) + UQ | A | `identity_already_linked`, `reauthentication_required` | `startGoogleAuth`, `googleAuthCallback` |
| T-AC-07 | Google conectado → desconectado | titular | Reautenticação; conta tem senha (outro método utilizável) | Remove o vínculo; sessões abertas pelo Google são encerradas | NAT | A | `last_access_method`, `reauthentication_required` | `unlinkGoogle` |
| T-AC-08 | conta sem senha → com senha (convite) | público (token do convite) | Convite válido para o e-mail da conta só com Google | A senha informada vira a primeira senha; o vínculo de equipe segue a regra do convite | ÚNICA-VEZ(token) | A | `invite_invalid` | `acceptInvite` |
| T-SS-08 | ∅ → active (sessão de cliente pelo Google) | público (callback do Google) | Identidade vinculada a conta ativa | Sessão SEMPRE de cliente (`auth_method=google`; o banco recusa equipe por Google); a sessão anterior do navegador é encerrada | ÚNICA-VEZ(state) | A | (redireciona com `google=falhou`) | `googleAuthCallback` |
| T-SS-07 | sessão de dono active → revoked (outra assume) | titular (credenciais válidas + desafio) | Conta com vínculo de dono; desafio de 2 min não usado, desta tentativa (cookie) e da MESMA geração da vaga; vínculo de dono ainda ativo | Revoga a sessão anterior (`owner_session_replaced`) e os vínculos desktop dela, cria a nova e passa a vaga numa só transação (ADR-0027); SSE antigo recebe `stream.session_ended` | ÚNICA-VEZ(desafio); repetição da mesma tentativa só reentrega o acesso | A | `owner_session_challenge_invalid`, `rate_limited` | `takeoverOwnerSession` |
| T-PW-01 | ∅ → token emitido | público | — (resposta igual exista ou não) | Token aleatório, armazenado em hash, 30 min | NAT | A (se conta existe) | `rate_limited` | `requestPasswordReset` |
| T-PW-02 | token → consumido | público | Token válido, não usado, ≤ 30 min | Nova senha | ÚNICA-VEZ | A | `token_invalid` | `confirmPasswordReset` |
| T-IN-01 | ∅ → pending (convite) | dono | Reautenticação; profissional sem acesso ativo; convite anterior pendente é revogado | Token aleatório em hash, 24 h, enviado por e-mail | IK | A | `reauthentication_required`, `invalid_transition` | `createInvite` |
| T-IN-02 | pending → accepted | público (token) | Token válido, não usado, ≤ 24 h | Vínculo `professional` ativo para a conta | ÚNICA-VEZ(token) | A | `invite_invalid` | `acceptInvite` |
| T-IN-03 | pending → revoked | dono | Pendente | Token inutilizado | IK | A | `invalid_transition` | `revokeInvite` |
| T-IN-04 | pending → expired | sistema | > 24 h | Token inutilizado | NAT | — | — | (verificação no uso) |
| T-VS-01 | ∅ → active (visitante) | público | Loja ativa; nick válido; sem visitante desta loja com visita ativa/pendente no navegador; limites de abuso | Credencial (cookie) + código de recuperação exibido **uma vez** | — (repetir cria outra identidade sem visita) | A | `visitor_visit_in_progress`, `rate_limited` | `createVisitorSession` |
| T-VS-02 | credencial anterior → revoked; nova → active | público (código) | Código válido; ≤ 30 dias da criação; ≤ 24 h após encerramento; tentativas limitadas | Novo cookie; **novo código**; credenciais anteriores revogadas e SSE encerrado | ÚNICA-VEZ(código) | A | `recovery_code_invalid`, `rate_limited` | `recoverVisitorSession` |
| T-VC-01 | contato do visitante: ∅/qualquer → pendente | visitante (si) | Credencial válida na loja; e-mail válido; limite por visitante e IP | E-mail cifrado + HMAC de busca; código de 6 dígitos (15 min) enviado; trocar o e-mail zera a verificação | NAT (limitado) | A | `validation_failed`, `rate_limited` | `setVisitorContact` |
| T-VC-02 | contato: pendente → verificado | visitante (si) | Código certo, vigente, até 5 tentativas | Contato passa a receber avisos e permite recuperar a reserva | ÚNICA-VEZ(código) | A | `contact_code_invalid`, `contact_code_expired` | `verifyVisitorContact` |
| T-VC-03 | link de acesso: ∅ → emitido | público | E-mail verificado de visitante vigente na loja; limites por IP e endereço | Token de uso único (30 min, só o hash guardado) enviado ao e-mail; resposta sempre 202 | NAT | — | `rate_limited` | `requestAppointmentAccess` |
| T-VC-04 | link: emitido → usado; nova credencial | público (token) | Token vigente e não usado | Credencial de visitante neste navegador (o visitante mais recente daquele e-mail); GET nunca muda estado | ÚNICA-VEZ(token) | A | `access_link_invalid`, `rate_limited` | `exchangeAppointmentAccess` |
| T-VS-03 | active → revoked | visitante (self) | — | Revoga credencial; **não** cancela a visita | NAT | A | — | `endVisitorSession` |
| T-VS-04 | active → expired | sistema | 7 dias sem uso, 30 dias da criação ou 24 h após encerramento da comanda | Credencial inválida; visita e dívida permanecem para a equipe | NAT | — | — | (verificação a cada uso) |

## 12. Processos controlados (fora da API HTTP)

| ID | Mudança | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-OP-01 | Criar loja + convite de dono inicial | operador | Comando de linha controlado, com acesso ao servidor; e-mail informado | Loja criada; convite de dono (24 h, uso único) | ÚNICA-VEZ(slug) | A | slug duplicado | CLI `fila-admin bootstrap` (ADR-0013) |
| T-OP-02 | Transferir administração / encerrar loja | operador, a pedido do dono atual | Identidade do dono confirmada fora de banda; encerramento exige pendências tratadas | Novo dono vinculado ou loja encerrada; nunca loja ativa sem dono | ÚNICA-VEZ | A+M | pendências abertas | CLI `fila-admin transfer-owner` / `close-establishment` (ADR-0013) |
| T-OP-03 | Worker: liberado ↔ travado por manutenção | operador | Acesso ao servidor; motivo (≥ 3 caracteres) para travar | Nenhuma tarefa do worker roda até soltar; a API segue atendendo; ao soltar, as tarefas voltam e retomam o que ficou parado | IDEMPOTENTE (travar de novo só atualiza a nota) | A+M | encerrar manutenção sobre trava de restauração | CLI `fila-admin manutencao iniciar/encerrar` (ADR-0026) |
| T-OP-04 | Worker: travado por restauração → conciliado → liberado | operador (travar/conciliar/liberar); sistema (detecção de banco restaurado ou copiado) | Liberar só com conciliação concluída DEPOIS do início da trava; conciliar só com trava de restauração | Época nova do SSE; todas as sessões e links de e-mail encerrados; pagamentos e estornos do provedor registrados ou em pendência (`restored_orphan_charge`, `restored_refund_unrecorded`); exclusões do diário reaplicadas com o mesmo protocolo | REPETÍVEL (conciliar de novo não duplica recebimento, pendência nem pedido) | A+M (resumo só com contagens) | `ErrNotReconciled`, `ErrNoRestoreHold`, provedor indisponível (a trava continua) | CLI `fila-admin restauracao travar/reconciliar/liberar` (ADR-0026) |

## Programa desktop do dono (ADR-0027)

| ID | Estado | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erros | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-DK-01 | ∅ → código emitido | operador (`fila-admin desktop emitir-codigo`) | Conta ativa com vínculo de dono | Código de uso único, 15 min; códigos pendentes da conta deixam de valer | — | A | conta inexistente ou sem vínculo de dono | (ferramenta administrativa) |
| T-DK-02 | código → instalação active | instalação (prova DPoP da chave nova) | Código válido, não usado, não vencido; chave P-256 ainda não registrada | Registra a chave pública e a impressão digital (jkt); consome o código | ÚNICA-VEZ(código, jti) | A | `desktop_activation_invalid`, `desktop_proof_invalid`, `rate_limited` | `activateDesktopInstallation` |
| T-DK-03 | ticket → vínculo active | instalação (prova DPoP) | Ticket de 60 s emitido pela sessão de dono ocupante da vaga, da MESMA conta da instalação; instalação não revogada | Token de acesso preso à chave, subordinado à sessão (no máximo 2 h); vínculo anterior da mesma sessão e instalação encerrado | ÚNICA-VEZ(ticket, jti) | A | `desktop_link_invalid`, `desktop_installation_invalid`, `desktop_proof_invalid` | `createDesktopBinding` |
| T-DK-04 | instalação active → revoked | operador (`fila-admin desktop revogar`) | Instalação ativa | Vínculos encerrados na hora; consultas seguintes recusadas; SSE do Dashboard reconferido | NAT | A | instalação inexistente ou já revogada | (ferramenta administrativa) |

## 14. Agenda e operação híbrida (Fila 2.0, ADR-0038)

Ordem de travas de todos os caminhos: identidade → fila(s) por id → participação → reserva → ocupação. A restrição de exclusão GiST em `agenda_occupancy` é a última barreira contra sobreposição.

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-AG-01 | jornada: nova versão com vigência | dono | Turnos válidos e sem sobreposição no dia; profissional dentro da abertura da loja; vigência hoje ou depois; nenhuma reserva confirmada fica fora | Versão imutável gravada (a mais recente vale na data); o passado não muda | IK | A | `validation_failed`, `schedule_conflicts_appointments` | `setAgendaHours` |
| T-AG-02 | exceção: ∅ → ativa | dono; profissional (própria agenda, com permissão) | Data hoje ou depois; nenhuma reserva ativa no período | Folga/feriado/bloqueio; ocupação "block" por profissional afetado | IK | A+M | `exception_conflicts_appointments`, `forbidden` | `createAgendaException` |
| T-AG-03 | exceção: ativa → removida | dono; profissional (própria) | Exceção ativa da loja | Libera a ocupação; histórico fica | IK | A | `not_found` | `removeAgendaException` |
| T-AP-01 | ∅ → confirmed (gratuita) / held (paga) | cliente, visitante (si); dono (balcão) | Modo agenda ou os dois; profissional ativo faz os serviços; início entre os oferecidos agora (sob trava); limite de futuras; sem sobreposição própria; visitante até 7 dias | Reserva com itens, preço, duração, margem, tolerância e janela congelados; ocupação protegida; balcão cria identidade recuperável | IK + UQ(exclusão por profissional e por cliente) | A | `slot_unavailable`, `appointment_limit_reached`, `appointment_overlaps_own`, `schedule_disabled`, `outside_booking_window` | `createAppointment` |
| T-AP-02 | confirmed → confirmed (nova revisão) | titular (antes do horário); equipe com motivo | Mesma loja e itens; destino oferecido agora (sob trava das duas cadeiras) | Destino validado antes de soltar a origem; revisão + 1 | IK + VE | A | `slot_unavailable`, `invalid_transition` | `rescheduleAppointment` |
| T-AP-03 | confirmed/held → cancelled | titular (até o horário); equipe com motivo | Ainda não chamada | Libera o intervalo; `cancelled_by` cliente ou loja | IK + VE | A (+M equipe) | `invalid_transition` | `cancelAppointment` |
| T-AP-04 | confirmed/called → (presença confirmada) | equipe da cadeira | A partir da janela de chegada | `arrived_at`; impede a falta automática | IK + VE | A | `invalid_transition` | `confirmAppointmentArrival` |
| T-AP-05 | confirmed/called → (aviso "Cheguei") | titular | A partir da janela de chegada | Só aviso à equipe | IK + VE | A | `invalid_transition` | `signalAppointmentArrival` |
| T-AP-06 | confirmed → called | equipe da cadeira | Cadeira livre; cliente sem outra visita ativa; janela de chegada (ou presença) | Visita chamada (origem `appointment`) + comanda com itens congelados; prazo = max(horário, chamada) + tolerância | IK + VE + UQ(uma cadeira, uma visita ativa) | A | `professional_busy`, `customer_has_active_visit`, `appointment_too_early` | `callAppointment` |
| T-AP-07 | called/in_service → in_service/completed/interrupted/no_show/cancelled | sistema (acompanha a visita; worker para a falta) | Visita da reserva mudou na mesma transação; falta só com chamada válida, prazo vencido e sem presença | Reserva acompanha a visita; estados finais liberam a ocupação | NAT (reconferido sob trava) | A | — | worker da agenda e os comandos da visita (iniciar, concluir, interromper, cancelar, ausência) |
| T-AP-08 | confirmed → (atraso da loja) | sistema (worker) | Horário + tolerância passou e a loja não chamou | Sinaliza "a loja não chamou" (não é falta do cliente) | NAT | A | — | worker da agenda |
| T-AP-09 | held → expired | sistema (worker; ou quem trava a cadeira depois do prazo) | Prazo da retenção vencido (no instante exato já venceu), sem pagamento confirmado | Libera a ocupação; obrigação → `expired` (T-BK-04); Pix pago depois vira tardio | NAT (reconferido sob trava da cadeira) | A | — | worker da agenda; `createAppointment`, `rescheduleAppointment`, `createAgendaException` (soltam retenções vencidas da cadeira antes de ocupar) |
| T-AP-10 | held → confirmed | provedor / sistema (webhook ou consulta) | Pix confirmado pelo provedor dentro do prazo da retenção (relógio do servidor) | Reserva confirmada; a ocupação deixa de ser retenção | NAT (reconferido sob trava: cadeira → reserva → obrigação) | A | — | `receiveAbacatePayWebhook`, worker de pagamento |

### 14.1 Reserva paga — obrigação da reserva (`BookingPaymentStatus`, ADR-0039)

Ordem de travas: identidade → fila → participação → reserva → comanda → obrigação → cobrança, recebimento e estorno. A cobrança Pix, o recebimento, o estorno e a pendência apontam para a comanda **ou** para a obrigação (XOR no banco). Nada é confirmado pela tela: só o provedor (webhook ou consulta).

| ID | De → Para | Ator | Pré-condições | Efeito | Idempotência | Auditoria | Erro esperado | Contrato |
|---|---|---|---|---|---|---|---|---|
| T-BK-01 | ∅ → awaiting_payment | cliente, visitante (reserva online) | Cobrança liberada (`FILA_AGENDA_COBRANCA`), Pix ligado e operacional, modo sinal ou taxa, valor ≥ mínimo do Pix, tempo para pagar antes do horário | Obrigação com valor, política e versão aceitos; reserva `held` até o prazo | IK (da reserva) + UQ(uma por reserva) | A | `booking_too_close` | `createAppointment` |
| T-BK-02 | awaiting_payment → (Pix criado) | titular | Reserva `held` com prazo valendo; nenhuma tentativa ativa | Três fases (grava, chama o provedor fora da transação, grava o resultado); QR vence antes da retenção | IK + UQ(uma tentativa ativa por obrigação) | A | `booking_hold_expired`, `booking_already_paid`, `pix_charge_outcome_unknown`, `provider_unavailable` | `createAppointmentPixCharge` |
| T-BK-03 | awaiting_payment → paid | provedor / sistema | Pix pago com valor e tentativa conferidos, retenção valendo no instante do processamento | Recebimento principal; reserva confirmada (T-AP-10). Pago depois do prazo, com a reserva encerrada ou em duplicidade → recebimento excedente, pendência `booking_late_payment` e devolução integral pedida | NAT (UQ um recebimento por tentativa; um principal por obrigação) | A | — | `receiveAbacatePayWebhook`, worker de pagamento |
| T-BK-04 | awaiting_payment → expired/cancelled | sistema; titular; equipe | Retenção vencida ou reserva cancelada antes do pagamento | Obrigação encerrada; tentativa aberta marcada para cancelar | NAT / IK | A | — | worker da agenda, `cancelAppointment` |
| T-BK-05 | paid → kept/refund_due | sistema | Reserva paga terminou sem atendimento (cancelada, falta, não chamada) ou taxa no fechamento | Loja cancela, cliente cancela na janela ou política "devolver" → devolução integral pedida (T-RF-01 do sistema); fora da janela com "reter" aceito (e confirmado pelo cliente) ou falta com "reter" → mantido; taxa nunca vira crédito | NAT | A | `cancel_retains_payment` | `cancelAppointment`, comandos da visita, worker da agenda |
| T-BK-06 | paid → applied | sistema (fechamento do total) | Sinal pago e comanda da visita da reserva com total fechado | Um registro de crédito = min(sinal, total); devido = total − crédito; sinal ≥ total → comanda paga sem recebimento de R$ 0; sobra → devolução parcial em revisão do dono + pendência `booking_refund_due` | NAT (UQ um crédito por obrigação e por comanda) | A | `credit_exceeds_total` (ajuste abaixo do crédito) | `completeService`, `closeInterruptedOrder` |
