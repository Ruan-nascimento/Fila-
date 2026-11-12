# ADR-0004 — HTTP para comandos, SSE para atualizações

- Situação: Aceita (decisão 04 do anexo)
- Data: 2026-11-12

## Contexto

A fila muda por ações de várias pessoas. As telas precisam refletir o estado sem recarregar, mas o canal de atualização não pode virar canal de comando nem prometer entrega com aba fechada.

## Decisão

- Entrar, sair, chamar, concluir, pagar: sempre requisição HTTP com resposta confiável.
- SSE (`GET /v1/stream`) só do servidor para o navegador, com snapshot inicial, cursor e retomada (detalhes em `contrato/eventos-sse.md`).
- Polling periódico não é usado como atualização normal; GET é usado para snapshot/recuperação.
- Sem promessa de aviso com aba fechada ou suspensa; ao voltar, a tela sincroniza antes de permitir ação.

## Alternativas consideradas

- WebSocket: bidirecional desnecessário; SSE usa HTTP comum, reconexão nativa e passa melhor por proxies.
- Polling: atraso e carga desnecessários; não atende "atualização normal" do anexo.
- Push/notificações: fora do MVP.

## Consequências

- Proxy reverso precisa de buffering desligado e timeouts compatíveis com heartbeat.
- HTTP/1.1 limita conexões por domínio; por isso uma conexão por aba e HTTP/2 recomendado.
