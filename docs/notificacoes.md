# Notificações

Avisos para o cliente e para a equipe (barbeiro e dono recebem as mesmas). Decisão: [ADR-0030](adr/0030-notificacoes-pelo-fluxo-sse.md). Regras: R-WEB-06, R-FILA-19. Cenários: C-WEB-05, C-FILA-15.

## 1. O que cada um recebe

### Cliente (página da loja, com visita ativa)

| Acontecimento | Aviso |
|---|---|
| Alguém à frente foi chamado ou saiu | "Faltam N pessoas na sua frente" / "Você é o próximo!" (substitui o anterior) |
| Chamada | "Chegou a sua vez!" — importante: vibra, toca e fica até a pessoa fechar |
| A barbearia desceu a pessoa (ela avisou atraso) | "A barbearia chamou o próximo" — continua na fila, em tal lugar |
| Início / fim / interrupção do atendimento | "Seu atendimento começou" / "terminou" / "foi interrompido" |
| Ausência registrada | "Você foi marcado como ausente" |
| Cancelamento pela loja | "A barbearia cancelou sua senha" + motivo |
| Transferência | "Você foi transferido de fila" |
| Pedido de mudança decidido | "O barbeiro aceitou seu pedido de mudança" / "recusou" + motivo |
| Pagamento confirmado | "Pagamento confirmado" |
| Pontos creditados (conta) | "Você ganhou N pontos" + saldo |

O que a própria pessoa fez (entrar, desistir, pedir mudança, avisar atraso) não gera aviso.

### Equipe (gestão: profissional da fila e dono)

| Acontecimento | Aviso |
|---|---|
| Entrada na fila (ou transferência para ela) | "Nova pessoa na fila" — com o profissional quando o dono vê várias filas |
| Cliente avisou que vai atrasar (botão "Vou me atrasar") | "Fulano avisou que vai atrasar" — importante; a fila mostra o selo e a equipe decide (ex.: "Vai atrasar: chamar o próximo") |
| Pedido de mudança na comanda | "Fulano pediu mudança na comanda" |
| Desistência do cliente | "Alguém saiu da fila" |

Ações da própria equipe (chamar, iniciar, concluir, cancelar com motivo) não geram aviso.

## 2. Onde aparecem

- **Olhando a página**: aviso no canto da tela (some sozinho; o importante fica mais tempo) e o sino do cabeçalho conta os novos.
- **Página em segundo plano** (outra aba, outro app, programa sem foco): notificação do sistema, se a pessoa permitiu. Tocar nela volta para a página.
- **Sino**: os avisos novos (desde o último **Limpar**), cada um levando ao seu lugar (ex.: a fila do barbeiro), e a preferência "notificações neste aparelho". **Limpar** esvazia só o sino.
- **Histórico** (botão "Ver histórico" no sino): tudo o que chegou neste aparelho nos últimos 30 dias (até 300 avisos), **inclusive o que foi limpo do sino**, agrupado por dia, com busca. "Apagar histórico" pede confirmação. Fica no `localStorage`, separado por loja e por pessoa (conta ou visitante): outra pessoa no mesmo computador não vê o histórico de quem saiu. Nada vai ao servidor.
- **Programa Fila Gestão**: o plugin de notificações do Tauri entrega como notificação nativa do Windows/Linux.

## 3. Permissão

- O navegador só pergunta depois de um toque em "Ativar notificações" (convite no topo da gestão e na visita do cliente, ou no sino). "Agora não" não insiste mais neste aparelho.
- Bloqueado no navegador: o sino explica como liberar nas configurações do site.
- A preferência fica no aparelho (`localStorage`); nada é enviado ao servidor.

## 4. Limites (conhecidos)

- Sem **push**: os avisos chegam enquanto a página da loja/gestão está aberta, mesmo em segundo plano. Com o navegador fechado, ou o celular suspendendo a aba por muito tempo, não chegam; ao voltar, a tela sincroniza.
- iPhone/iPad: o Safari só mostra notificações de sites instalados na tela de início (com push). Lá os avisos aparecem só dentro da página.
- Celular Android: usa um service worker mínimo (`/sw-notificacoes.js`) só para mostrar a notificação; ele não guarda cache nem intercepta pedidos.
- Som: navegadores bloqueiam som antes do primeiro toque na página; nesse caso fica só o aviso visual/vibração.

## 5. Implementação

| Peça | Onde |
|---|---|
| Regras (evento → aviso, sem repetir) | `frontend/src/features/notificacoes/regras.ts` (`RegrasCliente`, `RegrasEquipe`) |
| Permissão, preferência, sistema, som | `frontend/src/features/notificacoes/sistema.ts` |
| Provedor, sino, histórico, convite, avisos na tela | `frontend/src/features/notificacoes/Notificacoes.tsx`, `historico.ts` |
| Aviso de atraso | `POST …/participations/{id}/late-notice` (`noticeLate`, T-P-12), coluna `participation.late_notice_at` (migração 00021) |
| Programa desktop | `tauri-plugin-notification` + capability `painel-notificacoes` (só a origem do painel, só 3 permissões de notificação) em `desktop-dono/src-tauri/src/lib.rs` |
