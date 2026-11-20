# Publicação da V1: estado, checkpoint e roteiro

Topologia: **Vercel** (site + proxy `/v1` e `/healthz`) → **Render** (API Go + worker Go) → **Neon** (PostgreSQL 18). **Cloudflare R2** guarda fotos, o diário de exclusões e os backups, **Resend** envia os e-mails, **Google** cuida do login de cliente e **AbacatePay** do Pix. Variáveis: [variaveis-v1.md](variaveis-v1.md). Recuperação: [backup-e-recuperacao-v1.md](backup-e-recuperacao-v1.md).

## 1. Checkpoint (atualize a cada sessão)

| Campo | Valor |
|---|---|
| Revisão inicial analisada | `a902365` (main, 20/11/2026) |
| Branch de trabalho | `publicacao-v1` |
| Última revisão desta preparação | ver `git log -1 publicacao-v1` |
| Serviços criados nos provedores | Neon: projeto `fila-homologacao` (20/11/2026, plano gratuito por enquanto). Resend: domínio `mail.fiveline.pro` verificado (20/11/2026). Hostinger: 5 registros DNS acrescentados em `fiveline.pro` (A e `www` intactos) |
| Recursos preexistentes reaproveitados | repositório GitHub, workflows `CI` e `Fila Gestão (instaladores)`, comandos `migrate`/`fila-admin` |
| Próxima ação | ver §8 |

### Situação de cada item

| Item | Situação |
|---|---|
| E-mail pelo Resend (adaptador, config, worker, dispatcher com reserva curta) | **Implementado e testado** localmente. **Configurado e validado no ambiente real**: domínio `mail.fiveline.pro` verificado (DKIM, SPF, DMARC `p=none`), rastreamento de abertura e clique desligado, uma chave *Sending access* restrita ao domínio por ambiente (a chave recebe 401 ao tentar administrar), envio real pelo `ResendSender` com idempotência confirmada (reenvio devolve o mesmo id). Falta o envio pelo worker em homologação, que depende do Render |
| Banco de homologação (Neon) | **Configurado e validado no ambiente real**: PostgreSQL 18.7, `uuidv7()`, conexão direta com `sslmode=require`, 23 migrations, papel `fila_app` sem DDL (`permission denied for schema public`), permissões verificadas. Conta ainda no plano gratuito |
| Fotos e logos no R2 (adaptador S3 com assinatura SigV4) | **Implementado e testado** contra servidor S3 real local (SeaweedFS com autenticação) e nos testes ponta a ponta de fotos da API. **Aguardando credencial** do R2 |
| Diário de exclusões no bucket + `fila-admin diario baixar` | **Implementado e testado**: a restauração ponta a ponta reconcilia a partir do diário baixado do bucket |
| IP do cliente atrás de Vercel → Render (proxy autenticado) | **Implementado e testado** (unidade e cadeia HTTP). **Não validado** na Vercel real |
| `vercel.json` + `middleware.ts` (proxy, SPA, cabeçalhos, CSP em modo relatório) | **Implementado**: build, tipos e testes do proxy passam. **Não validado** na Vercel real |
| Blueprints do Render (homologação e produção) | **Preparado**, não aplicado (exige contratação) |
| Workflow `Publicar` (migrations → deploy da mesma revisão → espera a revisão no ar) | **Preparado**, não executado |
| Workflow `Backup` (dump cifrado + cópia de fotos e diário + sinal de vida) | **Preparado**, não executado. O ciclo dump → age → restauração foi **ensaiado localmente** |
| Sinal de vida do worker para monitor externo (`FILA_HEARTBEAT_URL`) | **Implementado e testado**. **Aguardando** a escolha do monitor |
| Script de ensaio de restauração | **Implementado e executado** localmente (ver §7) |
| Desktop 1.0.0 (versões, checksums, notas, rascunho de Release) | **Preparado**; resultado do build Linux local em §7. Windows **não testado** |
| Google (código já existia) | **Aguardando** cliente OAuth |
| AbacatePay (código já existia) | **Aguardando** chave de teste; sandbox não executado |
| SSE pela Vercel (> 35 min, reconexão, deploy) | **Não validado**: depende da homologação real |
| Primeira loja | **Aguardando** e-mail, API, worker e banco em produção |

**A V1 não está pronta.** Faltam todas as validações com provedores reais, o SSE pelo percurso real, o Pix em sandbox e o teste do Windows.

## 2. Decisões desta preparação

- **E-mail:** sem SDK. HTTPS direto para `POST /emails` do Resend. A API só grava na caixa de saída cifrada; o worker entrega. O dispatcher deixou de enviar dentro de uma transação longa (migration aditiva `00023`: `lease_until` e `provider_message_id`).
- **Fotos:** cliente S3 mínimo em `internal/platform/objstore`, sem SDK, verificado contra os vetores oficiais de assinatura da AWS. O bucket é privado e a leitura continua pelas rotas autorizadas da API.
- **IP do cliente:** a Vercel envia o IP que viu mais um segredo compartilhado. Sem o segredo, vale o IP que o balanceador do Render viu. Isso evita juntar todos os clientes no IP da Vercel e impede escolher o próprio IP chamando a API direto.
- **Proxy na Vercel por middleware**, e não por `rewrites` fixos, porque o destino vem de variável **por ambiente**. Assim um preview não aponta para a produção, e o segredo não fica no Git.
- **Diário de exclusões no bucket** em vez do disco do worker: outro processo do Render não monta aquele disco, e o bucket é legível pelo `fila-admin` e pelo backup.
- **Monitoramento sem dar ao monitor acesso ao banco:** o worker avisa um serviço externo a cada minuto (`/fail` quando há alerta interno). Se o worker para, o serviço externo nota a falta do sinal. O site e a API recebem verificações HTTP externas.
- **Migrador fora do Render:** a credencial de dono do banco fica só no GitHub Environment do workflow `Publicar`.

## 3. Custos a autorizar antes de criar (valores para conferir no painel de cada provedor)

| Serviço | Para quê | Plano sugerido | Observação |
|---|---|---|---|
| Render | API + worker, sempre ativos | 2× *Starter* por ambiente | O gratuito suspende por inatividade e não serve para fila em tempo real |
| Neon | PostgreSQL 18 | Plano pago com computação contínua | O worker consulta o banco sem parar, então o banco não "dorme" |
| Vercel | Site + proxy | *Pro* para uso comercial | O *Hobby* é para uso pessoal, não comercial |
| Cloudflare R2 | Fotos, diário, backups | Pago por uso; há franquia gratuita | Confira o cadastro de pagamento exigido pela Cloudflare |
| Resend | E-mails | Gratuito no início (limite diário/mensal) | Precisa de domínio próprio verificado |
| Monitor externo (Better Stack ou Healthchecks.io) | Avisar quando cair | Gratuito no início | Confira os termos de uso comercial |
| Domínio | Endereço fixo (OAuth, QR, desktop, e-mail) | — | Se você ainda não tiver um |

Nada foi contratado. Cada contratação depende da sua escolha.

## 4. Roteiro de homologação (ordem por dependência)

### 4.1 Neon

1. Crie o projeto **fila-homologacao** com **PostgreSQL 18**, na região **AWS US East (N. Virginia)**, a mesma do Render `virginia`.
2. *Connect* → desligue *Connection pooling* → copie a URL **direta** (sem `-pooler`). Esta é a URL do **migrador**. Guarde no gerenciador de senhas.
3. No *SQL Editor*, crie o papel restrito. Troque a senha por uma gerada e **não** salve este SQL com a senha:
   ```sql
   CREATE ROLE fila_app LOGIN PASSWORD '<senha forte>' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
   ALTER ROLE fila_app SET statement_timeout = '30s';
   ALTER ROLE fila_app SET lock_timeout = '10s';
   ALTER ROLE fila_app SET idle_in_transaction_session_timeout = '60s';
   GRANT CONNECT ON DATABASE neondb TO fila_app;   -- use o nome real do banco
   SHOW server_version; SELECT uuidv7();
   ```
4. Monte a URL do `fila_app` (mesmo host e banco, `sslmode=require`). Se a senha tiver caractere especial, codifique-o na URL.

### 4.2 Cloudflare R2

1. R2 → crie os buckets **privados** `fila-hml-midia`, `fila-hml-diario` e `fila-hml-backup`, sem acesso público e sem domínio público.
2. *Manage API tokens* → token **Object Read & Write** restrito a `fila-hml-midia` e `fila-hml-diario`. Ele vai para o Render.
3. Crie outro token, restrito aos três buckets, para o workflow `Backup`.
4. Em `fila-hml-backup`, crie uma regra de ciclo de vida **só no prefixo `banco/`**, apagando após 35 dias.

### 4.3 Render

1. *New → Blueprint* → repositório `Fila-` → *Blueprint path*: `deploy/render/homologacao.render.yaml` → branch `publicacao-v1` (até o merge).
2. Preencha os campos `sync: false`. Gere `FILA_SECRET_KEY` uma única vez: `go -C backend run ./cmd/fila-admin gerar-chave`.
3. Antes do primeiro deploy, aplique as migrations (§4.6, ou manualmente como em `GUIA-PUBLICACAO-FILA-V1.md` §4.3).
4. Confira: a API responde em `https://<api>.onrender.com/healthz/ready`, e o log do worker mostra `worker pronto`.

### 4.4 Vercel

1. *Add New → Project* → `Fila-` → **Root Directory `frontend`** (o `vercel.json` e o `middleware.ts` estão lá).
2. *Environment Variables*: `FILA_API_ORIGIN` e `FILA_PROXY_SHARED_SECRET` (marque *Sensitive*). Para homologação, use o ambiente *Production* de um projeto separado, `fila-hml`. No projeto de produção, deixe *Preview* sem `FILA_API_ORIGIN`.
3. Domínio: `teste.<seu domínio>`. Copie o registro DNS indicado pela Vercel.
4. No Render, coloque essa origem em `FILA_PUBLIC_BASE_URL` e `FILA_ALLOWED_ORIGINS`, e o mesmo segredo do proxy na API.
5. Confira, no navegador: `/healthz/live` e `/healthz/ready` devolvem JSON (não HTML). `/healthz/client-ip` mostra o **seu** IP e `"proxy_autenticado": true`. `/v1/nao-existe` devolve 404 JSON da API. `/conta` recarrega sem 404.

### 4.5 Resend

1. *Domains → Add Domain* → `mail.<seu domínio>`. Copie **exatamente** os registros mostrados. Não altere os MX do seu e-mail atual e não crie um segundo SPF no mesmo nome.
2. Espere o status *Verified*. Depois: *API Keys* → **Sending access** restrito ao domínio → coloque no worker (`FILA_RESEND_API_KEY`).
3. Em *Domain settings*, desligue o rastreamento de cliques e de aberturas (os links levam tokens).

### 4.6 GitHub

*Settings → Environments → `homologacao`*: secrets e variables do workflow `Publicar` ([variaveis-v1.md](variaveis-v1.md)). Depois: *Actions → Publicar → Run workflow → homologacao*.

### 4.7 Monitoramento

No monitor externo:
1. HTTP: `https://teste.<domínio>/` (site) e `https://teste.<domínio>/healthz/ready` (API pelo percurso real), a cada 1 a 3 min.
2. Sinal de vida **worker**: período de 1 min, tolerância de 5 min. A URL vai em `FILA_HEARTBEAT_URL`.
3. Sinal de vida **backup**: período de 24 h, tolerância de 2 h. A URL vai em `FILA_BACKUP_HEARTBEAT_URL`.
4. Canal: e-mail **e** um segundo canal (Telegram ou SMS), que não dependam do Fila.
5. Teste de falha: suspenda o worker de homologação, confirme que o aviso chegou, reative e confirme o aviso de recuperação.

### 4.8 Testes de aceitação em homologação

Roteiro mínimo (registre data, resultado e observações em [evidencias.md](evidencias.md)):

- Cadastro → e-mail de verificação chega → link abre o domínio certo e funciona uma vez.
- Recuperar senha; convite de profissional; convite do dono (`fila-admin bootstrap`); conta somente Google (código de reautenticação).
- Visitante entra na fila; profissional chama, inicia e conclui; comanda e serviços; pagamento presencial; pontos; histórico.
- Foto de perfil e logo: envio, processamento, exibição autorizada, recusa para quem não pode ver, remoção. Reinicie a API e o worker separadamente; a foto continua lá.
- SSE: celular (cliente) e computador (profissional). A mudança aparece sem recarregar. Mantenha aberto por mais de 35 min. Desligue e religue o Wi-Fi. Rode o `Publicar` com a página aberta. Não pode haver evento duplicado nem posição perdida.
- Dois aparelhos em redes diferentes, cada um em `/healthz/client-ip`: os IPs devem ser diferentes.
- Cookies: `__Host-`, `Secure` e `HttpOnly` no domínio do site.
- Cabeçalho `x-vercel-cache` em `/v1/...` nunca deve ser `HIT`.
- Console do navegador sem violações da CSP (modo relatório). Só depois torne a CSP obrigatória.

### 4.9 Google e AbacatePay

Siga `GUIA-PUBLICACAO-FILA-V1.md` §8 (Google) e §10 (AbacatePay, sandbox). O código dessas integrações não mudou nesta preparação. Callback: `https://teste.<domínio>/v1/auth/google/callback`. Webhook: `https://teste.<domínio>/v1/webhooks/abacatepay`, com o secret cadastrado no painel da AbacatePay e o mesmo valor em `FILA_ABACATEPAY_WEBHOOK_SECRET` ([../pagamentos-abacatepay.md](../pagamentos-abacatepay.md), passo 7). Ele passa pelo proxy da Vercel como qualquer `/v1`.

O código usa **uma chave AbacatePay por implantação**: um único recebedor. A V1 é um piloto com esse recebedor. Não há split nem repasse para barbearias diferentes.

## 5. Produção

Repita §4 com `producao.render.yaml`, buckets `fila-prod-*`, projeto Neon e domínio próprios e **chaves diferentes**. Ligue *Required reviewers* no environment `producao`. Pix real só depois do sandbox e com sua autorização específica para a primeira cobrança real.

## 6. Desktop 1.0.0

*Actions → Fila Gestão (instaladores) → Run workflow* com a origem HTTPS final e `rascunho_release` marcado. O resultado é um rascunho de Release com `.exe`, `.deb`, `SHA256SUMS-*.txt` e [NOTAS-DA-VERSAO.md](../../desktop-dono/NOTAS-DA-VERSAO.md). Publique o rascunho só depois de instalar e testar no Windows e no Linux reais (roteiro em [../desktop/guia-do-dono.md](../desktop/guia-do-dono.md)).

## 7. Testes executados nesta preparação

Ver [evidencias.md](evidencias.md), seção "Preparação da V1 (20/11/2026)".

## 8. Próxima ação

Domínio `fiveline.pro` e custos de homologação autorizados. Neon (§4.1) e Resend (§4.5) prontos, com as chaves em `~/.config/fila-v1/` (fora do Git, permissão 600; entrada por `scripts/segredo.sh`). Próximo: R2 (§4.2), depois Render (§4.3) e Vercel (§4.4). A chave *Full access* do Resend usada na configuração (`RESEND_SETUP_API_KEY`) deve ser apagada no painel quando não for mais necessária.
