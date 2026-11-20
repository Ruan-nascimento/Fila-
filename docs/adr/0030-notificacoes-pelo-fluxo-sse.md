# ADR-0030 — Notificações derivadas do fluxo SSE (sem push) e aviso de atraso do cliente

- Situação: Aceita
- Data: 2026-11-20

## Contexto

Pedido do responsável: notificar o cliente (fila andou, chegou a vez, atendimento começou/terminou, pontos, pedido de mudança aceito/recusado…) e a equipe — barbeiro e dono com as mesmas notificações (entrou, saiu, avisou atraso…) —, pedindo permissão no navegador e no programa desktop. "Avisou que vai atrasar" não existia: o cliente só conseguia avisar fora do app.

O sistema já tem uma conexão SSE por página com eventos autorizados por audiência, versão por recurso e retomada por cursor (ADR-0010, ADR-0021).

## Decisão

1. **As notificações são derivadas no navegador a partir dos eventos SSE já recebidos.** Duas classes puras (`RegrasCliente`, `RegrasEquipe`) comparam o estado novo com a base (snapshot + eventos anteriores) e só avisam transições feitas pela outra parte. Cada aviso tem uma chave lógica (ex.: chamada daquela senha naquele horário): evento repetido na reconexão não avisa de novo; a chave vira a `tag` da notificação do sistema, então várias abas abertas substituem em vez de empilhar.
2. **Exibição:** olhando a página → aviso na tela (+ som/vibração no importante); página em segundo plano ou sem foco → Notification API, se permitido. Permissão pedida só por gesto ("Ativar notificações"); preferência por aparelho no `localStorage`. Android usa um service worker mínimo, só para `showNotification`.
3. **Programa desktop:** `tauri-plugin-notification` (substitui `window.Notification` pela notificação nativa). A gestão é página remota sem acesso a comandos; ganha uma capability criada em tempo de execução **só** para a origem do painel, **só** na janela principal e **só** com `allow-is-permission-granted`, `allow-request-permission` e `allow-notify`.
4. **Aviso de atraso (T-P-12, `noticeLate`):** comando do dono da visita que grava `participation.late_notice_at` sem mudar estado, posição ou tolerância; emite `participation.updated` e `queue.updated` (a equipe vê o selo e é notificada). Nova chamada limpa o aviso. A decisão continua com a equipe (ex.: `moveParticipation`, ADR-0029).

## Alternativas

- **Web Push (VAPID + service worker + tabela de inscrições + envio pelo worker):** entregaria com o navegador fechado, mas exige chaves novas, serviços externos de push (Google/Mozilla/Apple), dados de inscrição por pessoa (retenção, exclusão de conta) e, no iPhone, instalar o site. Fica como evolução; as regras e o texto dos avisos já estão isolados para serem reutilizados no servidor.
- **Eventos de notificação gravados pelo servidor (tipo SSE novo):** texto centralizado, mas duplicaria cada transição em todos os módulos e não acrescenta entrega com a página fechada. Derivar no cliente não expõe dado novo: usa só o que a audiência já recebe.

## Consequências

- Sem página aberta não há aviso (documentado em `docs/notificacoes.md`). Ao voltar, o snapshot sincroniza sem avisar o que já passou.
- Nenhuma tabela, chave ou serviço externo novo para notificações; só a coluna `late_notice_at`.
- A regra "páginas remotas não chamam comandos do programa" ganha uma exceção restrita e documentada (notificação).
