# Fila 2.0 — agenda, fila e atendimento híbrido (checkpoint)

Documento de retomada do trabalho descrito em `Fila-2.0-Prompt-de-implementacao.md` (três partes). Leia primeiro ao retomar.

| | |
|---|---|
| Worktree | `../Fila-2.0` (branch `agenda-2.0`) |
| Base usada | `36e2d08` (origin/main, "Merge pull request #27 … docs-1.0.0") — a mesma citada no documento; nenhuma divergência entre HEAD e a base |
| Situação | **Parte 1 concluída e testada localmente**, sem commit (aguardando autorização) |
| Decisão | [ADR-0038](../adr/0038-agenda-hibrida-e-uma-cadeira.md) |

Atenção ao juntar: a worktree principal (`indicadores-e-carregamento`) tem alterações de interface não commitadas em `Gestao.tsx`, `ui.tsx`, `styles.css` e várias páginas. A parte 1 também mexe em `Gestao.tsx`, `Loja.tsx`, `Painel.tsx` e `Conta.tsx`: espere conflitos de texto nesses arquivos.

## 1. O que a parte 1 entrega

- **Forma de atendimento por loja**: "Por ordem de chegada" (padrão), "Com horário marcado" ou "Os dois", em Configurações da loja → **Agenda**, com aviso de impacto antes de trocar. Liberada no servidor por `FILA_AGENDA` (ligada em desenvolvimento; desligada em homologação/produção até a operação ligar).
- **Horários** (gestão): jornada semanal da loja e de cada profissional (vários turnos, pausa, modalidade fila/agenda/compartilhado no modo "os dois"), "Copiar segunda", prévia do dia, data de vigência; folgas, feriados e bloqueios com motivo interno. Mudanças que deixariam reserva de fora são recusadas com a lista.
- **Motor de disponibilidade** (Go, puro): grade, duração × quantidade + preparação, pausas, antecedência, horizonte, fuso da loja, horário de verão, meia-noite; hoje desconta a cadeira em uso e a fila projetada.
- **Reserva gratuita** de ponta a ponta: cliente (conta ou visitante) escolhe profissional → serviços/quantidades → dia/horário → revisão → confirmação; "Minhas reservas" com Cheguei, Remarcar e Cancelar; reserva de balcão pelo dono (código de recuperação para o cliente).
- **Uma cadeira por profissional**: restrição de exclusão GiST no banco + trava da fila em todos os caminhos; "Chamar próximo", "descer", iniciar e aumentar serviço respeitam o próximo horário protegido, com o motivo na tela e o botão "chamar e iniciar agora".
- **Agenda da equipe**: "Hoje" (cadeira atual, próximo da fila, horários do dia) e "Por dia" (colunas por profissional no computador, lista no celular), com Registrar chegada, Chamar, Remarcar e Cancelar; a visita da reserva aparece em Filas com o selo "Horário marcado" e segue o fluxo de sempre (mesma comanda, mesmo pagamento).
- **Worker**: falta automática (só com chamada válida e sem presença) e "a loja não chamou" (não é falta do cliente).
- **Foto do dono** nos dois links "Minha conta" da gestão (lateral e compacto), com iniciais de reserva.

## 2. Migração

`backend/migrations/00029_agenda.sql` (instala `btree_gist`, extensão confiável). Testado: banco limpo com as 29 migrações aplicadas por um dono de banco **sem superusuário** (como `fila_migrador`/Neon). Lojas existentes ficam em `service_mode = 'queue'`.

## 3. Como testar pela interface (os três modos)

Ambiente local: API + worker + Vite (README, seção de desenvolvimento) com `FILA_AGENDA` omitida (em desenvolvimento ela já vem ligada). Para a foto do dono, `FILA_MEDIA_STORAGE=filesystem`.

1. **Por ordem de chegada (padrão)** — Sem mudar nada, a loja abre como hoje: gestão começa em **Filas**; a página pública `/loja/<endereço>` mostra só a fila; não há "Agendar horário".
2. **Os dois** — Gestão → Configurações da loja → aba **Agenda** → "Os dois" → Salvar agenda → confira o aviso ("Agora há N reserva(s) futura(s) e M pessoa(s) na fila…") → **Mudar a forma de atendimento**. Em **Horários**: Segunda → "Abrir neste dia" (09:00–18:00) → "Copiar segunda para terça a sábado" → Salvar jornada. Na página pública aparecem **Entrar na fila** e **Agendar horário**: Agendar → profissional → serviço (+ quantidade) → dia → horário → revisão ("Reserva gratuita: nada a pagar agora") → apelido ou conta → Confirmar → "Ver minhas reservas". Na gestão, que agora abre em **Agenda → Hoje**, use "Por dia" para ver a reserva com Registrar chegada / Chamar / Remarcar / Cancelar. Para ver a regra de encaixe: com uma reserva daqui a ~40 min, ponha alguém na fila com um serviço longo e tente "Chamar próximo" — a tela explica "Próximo horário marcado às … Este serviço precisa de … min com a limpeza" e oferece "Cliente já está aqui: chamar e iniciar agora".
3. **Com horário marcado** — Agenda → "Com horário marcado". A página pública abre direto em "Com quem?" (agendamento); entrar na fila é recusado ("Agora a entrada é só com horário marcado"); quem já estava na fila continua sendo atendido. Chamado o horário, a visita aparece em Filas e o cliente acompanha/paga em `/loja/<endereço>/visita` (link em "Minhas reservas").
4. **Balcão** — Agenda → Por dia → **Reserva de balcão**: nome, profissional, serviços, horário → o código aparece uma única vez para entregar ao cliente ("Já tenho um código" na página da loja).
5. **Foto do dono** — Minha conta → enviar foto → Salvar foto → voltar à gestão: a foto aparece no rodapé da lateral e no ícone da barra do celular; recarregue; remova e confira as iniciais; saia e entre com outra pessoa no mesmo computador.

## 4. Testes executados (24/11/2026, banco descartável `fila-teste-v1`, PostgreSQL 18.6)

| O quê | Resultado |
|---|---|
| `go test ./...` (backend inteiro, inclui integração com PostgreSQL real) | ok — todos os pacotes; antes da varredura ajustada, uma falha real corrigida (anônimo recebia 400 em vez de 401 ao reservar) |
| Agenda, integração (`internal/api/agenda_test.go`, C-AG-01…11 e 14) | 12 testes ok |
| Motor (`internal/agenda/motor_test.go`, C-AG-12) | 10 testes ok |
| Varredura de autorização com reserva e exceção reais da loja A | 678 chamadas negadas por contrato, 0 inconclusivas, sem vazamento |
| Concorrência | 6 reservas simultâneas no último horário → 1 vence, 5 `slot_unavailable`; duas conexões reais sem trava da aplicação → a exclusão GiST recusa a segunda |
| Frontend: `tsc`, `eslint`, `prettier --check`, `vitest` (156 testes), `vite build` | ok |
| E2E Chrome real (`frontend/e2e/agenda.spec.ts`: três modos + foto do dono) | 2 de 2 ok |
| E2E já existentes (Chrome): `telas.spec.ts` e `jornadas.spec.ts` | `telas`: 5 de 6 ok (a falha de acessibilidade do link "Minha conta" compacto, causada pela foto, foi corrigida); `telas do cliente` e 3 de `jornadas` falham por estarem desatualizados desde a 1.0.0 (procuram caixas "Corte" e "Seus pontos" na página inicial da loja, que já não existiam na base `36e2d08`) |
| `verificar_contrato.py`, `rastrear_evidencias.py`, Redocly lint, `go vet`, `staticcheck` | sem erro |

Não executados: Edge (só Chrome nesta máquina), Windows, programa desktop (Tauri), navegador físico de celular (só emulação de viewport), homologação/Neon.

## 5. Pendências conscientes (próximas partes)

- Parte 2: sinal/taxa via Pix, e-mails de confirmação/lembrete/chamada, recuperação do visitante por e-mail (hoje o visitante marca até 7 dias à frente e recupera pelo código), política de cancelamento em horas.
- Parte 3: calendário mensal agregado, histórico da equipe paginado por cursor, indicadores no dashboard desktop, medição de carga, guia prático e PDF atualizados, ensaio do worker parado/restauração com reservas.
- Desativar profissional com reserva futura já é recusado (`professional_has_appointments`, com a contagem); falta uma tela que liste e remaneje em lote (parte 3).
