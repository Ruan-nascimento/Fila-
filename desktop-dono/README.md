# Fila Gestão — programa do dono (Tauri 2)

Programa instalável que abre a **gestão do site** direto no acesso do dono. Não há uma segunda gestão: a janela carrega o frontend hospedado (mesmo login, mesmas telas, mesma API e o mesmo tempo real). Desde a versão 0.2.0, o programa traz também o **Dashboard analítico**, uma página local exclusiva deste programa num computador autorizado (ADR-0027).

Passo a passo para o dono e o operador: [docs/desktop/guia-do-dono.md](../docs/desktop/guia-do-dono.md). Dicionário das métricas: [docs/desktop/metricas.md](../docs/desktop/metricas.md).

| Pasta / arquivo | O que tem |
|---|---|
| `painel.url` | Origem HTTPS do painel em produção (uma linha, sem caminho) |
| `carregador/` | Tela local: confere se o painel responde e abre `/dono`; sem resposta, mostra o motivo e **Tentar novamente** |
| `dashboard/` | Dashboard local (React + Vite, tokens e componentes do `frontend/`); `npm run build:ui` gera `dist/` com o carregador |
| `src-tauri/` | Projeto Tauri: janela, trava de origem, ícones, configuração de pacote (`tauri.conf.json`) |
| `src-tauri/src/chave.rs` | Chave P-256 da instalação (cofre do sistema) e prova DPoP (RFC 9449, ES256) |
| `src-tauri/src/api.rs` | Pedidos nativos **tipados** do Dashboard (só o painel fixado; sem pedido livre) e fluxo de invalidação |
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
- As páginas do painel **não** têm acesso a nenhum comando do programa: só a tela local tem, conforme `src-tauri/capabilities/carregador.json`. Única exceção: as notificações (abaixo).

**Notificações.** A gestão avisa quando alguém entra na fila, sai, avisa que vai atrasar ou pede mudança na comanda ([docs/notificacoes.md](../docs/notificacoes.md)). No programa, o `tauri-plugin-notification` troca a notificação do navegador pela nativa do Windows/Linux. A página remota recebe, por uma capability criada em `src/lib.rs` (`painel-notificacoes`), **só** as permissões `notification:allow-is-permission-granted`, `allow-request-permission` e `allow-notify`, **só** para a origem do painel e **só** na janela principal. Com o programa em primeiro plano, o aviso aparece dentro da tela; minimizado ou atrás de outra janela, vira notificação do sistema.

**Segundo plano (ADR-0037).** Preferência do computador, desligada por padrão, no menu da bandeja (`src/segundo_plano.rs`). Ligada, o X esconde a janela e a página continua recebendo o fluxo de avisos; *Sair do Fila* encerra o processo. Sem bandeja funcional, fechar sempre encerra. Instância única (`tauri-plugin-single-instance`): abrir de novo mostra a janela existente. O som é gravado na página por `webview.eval` (a página remota continua sem acesso a comando nenhum). Teste no Linux: `testes/segundo-plano.sh` (X aninhado e D-Bus isolado).

## Dashboard: como a mesma sessão chega aos gráficos

1. Na gestão (página remota, cookie HttpOnly da sessão de dono), o item **Dashboard** pede ao servidor um ticket de 60 s e navega para `/dono/painel-analitico#t=…`.
2. O componente nativo intercepta essa navegação: guarda o ticket na memória, cancela a navegação e abre `dashboard.html` (local).
3. O nativo troca o ticket por um acesso preso à chave da instalação (`POST /v1/desktop/bindings` com prova DPoP). O acesso vale enquanto a sessão de dono da gestão for a ativa.
4. Cada consulta leva o token e uma prova nova. O servidor confere sessão, vaga de dono, instalação, prova e vínculo com a loja.

O item **Dashboard** só aparece dentro do programa: o programa injeta `window.__GESTAO_DESKTOP__`, uma dica de apresentação. No navegador comum, `/dono/painel-analitico` mostra só um aviso e não consulta nada. Nenhum cookie é copiado. As páginas remotas continuam sem acesso a comando nenhum. Os dados analíticos ficam só em memória e são apagados ao sair, ao perder a sessão ou ao trocar de loja.

## 1. Desenvolvimento

Na máquina de desenvolvimento: Node 24.15+ ou 26, Rust (stable) e, no Linux, as bibliotecas da seção 4. Com a API, o worker e o Vite do frontend rodando (README da raiz, passos 5 e 6):

```bash
cd frontend && npm ci && cd ..   # o Dashboard usa estilos e componentes do frontend
cd desktop-dono
npm ci
npm run dev          # gera o Dashboard (build:ui) e abre o programa
```

Para ativar o computador em desenvolvimento, na raiz do repositório: `go -C backend run ./cmd/fila-admin desktop emitir-codigo -email dono@exemplo.com -nome "PC de teste"`. No Linux, o cofre usado é o chaveiro da sessão (GNOME Keyring/KWallet), que precisa estar ativo.

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

Arquivo produzido: `desktop-dono\src-tauri\target\release\bundle\nsis\Fila Gestão_0.2.0_x64-setup.exe`

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

Arquivo produzido: `desktop-dono/src-tauri/target/release/bundle/deb/Fila Gestao_0.2.0_amd64.deb` (sem acento no nome do arquivo e do pacote `fila-gestao`, exigência do Debian; no menu aparece "Fila Gestão"). Com `APPIMAGE=1`, sai também um `.AppImage` em `bundle/appimage/`.

### No computador do dono

```bash
sudo apt install ./"Fila Gestao_0.2.0_amd64.deb"
```

Também dá para abrir o `.deb` com dois cliques (Instalador de pacotes). O programa aparece no menu como **Fila Gestão**. As dependências (`libwebkit2gtk-4.1-0`, `libgtk-3-0`) vêm do próprio Mint. Para remover: `sudo apt remove fila-gestao`.

## 5. Testes

```bash
cd desktop-dono/src-tauri && cargo test      # trava de origem, atalho do Dashboard, prova ES256, caminhos tipados
cd desktop-dono && npm run test:ui           # Dashboard (jsdom): indicadores, tabela = gráfico, loja trocada, sessão encerrada
```

O frontend tem testes de componente (`frontend/src/pages/dono/*.test.tsx`) e de navegador real (`frontend/e2e/dono.spec.ts`, no mesmo ambiente de `docs/web-validacao.md`).

A janela real é testada por `testes/janela.mjs`, só no Linux: o WebDriver do Tauri não existe para o WebView2 no Windows sem o Edge Driver.

1. Suba um ambiente de **teste**: API, worker, Vite e e-mails em pasta.
2. Gere um pacote com `FILA_PAINEL_TESTE_LOCAL=1` apontando para esse ambiente.
3. Extraia o pacote (`dpkg -x pacote.deb pasta`).
4. Rode `tauri-driver --native-driver /usr/bin/WebKitWebDriver`, que vem no pacote `webkit2gtk-driver`.
5. Rode o teste:

```bash
FG_APP=pasta/usr/bin/fila-gestao FG_BASE=http://127.0.0.1:15173 FG_ADMIN=…/fila-admin \
FG_ADMIN_CWD=pasta-do-.env-de-teste FG_MAIL_DIR=pasta-dos-emails node testes/janela.mjs completo   # ou: dashboard
```

O cenário `dashboard` usa o cofre de chaves real da sessão e, na primeira execução, ativa o computador. Para repetir do zero, apague a chave (botão **Ativar de novo** no programa) e o arquivo `~/.local/share/br.fila.gestao/cookies` **do ambiente de teste**.

## Validação registrada (18/11/2026, versão 0.2.0)

| O que | Plataforma | Resultado |
|---|---|---|
| `cargo test` (origem, atalho do Dashboard, prova ES256 verificável, caminhos tipados e recusa de injeção) | Linux | 4/4 |
| `npm run test:ui` (Dashboard) | Linux, jsdom | 4/4 |
| `testes/janela.mjs completo` no `.deb` extraído | Linux, WebKitGTK 2.52 (janela Tauri real) | 13/13 |
| `testes/janela.mjs dashboard` no `.deb` extraído: ativação, mesma sessão, R$ conferido, abas, navegador recusado no analítico, troca web ↔ desktop com limpeza, alternância Dashboard/Filas | Linux, WebKitGTK 2.52, cofre Secret Service da sessão | 11/11 |
| Instalador Windows (NSIS) e cofre do Windows | **não executado**: não havia Windows disponível; use a seção 3 ou o workflow | — |
| `sudo apt install` do `.deb` | **não executado** (sem sudo na máquina de teste); o pacote foi extraído e executado | — |

## Limitações conhecidas

- Se a rede cair **depois** que o painel abriu, o próprio site mostra os avisos de conexão e o "Reconectando…" do tempo real. A tela local só aparece ao abrir o programa ou quando a janela para fora da origem.
- Sem atualização automática do programa. Isso só importa para mudanças na casca: as telas vêm do site e se atualizam a cada implantação do frontend.
- Sem assinatura de código: há aviso do SmartScreen no Windows.
- O cofre de chaves no Linux exige o chaveiro da sessão (Secret Service). Sem ele, o Dashboard explica o erro e a gestão continua funcionando.
- O Dashboard é recarregado com o programa: mudanças na interface dos gráficos exigem um instalador novo.
- Notificações só chegam com o programa aberto (pode estar minimizado). Para receber as notificações, gere um instalador novo (versão com o plugin).
