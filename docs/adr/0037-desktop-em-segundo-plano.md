# ADR-0037 — Programa desktop em segundo plano

- Situação: Aceita
- Data: 2026-11-23

## Contexto

O dono quer receber os avisos da gestão (entrou na fila, saiu, atraso, pedido de mudança) com a janela do Fila Gestão fechada. Até aqui, fechar a janela encerrava o programa.

## Decisão

1. **Preferência do computador** "Continuar executando em segundo plano ao fechar a janela", em `segundo-plano.json` na pasta de configuração do programa. Começa **desligada** e só muda por gesto da pessoa no menu da bandeja. Na primeira ativação aparece: "O Fila continuará aberto na bandeja para receber avisos. Para encerrar completamente, use Sair do Fila."
2. **X com a preferência ligada** esconde a janela; o processo e a página continuam. **Desligada**, fechar encerra como antes.
3. **Bandeja** (recurso `tray-icon` do Tauri): Abrir Fila; Som dos avisos; Continuar em segundo plano; Sair do Fila (encerrar o programa). "Sair do Fila" encerra o processo de verdade (não esconde, não toca em atendimentos da loja) e é diferente de "Sair" da conta na página. Clique esquerdo no ícone reabre a janela.
4. **Sem bandeja funcional** (criação falhou), a janela nunca é escondida. Além disso, **instância única** (`tauri-plugin-single-instance`): abrir o programa de novo reabre a janela existente — um caminho confiável mesmo em áreas de trabalho que escondem ícones de bandeja — e impede duas instâncias gerando avisos duplicados.
5. **Mesma fonte de avisos**: continua sendo a própria página da gestão (fluxo SSE da sessão de dono). Não há segunda sessão nem fluxo nativo paralelo; logout, expiração, revogação, troca de sessão de dono e troca de loja valem igual, e a deduplicação por id de evento é a já existente da página. Verificado no Linux (WebKitGTK) que a página escondida segue recebendo o fluxo (evidências).
6. **Som e conteúdo discreto** são preferências do aparelho guardadas pela página. A bandeja grava o som avaliando um script na própria janela; a página remota **não** ganha acesso a comando nenhum do programa. O modo discreto troca título e texto da notificação por "Fila / Há uma atualização".
7. Fora desta entrega: iniciar com o sistema operacional; indicador de conexão na bandeja (exigiria dar à página remota um canal para o programa); clique na notificação reabrindo a janela (o plugin de notificação não entrega o clique no desktop — use a bandeja ou abra o programa de novo).

## Alternativas

- Processo nativo próprio de notificações: duplicaria sessão e regras de autorização.
- Esconder sempre ao fechar: mudaria o comportamento sem a pessoa pedir.

## Consequências

- O pacote passa a depender de `tauri-plugin-single-instance` 2.5.5 e `tauri-plugin-dialog` 2.8.4 (oficiais).
- Exige nova compilação e distribuição do instalador. Windows **não testado** nesta entrega.
- Computador desligado ou suspenso não recebe avisos; ao voltar, a página reconecta pelo cursor do SSE (comportamento existente).
