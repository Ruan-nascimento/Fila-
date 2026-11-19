# ADR-0027 — Sessão única do dono e Dashboard exclusivo do programa desktop

- Situação: Aceita
- Data: 2026-11-18

## Contexto

O dono usa a gestão no navegador e no programa Fila Gestão (Tauri, `desktop-dono/`). Duas exigências novas:

1. **Sessão única do dono.** Uma conta pode ter no máximo uma sessão capaz de exercer privilégios de dono, somando navegador, programa, computadores, perfis e janelas anônimas. A regra vale também para quem entra pela rota comum de equipe (conta dona de A e profissional de B). Clientes, visitantes e profissionais sem vínculo de dono não mudam.
2. **Dashboard analítico só no programa, num computador autorizado.** O servidor precisa negar os dados analíticos quando faltar qualquer uma destas três condições: sessão de dono ativa, vínculo de dono com a loja pedida e prova de uma instalação desktop autorizada. Flag JavaScript, User-Agent, parâmetro ou segredo igual em todos os instaladores não servem como prova.

Restrições do projeto: monólito Go, PostgreSQL como registro oficial, cookies HttpOnly (ADR-0006), contrato OpenAPI único (ADR-0008), módulos que só leem as próprias tabelas (ADR-0001).

## Decisão

### 1. Vaga de dono por conta (`owner_session_slot`)

- Uma linha por conta com vínculo de dono. `account_id` é a chave primária e `session_id` é `UNIQUE`. Só a sessão apontada pela linha exerce privilégios de dono. A validade é conferida a cada uso: sessão revogada ou vencida conta como vaga livre.
- **Login de equipe** de uma conta dona, **troca** e **logout** travam a linha com `SELECT … FOR UPDATE`. A linha é criada antes da trava (`INSERT … ON CONFLICT DO NOTHING`), então dois logins simultâneos sem vaga prévia também ficam em fila. Não existe o padrão "SELECT e depois INSERT" sem proteção.
- **Guarda no ponto de efetivação.** O middleware marca o contexto de cada pedido da sessão ocupante. `database.InTx` roda primeiro `SELECT … FOR SHARE` na vaga e confere que a sessão ainda é a ocupante.
  - A troca espera as mutações que já estavam em andamento: elas ficam registradas.
  - Depois do commit da troca, qualquer transação da sessão antiga é recusada (`401 owner_session_replaced`).
  - Login, troca e logout desligam a guarda, porque travam a mesma linha em modo exclusivo.
- **Autorização.** `staffActor` só dá o papel de dono se a sessão for a ocupante (`OwnerHolder`, lido junto com a sessão). Uma sessão de equipe fora da vaga age, no máximo, como profissional. Esse caso aparece com sessão anterior à migração ou com conta que virou dona durante a sessão. Sem vínculo de profissional, a resposta é `403 owner_session_required`.

### 2. Conflito no login e troca confirmada

- Antes de qualquer decisão sobre a vaga, as credenciais são validadas com o mecanismo atual. Uma senha errada nunca revela que existe sessão ativa.
- Se outra sessão válida ocupa a vaga, o login responde `409 owner_session_active`.
  - A outra sessão continua funcionando. A tentativa não recebe cookie de sessão nem dado da loja.
  - `Problem.current` traz um **desafio** de 2 minutos e o contexto confiável da outra sessão: programa autorizado, com o nome dado pelo operador, ou "outro aparelho ou navegador", e o último uso arredondado a 5 minutos.
  - Junto vai o cookie HttpOnly `__Host-fila_owner_attempt`, de 5 minutos. O banco guarda só os hashes do desafio e da tentativa, e o desafio fica preso à conta, à tentativa e à **geração** da vaga.
- `POST /v1/auth/owner-session/takeover` faz tudo numa transação: valida o desafio, revoga a sessão anterior (`owner_session_replaced`) e os vínculos desktop dela, cria a nova sessão, passa a vaga (a geração cresce) e audita (`T-SS-07`). O cookie de sessão só é definido depois do commit.
  - Se a vaga mudou enquanto a confirmação estava aberta, por outra troca, logout ou vencimento, o desafio fica `stale` e nada é derrubado.
  - Repetir a **mesma** confirmação depois de uma queda de rede reentrega o acesso, girando o token da sessão vencedora, só se essa tentativa venceu e continua ocupando a vaga. Nunca revoga de novo.
- `POST /v1/auth/owner-session/cancel` descarta o desafio e mantém a outra sessão. Responde sempre 204.
- Fechar a janela ou desligar o PC não é logout. O dono pode assumir a vaga de um aparelho desligado ou perdido.
- O logout libera a vaga só se o cookie apresentado for o da ocupante. Um logout atrasado da sessão antiga nunca derruba a nova.
- O motivo do encerramento chega ao cliente antigo de três formas:
  - no SSE: `stream.session_ended` com `reason: owner_session_replaced`;
  - nas respostas: `401 owner_session_replaced`;
  - em `GET /v1/session`: `ended_reason`, nas 12 h seguintes.

  Vale também para o aparelho que volta de suspensão ou recupera a rede.

**Sessões anteriores à migração (00017).** Para cada conta com vínculo ativo de dono, fica a sessão de equipe válida usada mais recentemente, que passa a ocupar a vaga. As demais sessões de equipe dessa conta são encerradas com `owner_session_migration`. Sessões de cliente e contas sem vínculo de dono não mudam. Nunca sobram duas sessões antigas autorizadas.

### 3. Instalação desktop autorizada e prova de posse (DPoP)

- **Ativação.** O operador emite um código de uso único com `fila-admin desktop emitir-codigo`. O código vale 15 minutos, fica ligado à conta e recebe um nome de computador. Emitir outro código cancela os pendentes.
  - No primeiro uso, o componente nativo gera um par P-256 e guarda a chave privada no cofre do sistema: Gerenciador de Credenciais no Windows, Secret Service no Linux.
  - O programa envia o código com uma prova DPoP assinada pela chave nova. O servidor guarda só a chave pública e a impressão digital RFC 7638 (`jkt`).
  - Uma página web comum não consegue fazer isso: não tem código nem chave.
- **Vínculo à MESMA sessão.** A gestão aberta na janela (cookie da ocupante) pede um ticket de 60 s (`POST /v1/desktop/link-tickets`) e navega para `/dono/painel-analitico#t=…`.
  - O componente nativo intercepta essa navegação, que nunca é carregada como página. O ticket fica só na memória nativa.
  - O nativo troca o ticket, com prova DPoP, por um token de acesso preso ao `jkt` (`POST /v1/desktop/bindings`). O token vale no máximo 2 h e só enquanto a sessão for a ocupante da vaga.
  - Nenhum cookie é copiado para JavaScript ou arquivo. O ticket só serve para uma instalação da **mesma conta**.
- **Consultas.** Cada pedido nativo leva `Authorization: DPoP <token>` e uma prova DPoP (RFC 9449, ES256). O servidor confere:
  - a prova: `typ dpop+jwt`, assinatura (go-jose), `htm`, `htu` (URL pública + caminho), `iat` em ±2 min, `jti` nunca visto (`desktop_proof_seen`, chave primária) e `ath` = hash do token;
  - que `jkt` da prova é igual ao `jkt` da instalação;
  - que a instalação não foi revogada;
  - que a sessão é válida e ocupa a vaga;
  - que a conta é dona da loja pedida (senão `404`).

  O programa corrige a diferença de relógio pelo cabeçalho `Date`.
- **Componente nativo restrito.** Só há comandos tipados: ativar, vincular, contexto, consultas por enums (métrica, dimensão, tipo de registro, granularidade, meio de pagamento) com datas e UUIDs validados, fluxo de invalidação, abrir telas fixas da gestão e sair. Não existe assinatura genérica, URL arbitrária nem pedido livre. As páginas remotas do painel continuam sem acesso a comando nenhum (capability só `local`).
- **Revogação.** Sair, trocar de aparelho, vencer a sessão, remover o vínculo de dono ou revogar a instalação (`fila-admin desktop revogar`) invalida as consultas seguintes e encerra o fluxo SSE do Dashboard, que é reconferido a cada 30 s e a cada aviso de revogação.

**Limite da garantia.** O servidor verifica a sessão e a **posse da chave** de uma instalação autorizada. Isso **não** atesta o executável. Alguém com controle total do computador autorizado, e com a sessão do dono aberta nele, consegue usar a chave. O cofre do sistema protege a chave contra outros usuários e contra cópia simples de arquivos, não contra o próprio usuário administrador da máquina.

### 4. Módulo `analitico` (somente leitura)

- O módulo agrega no banco dados de fila, comanda, recebimentos, estornos, pendências e fidelidade. Pela regra da ADR-0001, isso exigiria uma função exportada por consulta em cinco módulos.
- Decisão: um módulo **só de leitura**, com lista fechada de tabelas permitidas (`analyticsReadTables`) e um teste que recusa qualquer `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `TRUNCATE` ou `FOR UPDATE/SHARE` (`TestAnalyticsIsReadOnly`). Tabelas de sessão, credencial e contato ficam fora.
- As consultas rodam em `InSnapshot` (REPEATABLE READ, somente leitura): totais e detalhes da mesma resposta enxergam o mesmo instante.
- A interface fica em `desktop-dono/dashboard/`, empacotada no instalador. O bundle web público não leva os gráficos.

## Alternativas consideradas

- **Advisory lock por conta.** Serializa, mas não deixa um registro autoritativo consultável por todos os processos, nem uma restrição de unicidade.
- **Revogar todas as sessões de equipe no login.** Derrubaria a outra sessão sem confirmação e criaria cadeias de revogação.
- **Segredo embutido no instalador ou cabeçalho fixo.** Qualquer cópia do instalador ou página web o reproduz. Recusado pela especificação.
- **mTLS com certificado por instalação.** Forte, mas exige uma PKI e um proxy que repasse o certificado. DPoP usa as primitivas que o projeto já tem (HTTP + JSON) e bibliotecas mantidas.
- **Copiar o cookie da WebView para o nativo.** Viola HttpOnly e criaria uma segunda credencial durável.
- **Funções exportadas em cada módulo para o analítico.** Muitos pontos de acoplamento para consultas que mudam com frequência. O módulo somente leitura, verificado por teste, deixa a fronteira explícita.

## Consequências

- Toda transação de um pedido da sessão de dono faz uma consulta a mais (`FOR SHARE` numa linha). O custo é desprezível e dá a garantia na efetivação.
- Testes e scripts que abriam duas sessões de dono para a mesma conta precisam encerrar a primeira ou confirmar a troca (`testes/janela.mjs`).
- Novos erros no contrato: `owner_session_active`, `owner_session_challenge_invalid`, `owner_session_replaced`, `owner_session_required`, `desktop_*` e `analytics_filter_not_applicable`.
- Novo processo operacional, documentado em `docs/desktop/guia-do-dono.md`: autorizar, substituir e revogar computadores pelo `fila-admin`.
