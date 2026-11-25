# ADR-0039 — Reserva com Pix (sinal ou taxa), avisos por e-mail e recuperação da reserva

- Situação: Aceita (Fila 2.0, parte 2)
- Data: 2026-11-25
- Relacionadas: ADR-0038 (agenda e uma cadeira), ADR-0021/0022 (Pix em três fases e conciliação), ADR-0024 (webhook, estorno e pendências), ADR-0017 (visitante e links por fragmento)

## Contexto

Na parte 1 toda reserva é gratuita. Lojas querem reduzir faltas cobrando um sinal (abatido do atendimento) ou uma taxa de reserva (cobrada além). O dinheiro chega ANTES de existir comanda, que só nasce quando a reserva é chamada. O cliente também precisa de avisos (confirmação, lembrete, chamada) e, sem conta, de um jeito de recuperar a reserva em outro aparelho.

## Decisão

1. **Política por loja** (seção Agenda, mesma versão): `booking_payment_mode` = `free` (padrão) | `deposit` | `fee`, `booking_payment_cents` (R$ 1 a R$ 1.000), `cancel_window_hours` (padrão 12), `late_cancel_policy` = `refund` (padrão) | `retain` e `reminder_hours` (padrão 24). Ligar sinal/taxa exige `FILA_AGENDA_COBRANCA`, `FILA_AGENDA` e Pix operacional e ligado na loja (`booking_payment_unavailable`); "reter" exige `FILA_AGENDA_RETENCAO` (política comercial aprovada; `retention_unavailable`). Em homologação e produção as duas variáveis vêm desligadas.
2. **Obrigação própria** (`booking_payment`, módulo pagamento, migration 00030): nasce com a reserva, copiando valor, modo, janela e política aceitos. Cobrança Pix, recebimento, estorno e pendência apontam para a comanda **ou** para a obrigação (XOR conferido no banco); nunca existe comanda fictícia. Sinal = min(valor configurado, valor dos serviços). Reserva de balcão continua gratuita.
3. **Retenção do horário**: reserva paga nasce `held` até `hold_expires_at` = agora + validade do QR + 3 min; a ocupação da cadeira guarda `hold_until`. Sem tempo de pagar antes do horário → `booking_too_close`. O QR vence antes da retenção. Retenção vencida (no instante exato já venceu) é solta pelo worker ou por quem trava a cadeira antes de ocupar (T-AP-09), sem `now()` em predicado de índice.
4. **Só o provedor confirma** (webhook ou consulta do worker): Pix pago com a retenção valendo no relógio do servidor → recebimento principal e a reserva confirma (T-AP-10/T-BK-03). Pago depois do prazo, com a reserva encerrada ou em duplicidade → recebimento **excedente** (nunca invisível), pendência `booking_late_payment` e devolução integral pedida ao provedor; nunca desloca a reserva de quem chegou depois. Três fases, chave de idempotência e uma tentativa ativa por obrigação, como no Pix da comanda.
5. **Ordem de travas**: identidade → fila → participação → reserva → comanda → obrigação → cobrança/recebimento/estorno. Webhook e worker do pagamento travam a cadeira e a reserva pela agenda (`Bookings.LockAppointment`) antes da obrigação. Na fila, a reserva passa a ser atualizada ANTES do fechamento da comanda; o crédito na comanda não trava a reserva.
6. **Crédito, não dinheiro novo** (T-BK-06): quando o total da comanda da visita fecha (conclusão ou revisão da interrupção), o sinal vira UM `booking_credit` = min(sinal, total) e a comanda guarda `credit_cents`; devido = total − crédito; a quitação confere recebimentos principais + crédito = total (gatilho adiado que relê a comanda atual). Sinal ≥ total → comanda paga sem recebimento de R$ 0 nem conferência. Sobra (ex.: interrompido) → devolução **parcial** em revisão do dono (o provedor só estorna o integral) com pendência `booking_refund_due`, confirmada com evidência (`confirmBookingRefund`). Taxa nunca vira crédito (fica `kept`). Caixa: o Pix da reserva conta quando entrou; o crédito não conta de novo (R$ 15 na reserva + R$ 45 na loja = R$ 60).
7. **Fim sem atendimento** (T-BK-04/05): aguardando pagamento → obrigação expira/cancela. Paga: loja cancela, cliente cancela dentro da janela ou política "devolver" → devolução integral pedida ao provedor; cliente fora da janela com "reter" aceito → o servidor exige `accept_retention: true` (`cancel_retains_payment`) e mantém; falta com "reter" → mantém; "a loja não chamou" nunca é falta do cliente. "Solicitada" não é "devolvida": a tela mostra o estado vindo do provedor ou da confirmação do dono.
8. **Avisos** (`appointment_notice`, módulo agenda): o comando grava, na mesma transação, um aviso por (reserva, revisão, tipo): confirmada, lembrete, chamada, remarcada, cancelada, falta, expirada, devolução confirmada. O worker revalida (aviso de revisão anterior ou de estado que mudou é pulado) e grava o e-mail na caixa de saída cifrada; o despachante entrega fora de transação, com reenvio e validade. Sem e-mail verificado o aviso é pulado com motivo, nunca falha a reserva.
9. **Contato e recuperação do visitante** (módulo identidade): e-mail por loja, cifrado, com HMAC para busca; vale só depois do código de 6 dígitos (15 min, 5 tentativas). Recuperar: pedido público sempre 202 (não revela se existe), link de uso único de 30 min (#token, só o hash guardado); abrir o link não muda nada; a página troca o token por credencial de visitante num POST. Limites por IP e por endereço. O prazo de 7 dias à frente para visitantes continua.

## Alternativas

- Criar a comanda já na reserva: anteciparia senha, pontos e "uma visita ativa", que a parte 1 evitou de propósito.
- Confirmar pelo retorno do navegador ("já paguei"): qualquer print viraria pagamento.
- Estornar sempre o integral na sobra do sinal: devolveria também o que pagou o serviço feito.

## Consequências

- Duas novas fontes de pendência no painel do dono e uma rota de confirmação de devolução da reserva (reautenticação).
- O crédito do sinal aparece na comanda ("Sinal pago na reserva −R$ 15") e no total devido; a análise por profissional não atribui o Pix da reserva antes da chamada (ele ainda não tem comanda).
- Lojas sem a liberação continuam exatamente como na parte 1.
