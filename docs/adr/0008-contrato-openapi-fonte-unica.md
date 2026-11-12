# ADR-0008 — Contrato OpenAPI como fonte única

- Situação: Aceita (etapa 00)
- Data: 2026-11-12

## Contexto

Backend em Go e frontend em TypeScript precisam concordar exatamente em rotas, campos, erros e eventos. Divergência silenciosa gera bugs de dinheiro e autorização.

## Decisão

- `docs/contrato/openapi.yaml` (OpenAPI 3.1) é a fonte única. Mudanças começam nele.
- A etapa 01 configura geração de código: tipos/servidor Go e tipos/cliente TypeScript a partir do OpenAPI, com ferramentas e versões escolhidas pela documentação oficial e travadas; o CI falha se o código gerado estiver desatualizado.
- O OpenAPI também carrega metadados de rastreabilidade: `x-authz`, `x-transitions`, `x-events`, `x-reauth`, `x-external-pending`.
- `docs/tools/verificar_contrato.py` confere a coerência entre contrato, regras, matriz e cenários; a matriz de permissões por operação é gerada dele.
- Lint com Redocly CLI (versão fixada) no CI.
- Códigos da API (estados, erros, campos) em inglês, estáveis; textos para pessoas e documentação em português.

## Alternativas consideradas

- Tipos escritos à mão nos dois lados: rejeitado (divergência).
- Gerar OpenAPI a partir do código Go: dificulta revisar o contrato antes de implementar.

## Consequências

- Toda nova rota precisa de `x-authz`, sem exceção.
- Campos desconhecidos no corpo são rejeitados (`additionalProperties: false`).
