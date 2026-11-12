# ADR-0006 — Autorização no servidor e sessões em cookie

- Situação: Aceita (decisão 06 do anexo + detalhes da etapa 00)
- Data: 2026-11-12

## Contexto

Clientes, visitantes e equipe usam o mesmo navegador e a mesma API. Nick não é senha. Identificadores e botões escondidos não protegem nada.

## Decisão

- **Negação por padrão.** Cada operação declara `x-authz` no OpenAPI; o backend aplica verificadores por escopo (`visit_owner`, `own_queue`, `own_attendance`, `establishment`…) e nega o resto. O teste C-SEG-01 é gerado a partir dessa lista.
- **Papel nunca vem do corpo.** Ator = sessão. Vínculo de equipe só vale com sessão de contexto `staff` (login escolhe `customer` ou `staff`); a mesma pessoa age como cliente com sessão `customer`.
- **Sessões opacas no servidor** (não JWT): token aleatório de 256 bits no cookie `__Host-fila_session` (`Secure; HttpOnly; SameSite=Lax; Path=/`), banco guarda só SHA-256. Revogação imediata por linha no banco. Visitante usa `__Host-fila_visitor` (ADR-0011).
- **Mesma origem**: frontend e API servidos pelo mesmo domínio via proxy reverso (`/v1` para a API). Evita CORS com credenciais e permite cookies `__Host-`.
- **CSRF**: `Origin` validado em todo método não seguro; token `X-CSRF-Token` (HMAC das sessões presentes) obtido em `GET /v1/session`; rotas sem sessão exigem `Content-Type: application/json`.
- **Senhas** com Argon2id (parâmetros definidos e medidos na etapa 02 seguindo a recomendação atual da OWASP/RFC 9106).
- **Reautenticação** (10 min) para exclusão de conta, acesso da equipe (convite, desativar/reativar), estorno e correção de comanda.
- **Recurso fora do escopo → 404**, para não revelar existência.
- **Limites de tentativa** por conta/alvo e por origem.

## Alternativas consideradas

- JWT em localStorage: rejeitado (exposto a XSS, revogação difícil).
- JWT em cookie: revogação imediata exigiria lista no servidor de qualquer forma.
- SameSite=Strict: quebraria a chegada pelo link/QR vindo de outro site (primeira navegação sem cookie).

## Consequências

- Toda consulta inclui `establishment_id` e o escopo do ator no `WHERE`.
- Revogar sessão também encerra SSE (ADR-0010).
- Implantação com domínios separados para front e API exigiria novo ADR.
