# ADR-0003 — Frontend web React + TypeScript + Vite

- Situação: Aceita (decisão 03 do anexo)
- Data: 2026-11-12

## Contexto

A primeira entrega é só web responsiva, usada por cliente, profissional e dona em celular, tablet e computador. Desktop e apps virão depois e devem reaproveitar a API.

## Decisão

- `frontend/` com React, TypeScript e Vite; versões escolhidas na etapa 01 pela documentação oficial e travadas em lockfile.
- O frontend **não** tem regra de negócio própria: mostra o estado do servidor e envia comandos. Validações locais são só conforto (o servidor valida tudo).
- Tipos e cliente HTTP gerados do OpenAPI (ADR-0008).
- Uma única conexão SSE por aba, gerenciada por um serviço central (ADR-0004).
- Sem Expo, React Native, PWA ou push nesta fase.

## Alternativas consideradas

- Next.js/SSR: desnecessário para app autenticado atrás de mesma origem; aumenta superfície.
- Framework mobile híbrido: fora do escopo do MVP.

## Consequências

- Adaptação desktop futura pode empacotar o mesmo frontend apontando para a mesma API.
- Responsividade e acessibilidade são critérios de aceite (R-WEB-*), não melhorias opcionais.
