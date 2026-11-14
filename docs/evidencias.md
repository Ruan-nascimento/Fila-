# Evidências da revisão final (etapa 13)

**Data:** 2026-11-14. **Código:** branch `main` no commit desta revisão (o hash está no histórico do Git; a mesma bateria roda no CI a cada commit).

Regra deste documento: só conta como **aprovado** o que foi **executado** e cujo resultado foi **observado**. O que não foi executado aparece na seção 8, com o motivo. As saídas brutas ficam em [evidencias/](evidencias/).

## 1. Ambiente

| Item | Valor |
|---|---|
| Máquina | Windows 11, Intel Core i5-1155G7 (4 núcleos), 16 GB |
| Go / Node | Go 1.27.4 · Node 26.13.1 |
| Banco para testes | PostgreSQL 18.7, portátil local e contêiner `postgres:18.7`. Os testes de integração criam um banco por teste e **conectam com o papel restrito da aplicação** (sem DDL, sem TRUNCATE, sem reescrever histórico) |
| Navegadores | Google Chrome 157 e Microsoft Edge 157 (instalados), pelo Playwright 1.64.1 |
| Homologação | `deploy/compose.yaml` em Docker Desktop 29.9.3: Caddy com TLS interno (`https://localhost:8443`), CSP, PostgreSQL com TLS obrigatório, papéis com menor privilégio, simulador de Pix (formato da API v2 da AbacatePay) com chave de teste |
| CI | GitHub Actions (Ubuntu 24.04): contrato, backend com `-race`, frontend, implantação (imagens, compose, shellcheck, promtool), gitleaks |

## 2. Revisão contra as regras

Rastreabilidade completa: [rastreabilidade.md](rastreabilidade.md), gerada por `python docs/tools/rastrear_evidencias.py` e conferida no CI.

| Situação | Regras (de 131) |
|---|---|
| Automatizado: todo cenário da regra tem teste que o cita | 126 |
| Automatizado (estrutural: arquitetura, contrato, build) | 4 |
| Sem teste automatizado | 1: **R-WEB-04** (testar Chrome, Firefox, Edge e Safari, inclusive celular real). Chrome e Edge foram testados (seção 4); Firefox, Safari e celulares físicos **não** (seção 8) |

Na revisão, 36 cenários estavam sem teste vinculado:

- **31** tinham teste que os cobria, mas citava só a regra. Foram ligados depois de conferir o texto de cada um contra o teste.
- **4** ganharam teste novo nesta etapa: privacidade da fila pública, limites de corpo, recarregar a página, nick com HTML na tela.
- **1** é manual: C-WEB-04, navegadores registrados.

## 3. Comandos executados e resultados observados

| # | O quê | Comando | Resultado |
|---|---|---|---|
| 1 | Backend inteiro: domínio, integração com PostgreSQL real, concorrência, contrato, SSE, pagamentos com simulador, operação. **Papel restrito** | `go test -count=1 ./...` (com `FILA_TEST_DATABASE_URL` e `FILA_TEST_APP_ROLE`) | **21/21 pacotes ok; 232 funções de teste** ([saída](evidencias/check-local.txt)). Uma falha na 1ª execução (o próprio teste de variáveis de implantação lia nomes sem dígitos, `FILA_CURL_IPV4`); corrigido e reexecutado: ok |
| 2 | **Detector de corridas** em toda a suíte, em Linux, com PostgreSQL 18.7 e o papel restrito | `go test -race -count=1 ./...` (contêiner `golang:1.27.4` + `postgres:18.7`) | **21/21 pacotes ok, nenhum `DATA RACE`** ([saída](evidencias/go-test-race.txt)). Também roda no CI a cada commit |
| 3 | Frontend: tipos do contrato, formatação, lint, tipos, componentes, build | `npm run check:api && npm run format:check && npm run lint && npm run typecheck && npm test && npm run build` | **tudo ok; Vitest 72/72** (11 arquivos) ([saída](evidencias/check-local.txt)) |
| 4 | Contrato e rastreabilidade | `python docs/tools/verificar_contrato.py` · `python docs/tools/rastrear_evidencias.py --verificar` | OK (92 operações, 101 transições, 107 cenários) |
| 5 | Navegador real na homologação: 21 telas × 6 larguras com axe, só teclado, 4 jornadas e o **roteiro do piloto**, em Chrome **e** Edge | `npx playwright test telas jornadas piloto` | **19 de 20 na 1ª execução**: o roteiro do piloto no Edge falhou no último passo por um defeito **do teste**, que mirava "a primeira" pendência quando havia duas. Corrigido o alvo: **roteiro 2/2 (Chrome e Edge)**; telas e jornadas **18/18** ([saída](evidencias/navegador-homologacao.txt)) |
| 6 | Exercícios de operação na homologação: implantar com fila, worker morto, restauração com conciliação | `E2E_OPERACAO=1 npx playwright test operacao --project=chrome` | **3/3** ([saída](evidencias/operacao-homologacao.txt), [medidas](evidencias/exercicios-operacao-etapa13.json)): implantação em 26 s com 73 leituras e **0 falhas**; worker morto → tudo em dia em 14 s; restauração do banco de ~1 GB → API no ar em 36 s, worker liberado em 39 s, Pix pago depois do backup recuperado |
| 7 | Vulnerabilidades em dependências Go | `go tool govulncheck ./...` | **0 vulnerabilidades no código** · 1 em módulo não usado (GO-2026-5932, `x/crypto/openpgp`, não importado) ([saída](evidencias/govulncheck.txt)) |
| 8 | Vulnerabilidades em dependências npm | `npm audit --audit-level=low` | **0** ([saída](evidencias/npm-audit.txt)) |
| 9 | Segredos no histórico do Git | `gitleaks git` (v8.30.1) | **nenhum** em 56 commits ([saída](evidencias/gitleaks.txt)) |
| 10 | Mutação (defeitos de propósito) nos testes novos | `mut12.py revisao` (fora do repositório) | 3 de 4 detectados. O 4º (tirar o filtro de loja de `getOrder`) não abre brecha: o handler confere a loja do ator e `LoadOrder` filtra de novo. São duas camadas; registrado |

## 4. Revisão de segurança e privacidade

| Tema | Como foi verificado | Resultado |
|---|---|---|
| **Autorização** por objeto, loja, papel, sessão | `TestAuthorizationSweepAllRoutes` (novo): **todas as rotas do contrato** chamadas por 6 intrusos (anônimo; dona de outra loja com a loja A na URL; dona de outra loja com a loja dela e objetos da loja A; cliente sem relação; visitante sem relação; outro profissional da mesma loja), com ids **reais** de visita, comanda paga, recebimento, cobrança, reserva e convite | **376 chamadas proibidas pelo contrato recusadas** (401/403/404), 19 delas provadas com corpo válido e a versão real do recurso; **0 inconclusivas**; 23 leituras de dados próprios e listas filtradas **sem nenhum id alheio**; nenhum recurso da loja A mudou; os intrusos continuaram logados até o fim |
| Autorização em eventos (SSE) | `TestStreamAudienceIsolation`, `TestStreamSessionRevoked`, `TestPublicQueueLeaksNothing` (novo) | Canais negados sem sessão; cursor emprestado não vaza; sessão revogada encerra; fila pública sem nick, nome, e-mail, serviços, valores ou ids de pessoa |
| Tentativas negativas por papel | `TestRoleEscalationAndCrossEstablishment`, `TestCrossEstablishmentIsolation`, `TestOrderAccessAndList`, `TestCatalogAuthorization`, `TestPublicSignupNeverGrantsStaffAccess` | ok |
| **CSRF e origem** | `TestCSRFAndOrigin` | Sem token ou com origem de fora → recusado |
| **XSS** em nick e descrições | `TestInjectionStringsAreData` (API devolve o texto) + **navegador**: nick `<img src=x onerror=alert(1)>` na tela da equipe aparece como texto, nenhum diálogo, nenhum `<img>` criado (roteiro do piloto); CSP do Caddy vigiada em toda tela; o frontend não usa `innerHTML` nem `eval` | ok |
| **Injeção de SQL** | Leitura de todo SQL dinâmico (só fragmentos fixos do código e marcadores `$N`) + `TestInjectionStringsAreData` (SQL em nick, slug, protocolo, login) | ok; o banco não mudou |
| **Enumeração de conta** | `TestPublicResponsesDoNotRevealAccounts` | Cadastro, recuperação e reenvio respondem igual para conta existente ou não |
| **Segredo em log** | `TestNoSecretsInLogsAuditOrDatabase` + varredura dos logs da homologação depois da suíte inteira (etapa 12) | Nenhum e-mail, token, senha ou chave |
| **Replay** | Idempotência (`TestPixDoubleClick`, `idempotency_test`), webhook repetido (`TestWebhookConfirmsPixOnceAndDeduplicates`), conciliação repetida (`TestRestoreReconcilesBeforeWorkerResumes`) | Efeito único |
| **Gasto duplo** | `TestNoDoubleSpend`, `TestLastUnitConcurrency`, `TestDeliveryVersusExpiry` (concorrência real no banco) | Um só vence; saldo e estoque nunca negativos |
| **Exclusão incompleta** | `TestDeletionLeavesNoPersonalDataAnywhere` (novo): depois de concluída, procura nome, e-mail e nick em **todas as 135 colunas** de texto, JSON e binárias de todas as tabelas | Achou o defeito **B-13-01** (nick nos eventos da equipe e nas respostas guardadas por até 24 h). **Corrigido** e o teste passa |
| Limites de corpo e campos | `TestBodyAndFieldLimits` (novo) | 1 MiB → 413; motivo de 10 mil caracteres → 400; nada processado |
| Menor privilégio no banco | `TestAppRoleHasLeastPrivilege` + suíte inteira com o papel restrito | ok |

## 5. Roteiro real do piloto (navegador)

`frontend/e2e/piloto.spec.ts`, na homologação, com celulares de 360 e 390 px, balcão de 1024 px e computador de 1440 px:

1. Visitante entra pelo link da loja e recebe o código.
2. **Recupera** a visita em outro aparelho; **recarregar** a página mostra o mesmo estado.
3. Conta entra pela tela e **muda o pedido** (R$ 45 → R$ 75) enquanto aguarda.
4. Nick com HTML aparece como texto na tela da equipe.
5. Beto chama: a tela da visitante muda sozinha. Depois da tolerância de 10 min, **ausência**.
6. **Rede da cliente cai**; Beto a chama; a rede volta e a chamada aparece sozinha.
7. Atendimento concluído pela tela; **Pix** gerado, confirmado pelo banco (simulador) e **conferido**; "Pago".
8. **15 pontos** creditados uma vez; prêmio **reservado e entregue**.
9. **Interrupção** com motivo; a cadeira é liberada.
10. Pix pago depois de trocar para dinheiro vira **pendência**; a **dona** a vê e resolve pela tela, e revisa a interrupção.

Resultado: **passou no Chrome e no Edge** (51 s cada) ([saída](evidencias/navegador-homologacao.txt)).

Ações feitas pela API, com as mesmas regras da tela, porque o objetivo era o resultado e não a tela da equipe:

- conferência do Pix;
- interrupção;
- revisão da interrupção;
- montagem do Pix tardio.

A passagem dos 10 minutos foi simulada no banco da homologação.

## 6. Pagamentos

| Item | Situação |
|---|---|
| Fluxo Pix contra o **simulador** (mesmo formato da API v2 da AbacatePay): criação, consulta, webhook assinado, estorno, tardio, divergência, conciliação pós-restauração | **Executado**: integração, navegador e homologação |
| **Sandbox real da AbacatePay** | **NÃO executado**: não há chave de teste disponível. Bloqueio externo. O checklist de [pagamentos-abacatepay.md](pagamentos-abacatepay.md) §5 segue pendente |
| Cobrança real | **Não ativada.** A produção recusa chave de teste, simulador e qualquer endereço que não seja o oficial (testado no CI) |

## 7. Defeitos

Lista completa, com gravidade e reprodução: [bugs.md](bugs.md).

- **Etapa 13:** 1 defeito de **alta** gravidade (privacidade), corrigido.
- **Etapa 12:** 5 defeitos, todos corrigidos.
- **Em aberto:** **nenhum defeito crítico** de integridade, privacidade ou dinheiro.

## 8. O que NÃO foi testado e o que depende de fora

| Item | Por quê | Risco |
|---|---|---|
| **Envio de e-mail real** | Não há provedor contratado. Em produção, convites, confirmação de conta e redefinição de senha **não chegam** | **Bloqueia o piloto em produção** ([piloto.md](piloto.md) §1) |
| **AbacatePay real** (sandbox e produção) | Sem chave | Pix fica desligado no piloto até validar |
| Firefox, Safari, celulares físicos (Android, iPhone) | Não disponíveis aqui; emulação de largura não prova o aparelho real | Testar antes do piloto |
| Leitor de tela real (NVDA, VoiceOver, TalkBack) | Só a verificação automática (axe) | Médio para acessibilidade |
| Servidor de produção real, TLS público (Let's Encrypt), cópia dos backups fora do servidor, envio de alertas a pessoas | Ainda não existem | Capacidade e tempos medidos só na homologação local |
| Banco gerenciado, restauração física / PITR, duas instâncias da API | Documentados, não exercitados | — |
| Prazos legais de retenção | Dependem de orientação jurídica | Padrões técnicos documentados |
| `go test -race` no Windows | Sem compilador C aqui; executado em contêiner Linux e no CI | — |
| Etapas 05–10: roteiros HTTP ponta a ponta (scripts PowerShell) | Não repetidos nesta etapa: os testes de integração e de navegador cobrem os mesmos fluxos. Última execução: etapa 11, todos aprovados | Baixo |

## 9. Critérios de pronto do anexo (§15)

| Critério | Evidência |
|---|---|
| Visita completa: seleção, entrada, chamada, serviço, pagamento e conferência | Roteiro do piloto (seção 5) + jornadas + integração |
| Visitante e conta funcionam; equipe no celular e no computador | Roteiro do piloto (larguras 360–1440) |
| Ninguém acessa visita ou loja alheia alterando identificadores | Varredura de autorização (seção 4) |
| Duas chamadas simultâneas não atendem duas pessoas | `TestFIFOAndDoubleCall` (concorrência) |
| Cancelar após chamada; ausência respeita 10 min; interrupção fecha por regra | `TestCancelParticipation`, `TestToleranceAndNoShow`, `TestInterruptionPositive`/`Zero`, roteiro do piloto |
| Concluir corte não encerra o acesso de pagamento do visitante | Jornada do visitante (C-VIS-03) |
| Pix duplicado ou tardio registrado e tratado | `TestLatePixIssuesAndResolution`, `TestWebhookDivergences`, roteiro do piloto |
| Dois resgates concorrentes não gastam o mesmo saldo ou estoque | `TestLastUnitConcurrency`, `TestNoDoubleSpend` |
| Estado após reconexão = banco; sessão revogada para de receber | `TestStreamResumeAndCursorFallbacks`, `TestStreamSessionRevoked`, roteiro do piloto (rede cai) |
| Layout nas larguras previstas | `telas.spec.ts` (6 larguras, axe, 44 px) |
| Sandbox financeiro e restauração têm evidências | Restauração: **sim** (etapa 12 e seção 3). Sandbox financeiro: **não** (seção 6) |
| Falhas críticas de acesso, dinheiro ou perda de dados impedem o lançamento | Nenhuma em aberto. **O lançamento em produção continua impedido pelos bloqueios externos** (e-mail; Pix real para usar Pix) |
