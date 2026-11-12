# ADR-0013 — Dono inicial e transferência por processo controlado

- Situação: Aceita (etapa 00)
- Data: 2026-11-12

## Contexto

Cadastro público nunca pode conceder acesso administrativo, e uma loja nunca pode ficar sem responsável. O MVP tem uma loja piloto.

## Decisão

- Executável `fila-admin` (mesmo backend), rodado por operador com acesso ao servidor:
  - `bootstrap`: cria a loja e emite convite de dono (uso único, 24 h) para o e-mail informado.
  - `transfer-owner`: vincula novo dono após confirmação de identidade fora de banda; o antigo pode então ser removido.
  - `close-establishment`: encerra a loja só sem pendências operacionais/financeiras abertas.
- Todos auditados com ator `operator` e motivo. Não há rota HTTP para essas ações no MVP.

## Alternativas consideradas

- Primeiro usuário cadastrado vira dono: rejeitado (qualquer um poderia ser o primeiro).
- Tela de super-admin: superfície extra sem necessidade para uma loja.

## Consequências

- Pedidos de transferência passam por suporte/operador.
- A exclusão de conta do dono único responde `sole_owner` até a transferência.
