# Fila — MVP web para barbearia

Sistema de fila, atendimento, pagamento e fidelidade para barbearia, construído em etapas (veja [REGRAS.md](REGRAS.md)).
**Situação atual: etapa 02 (identidade, visitantes e acesso).** Já funcionam: conta de cliente com confirmação de e-mail, login de cliente e de equipe, troca e recuperação de senha, convite de profissionais, dono inicial por comando controlado e visitante com código de recuperação. Fila, serviços, comanda e pagamento chegam nas próximas etapas.

| Pasta | O que tem |
|---|---|
| [backend/](backend/) | Servidor em Go: `cmd/api` (HTTP), `cmd/worker` (tarefas em segundo plano), `cmd/migrate` (migrações), módulos de domínio em `internal/` |
| [frontend/](frontend/) | Aplicação web React + TypeScript + Vite |
| [docs/](docs/README.md) | Regras, contrato OpenAPI, matrizes, cenários e decisões (ADRs) |
| [compose.yaml](compose.yaml) | PostgreSQL local via Docker |
| [.github/workflows/ci.yml](.github/workflows/ci.yml) | Integração contínua |

---

## 1. Instalar as ferramentas (uma vez)

| Ferramenta | Versão | Onde baixar | Conferir no terminal |
|---|---|---|---|
| Git | atual | https://git-scm.com/downloads | `git --version` |
| Go | 1.27 ou mais nova* | https://go.dev/dl/ | `go version` |
| Node.js | 26 LTS (ou 24.15+) | https://nodejs.org/ | `node --version` |
| Python | 3.10+ (só para verificar a documentação) | https://www.python.org/downloads/ | `python --version` |
| Docker Desktop | atual (recomendado para o banco) | https://www.docker.com/products/docker-desktop/ | `docker --version` |

\* Se você tiver um Go mais antigo (1.21+), ele baixa sozinho a versão exigida pelo projeto na primeira execução.

Sem Docker? Instale o **PostgreSQL 18** pelo instalador oficial (https://www.postgresql.org/download/) e siga a seção 3B.

Todos os comandos abaixo são para o **PowerShell**, executados **na pasta raiz do repositório** (onde está este README). No Linux/macOS os comandos são os mesmos, trocando `Copy-Item` por `cp` e `$env:NOME="valor"` por `export NOME="valor"`.

## 2. Criar o arquivo de configuração `.env`

```powershell
Copy-Item .env.example .env
```

Abra o `.env` num editor e troque `troque-esta-senha-local` por uma senha sua, **nas duas linhas** (`POSTGRES_PASSWORD` e dentro de `FILA_DATABASE_URL`, mantendo o `fila:` antes dela: `postgres://fila:SUA-SENHA@...`).
Use na senha só letras, números, `-` e `_` (símbolos como `@ : / # %` quebram a URL).

Depois gere a chave secreta do servidor e cole o resultado em `FILA_SECRET_KEY=` no `.env`:

```powershell
go -C backend run ./cmd/fila-admin gerar-chave
```

O `.env` fica só no seu computador: o Git o ignora. Nunca coloque senhas ou chaves reais no `.env.example`.

## 3A. Subir o banco com Docker (recomendado)

```powershell
docker compose up -d --wait
```

O comando termina quando o PostgreSQL está pronto. Para parar: `docker compose stop`. Para **apagar todos os dados locais**: `docker compose down -v`.

## 3B. Alternativa: PostgreSQL 18 instalado no Windows

Depois de instalar, abra o **SQL Shell (psql)** ou rode `psql -U postgres` e execute (troque a senha pela do seu `.env`):

```sql
CREATE ROLE fila LOGIN CREATEDB PASSWORD 'sua-senha-do-env';
CREATE DATABASE fila OWNER fila ENCODING 'UTF8' TEMPLATE template0;
```

## 4. Criar as tabelas (migrações)

```powershell
go -C backend run ./cmd/migrate up
```

Saída esperada na primeira vez: `aplicada: 00001`. Rodar de novo mostra `nada a aplicar`. Para ver a situação: `go -C backend run ./cmd/migrate status`.

## 5. Rodar a API

```powershell
go -C backend run ./cmd/api
```

Em outro terminal (ou no navegador), confira:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz/live    # {"status":"ok"} = processo vivo
Invoke-RestMethod http://127.0.0.1:8080/healthz/ready   # {"status":"ok"} = banco acessível e migrado
```

Para parar, aperte `Ctrl+C` no terminal da API (encerramento gracioso: termina o que está em andamento).

### 5.1 Rodar o worker (entrega os e-mails)

Em **outro terminal**, na raiz do repositório:

```powershell
go -C backend run ./cmd/worker
```

Ainda não há provedor de e-mail: em desenvolvimento cada e-mail (verificação de conta, redefinição de senha, convite) vira um arquivo `.txt` na pasta `backend\.correio\`. Abra o arquivo e copie o link para o navegador. Essa pasta não vai para o Git.

### 5.2 Criar a loja e o dono inicial (uma vez)

O cadastro pelo site nunca dá acesso de dono. A loja e o convite do dono são criados por este comando (com a API parada ou rodando, tanto faz):

```powershell
go -C backend run ./cmd/fila-admin bootstrap -nome "Barbearia Teste" -slug barbearia-teste -email seu-email@exemplo.com
```

Com o worker rodando, o convite aparece em `backend\.correio\`. Abra o link, defina nome e senha, e entre escolhendo **Equipe**. O convite vale 24 horas e só funciona uma vez.

## 6. Rodar o frontend

```powershell
cd frontend
npm ci
npm run dev
```

Abra http://127.0.0.1:5173. A página inicial mostra "API pronta" quando a API (passo 5) está rodando e o banco está migrado.
O Vite repassa `/v1` e `/healthz` para a API, então frontend e API ficam na mesma origem, como em produção.

Telas desta etapa:

| Endereço | Para quê |
|---|---|
| `/cadastro` | Criar conta de cliente (o link de confirmação chega por e-mail — em desenvolvimento, na pasta `backend\.correio\`) |
| `/entrar` | Entrar como **Cliente** ou como **Equipe da barbearia** |
| `/conta` | Ver a conta, trocar a senha e sair |
| `/esqueci-senha` | Pedir link para criar senha nova (vale 30 minutos) |
| `/loja/barbearia-teste` | Página da loja (use o `-slug` do passo 5.2): entrar sem conta com um apelido e receber o **código de recuperação**, ou recuperar a visita com o código |
| `/verificar-email`, `/redefinir-senha`, `/convite` | Abertos pelos links dos e-mails |

Use sempre o mesmo endereço (`http://127.0.0.1:5173` ou `http://localhost:5173`) durante o uso: cada um tem seus próprios cookies.

## 7. Rodar os testes

Testes que não precisam de banco:

```powershell
go -C backend test ./...
cd frontend; npm test; cd ..
```

Testes **com PostgreSQL real** (criam e apagam um banco temporário por teste; o usuário precisa da permissão `CREATEDB` — o do Docker e o da seção 3B têm):

```powershell
$env:FILA_TEST_DATABASE_URL = "postgres://fila:SUA-SENHA@127.0.0.1:5432/postgres?sslmode=disable"
go -C backend test -count=1 ./...
```

Sem essa variável, os testes de banco aparecem como **PULADOS** (`go test -v` mostra `SKIP`) — pulado não é aprovado.

## 8. Rodar tudo o que o CI roda

```powershell
powershell -ExecutionPolicy Bypass -File scripts\verificar.ps1
```

Inclui: verificação da documentação e do contrato, formatação, análise estática, vulnerabilidades conhecidas, testes e build do backend e do frontend.

## 9. Quando algo dá errado

| Mensagem | O que fazer |
|---|---|
| `configuração inválida (nenhum valor é exibido por segurança)` seguida de uma lista | Corrija cada variável listada no `.env`. A saída nunca mostra senhas |
| `FILA_XXX: variável desconhecida (erro de digitação?)` | Nome errado no `.env` ou no terminal |
| `banco inacessível; confira se o PostgreSQL está rodando` | Rode `docker compose up -d --wait` (3A) ou inicie o serviço do PostgreSQL (3B) |
| `/healthz/ready` responde 503 | Banco fora ou sem migrações: veja o log da API e rode o passo 4 |
| `worker não iniciado: banco indisponível ou sem migrações` | Rode o passo 4 |
| `não foi possível abrir o endereço HTTP (porta em uso…)` | Outra API já está rodando; feche-a ou mude `FILA_HTTP_ADDR` |
| `npm error engine Unsupported engine` | Instale Node 26 (ou 24.15+) |
| `Defina POSTGRES_PASSWORD no arquivo .env` | Falta o passo 2 |
| `FILA_SECRET_KEY: obrigatória` | Gere a chave (passo 2) e cole no `.env` |
| `password authentication failed` / `recusou o usuário ou a senha` | A senha dentro de `FILA_DATABASE_URL` não é a mesma do banco; confira também o `fila:` antes dela |
| Tela "Pedido não confirmado" | A página ficou desatualizada (sessão mudou em outra aba): recarregue a página |
| Tela "Origem não permitida" | Abra o site por `http://127.0.0.1:5173` ou `http://localhost:5173` (ou ajuste `FILA_ALLOWED_ORIGINS`) |
| E-mail não aparece em `backend\.correio\` | O worker (passo 5.1) precisa estar rodando |

Códigos de saída dos programas: `0` sucesso, `1` falha durante a execução, `2` configuração ou uso inválido.

## 10. Regras de segurança do repositório

- Segredos só em `.env` (local) ou nas variáveis da plataforma (produção). Em produção o `.env` é ignorado.
- Os logs ocultam automaticamente campos como senha, token, cookie e URL do banco — mesmo assim, não registre dados pessoais nem segredos.
- Variáveis com prefixo `VITE_` vão para o navegador: **nunca** coloque segredo nelas.
- O CI procura segredos versionados (gitleaks) em todo o histórico.
