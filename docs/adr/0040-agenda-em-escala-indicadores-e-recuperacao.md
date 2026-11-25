# ADR-0040 — Agenda em escala: leituras, invalidação, indicadores do dono e recuperação

- Situação: Aceita (Fila 2.0, parte 3)
- Data: 2026-11-25
- Relacionadas: ADR-0038 (agenda e uma cadeira), ADR-0039 (reserva com Pix), ADR-0021 (fluxo SSE), ADR-0027 (Dashboard desktop)

## Contexto

As partes 1 e 2 entregaram a agenda e a reserva paga. Faltava provar que a mesma implementação aguenta muitos clientes olhando o mesmo dia, que o dono acompanha agendamentos no computador sem duplicar regras, e que uma parada do worker não vira falta em massa nem aviso fora de hora.

## Decisão

1. **Sem Redis, sem cache primeiro.** Medir antes (ensaio em `internal/api/ensaio_agenda_test.go`, `FILA_PERF=1`): consultas SQL por requisição, latência com 1/10/100 leitores, disputa pelo último horário, rodada ociosa do worker e conexões retidas. O que o ensaio mostrou foi corrigido na origem:
   - **N+1 na disponibilidade**: os itens eram resolvidos uma vez por profissional (2–3 consultas cada). Agora uma resolução por requisição (`atendimento.ResolveEach`) e a checagem de cada profissional em memória: 19 → 11 consultas na leitura do dia, constantes ao dobrar os horários.
   - **Trava em leitura pública**: o catálogo era lido com `FOR SHARE` também na disponibilidade; 100 leitores no mesmo serviço disputavam a trava compartilhada. Leituras públicas usam `ServicesForRead`/`ProductsForRead` (sem trava); a confirmação continua relendo com trava.
   - **Tarefa "a loja não chamou"** lia todas as reservas confirmadas (prazo é expressão). Consulta limitada por `starts_at <= agora` e índice parcial `appointment_store_late_due_idx` (migração 00031): 15 ms/11,6 mil blocos → 2 ms/2 blocos por rodada.
   Cache só entra em outro incremento, com benchmark entre réplicas e comportamento com o cache fora do ar.
2. **Calendário mensal agregado** (`getAvailabilityDays`): até 62 dias, uma leitura de jornada (todas as versões), exceções e ocupações; cada dia calculado em memória com a MESMA regra da consulta do dia e da confirmação. Horários detalhados só do dia escolhido. Na tela: pedido anterior cancelado (`AbortController`), resposta velha ignorada, revalidação ao voltar para a aba; nenhuma consulta periódica.
3. **Invalidação pública pequena** (`availability.updated`, audiência pública): só loja, profissional e data (nulos = loja inteira), nunca quem reservou. Emitida na mesma transação de reservar, remarcar (dia antigo e novo), cancelar, vencer e encerrar, e nas mudanças de jornada/exceção. A página de agendamento agrupa eventos em 400 ms e relê só o dia visível e o agregado.
4. **Recuperação depois de parada do worker**: falta automática só para prazos vencidos há até 30 min; chamadas mais antigas ficam para a equipe decidir (alerta `FilaChamadaDeReservaSemDesfecho`). Cada aviso por e-mail tem validade a partir do vencimento (chamada 15 min, confirmação 6 h, lembrete até o horário…); vencido é pulado com motivo, nunca enviado fora de hora. Retenções vencidas continuam sendo soltas (ninguém perde horário por isso).
5. **Indicadores no Dashboard** (`getAnalyticsAppointments`, aba Agendamentos): contagens pelo horário marcado, dinheiro pela data em que entrou/foi aplicado/devolvido, pendências como fotografia. Taxa de falta = faltas ÷ (faltas + comparecimentos) entre reservas com desfecho. Ocupação planejada e utilização real calculadas com `agenda.WorkIntervals` (jornada vigente em cada data: versões imutáveis, então hoje não reescreve ontem), com cobertura de dias com jornada. Visita de reserva tem origem `appointment` e não entra nas entradas nem na espera da fila (corrigido no analítico). Detalhamento `records/appointments` abre a Agenda no dia (destino nativo `agenda`, data validada no Rust).
6. **Navegação**: links de entrada do dono e a logo da gestão vão para a entrada da loja, que decide entre Agenda e Filas pela forma de atendimento; o sino da equipe avisa nova reserva, reserva paga, cancelamento do cliente e "Cheguei" (abre a Agenda no dia); o do cliente avisa a chamada do horário.
7. **Operação**: métricas `fila_agenda_*` no worker (retenções, retenções presas, chamadas sem desfecho, avisos pendentes/idade/vencidos) e três alertas no Prometheus e em `fila-admin verificar`.

## Alternativas

- Cache de disponibilidade por TTL: esconderia o N+1 e a trava, e exigiria invalidar por reserva, jornada, fila e passagem do tempo.
- Falta automática sem limite de atraso: depois de uma queda, marcaria falta em clientes que estavam na loja.
- Um ticker por reserva ou consulta por segundo no navegador: custo proporcional ao número de clientes.

## Consequências

- O alvo de p95 ≤ 300 ms para 100 leitores do mesmo dia ficou no limite com o pool de teste de 5 conexões (293–335 ms em duas rodadas, PC de 8 núcleos); em homologação aquecida o número precisa ser medido de novo antes de virar SLA.
- O Dashboard instalado precisa da versão nova do programa (consulta `agendamentos` e destino `agenda`); a gestão web não muda de versão por isso.
