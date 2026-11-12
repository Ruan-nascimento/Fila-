# ADR-0016 — Fundação: ferramentas, versões e configuração

- Situação: Aceita (etapa 01)
- Data: 2026-11-12

## Contexto

A etapa 01 cria a estrutura do backend e do frontend. É preciso escolher versões suportadas, bibliotecas mínimas e um jeito único de configurar os ambientes, sem introduzir infraestrutura desnecessária (ADR-0001). As versões foram consultadas nas fontes oficiais em 12/11/2026 (go.dev/dl, nodejs.org/dist, postgresql.org, registro npm, proxy.golang.org, PyPI, GitHub Releases). Como regra de cadeia de suprimentos, preferimos versões publicadas há pelo menos ~2 semanas.

## Decisão

| Item | Escolha | Motivo |
|---|---|---|
| Go | `go 1.27.0`, toolchain `go1.27.4` (go.mod) | Versão estável atual; o Go baixa o toolchain certo sozinho (GOTOOLCHAIN=auto) |
| HTTP | `net/http` da biblioteca padrão (roteamento por método do Go 1.22+) | Sem framework; menos dependências |
| Logs | `log/slog` em JSON (texto opcional em dev), com remoção de campos sensíveis | Padrão da linguagem; estruturado |
| PostgreSQL | 18 (mínimo verificado pela migração 00001; `uuidv7()` nativo) | Série estável atual; imagem `postgres:18.6` fixada por digest até a 18.7 ser publicada no Docker Hub |
| Driver | `pgx/v5` v5.11.0 + `pgxpool` | Driver nativo mais usado; pool próprio |
| Migrações | `goose/v3` v3.29.0, SQL embutido no executável, trava por advisory lock | Versionadas, simples, seguras com várias instâncias |
| Política de migração | Só para frente: migração aplicada nunca é editada; correção = nova migração. Sem `down` automático em produção | Reversão será documentada na etapa 12 |
| Tipos Go do contrato | `oapi-codegen` v2.8.1, só modelos (`internal/contrato/types.gen.go`) | Suporta os construtos 3.1 usados (verificado: nulos viram ponteiros) |
| Análise Go | `go vet`, `staticcheck` v0.8.2, `govulncheck` v1.9.0 (diretiva `tool` no go.mod) | Versões travadas no go.sum |
| Node | 26 (LTS ativa) em `.nvmrc`; aceita 24.15+ (`engines` + `engine-strict`) | `jsdom` e `vitest` atuais exigem ≥ 22.22/24.15 |
| Frontend | React 19.3.1, Vite 8.3.5, Vitest 5.0.5, ESLint 10.13.0, Prettier 3.9.10 | Versões estáveis atuais |
| TypeScript | 5.9.3 | `typescript-eslint` aceita < 6.1 e `openapi-typescript` declara TS ^5; a 5.9 atende todos sem forçar dependências. Rever quando as ferramentas suportarem TS 6/7 |
| Tipos TS do contrato | `openapi-typescript` 7.13.0 + cliente `openapi-fetch` 0.17.0 | Gera tipos do mesmo OpenAPI; CI confere com `--check` |
| `minimatch` (dev) | override `>=7 <11.0.1 → 11.0.5` | Corrige GHSA-g3hv-75wg-m249 (DoS) em ferramentas de lint sem rebaixar o ESLint; verificado que o lint continua funcionando |
| Segredos no repositório | `gitleaks` v8.30.1 no CI (histórico completo) | Detecta chave/senha versionada por engano |
| Atualizações | Dependabot semanal com espera de 7 dias | Atualização revisada, sem pacote recém-publicado |

**Configuração.** Só variáveis de ambiente com prefixo `FILA_`, validadas na partida. Problemas são listados todos de uma vez, sem mostrar valores; variável `FILA_` desconhecida é erro (pega digitação). O arquivo `.env` é lido só fora de produção (da pasta atual ou da pasta acima); variáveis do processo vencem o arquivo. Em produção exige `sslmode` seguro e recusa senha de exemplo. Códigos de saída: 0 ok, 1 falha de execução, 2 configuração/uso inválido.

**Processos.** `cmd/api` (HTTP), `cmd/worker` (tarefas; nesta etapa só partida/encerramento) e `cmd/migrate` (`up`/`status`). A API sobe mesmo com banco fora (readiness 503); o worker não sobe sem banco migrado. Encerramento gracioso: readiness passa a falhar, novas conexões param, requisições em andamento terminam até `FILA_SHUTDOWN_TIMEOUT`.

**Saúde.** `/healthz/live` não consulta dependências; `/healthz/ready` confere banco e versão das migrações, respondendo `problem+json` sem detalhes internos (o motivo vai só para o log).

## Alternativas consideradas

- Framework HTTP (chi, gin, echo): desnecessário com o roteador da biblioteca padrão.
- ORM (GORM) ou gerador de SQL (sqlc): adiado; SQL parametrizado com pgx atende a etapa. `sqlc` pode entrar com ADR próprio quando houver consultas de domínio.
- `golang-migrate`/`tern`: equivalentes; goose tem API de provedor com trava e embed bem documentada.
- TypeScript 6/7: bloqueado por compatibilidade das ferramentas (ver tabela).
- Rebaixar ESLint para 9 para resolver o `minimatch`: rejeitado; o override é localizado e testado.

## Consequências

- `go`, `node`/`npm` e `python` são pré-requisitos locais; Docker é recomendado para o banco, mas o README também cobre PostgreSQL instalado.
- O override do `minimatch` deve ser removido quando ESLint/typescript-eslint passarem a depender de versão corrigida (o `npm audit` no CI avisa se voltar a haver vulnerabilidade).
- O detector de corridas (`go test -race`) roda no CI Linux; no Windows exige compilador C.
- Toda variável nova precisa ser registrada em `internal/platform/config` e no `.env.example`.
