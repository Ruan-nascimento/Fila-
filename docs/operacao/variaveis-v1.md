# Variáveis da V1 (Vercel + Render + Neon + R2 + Resend)

Esta tabela vale a partir da revisão em que `FILA_MAIL_MODE=resend`, `FILA_MEDIA_STORAGE=s3`, `FILA_PROXY_SHARED_SECRET`, `FILA_DELETION_JOURNAL_BUCKET` e `FILA_HEARTBEAT_URL` passaram a ser aceitas pelo carregador (`backend/internal/platform/config/config.go`). O backend **recusa** variável `FILA_` desconhecida e ignora `.env` em homologação e produção.

**Segredo** significa: preencher só no painel do provedor (Render, Vercel ou GitHub Environments). O valor nunca vai para o Git, para o chat, para logs, para variável `VITE_` ou para screenshot.

Em toda variável que é segredo do backend, a forma `NOME_FILE=/caminho` também é aceita (arquivo montado pela plataforma).

## Render: grupo comum (`fila-hml-comum` / `fila-prod-comum`), API e worker

| Variável | Valor / finalidade | Segredo? |
|---|---|---|
| `FILA_ENV` | `homologacao` ou `production` | não |
| `FILA_DATABASE_URL` | URL **direta** do Neon (host sem `-pooler`), papel **`fila_app`**, `sslmode=require` | **sim** |
| `FILA_SECRET_KEY` | Gerada **uma vez por ambiente** (`fila-admin gerar-chave`). A mesma vale na API, no worker e no `fila-admin`. Não gere outra a cada deploy | **sim** |
| `FILA_PUBLIC_BASE_URL` | Origem do site na Vercel, ex. `https://app.seudominio.com.br` | não |
| `FILA_ALLOWED_ORIGINS` | A mesma origem. Sem curinga e sem caminho | não |
| `FILA_COOKIE_SECURE` | `true` | não |
| `FILA_LOG_FORMAT` / `FILA_LOG_LEVEL` | `json` / `info` | não |
| `FILA_DB_MAX_CONNS` | `8` (por processo; somar API, worker, listeners e migrador frente ao limite do Neon) | não |
| `FILA_DB_CONNECT_TIMEOUT` | `10s` | não |
| `GOTOOLCHAIN` | `auto` (variável do Go, não do Fila: baixa a toolchain do `go.mod`) | não |
| `FILA_MEDIA_STORAGE` | `s3` | não |
| `FILA_S3_ENDPOINT` | `https://<id-da-conta>.r2.cloudflarestorage.com`, sem o bucket no caminho | não |
| `FILA_S3_BUCKET` | `fila-hml-midia` / `fila-prod-midia` (bucket **privado**) | não |
| `FILA_S3_REGION` | `auto` | não |
| `FILA_S3_ACCESS_KEY_ID` | Access Key ID do token R2 (Object Read & Write, só nos buckets do ambiente) | **sim** |
| `FILA_S3_SECRET_ACCESS_KEY` | Secret Access Key do mesmo token | **sim** |
| `FILA_S3_PREFIX` | Opcional, ex. `producao/`. Use quando o mesmo bucket servir mais de um ambiente. O recomendado é um bucket por ambiente | não |
| `FILA_DELETION_JOURNAL_BUCKET` | `fila-hml-diario` / `fila-prod-diario` (diário de exclusões fora do banco; usa as credenciais `FILA_S3_*`) | não |
| `FILA_PIX_PROVIDER` | `disabled` até concluir os testes; depois `abacatepay` | não |
| `FILA_ABACATEPAY_API_KEY` | `abc_dev_…` em homologação; `abc_prod_…` só em produção e só após a validação | **sim** |
| `FILA_ABACATEPAY_PRODUCTION_VALIDATED` | `true` só em produção, depois do roteiro de validação | não |

## Render: só na API (`fila-api-*`)

| Variável | Valor / finalidade | Segredo? |
|---|---|---|
| `FILA_HTTP_ADDR` | `:10000` (todas as interfaces; o Render encaminha para a porta 10000) | não |
| `FILA_TRUST_X_FORWARDED_FOR` | `true`: o balanceador do Render acrescenta ao fim do `X-Forwarded-For` o IP de quem conectou | não |
| `FILA_PROXY_SHARED_SECRET` | O **mesmo** valor configurado no projeto da Vercel. De 32 a 256 caracteres visíveis (pode usar a saída de `fila-admin gerar-chave`) | **sim** |
| `FILA_ABACATEPAY_WEBHOOK_SECRET` | Secret do webhook cadastrado na AbacatePay | **sim** |
| `FILA_GOOGLE_CLIENT_ID` | ID do cliente OAuth (Web) do ambiente | não (é público) |
| `FILA_GOOGLE_CLIENT_SECRET` | Secret do cliente OAuth | **sim** |

## Render: só no worker (`fila-worker-*`)

| Variável | Valor / finalidade | Segredo? |
|---|---|---|
| `FILA_MAIL_MODE` | `resend` | não |
| `FILA_RESEND_API_KEY` | Chave `re_…` com permissão **Sending access**, restrita ao domínio | **sim** |
| `FILA_MAIL_FROM` | `Fila <nao-responda@mail.seudominio.com.br>` (domínio verificado no Resend) | não |
| `FILA_MAIL_REPLY_TO` | Opcional: caixa real de atendimento | não |
| `FILA_MAIL_TIMEOUT` | `15s` (padrão) | não |
| `FILA_HEARTBEAT_URL` | URL do monitor de sinal de vida do worker (ex.: Healthchecks.io) | **sim** (contém identificador do monitor) |

Com `FILA_MAIL_MODE=resend`, o worker **não inicia** sem `FILA_RESEND_API_KEY` e `FILA_MAIL_FROM` válidas. Para subir o worker antes do Resend estar pronto, use `FILA_MAIL_MODE=disabled` temporariamente. Os e-mails ficam guardados na caixa de saída e vencem conforme o tipo (ver "E-mail" abaixo).

## Vercel (projeto do site, por ambiente)

Variáveis do **projeto** na Vercel (Settings → Environment Variables). Elas são lidas só pelo `frontend/middleware.ts` e **não** vão para o bundle.

| Variável | Production | Preview | Segredo? |
|---|---|---|---|
| `FILA_API_ORIGIN` | `https://fila-api-prod.onrender.com` (o endereço real atribuído pelo Render) | endereço da API de **homologação**, ou vazio (o proxy responde 503) | não |
| `FILA_PROXY_SHARED_SECRET` | o mesmo valor da API de produção | o mesmo valor da API de homologação | **sim** (marcar *Sensitive*) |

Nunca configure `VITE_` com segredo. `FILA_API_PROXY_TARGET` só vale no `vite dev` local e não configura o site publicado.

## GitHub (Settings → Environments → `homologacao` / `producao`)

| Nome | Tipo | Usado por | Segredo? |
|---|---|---|---|
| `FILA_MIGRATOR_DATABASE_URL` | secret | `Publicar`: URL direta do Neon com o **dono do banco** (migrador) | **sim** |
| `RENDER_DEPLOY_HOOK_API` / `RENDER_DEPLOY_HOOK_WORKER` | secret | `Publicar` | **sim** |
| `FILA_PUBLIC_BASE_URL` | variable | `Publicar` (espera a revisão nova no ar) | não |
| `FILA_APP_ROLE` | variable | `Publicar`: `fila_app` | não |
| `FILA_BACKUP_DATABASE_URL` | secret | `Backup`: URL direta do Neon (dono do banco) | **sim** |
| `R2_BACKUP_ACCESS_KEY_ID` / `R2_BACKUP_SECRET_ACCESS_KEY` | secret | `Backup`: token R2 com leitura nos buckets de fotos e diário e escrita no de backup | **sim** |
| `FILA_BACKUP_HEARTBEAT_URL` | secret | `Backup`: monitor de sinal de vida do backup | **sim** |
| `R2_ENDPOINT`, `R2_BACKUP_BUCKET`, `R2_MEDIA_BUCKET`, `R2_JOURNAL_BUCKET` | variable | `Backup` | não |
| `BACKUP_AGE_RECIPIENT` | variable | `Backup`: chave **pública** age (`age1…`). A privada fica só com o responsável, fora do GitHub e do R2 | não |

No environment `producao`, ligue **Required reviewers**. O repositório é público: os workflows não imprimem segredos, e o dump nunca vira artefato.

## Comportamentos que dependem dessas variáveis

- **E-mail:** o worker reserva cada mensagem por 2 min, envia ao Resend fora de transação com `Idempotency-Key: fila-mail/<id>` e confirma individualmente. Se cair depois do aceite, a nova tentativa usa a mesma chave, e o Resend deduplica por 24 h. Validade: código de reautenticação 10 min, redefinição de senha 30 min, demais 24 h. Mensagem vencida é descartada sem envio. Até 8 tentativas, com espera crescente. "Enviado" significa **aceito pelo Resend**, não que chegou na caixa de entrada.
- **IP do cliente:** pedido com `X-Fila-Proxy-Auth` correto usa `X-Fila-Client-Ip`, o IP que a Vercel viu. Sem o segredo, vale o último item do `X-Forwarded-For` acrescentado pelo Render (quem conectou de verdade). Por isso, chamar a API direto não permite escolher o próprio IP. Diagnóstico: `GET /healthz/client-ip` devolve só o IP de quem pergunta e se o proxy foi autenticado.
- **Fotos:** bucket privado. Toda leitura passa pelas rotas da API, que conferem a autorização. A limpeza por idade só olha `staging/` (originais nunca associados); imagens publicadas só são apagadas quando o registro deixa de usá-las.
