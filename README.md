# Fila — fila, atendimento, pagamento e fidelidade para barbearias

**Versão 1.0.0 — em produção.** O cliente entra na fila pelo link ou QR Code da loja (com conta ou só com um apelido), acompanha a vez em tempo real e paga em dinheiro, cartão ou Pix. A equipe chama, atende, registra o que fez e recebe; o dono cuida das filas, do catálogo, das configurações, das pendências financeiras e vê os indicadores no programa **Fila Gestão**.

## 📘 Guia prático (comece por aqui)

| | |
|---|---|
| **[⬇ Baixar o Guia prático em PDF](https://github.com/Ruan-nascimento/Fila-/raw/main/docs/Guia-Pratico-Fila-1.0.0.pdf)** | Todas as funções, passo a passo, para cliente, profissional, dono e operador (15 páginas, para imprimir ou mandar por mensagem) |
| [Ler o guia no GitHub](docs/guia-pratico.md) | O mesmo conteúdo, navegável |
| [Guia de uma página por papel](docs/guia-por-papel.md) | Resumo para colar no balcão |
| [⬇ Baixar o programa Fila Gestão](https://github.com/Ruan-nascimento/Fila-/releases/latest) | Windows (`Fila-Gestao_1.0.0_x64-setup.exe`) e Linux Mint/Ubuntu (`Fila-Gestao_1.0.0_amd64.deb`) |

## O que a versão 1.0.0 faz

| Para quem | Funções |
|---|---|
| **Cliente** | Painel com busca de barbearias (mais perto, abertas agora, A–Z) e as filas em que está · entrar **sem conta** (apelido + código de recuperação), com conta ou com Google · escolher serviços com **quantidade**, buscar por nome e categoria, adicionar **produtos** · acompanhar senha, posição e espera em tempo real · alterar o pedido, pedir alteração depois de chamado, **Vou me atrasar**, sair da fila · pagar em dinheiro, cartão ou **Pix** · **pontos e prêmios** · histórico · notificações · Minha conta e exclusão com protocolo |
| **Profissional** | A própria fila: chamar, iniciar, marcar ausência, **subir/descer** na fila · comanda por unidade (**+1 realizado**, **Todos realizados**, **Entregar** produto) · concluir ou interromper · aceitar/recusar pedidos de mudança · fechar/reabrir a entrada da própria fila · receber (dinheiro com troco, débito/crédito, Pix com conferência) · entregar prêmios · avisar indisponibilidade |
| **Dono** | Tudo do profissional em todas as filas · abrir, pausar e encerrar a operação · **limite por fila** e entrada aberta/fechada · transferir e cancelar com motivo · **Pendências** · comandas (revisar interrupção, corrigir valor) · **estornos** · **Catálogo** (serviços, produtos, categorias) · profissionais e convites · **Configurações da loja** por seção (identidade, logo, cor, QR Code, visitantes, tolerância, meios de pagamento e **custos de recebimento**, permissões) · prêmios · exclusões · auditoria · sessão única de dono |
| **Programa Fila Gestão** | A mesma gestão no computador + **Dashboard analítico** (só em computador autorizado): atendimentos, unidades, produtos, espera, recebido e recebido após custos · bandeja do sistema, **segundo plano** e avisos nativos |
| **Operador** | `fila-admin` (loja, dono, códigos do desktop, verificação, manutenção) · workflows **Publicar**, **Backup** e **Fila Gestão (instaladores)** · monitoramento e restauração ([docs/operacao/](docs/operacao/README.md)) |

Em produção: site e proxy na **Vercel**, API e worker em Go no **Render**, PostgreSQL 18 no **Neon**, fotos e backups no **Cloudflare R2**, e-mails pelo **Resend**, login de cliente pelo **Google**, Pix pela **AbacatePay** (modo teste validado; cobrança real depende do checklist em [docs/pagamentos-abacatepay.md](docs/pagamentos-abacatepay.md)). Detalhes em [docs/operacao/publicacao-v1.md](docs/operacao/publicacao-v1.md).

## Onde está cada coisa

| Pasta | O que tem |
|---|---|
| [backend/](backend/) | Servidor em Go: `cmd/api` (HTTP), `cmd/worker` (tarefas em segundo plano), `cmd/migrate` (migrações), módulos de domínio em `internal/` |
| [frontend/](frontend/) | Aplicação web React + TypeScript + Vite |
| [desktop-dono/](desktop-dono/README.md) | **Fila Gestão**: programa instalável do dono (Tauri 2, Windows e Linux Mint) que abre a gestão do site em `/dono` e o **Dashboard analítico** exclusivo ([guia](docs/desktop/guia-do-dono.md), [métricas](docs/desktop/metricas.md)) |
| [docs/](docs/README.md) | **Guia prático** (e PDF), regras, contrato OpenAPI, matrizes, cenários, decisões (ADRs) e operação |
| [compose.yaml](compose.yaml) | PostgreSQL local via Docker (desenvolvimento) |
| [deploy/](deploy/) | Homologação e produção: imagens, compose com Caddy (TLS), papéis do banco, backup/restauração, alertas ([docs/operacao/](docs/operacao/README.md)) |
| [.github/workflows/](.github/workflows/) | CI, Publicar (migrações + deploy), Backup e instaladores do Fila Gestão |

---

# Para desenvolvedores: rodar o projeto no seu computador

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

O worker também **conclui as exclusões de conta** (quando não há pendência) e remove nicks antigos de visitantes, e **publica as atualizações em tempo real**: sem ele rodando, as telas não se atualizam sozinhas (os comandos continuam funcionando e nada se perde — ao ligar o worker, tudo é publicado). Para ver se há atraso: `go -C backend run ./cmd/fila-admin eventos`.

**Pix (opcional).** Sem configurar nada, o Pix fica desligado e a equipe recebe em dinheiro ou cartão. Para testar o Pix da AbacatePay, coloque no `.env` uma chave de **teste** (`abc_dev_...`, criada no painel da AbacatePay), que nunca cobra de verdade:

```
FILA_PIX_PROVIDER=abacatepay
FILA_ABACATEPAY_API_KEY=abc_dev_sua-chave-de-teste
```

A API e o worker leem a mesma configuração: o worker é quem consulta a AbacatePay para saber se o Pix foi pago e quem processa os estornos.

**Fotos, logo e Google (opcional).** Sem configurar nada, as telas mostram iniciais e o login é só por e-mail/senha (e visitante). Para testar fotos, coloque `FILA_MEDIA_STORAGE=filesystem` no `.env` (as imagens vão para `backend/.midia/`, fora do Git) — **o worker precisa estar rodando**, é ele quem prepara as fotos. Para o Google, siga o passo a passo em [docs/configuracoes-e-identidade.md](docs/configuracoes-e-identidade.md#43-configurar-no-google-cloud-passo-a-passo) e preencha `FILA_GOOGLE_CLIENT_ID` e `FILA_GOOGLE_CLIENT_SECRET`. Cores, informações públicas e QR Code funcionam sem nada disso.

Opcional: para receber os avisos da AbacatePay (webhook), cadastre no painel deles a URL pública **https** da sua API terminando em `/v1/webhooks/abacatepay`, com um secret de 32+ caracteres, e coloque o mesmo valor em `FILA_ABACATEPAY_WEBHOOK_SECRET`. Sem isso o sistema funciona do mesmo jeito, só que confirma os Pix pela consulta do worker (alguns segundos depois). Detalhes, limites e o checklist antes de cobrar de verdade estão em [docs/pagamentos-abacatepay.md](docs/pagamentos-abacatepay.md).

**Pontos e prêmios.** O programa começa desligado. O dono liga em Gestão → Prêmios, define os pontos de cada serviço em Gestão → Catálogo e cadastra os prêmios (cortesias). O worker vence as reservas de prêmio depois de 24 h e devolve os pontos.

**E-mails em desenvolvimento.** Sem `FILA_MAIL_MODE=resend` (usado em produção), cada e-mail (verificação de conta, redefinição de senha, convite) vira um arquivo `.txt` na pasta `backend\.correio\`. Abra o arquivo e copie o link para o navegador. Essa pasta não vai para o Git.

### 5.2 Criar a loja e o dono inicial (uma vez)

O cadastro pelo site nunca dá acesso de dono. A loja e o convite do dono são criados por este comando (com a API parada ou rodando, tanto faz):

```powershell
go -C backend run ./cmd/fila-admin bootstrap -nome "Barbearia Teste" -slug barbearia-teste -email seu-email@exemplo.com
```

Com o worker rodando, o convite aparece em `backend\.correio\`. Abra o link, defina nome e senha, e entre escolhendo **Equipe**. O convite vale 24 horas e só funciona uma vez.

### 5.3 Trocar o dono ou encerrar a loja (processo controlado)

Uma loja nunca fica sem dono: se você for a única pessoa dona, a exclusão da sua conta é recusada (`sole_owner`) até outra pessoa assumir ou a loja ser encerrada. Quem roda estes comandos é o operador, depois de confirmar o pedido com o dono fora do sistema:

```powershell
# convida um novo dono (ele recebe o e-mail e aceita); depois o dono antigo pode excluir a conta
go -C backend run ./cmd/fila-admin transfer-owner -slug barbearia-teste -email novo-dono@exemplo.com -motivo "Ana vendeu a loja"
# encerra a loja (só sem ninguém na fila e sem comanda pendente)
go -C backend run ./cmd/fila-admin close-establishment -slug barbearia-teste -motivo "loja fechou"
```

O que o sistema guarda e por quanto tempo está em [docs/retencao.md](docs/retencao.md) (prazos legais ainda precisam de validação antes de produção).

## 6. Rodar o frontend

```powershell
cd frontend
npm ci
npm run dev
```

Abra http://127.0.0.1:5173. Se a API (passo 5) não estiver rodando ou o banco não estiver migrado, a página inicial mostra um aviso; com tudo pronto, ela só explica como entrar pela loja.
O Vite repassa `/v1` e `/healthz` para a API, então frontend e API ficam na mesma origem, como em produção.

Telas (o passo a passo de cada uma está no [guia prático](docs/guia-pratico.md)):

| Endereço | Para quê |
|---|---|
| `/cadastro` | Criar conta de cliente (o link de confirmação chega por e-mail — em desenvolvimento, na pasta `backend\.correio\`) |
| `/entrar` | Entrar como **Cliente** ou como **Equipe da barbearia**. Cliente também tem **Continuar com o Google** (quando configurado) e **Entrar sem conta**; com `?loja=slug` mostra nome, logo e cor da loja e volta para ela |
| `/conta` | **Minha conta**: nome, foto pessoal, aparência (claro/escuro/sistema), e-mail (só leitura), como entra (senha e Google), perfil profissional na barbearia (para quem atende), sair e excluir ([guia](docs/configuracoes-e-identidade.md)) |
| `/esqueci-senha` | Pedir link para criar senha nova (vale 30 minutos) |
| `/` | **Painel do cliente**: as filas em que você está em qualquer barbearia (conta ou visitante deste navegador) e a busca de barbearias — mais perto primeiro (com a localização permitida), abertas agora ou A–Z, com páginas |
| `/loja/barbearia-teste` | Página pública da loja (use o `-slug` do passo 5.2): serviços com preço, profissionais e situação das filas; entrar sem conta com um apelido (e guardar o **código de recuperação**) ou com a conta, **entrar na fila** e acompanhar senha, posição e espera estimada; sair da fila; ver a **comanda item a item**, trocar serviços enquanto aguarda, pedir alteração depois de chamado e ver o valor a pagar. Com conta, as abas `/loja/…/historico` e `/loja/…/pontos` mostram histórico e fidelidade da loja |
| `/gestao` | Área da equipe (entre como **Equipe**). Dono: filas (abrir, pausar, fechar; chamar o próximo, iniciar, marcar ausência, cancelar com motivo e transferir com a concordância do cliente), profissionais (cadastrar, convidar, renomear, desativar), serviços (preço, duração, pontos, quem atende) e dados da loja. Profissional: vê a própria fila, chama o próximo, inicia, marca ausência, avisa indisponibilidade e consulta os serviços. Na fila, cada pessoa tem a **comanda**: aprovar/recusar pedido do cliente, marcar serviço realizado, alterar serviços (com motivo), **concluir** ou **interromper**. **Comandas**: lista e detalhe (`/gestao/…/comandas/:id`, filtro em `?mostrar=`); o dono revisa interrupções e corrige valor fechado (pede a senha de novo). No computador a navegação é lateral, agrupada em Operação, Cadastros e Administração; no celular e no tablet, Filas · Comandas · Mais |
| `/gestao/…/loja` (dono) | **Configurações da barbearia**: nome, logo, cor (16 paletas), descrição, endereço e telefone públicos, link e **QR Code** (abrir a loja, entrar sem conta, entrar com conta; copiar, PNG, SVG, imprimir) |
| `/loja/…?entrada=visitante` | Destino do QR "entrar sem conta": abre a identificação por apelido (nada é criado até confirmar) |
| `/dono/painel-analitico` | Atalho do **Dashboard**: dentro do programa é interceptado e abre os gráficos locais; no navegador comum só mostra um aviso (nenhum dado analítico) |
| Sessão única do dono | Uma conta dona tem **uma** sessão de dono por vez (navegador, programa, outro PC). Ao entrar em outro lugar: “Continuar neste aparelho e encerrar a outra sessão” ou “Cancelar”. A sessão encerrada mostra “Sua sessão foi encerrada porque você entrou em outro aparelho” ([ADR-0027](docs/adr/0027-sessao-unica-do-dono-e-dashboard-desktop.md)) |
| `/dono` | Entrada do dono, aberta pelo programa **Fila Gestão** ([desktop-dono/](desktop-dono/README.md)): o mesmo login (só **Equipe**) e a mesma gestão, oferecendo só as lojas em que a conta é **dona**; conta só profissional ou de cliente vê o motivo e **Trocar de conta** |
| `/conta/excluir` (link "Excluir conta" em `/conta`) | Mostra os efeitos, pede a senha de novo e dá um **protocolo** (também por e-mail). Atendimento em andamento ou pagamento pendente ficam com a loja: a exclusão aparece "em processamento" até resolver |
| `/exclusao` | Consultar a situação da exclusão pelo protocolo |
| `/gestao` → **Exclusões** (dono) | Exclusões que dependem da loja e o que falta resolver; destaque perto da meta operacional de 30 dias |
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

Testes em **navegador real** (Chrome e Edge instalados, contra a API real, nas larguras de celular a desktop): veja [docs/web-validacao.md](docs/web-validacao.md) — inclui quais navegadores foram e quais **não** foram testados.

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

## 10. Colocar no ar (homologação e produção)

Servidor Linux com Docker, domínio apontando para ele e portas 80/443 abertas. Passo a passo completo, com segredos, banco com menor privilégio, backup, restauração, alertas e rotinas de incidente: [docs/operacao/](docs/operacao/README.md).

```bash
deploy/implantar.sh /etc/fila/producao.env $(git rev-parse --short HEAD)   # backup, migrações, troca sem perder fila
docker compose -f deploy/compose.yaml --env-file /etc/fila/producao.env --profile ferramentas run --rm fila-admin verificar
```

Produção **não sobe** com Pix simulado (chave de teste ou endereço que não seja o oficial da AbacatePay): o processo sai com erro de configuração.

## 11. Regras de segurança do repositório

- Segredos só em `.env` (local) ou nas variáveis da plataforma (produção). Em produção o `.env` é ignorado.
- Os logs ocultam automaticamente campos como senha, token, cookie e URL do banco — mesmo assim, não registre dados pessoais nem segredos.
- Variáveis com prefixo `VITE_` vão para o navegador: **nunca** coloque segredo nelas.
- O CI procura segredos versionados (gitleaks) em todo o histórico.
