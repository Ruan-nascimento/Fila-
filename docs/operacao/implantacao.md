# Implantação: homologação e produção

Como colocar o sistema num servidor, com TLS, segredos fora do código, banco com menor privilégio, migrações controladas e volta atrás. Arquivos em [`deploy/`](../../deploy/). Decisões no [ADR-0026](../adr/0026-operacao-trava-restauracao-metricas.md).

## 1. Ambientes

| Ambiente | `FILA_ENV` | Pix | E-mail | Dados | Quem usa |
|---|---|---|---|---|---|
| Desenvolvimento | `development` | desligado ou chave de teste | arquivos em `backend/.correio` | do desenvolvedor | quem programa (README da raiz) |
| Teste automático | `test` | simulador local | captura em memória | bancos temporários, criados e apagados a cada teste | `go test`, CI |
| **Homologação** | `homologacao` | **só chave de teste** (`abc_dev_`): simulador local ou sandbox da AbacatePay | arquivos (sem provedor de e-mail) | fictícios | ensaio de implantação, restauração e piloto |
| **Produção** | `production` | desligado até o checklist; depois só `abc_prod_` **e** só o endereço oficial `https://api.abacatepay.com` | desligado até haver provedor (**bloqueio externo**) | reais | a barbearia |

Homologação e produção têm as **mesmas travas**:

- `.env` ignorado;
- origens e links só `https`;
- cookies `Secure` com prefixo `__Host-`;
- banco só com `sslmode=require` ou mais forte;
- senha de exemplo recusada.

A diferença: a homologação **nunca** aceita chave de produção, e a produção **nunca** aceita chave de teste, simulador ou outro endereço de Pix. A imagem recusa subir nesse caso: o processo sai com código 2, o que é testado no CI.

## 2. O que roda (compose)

```
Internet ──443/80──▶ web (Caddy: TLS, site, proxy) ──▶ api:8080 ──▶ postgres:5432 (TLS)
                                                     worker ─────▶ postgres
                                                     (métricas internas :9090) ◀── prometheus (só 127.0.0.1)
```

| Serviço | Imagem | Observações |
|---|---|---|
| `web` | `caddy:2.11.9-alpine` (oficial, fixada por digest) | Único com portas públicas. TLS automático (Let's Encrypt) ou interno (homologação local). Cabeçalhos de segurança: HSTS, CSP, X-Frame-Options e outros. SSE sem buffer. Segura as requisições por até 20 s enquanto a API troca. **Não é recriado a cada versão.** |
| `site` | `fila-web:<versão>` | Roda e sai: publica o site da versão no volume que o Caddy serve. Os assets antigos ficam, para páginas abertas, e o `index.html` é trocado de uma vez. |
| `api`, `worker` | `fila-backend:<versão>` (distroless, sem shell, usuário 65532, sistema de arquivos só leitura, sem capacidades Linux) | Segredos por `*_FILE`. Healthcheck da API com `api healthcheck`. |
| `migrate`, `fila-admin` | `fila-backend:<versão>` | Ferramentas (perfil `ferramentas`). O `migrate` usa o papel dono das tabelas; o resto usa o papel da aplicação. |
| `postgres` | `postgres:18.7` (perfil `banco-local`) | TLS obrigatório para conexões de fora do contêiner. Sem banco gerenciado, roda aqui. |
| `backup`, `restore` | `postgres:18.7` | pg_dump e pg_restore da mesma versão do servidor. |
| `prometheus` | `prom/prometheus:v3.15.1` (perfil `monitoramento`) | Porta só em 127.0.0.1 (acesso por túnel SSH). |

## 3. Segredos

Ficam em arquivos numa pasta do servidor (`FILA_SEGREDOS`, ex.: `/etc/fila/segredos`, dono `root`, `0700`; arquivos `0444`), **separados por quem precisa**:

```
banco/     pg_senha_superusuario  pg_senha_migrador  pg_senha_app  pg_senha_backup
migrador/  database_url                     (postgres://fila_migrador:…@postgres:5432/fila?sslmode=require)
app/       database_url  secret_key  [abacatepay_api_key]  [abacatepay_webhook_secret]  [secret_key_previous]
backup/    pg_senha_backup  backup_chave
```

Cada contêiner monta **só a sua** pasta, em `/run/secrets`. A aplicação lê `FILA_DATABASE_URL_FILE`, `FILA_SECRET_KEY_FILE` e as demais. O erro de configuração cita a variável, nunca o conteúdo nem o caminho.

Gere as senhas com 24+ bytes aleatórios e a chave do servidor com `fila-admin gerar-chave`. Para trocar um segredo, veja [rotinas.md#rotacao-de-segredo](rotinas.md#rotacao-de-segredo).

Nada disso entra no Git: `.gitignore` e `.dockerignore` excluem `.env`, `deploy/env/*.env`, `*.key`, `*.pem` e dumps. O gitleaks roda no CI.

## 4. Banco com menor privilégio

| Papel | Pode | Não pode |
|---|---|---|
| `fila_dba` (superusuário do contêiner) | administrar o PostgreSQL | é usado só na criação e na restauração |
| `fila_migrador` | dono do banco e das tabelas; roda as migrações | — |
| `fila_app` (api, worker, fila-admin) | ler e escrever dados | criar, alterar ou apagar tabelas; `TRUNCATE`; mexer nas versões das migrações; `UPDATE`/`DELETE` em histórico (auditoria, extrato de pontos, movimentos de estoque, conferências, acertos, transferências). Consultas são cortadas em 30 s, espera por trava em 10 s e transação parada em 60 s |
| `fila_backup` | ler tudo (`pg_read_all_data`) | escrever |

- **Criação:** `deploy/postgres/papeis.sql`, rodado sozinho na criação do volume.
- **Permissões finas:** `migrate permissoes -app fila_app`, rodado depois de cada migração pelo `implantar.sh`.
- **Conferência:** `migrate permissoes -app fila_app -verificar` sai com 1 se o papel puder mais do que deveria.
- **Testes:** **toda a suíte de integração** roda conectada com um papel assim (`FILA_TEST_APP_ROLE`, também no CI). O teste `TestAppRoleHasLeastPrivilege` prova as recusas.

## 5. Primeira instalação (servidor Linux com Docker)

1. **DNS:** o domínio aponta para o servidor, e as portas 80 e 443 estão abertas no firewall.
2. **Código no servidor:** `git clone` deste repositório, por exemplo em `/opt/fila`.
3. **Configuração:**
   - copie `deploy/env/producao.env.example` para `/etc/fila/producao.env` e `aplicacao-producao.env.example` para `/etc/fila/aplicacao-producao.env`;
   - ajuste domínio e caminhos.
4. **Segredos:** crie as pastas e os arquivos de segredo da seção 3.
5. **Implantação:** `deploy/implantar.sh /etc/fila/producao.env $(git rev-parse --short HEAD)`. Ela constrói as imagens, sobe o banco e roda backup (a partir da segunda vez), migrações e permissões; depois troca API e worker, publica o site e confere `/healthz/ready` pela porta pública.
6. **Loja e dono:** `docker compose … run --rm fila-admin bootstrap -nome … -slug … -email …`.
7. **Agendamentos (cron do servidor):**
   - backup diário: `docker compose … run --rm backup`;
   - verificação a cada 5 min: `docker compose … run --rm fila-admin verificar -backups /backups`. Saída 3 avisa o operador; o canal de aviso é escolha da operação.
8. **Monitoramento:** `--profile monitoramento up -d prometheus` (seção de [monitoramento](monitoramento.md)).

## 6. Atualizar a versão sem perder fila

`deploy/implantar.sh <ambiente> <versão>` faz, nesta ordem:

1. Backup **antes** de migrar.
2. Migrações: **só para frente**, cada uma compatível com a versão anterior da aplicação.
3. Permissões mínimas.
4. **Troca da API.** O Caddy segura as requisições até a nova responder, e quem estava na fila continua: tudo está no banco. As telas reconectam o tempo real sozinhas, com o cursor.
5. **Troca do worker.** Cada tarefa retoma de onde parou.
6. **Publicação do site.** O Caddy não reinicia.
7. Conferência pela porta pública.

Medido na homologação: troca em 15 s com tráfego contínuo, **0 requisições falhas**, ninguém sumiu da fila e a tela do cliente atualizou sozinha ([evidencias.md](evidencias.md)).

**Limite:** com **uma** instância da API, as requisições ficam paradas por alguns segundos durante a troca (até 4,2 s medidos). Para zero espera seriam necessárias duas instâncias atrás do Caddy; isso não foi configurado nem testado.

## 7. Voltar atrás (reversão)

| Situação | O que fazer |
|---|---|
| Versão nova com defeito, migração sem problema | `FILA_SEM_BUILD=1 deploy/implantar.sh <ambiente> <versão anterior>`: o esquema migrado é compatível com a anterior. A API antiga tolera o banco mais novo; ela só se recusa a rodar com banco **mais velho** que ela. |
| Migração falhou | A migração roda em transação e o banco fica como estava; a versão antiga continua no ar. Corrija com uma **nova** migração (a aplicada nunca é editada). |
| Migração estragou dados | Restaurar o backup feito pelo `implantar.sh` antes de migrar: [backup-restauracao.md](backup-restauracao.md). Depois, implantar a versão anterior. |

Não há "down" automático em produção ([ADR-0016](../adr/0016-fundacao-ferramentas-e-configuracao.md)). Os blocos `-- +goose Down` das migrações documentam o inverso, mas **não são usados nem testados** como procedimento.

## 8. Limites de requisição, tempos e tamanhos (valores em uso)

| Item | Valor | Onde |
|---|---|---|
| Requisições por IP (por processo da API) | 600/min, rajada de 300; acima → 429 com `Retry-After` | `FILA_HTTP_REQUESTS_PER_MINUTE` |
| Tentativas de senha, código, cadastro, recuperação | limites próprios no banco (etapas 02–07) | `ratelimit` |
| Conexões SSE | 6 por conta ou visitante; 100 por IP; 2000 por processo (acima → 503) | `FILA_SSE_*` |
| Corpo da requisição | 64 KB na API (256 KB no webhook); 1 MB no Caddy | `httpserver`, Caddyfile |
| Cabeçalhos | 32 KB | `httpserver` |
| Tempos na API | leitura do cabeçalho 5 s, leitura 15 s, escrita 30 s (o SSE ajusta o prazo **por mensagem**, 5 s), ociosa 60 s, encerramento 20 s | `FILA_HTTP_*` |
| Tempos no Caddy | cabeçalho 10 s, corpo 30 s, ociosa 2 min, **sem** limite de escrita (SSE até 30 min) | Caddyfile |
| SSE | ping a cada 15 s; vida máxima 30 min; buffer de 256 eventos | `eventos-sse.md` §8 |
| Banco (papel da aplicação) | consulta 30 s, espera por trava 10 s, transação parada 60 s | `papeis.sql` |

## 9. Logs sem dados pessoais

- **API:** cada requisição registra método, **rota-modelo**, status, bytes e duração. Não registra IP, query string, corpo, cabeçalhos nem cookies.
- **Worker:** registra contagens e ids técnicos.
- **Caddy:** **sem log de acesso**, porque gravaria IP e user-agent.
- **Rotação:** 10 arquivos de 20 MB por contêiner (driver `json-file`).
- **Verificado** na homologação, depois da suíte inteira de navegador: nenhum e-mail, token, senha, chave ou nick nos logs da API, do worker e do Caddy.
- **Guarda e acesso aos logs** fora do servidor dependem da hospedagem escolhida (pendente; [retencao.md](../retencao.md)).
