# Evidências: sessão única do dono e Dashboard desktop (ADR-0027)

**Data:** 18–19/11/2026. **Máquina:** Linux (Ubuntu 24.04 / base do Linux Mint 22), Go 1.27.4, Node 26.13.1, Rust 1.100, PostgreSQL 18.6 (Docker local), WebKitGTK 2.52.

Regra deste documento: só conta como **aprovado** o que foi **executado** e cujo resultado foi **observado**.

## Resultados

| # | O quê | Comando (pasta) | Resultado |
|---|---|---|---|
| 1 | Backend inteiro com PostgreSQL real, incluindo os testes novos de sessão única, desktop/DPoP, cálculos, migração e arquitetura | `go -C backend test -count=1 ./...` (raiz, com `FILA_TEST_DATABASE_URL`) | **23/23 pacotes ok** |
| 2 | Análise estática do backend | `go -C backend vet ./...` e `go -C backend tool staticcheck ./...` (raiz) | sem apontamentos |
| 3 | Contrato, matriz, cenários | `python3 docs/tools/verificar_contrato.py` (raiz) | OK: 104 operações, 106/106 transições citadas em cenário |
| 4 | Frontend web: tipos do contrato, formatação, lint, tipos, componentes, build | `npm run check:api`, `npx prettier --check src`, `npx eslint .`, `npx tsc -b`, `npx vitest run`, `npm run build` (`frontend/`) | **tudo ok; Vitest 95/95**. O bundle web não contém o Dashboard (busca por `desktop/analytics` e textos do Dashboard em `dist/assets`: nada) |
| 5 | Dashboard local (jsdom) | `npm run test:ui` (`desktop-dono/`) | **4/4** |
| 6 | Componente nativo | `cargo test` (`desktop-dono/src-tauri/`) | **4/4** |
| 7 | Janela Tauri real, cenário original | `node testes/janela.mjs completo` (`desktop-dono/`, com o `.deb` de teste local extraído) | **13/13** |
| 8 | Janela Tauri real, cenário Dashboard | `node testes/janela.mjs dashboard` (`desktop-dono/`) | **12/12**: ativação pelo código, ticket fora da URL, mesma sessão, R$ conferido, leitura em 1366×768, 1920×1080 e 820×620, abas sob demanda, navegador recusado no analítico, troca web → desktop e desktop → web com limpeza, alternância Dashboard/Filas |
| 9 | Navegador real, entrada do dono | `npx playwright test e2e/dono.spec.ts --project=chrome` (`frontend/`) | **3/3 no Chrome** |
| 9b | Navegador real, suíte inteira no Chrome | `npx playwright test --project=chrome` (`frontend/`) | **8 passaram, 5 falharam**. Para separar regressão de ambiente, as mesmas specs rodaram contra o código **anterior** a esta entrega (worktree de `redesign-frontend`, banco e portas próprios): lá falham as **mesmas 5 e mais uma**. São falhas que já existiam neste ambiente (sem simulador de Pix; jornadas do redesign), não regressões. Os helpers do e2e foram ajustados para a sessão única (confirmar a troca e compartilhar a sessão do navegador com a preparação) |
| 10 | Desempenho com 60 mil atendimentos | `FILA_PERF=1 go -C backend test -run TestDesempenho -v ./internal/analitico/` | ver [desempenho.md](desempenho.md) e [saída](../evidencias/desktop-desempenho.txt) |

### Testes novos, por exigência

| Exigência | Teste |
|---|---|
| Cancelar mantém a web; confirmar transfere; web perde leitura, mutação e SSE; logout tardio não derruba a nova; repetição após queda de rede | `TestOwnerSessionConflictThenCancel`, `TestOwnerSessionTakeover` |
| Logins simultâneos: no máximo uma sessão | `TestOwnerConcurrentLogins` (8 logins paralelos) |
| Duas confirmações contra a mesma sessão; confirmação desatualizada | `TestOwnerTakeoverRaceAndStaleChallenge` |
| Mutação em andamento × troca | `TestOwnerTakeoverWaitsForInFlightMutation` |
| Abas, desafio sem cookie da tentativa, desafio vencido | `TestOwnerSameCookieAndChallengeBinding` |
| Contas distintas donas da mesma loja; trocar de loja; conta mista; cliente não ocupa a vaga | `TestOwnerSlotIsPerAccount` |
| Troca de senha, expiração, sessão fora da vaga, vínculo removido | `TestOwnerSlotLifecycle` |
| Sessões anteriores à migração | `TestUpgradeKeepsOneOwnerSessionPerAccount` |
| Navegador recusado no analítico; sem ativação; código reutilizado; prova reutilizada, de outro caminho ou vencida; token sem a chave; instalação revogada | `TestDesktopActivationAndProof` |
| Desktop ↔ web; ticket de uso único; sem sessão não há ticket; logout | `TestDesktopFollowsOwnerSession` |
| Dono de A e profissional de B; id de B na URL ou no filtro; ticket de outra conta | `TestDesktopStoreScope` |
| Cálculos: dois serviços × um recebimento, recebimento e estorno em dias diferentes, troco, Pix pendente, estorno em análise, chamada ausente, meia-noite no fuso, registros = gráfico, antes da coleta, comparação, filtro inaplicável | `TestAnalyticsCalculations` |
| Resposta atrasada da loja A depois de trocar para B; sessão encerrada limpa os números; tabela = gráfico | `desktop-dono/dashboard/src/dashboard.test.tsx` |
| Conflito, cancelar, queda de rede, aviso de outro aparelho, navegador sem Dashboard | `frontend/src/pages/dono/sessao-unica.test.tsx` |

## Não executado (e por quê)

| Item | Motivo |
|---|---|
| Instalador Windows (NSIS) e cofre de credenciais do Windows | Sem Windows disponível. Use `scripts/gerar-instalador-windows.ps1` ou o workflow "Fila Gestão (instaladores)" |
| `sudo apt install` do `.deb` | Sem sudo na máquina; o pacote foi extraído (`dpkg -x`) e executado |
| Microsoft Edge (Playwright) | Edge não instalado neste Linux |
| Teste de detector de corridas (`-race`) desta entrega | Não rodado nesta máquina; roda no CI |
