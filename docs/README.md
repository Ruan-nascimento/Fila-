# Documentação do MVP de filas

Regras, decisões e contrato do MVP (etapa 00), usados pelo backend (`backend/`, Go) e pelo frontend (`frontend/`, React + TypeScript + Vite). Como instalar e rodar o projeto: [README da raiz](../README.md).

## Onde está cada coisa

| Arquivo | Conteúdo |
|---|---|
| [regras.md](regras.md) | Regras do anexo numeradas (`R-…`), decisões já tomadas e limitações externas reais |
| [matriz-transicoes.md](matriz-transicoes.md) | Máquinas de estado separadas (fila, participação, comanda, Pix, recebimento, estorno, reserva…) com ator, pré-condição, efeito, idempotência, auditoria e erro de cada transição (`T-…`) |
| [matriz-permissoes.md](matriz-permissoes.md) | Quem pode o quê: resumo por papel + tabela por operação gerada do contrato |
| [cenarios-aceite.md](cenarios-aceite.md) | Cenários `C-…` em "Dado / Quando / Então", incluindo disputas concorrentes |
| [contrato/openapi.yaml](contrato/openapi.yaml) | Contrato HTTP (OpenAPI 3.1) — fonte única para backend e frontend |
| [contrato/convencoes.md](contrato/convencoes.md) | Envelopes, erros (RFC 9457), paginação, IDs, dinheiro em centavos, datas UTC, idempotência, sessões e CSRF |
| [contrato/eventos-sse.md](contrato/eventos-sse.md) | Eventos SSE: versão, audiência, cursor pós-commit, snapshot e recuperação |
| [adr/](adr/README.md) | Decisões de arquitetura (ADR-0001…0026) |
| [evidencias.md](evidencias.md) | **Revisão final (etapa 13):** o que foi executado, onde, quando e com que resultado; o que não foi testado e o que depende de fora |
| [rastreabilidade.md](rastreabilidade.md) | Cada regra → cenários → testes que os citam (gerado) |
| [bugs.md](bugs.md) | Defeitos encontrados, gravidade, reprodução e situação |
| [guia-por-papel.md](guia-por-papel.md) | Guia curto para cliente, profissional, dono e operador |
| [piloto.md](piloto.md) | Plano de piloto, critérios de parada e volta atrás |
| [operacao/](operacao/README.md) | Implantação, monitoramento e alertas, backup e restauração, rotinas de incidente e evidências (etapa 12) |
| [retencao.md](retencao.md) | Matriz de retenção por categoria (o que o sistema faz hoje; prazos legais pendentes de validação) |
| [tools/verificar_contrato.py](tools/verificar_contrato.py) | Verificador de rastreabilidade regra → cenário → transição → operação |

## Como verificar (a partir da raiz do repositório)

Pré-requisitos: Python 3.10+ com PyYAML (`python -m pip install --require-hashes --no-deps -r docs/tools/requirements.txt`) e Node.js 24.15+.

```bash
# 1. Coerência entre regras, matriz, cenários, eventos e contrato (sai com erro se algo não bate)
python docs/tools/verificar_contrato.py

# 2. Depois de mudar x-authz no OpenAPI, regenerar a tabela de permissões
python docs/tools/verificar_contrato.py --write

# 3. Validar o OpenAPI (versão fixada; o arquivo .redocly.lint-ignore.yaml lista as exceções justificadas)
cd docs/contrato
npx --yes @redocly/cli@2.72.0 lint openapi.yaml
```

Os dois comandos rodam no CI (`.github/workflows/ci.yml`, job "Contrato e rastreabilidade").

Depois de mudar o OpenAPI, regenere também os tipos usados pelo código:

```bash
go -C backend generate ./internal/contrato   # tipos Go
cd frontend && npm run gen:api               # tipos TypeScript
```

## Como mudar uma regra ou o contrato

1. Regra de produto nova ou alterada → nova versão do documento de regras (anexo) e atualização de `regras.md`, preservando histórico.
2. Mudança de contrato → editar primeiro `contrato/openapi.yaml` (incluindo `x-authz`, `x-transitions`, `x-events`), depois matriz e cenários; rodar o verificador e o lint.
3. Decisão de arquitetura nova → novo ADR; não reescrever ADR antigo.
