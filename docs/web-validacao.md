# Validação da web (etapa 11)

Como as telas foram verificadas, em quais navegadores, e o que **não** foi verificado. Os testes de navegador ficam em `frontend/e2e/` (Playwright) e rodam contra a **API real** (com worker, banco, SSE e o simulador local de Pix). Não são simulações da API.

## 1. Navegadores

| Navegador | Versão usada | Como | Situação |
|---|---|---|---|
| Google Chrome (Windows) | 157 | instalado na máquina, `channel: chrome` | testado |
| Microsoft Edge (Windows) | 157 | instalado na máquina, `channel: msedge` | testado |
| Firefox | — | não instalado | **não testado** |
| Safari (macOS/iOS) | — | sem Mac/iPhone disponível | **não testado** |
| Chrome Android / Samsung Internet | — | sem aparelho físico | **não testado** |

As larguras de celular (360 e 390 px) foram **emuladas** no navegador de computador (tamanho da janela e toque). Emulação **não prova** compatibilidade com aparelho físico: teclado virtual, área segura (notch), rolagem com inércia e economia de bateria do iOS/Android não foram exercitados. Antes do piloto, testar em pelo menos um Android e um iPhone reais.

## 2. O que é verificado em cada tela

Em cada tela, a verificação acontece nas larguras 360, 390, 768, 1024 e 1440 px e no equivalente a **zoom de 200%** numa tela de 1280 px (640 px de largura útil). As telas:

- **públicas:** início, entrar, criar conta, esqueci a senha, consultar exclusão, loja sem identificação e página inexistente;
- **cliente:** loja com visita, pontos, prêmios e histórico; minha conta;
- **dona:** todas as abas da gestão;
- **profissional:** filas, comandas e prêmios.

Cada verificação confere:

- **sem rolagem horizontal da página** (`scrollWidth ≤ largura da janela`), apontando o elemento que vazou;
- **alvos de toque de pelo menos 44×44 px** em botões, campos, listas e links do menu e das abas. Caixas de marcar e de opção contam pela área do rótulo, que é clicável;
- **acessibilidade automática (axe-core, WCAG 2.1 A e AA)**: contraste, rótulos, nomes de botões, estrutura, ARIA;
- **foto da página inteira** (`frontend/e2e-resultados/telas/<navegador>/`) para revisão humana.

Há ainda um teste **só com teclado**: o link "Pular para o conteúdo", o login inteiro sem mouse, e o foco sempre visível (contorno de pelo menos 2 px).

## 3. Jornadas ponta a ponta (navegador real + API real)

1. **Visitante no celular e Beto no computador.**
   - O visitante abre o link da loja, informa o apelido, recebe o código de recuperação e entra na fila.
   - Beto chama, inicia e conclui; a tela do visitante muda sozinha (SSE).
   - O visitante escolhe dinheiro. Beto recebe com troco e a tela mostra "Pago".
   - Nada aparece como pago antes do registro da equipe.
2. **Cliente no celular.**
   - Cria a conta, confirma pelo link do e-mail e entra.
   - Na fila, escolhe Pix.
   - **A rede cai** duas vezes: primeiro o aparelho fica sem internet; depois a resposta se perde **depois** de o servidor criar o Pix.
   - Toda tentativa usa a **mesma chave de idempotência**, e existe **um único Pix**. O QR aparece pela repetição ou pela atualização em tempo real, e o erro antigo some.
   - O SSE mostra "Reconectando…" enquanto não há rede e volta sozinho.
   - O banco (simulador) confirma e a tela muda para "Pagamento recebido pelo banco". Nenhum clique do cliente confirma pagamento.
   - A equipe confere e a tela fica "Pago". Os pontos aparecem, ele reserva o prêmio, Beto entrega, e o extrato mostra a entrega.
3. **Conta.**
   - Esqueci a senha → link do e-mail → senha nova → entrar.
   - A sessão é encerrada em outro aparelho (troca de senha): a próxima ação **não é aplicada**, aparece "Sua sessão terminou…" no topo e um link para entrar de novo.
   - Editar o nome.
   - Excluir a conta com senha: protocolo mostrado uma vez e consultado depois ("em processamento" até o worker concluir).
4. **Dona no tablet.**
   - Convida um profissional; a tela pede a senha de novo e repete a mesma ação.
   - Cancela o convite.
   - Vê pendências e auditoria.
   - O profissional não vê as abas da dona, nem pelo endereço direto.

## 4. Segredos fora do bundle

O CI e a verificação local procuram no `dist/` do build:

- nomes de configuração do servidor (`FILA_`, `POSTGRES_`, `DATABASE_URL`);
- dados do provedor: chave `abc_dev_`/`abc_prod_`, secret e cabeçalho de assinatura do webhook, endereço da API da AbacatePay e a chave de assinatura.

Nenhum foi encontrado.

## 5. Como rodar

O ambiente isolado precisa estar no ar: API, worker, Vite, banco de teste e simulador de Pix, com e-mails gravados em pasta (`FILA_MAIL_MODE=file`). Depois:

```powershell
cd frontend
$env:E2E_BASE_URL = "http://127.0.0.1:15173"     # Vite do ambiente de teste
$env:E2E_MAIL_DIR = "C:\caminho\da\pasta\de\emails"
$env:E2E_ADMIN = "C:\caminho\fila-admin.exe"      # cria a loja de teste (bootstrap)
$env:E2E_ADMIN_CWD = "C:\pasta\com\o\.env\do\teste"
$env:E2E_PIX_CONTROL = "http://127.0.0.1:18090"   # simulador local de Pix (nunca a AbacatePay real)
# opcional: zera limites anti-abuso por IP no banco DE TESTE (todos os testes saem de 127.0.0.1)
$env:E2E_PSQL = "C:\caminho\psql.exe"; $env:E2E_DB_URL = "postgres://...banco de teste..."
npx playwright test            # Chrome e Edge; relatório em e2e-relatorio/
```

As variáveis começam com `E2E_` (e não com `FILA_`) porque a configuração do servidor recusa variáveis `FILA_` desconhecidas.

O Playwright **não baixa navegadores** (`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` na instalação): usa o Chrome e o Edge já instalados. Esses testes **não rodam no CI**, que não sobe o ambiente completo nem tem os navegadores. No CI rodam os testes de componente (Vitest) e a verificação de segredos do bundle.

## 6. Limitações conhecidas

- Firefox, Safari e aparelhos físicos não testados (seção 1).
- O axe encontra só problemas detectáveis automaticamente. Leitor de tela real (NVDA, VoiceOver, TalkBack) não foi usado.
- Zoom de 200% foi verificado pela largura equivalente (640 px), não pelo zoom do navegador com texto ampliado. Ampliação só de texto (sem zoom da página) não foi testada.
- Os testes compartilham um ambiente e rodam um de cada vez (limites por IP, filas).
