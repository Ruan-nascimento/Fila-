# Fila Gestão — programa do dono (Tauri 2)

Programa instalável que abre a **gestão do site** direto no acesso do dono. Não há uma segunda interface: a janela carrega o frontend hospedado (mesmo login, mesmas telas, mesma API e o mesmo tempo real).

| Pasta / arquivo | O que tem |
|---|---|
| `painel.url` | Origem HTTPS do painel em produção (uma linha, sem caminho) |
| `carregador/` | Tela local: confere se o painel responde e abre `/dono`; sem resposta, mostra o motivo e **Tentar novamente** |
| `src-tauri/` | Projeto Tauri: janela, trava de origem, ícones, configuração de pacote (`tauri.conf.json`) |
| `icone/icone.svg` | Fonte do ícone (`npm run icone` regenera `src-tauri/icons/`) |
| `scripts/` | Gerar o instalador do Windows e o pacote do Linux |
| `testes/janela.mjs` | Teste da janela real (Linux: tauri-driver + WebKitWebDriver) |

## Como funciona

1. A janela abre a tela local (`carregador/`), que pergunta ao servidor se ele está no ar (`/healthz/live`).
2. Respondendo, a janela vai para `https://SEU-PAINEL/dono`, a rota do frontend que serve de entrada do dono:
   - sem sessão: o login de sempre, já fixo em **Equipe da barbearia**, sem cadastro de cliente;
   - sessão de equipe **com vínculo ativo de dono**: com uma loja, vai direto para `/gestao/<loja>/filas`; com várias, lista **só** as lojas em que a conta é dona;
   - sessão de equipe sem vínculo de dono (só profissional) ou sessão de cliente: mensagem clara e **Trocar de conta**.
3. Daí em diante são as telas de gestão do site. Enquanto a janela existir, ela fica em **modo dono**: o endereço direto (ou a troca de loja) de uma loja em que a conta é só profissional mostra "Nesta janela só aparecem lojas em que você é dono"; sair da conta volta para `/dono`.

O que decide a permissão continua sendo **o servidor**: cada pedido é autorizado pelo vínculo ativo e pela sessão de equipe, e as ações de dono exigem o papel de dono. O modo dono só restringe o que a janela oferece. Ele não concede nada.

**Cookies, CSRF e SSE** seguem o fluxo da web, porque a página **está** na origem do painel: os cookies são os do site, o cabeçalho `Origin` é o do site (aceito por `FILA_ALLOWED_ORIGINS`) e o `EventSource` é o mesmo. Nada muda no servidor.

**Segurança da janela.**
- Em produção, o programa só abre a origem HTTPS fixada na compilação. Qualquer navegação para outra origem é bloqueada: links `https`, `mailto` e `tel` vão para o navegador padrão, e o resto é ignorado.
- Se a janela parar fora da origem (por exemplo, numa página de erro do WebView), ela volta à tela local.
- As páginas do painel **não** têm acesso a nenhum comando do programa: só a tela local tem, conforme `src-tauri/capabilities/carregador.json`.

## 1. Desenvolvimento

Na máquina de desenvolvimento: Node 24.15+ ou 26, Rust (stable) e, no Linux, as bibliotecas da seção 4. Com a API, o worker e o Vite do frontend rodando (README da raiz, passos 5 e 6):

```bash
cd desktop-dono
npm ci
npm run dev
```

Em desenvolvimento a janela abre `http://127.0.0.1:5173/dono` (o Vite). O título mostra `Fila Gestão (desenvolvimento: …)`. Para usar outra origem em desenvolvimento: `FILA_PAINEL_URL=https://homologacao.exemplo npm run dev`.

## 2. Configurar a URL real do painel

Edite `painel.url` e deixe **uma** linha sem `#` com a origem do painel, por exemplo:

```
https://fila.suabarbearia.com.br
```

Outra opção é a variável `FILA_PAINEL_URL`, que tem prioridade sobre o arquivo. A URL fica gravada no programa na compilação. O build de produção **recusa**:
- `http://`;
- endereço com caminho, `?` ou `#`;
- usuário ou senha na URL;
- o domínio de exemplo;
- a falta de URL.

A origem precisa estar em `FILA_ALLOWED_ORIGINS` no servidor, o que já vale para o site.

> **Teste local do pacote:** `FILA_PAINEL_TESTE_LOCAL=1` com `FILA_PAINEL_URL=http://127.0.0.1:PORTA` permite um pacote de release apontando para um ambiente local. O título da janela mostra "teste-local". **Nunca distribua esse pacote.**

## 3. Instalador para Windows (prioridade)

### Máquina de build Windows (uma vez)

- **Microsoft C++ Build Tools**, com a carga "Desenvolvimento para desktop com C++": https://visualstudio.microsoft.com/visual-cpp-build-tools/
- **Rust**: https://rustup.rs (escolha o padrão, `x86_64-pc-windows-msvc`).
- **Node.js** 26 LTS: https://nodejs.org/
- O NSIS é baixado sozinho pelo Tauri no primeiro build.

### Gerar

```powershell
cd desktop-dono
powershell -ExecutionPolicy Bypass -File scripts\gerar-instalador-windows.ps1 -PainelUrl https://fila.suabarbearia.com.br
```

Arquivo produzido: `desktop-dono\src-tauri\target\release\bundle\nsis\Fila Gestão_0.1.0_x64-setup.exe`

Sem máquina Windows, há uma alternativa no GitHub: **Actions → "Fila Gestão (instaladores)" → Run workflow**, informando a URL. O `.exe` e o `.deb` ficam em *Artifacts* da execução. Workflows manuais só aparecem depois que o arquivo `.github/workflows/desktop-dono.yml` está no branch padrão.

### No computador do dono

O dono só precisa do instalador: nada de Node, Rust, Go ou Docker.
- Dê dois cliques no `.exe`.
- A instalação é **só para o usuário**: não pede administrador e vai para `%LOCALAPPDATA%\Fila Gestão`.
- O programa aparece no menu Iniciar como **Fila Gestão**.
- O WebView2 já vem no Windows 10 e 11. Se faltar, o instalador o baixa (precisa de internet, como o próprio painel).
- Sem assinatura de código, o Windows SmartScreen mostra "O Windows protegeu o computador". Escolha *Mais informações → Executar assim mesmo*. Para eliminar esse aviso, assine o instalador com um certificado de assinatura de código (fora do escopo deste MVP).

## 4. Pacote para Linux Mint (22.x, base Ubuntu 24.04)

### Máquina de build Linux (uma vez)

```bash
sudo apt install libwebkit2gtk-4.1-dev libgtk-3-dev librsvg2-dev build-essential curl
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
# Node 26: https://nodejs.org/
```

### Gerar

```bash
cd desktop-dono
scripts/gerar-pacote-linux.sh https://fila.suabarbearia.com.br
```

Arquivo produzido: `desktop-dono/src-tauri/target/release/bundle/deb/Fila Gestao_0.1.0_amd64.deb` (sem acento no nome do arquivo e do pacote `fila-gestao`, exigência do Debian; no menu aparece "Fila Gestão"). Com `APPIMAGE=1`, sai também um `.AppImage` em `bundle/appimage/`.

### No computador do dono

```bash
sudo apt install ./"Fila Gestao_0.1.0_amd64.deb"
```

Também dá para abrir o `.deb` com dois cliques (Instalador de pacotes). O programa aparece no menu como **Fila Gestão**. As dependências (`libwebkit2gtk-4.1-0`, `libgtk-3-0`) vêm do próprio Mint. Para remover: `sudo apt remove fila-gestao`.

## 5. Testes

```bash
cd desktop-dono/src-tauri && cargo test      # trava de origem
```

O frontend tem testes de componente (`frontend/src/pages/dono/dono.test.tsx`) e de navegador real (`frontend/e2e/dono.spec.ts`, mesmo ambiente de `docs/web-validacao.md`).

A janela real é testada por `testes/janela.mjs`, só no Linux: o WebDriver do Tauri não existe para o WebView2 no Windows sem o Edge Driver.

1. Suba um ambiente de **teste**: API, worker, Vite, e-mails em pasta.
2. Gere um pacote com `FILA_PAINEL_TESTE_LOCAL=1` apontando para esse ambiente.
3. Extraia o pacote (`dpkg -x pacote.deb pasta`).
4. Rode `tauri-driver --native-driver /usr/bin/WebKitWebDriver`, que vem no pacote `webkit2gtk-driver`.
5. Rode o teste:

```bash
FG_APP=pasta/usr/bin/fila-gestao FG_BASE=http://127.0.0.1:15173 FG_ADMIN=…/fila-admin \
FG_ADMIN_CWD=pasta-do-.env-de-teste FG_MAIL_DIR=pasta-dos-emails node testes/janela.mjs completo
```

## Validação registrada (18/11/2026)

| O que | Plataforma | Resultado |
|---|---|---|
| Testes de componente do frontend, incluindo os 10 de `/dono` | Linux (Ubuntu 24.04, jsdom) | 91/91 |
| `frontend/e2e/dono.spec.ts`: login do dono, recusa de conta profissional, só a loja de dono, URL direta, tempo real, navegador comum inalterado | Linux, Google Chrome, API real | 3/3 |
| `testes/janela.mjs completo` no **pacote `.deb` extraído** (`dpkg -x`): as 13 verificações | Linux, WebKitGTK 2.50 (janela Tauri real) | 13/13 |
| `testes/janela.mjs falha`: painel fora do ar, tela local e "Tentar novamente" | Linux, WebKitGTK | 3/3 |
| Binário de produção (HTTPS, rustls) contra uma origem HTTPS sem `/healthz` | Linux | erro tratado na tela local |
| Recusas do build de produção: sem URL, `http`, domínio de exemplo, com caminho, com usuário | Linux | 5/5 recusadas |
| Instalador Windows (NSIS) | **não executado**: não havia Windows disponível; use a seção 3 ou o workflow | — |
| `sudo apt install` do `.deb` | **não executado** (sem sudo na máquina de teste); o pacote foi extraído e executado | — |

## Limitações conhecidas

- Se a rede cair **depois** que o painel abriu, o próprio site mostra os avisos de conexão e o "Reconectando…" do tempo real. A tela local só aparece ao abrir o programa ou quando a janela para fora da origem.
- Sem atualização automática do programa. Isso só importa para mudanças na casca: as telas vêm do site e se atualizam a cada implantação do frontend.
- Sem assinatura de código: há aviso do SmartScreen no Windows.
