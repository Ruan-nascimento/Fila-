# Evidências da etapa 12: o que foi verificado, onde e com que números

Tudo aqui foi **executado** em 2026-11-14. O que não foi executado está na seção 6.

## 1. Ambiente usado

| Item | Valor |
|---|---|
| Máquina | notebook Windows 11, Intel Core i5-1155G7 (4 núcleos), 16 GB |
| Docker | Docker Desktop 29.9.3 (8 CPUs e 7,6 GB para os contêineres) |
| Homologação | `deploy/compose.yaml` com `FILA_ENV=homologacao`, Caddy com TLS interno em `https://localhost:8443`, PostgreSQL 18.7 com TLS obrigatório, papéis `fila_migrador`/`fila_app`/`fila_backup`, segredos em arquivos gerados na hora (fora do repositório) |
| Pix | simulador local (mesmo formato da API v2 da AbacatePay) com chave de teste; **a AbacatePay real (sandbox ou produção) não foi usada**, porque não há chave disponível |
| Imagens | `fila-backend` (distroless, 78 MB), `fila-web` (publicação do site), `caddy:2.11.9-alpine`, `postgres:18.7`, `prom/prometheus:v3.15.1`, todas fixadas por digest |

**Não é o servidor de produção:** cliente e servidor na mesma máquina, e o Docker Desktop encaminha portas do Windows para os contêineres. Os números servem como referência; refaça no servidor real.

## 2. Implantação, TLS, segredos e banco

| Verificação | Resultado |
|---|---|
| Primeira implantação pelo `deploy/implantar.sh` (banco vazio → 16 migrações → permissões → API → worker → site → saúde pela porta pública) | ok em 11 s |
| TLS no site | `https://localhost:8443` com HSTS, CSP, X-Frame-Options, nosniff, Referrer-Policy; o cabeçalho `Server` foi removido |
| Banco sem TLS | recusado (`pg_hba.conf rejects connection … no encryption`); a aplicação conecta com TLS 1.3 |
| Papel da aplicação | `CREATE TABLE` negado (`permission denied for schema public`); `migrate permissoes -verificar` limpo. **Toda a suíte de integração** (backend) passa conectada com um papel assim, localmente e no CI |
| Navegador real na homologação (Chrome 157 e Edge 157) | 18/18 (21 telas × 6 larguras + 4 jornadas), passando por Caddy, TLS, CSP e o papel restrito; **nenhuma violação de CSP** |
| Logs depois da suíte inteira (API 824 linhas, worker, Caddy) | 0 `ERROR`, 0 respostas 5xx, nenhum e-mail, token, senha, chave ou nick |
| Produção com Pix simulado | a imagem recusa subir (saída 2): chave de teste e endereço que não é o oficial. Teste local e no CI (job "Implantação") |

## 3. Implantar sem perder a fila (`operacao.spec.ts`, teste 1)

1. Loja com 3 pessoas na fila pela API e uma 4ª (Lia) pelo **celular no navegador**.
2. Implantação de uma versão nova com tráfego contínuo de leitura.
3. No meio da troca, Beto chama e inicia o primeiro atendimento: comandos repetidos com a mesma chave até dar certo.

| Medida | Resultado |
|---|---|
| Duração da implantação | 15 s (backup 1 s, migrações, troca da API em ~6 s, worker, site) |
| Leituras durante a troca | 40, **0 falhas** (todas 200); a mais lenta levou 4,2 s, segurada pelo Caddy enquanto a API trocava |
| Fila | ninguém sumiu: o primeiro em atendimento, os outros aguardando |
| Tela da Lia | passou de "4º" para "3º" **sozinha**, sem "Reconectando" |

O exercício também **achou dois problemas**, corrigidos nesta etapa:

- A posição na fila não atualizava sozinha quando alguém da frente era chamado (defeito das etapas 06/11). Correção na tela da visita, com teste.
- A primeira versão do script trocava o contêiner do Caddy e deixava a porta pública fechada por ~3 s (12 leituras falharam). Agora o Caddy fica no ar e só o site é publicado.

## 4. Worker morto no meio do trabalho (`operacao.spec.ts`, teste 2)

O worker foi morto com `SIGKILL` (sem encerramento). Com ele morto:

- um Pix foi pago no provedor;
- uma reserva de prêmio venceu;
- uma conta pediu exclusão;
- um e-mail de redefinição de senha foi pedido.

Nada andou. Ao religar:

| Medida | Resultado |
|---|---|
| Pix confirmado | 16,2 s depois de religar |
| Tudo em dia (Pix, pontos da reserva devolvidos, exclusão concluída, e-mail entregue) | 16,3 s |
| Efeito único | 1 recebimento para o Pix, 1 liberação para a reserva |

## 5. Backup e restauração (`operacao.spec.ts`, teste 3, e medição com volume)

| Medida | Banco pequeno (homologação) | Banco com ~1 GB (2 milhões de linhas sintéticas de auditoria) |
|---|---|---|
| Backup (dump, conferência, cifra, HMAC) | 1,4 s (327 KB cifrado) | 15 s (85 MB cifrado) |
| Restauração do banco (pg_restore) | 1 s | 33 s |
| **API no ar** depois de começar a restaurar | **14 s** | **51 s** |
| Conciliação | 1 s | ~2 s |
| **Worker liberado** | **17 s** | **54 s** |

Resultado da conciliação no exercício:

- 1 pagamento registrado, com a tentativa recriada a partir do provedor;
- 1 exclusão reaplicada pelo diário, com o mesmo protocolo e concluída depois de liberar;
- 56 sessões encerradas;
- época do SSE trocada.

A entrada na fila feita depois do backup **não existe** depois de restaurar: é a perda (RPO) esperada.

**Monitoramento:** com o worker travado, `fila-admin verificar` saiu com 3 e `FilaWorkerTravadoRestauracao`.

## 6. Monitoramento e capacidade

| Verificação | Resultado |
|---|---|
| Prometheus coletando API e worker | `up` nos dois; métricas de requisições e da trava presentes |
| Alerta real | worker parado → `FilaWorkerForaDoAr` (crítico, operador, rotina worker-parado) disparou em **140 s** (regra de 2 min + coleta) e sumiu ao religar |
| Limite por IP (produção, 600/min) | carga de um único IP por 20 s: 498 atendidas (rajada + taxa), **87 378 recusadas com 429**, 0 erros de rede, servidor pronto depois |
| Leituras com limite aberto (só para medir) | **1 806 req/s** de leitura da fila pública com 50 clientes; p50 26 ms, p95 40 ms, p99 53 ms; 0 erros |
| SSE simultâneas (pela rede interna do Docker, via Caddy) | **1 900 abertas, 0 falhas**; API 204 MB, Caddy 294 MB; saúde respondendo em 26 ms durante a carga |
| Acima do teto (2 100 pedidas) | 2 000 abertas, **100 recusadas com 503**, como configurado |
| Limitação **do ambiente** | vindas do Windows pelo encaminhamento de portas do Docker Desktop, só ~508 conexões abriram e novas conexões TLS falharam. Pela rede interna, 1 900 funcionaram: é limite do Docker Desktop, não da aplicação. **Medir no servidor real.** |

Também nesta medição: quem só olha a página da loja (sem credencial) era limitado a 6 conexões **por IP**, ou seja, 6 pessoas no Wi-Fi da barbearia. Corrigido: para anônimos vale só o teto por IP (100).

## 7. Testes automatizados da etapa

| Tipo | O que prova |
|---|---|
| Integração (banco real) | troca de chave sem perder códigos e protocolos; tetos SSE; trava, cópia de banco detectada e liberação só com conciliação; supervisor para e religa tarefas; worker travado não faz nada; restauração ponta a ponta com o simulador (órfã, estorno, tentativa que nunca chegou, sessões, diário, repetição); comandos do operador com auditoria; papel restrito; indicadores e alertas |
| Unidade | métricas no formato Prometheus; limite por IP; segredos por arquivo; homologação × produção; Pix oficial só em produção; listagem paginada da AbacatePay; paridade alertas × Prometheus |
| Componente (Vitest) | pendência sem comanda; posição anda com o evento público |
| Mutação | 35 defeitos introduzidos de propósito nas partes críticas (34 no backend: chaves, segredos, limites, métricas, trava, conciliação, diário, alertas; 1 no frontend: posição na fila). Todos foram detectados; 4 só depois de reforçar os testes que os deixavam passar |
| CI | tudo acima, mais a suíte inteira com o papel restrito; shellcheck; compose; promtool; build das imagens; site sem segredo; imagem recusa produção com Pix simulado; executáveis sem pacote de teste |

## 8. O que NÃO foi verificado

- O **servidor de produção** (não existe ainda): números de capacidade e de recuperação lá.
- **TLS público (Let's Encrypt):** só o TLS interno foi exercitado. O ACME depende de domínio e DNS reais.
- **AbacatePay real** (sandbox e produção): conciliação, listagem paginada e webhook só contra o simulador. O checklist de [pagamentos-abacatepay.md](../pagamentos-abacatepay.md) segue pendente.
- **Envio de alertas a pessoas** (Alertmanager e canal): bloqueio externo.
- **Cópia dos backups para fora do servidor:** depende da hospedagem.
- **Banco gerenciado** (sem o perfil `banco-local`) e **restauração física / PITR**: documentadas, não exercitadas.
- **Duas instâncias da API:** não configuradas. Com uma, a troca segura as requisições por alguns segundos.
- Os blocos `Down` das migrações (reversão usada é aplicação anterior + backup).

## Preparação da V1 (20/11/2026, branch `publicacao-v1`, a partir de `a902365`)

Executado **localmente** (Linux, Go 1.27.4, Node 26, PostgreSQL 18.6 descartável em Docker, SeaweedFS como servidor S3 com autenticação). Nenhum provedor real foi usado.

| O que | Resultado |
|---|---|
| Linha de base antes das mudanças: `go test ./...` | todos os pacotes ok |
| `gofmt`, `go mod verify`/`tidy -diff`, contrato gerado em dia, `go vet`, `staticcheck`, `govulncheck` | ok; nenhuma vulnerabilidade |
| `go test -race ./...` com banco real e S3 local (`FILA_REQUIRE_DB_TESTS=1`, `FILA_REQUIRE_S3_TESTS=1`) | 24 pacotes ok; 1 falha de **tempo** (`TestProcessAtLimitMeasured`: 38 s > 30 s) com a CPU disputada pela compilação Rust; isolado com `-race`: **passou (16,6 s)**. Código de imagens não alterado |
| Dispatcher de e-mail: 4 workers simultâneos, 30 mensagens | cada mensagem enviada exatamente 1 vez |
| Queda depois do aceite e antes da confirmação | reenvio com a mesma chave de idempotência; a confirmação atrasada não sobrescreve |
| Link vencido / erro permanente do provedor | descartado sem envio / desistência na 1ª tentativa |
| Resend (servidor HTTP simulado): cabeçalhos, 429, 5xx, 401/403, 409, 400/422, tempo esgotado | classificação correta; nenhum erro contém destinatário, conteúdo ou chave |
| Assinatura S3 SigV4 contra os vetores oficiais da AWS (GET Object, ListObjects) | idênticos |
| Adaptador S3 contra SeaweedFS autenticado (anônimo recusado com 403) | gravar/ler/apagar, listagem paginada, isolamento por prefixo, credencial errada sem vazamento |
| Testes ponta a ponta de fotos da API gravando no S3 (8 testes) | ok; objetos confirmados no bucket |
| Restauração ponta a ponta com diário de exclusões **baixado do bucket** | ok |
| IP do cliente: proxy autenticado, cabeçalhos forjados sem segredo, IP inválido, clientes distintos | ok |
| Frontend: build, tipos, lint, prettier, 149 testes + 4 do proxy de borda; bundle sem vestígio do proxy | ok |
| Ciclo backup: `pg_dump` → `age` → `scripts/ensaio-restauracao.sh` (restaura, migra, trava, reconcilia, libera, indicadores) | ok, em segundos; contêiner e arquivo decifrado apagados ao fim |
| Workflows (`actionlint`) e Blueprints (YAML) | sem erros |
| Desktop: `npm run test:ui` | 4/4 |
| Desktop: `.deb` 1.0.0 (origem de teste `https://fila-v1-build.test`, **não distribuir**) | gerado: `fila-gestao` 1.0.0, 3,76 MiB |

**Não executado:** nada com Neon, Render, Vercel, R2, Resend, Google ou AbacatePay; SSE pelo percurso Vercel → Render; instalação do `.deb` com `apt`; Windows (nem build nem instalação).

### Homologação real (20/11/2026)

| Verificação | Resultado |
|---|---|
| Neon `fila-homologacao` | PostgreSQL 18.7, `uuidv7()` ok, 23 migrations, `fila_app` sem DDL |
| Resend `mail.fiveline.pro` | Verificado (DKIM, SPF, DMARC `p=none`); rastreamento desligado; chave restrita recebe 401 ao administrar |
| R2 `fila-hml-*` | 10/10 verificações de `scripts/r2-preparar.py`; testes `internal/midia` (S3) passando contra o R2 real |
| Render `fila-api-hml` e `fila-worker-hml` | Revisão no ar igual ao commit (`X-Fila-Revision`); `/healthz/ready` 200; worker `pronto`, e-mail pelo Resend |
| Vercel `fila-hml` em `teste.fiveline.pro` | Certificado ok; `/healthz/ready` JSON da API; `/healthz/client-ip` com o IP público real e `proxy_autenticado: true`; `/v1/nao-existe` 404 da API; `/conta` 200 (SPA); HSTS, `nosniff`, `DENY`, `no-referrer`, CSP em relatório; `/healthz` com `no-store` |
| Acesso direto a `fila-api-hml.onrender.com` | `/v1/*` → 403 `proxy_required`; `/healthz/ready` → 200 |
| E-mail ponta a ponta | Cadastro em `teste.fiveline.pro` → caixa de saída → worker → Resend `delivered` (dois testes, endereços `delivered+…@resend.dev`) |
| Backup (workflow `Backup`, homologação) | Dump com papel só-leitura `fila_backup` (56 tabelas), cifrado com age, enviado a `fila-hml-backup/banco/2026/11/20/`; cópia de mídia e diário. Primeira execução falhou (dump criado como root não podia ser apagado); corrigido em `e4684ba` |
| Ensaio de restauração do backup real | Baixado do R2 com a credencial de backup, decifrado e restaurado em PostgreSQL descartável; conciliação concluída (época nova, links invalidados), worker liberado, nenhum alerta; ~15 s por passo |

Erros encontrados e corrigidos nesta etapa: import do middleware sem extensão `.js` (500 `MIDDLEWARE_INVOCATION_FAILED` na Vercel), arquivo `.env.local` com token OIDC criado pelo `vercel link` (apagado e ignorado), dono do arquivo do dump no workflow de backup.

### Pix sandbox (AbacatePay, homologação, 20/11/2026)

Loja de teste `Barbearia Teste HML` (`teste-hml11202052`), criada com `fila-admin bootstrap`; dona, profissional e cliente com endereços `delivered+…@resend.dev`; serviço de teste de R$ 5,00. Chave `abc_dev_` (nada cobrado).

| Passo | Resultado |
|---|---|
| Cliente gera Pix pelo site | `pending`, R$ 5,00, copia-e-cola presente, `test_mode=true` |
| `simulate-payment` (sandbox) | Webhook `transparent.completed` autenticado (secret e assinatura), `devMode=true`, aplicado; 1 recibo com evidência `provider_webhook`; comanda em `awaiting_confirmation` |
| Conferência pelo profissional | 200; comanda `settled` |
| Estorno pedido pela dona (com reautenticação) | 202; worker pediu à AbacatePay; webhook `transparent.refunded` aplicado; estorno `confirmed` com evidência `provider` em ~1 s; consulta à AbacatePay mostra `REFUNDED`; 1 estorno só |
| Webhook sem secret ou com secret errado (pelo site) | 401; nada gravado |
| Reenvio do mesmo webhook pelo painel | **pendente** (ação no painel) |

### SSE pela Vercel (homologação, 40 min, 20–21/11/2026)

`/v1/stream` em `teste.fiveline.pro`, evento público gerado a cada 5 min. 3 conexões: a Vercel encerra o fluxo a cada **15 min exatos** (o servidor encerraria em 30); o cliente retoma com `Last-Event-ID` em 1–2 s, sem perder eventos. 17 eventos, 157 batimentos, maior silêncio 15 s (o batimento). Atraso da ação ao evento: 1,0–1,4 s; 5,1 s no evento que coincidiu com a reconexão. Primeiros bytes em 0,3–0,5 s.

### Produção (21/11/2026)

| Verificação | Resultado |
|---|---|
| `Publicar` em produção (aprovação do responsável, só `main`) | Sucesso; `X-Fila-Revision: c5cba95f4390` = `main` |
| `app.fiveline.pro` | `/healthz/ready` 200; IP real com `proxy_autenticado: true`; `/v1` direto no Render → 403 |
| Isolamento | Nenhum segredo igual entre homologação e produção; credenciais R2 de um ambiente recusadas no outro |
| Monitoramento | Heartbeat do worker de produção `up`; monitor do site e do backup criados |
| Primeira loja | `Barbearia WE` (`webarbearia`) criada com `fila-admin bootstrap`; convite do dono enviado pelo worker de produção (Resend `delivered`); página pública 200 |

### Incidente: zona DNS de `fiveline.pro` zerada (21/11/2026 ~00:45 UTC)

Durante a publicação, a zona na Hostinger voltou ao padrão (`@` → `2.57.91.91`, `www` → raiz), sem os registros da Vercel, do Resend, `teste` e `app`. Os servidores de nome não mudaram e todas as escritas desta preparação usaram `overwrite:false`. Causa não identificada (provável ação no painel da Hostinger). Restaurado pela API às ~00:50 com os valores conhecidos; conferido no servidor autoritativo; o domínio no Resend permaneceu `verified`. Também: `fiveline.pro` e `www` estavam sem projeto na Vercel (`DEPLOYMENT_NOT_FOUND`) e foram religados ao projeto `fiveline` a pedido do responsável.
