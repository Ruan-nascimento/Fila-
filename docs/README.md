# Documentação do Fila (versão 1.0.0)

**Quer usar o sistema?** Leia o [Guia prático](guia-pratico.md) ou [baixe o PDF](https://github.com/Ruan-nascimento/Fila-/raw/main/docs/Guia-Pratico-Fila-1.0.0.pdf): todas as funções, passo a passo, por papel.

O resto desta pasta são as regras, decisões, contrato e operação usados pelo backend (`backend/`, Go), pelo frontend (`frontend/`, React + TypeScript + Vite) e pelo programa do dono (`desktop-dono/`, Tauri). Como instalar e rodar o projeto: [README da raiz](../README.md).

## Onde está cada coisa

| Arquivo | Conteúdo |
|---|---|
| [guia-pratico.md](guia-pratico.md) · [PDF](Guia-Pratico-Fila-1.0.0.pdf) | **Guia prático 1.0.0**: cada função do cliente, profissional, dono, programa Fila Gestão e operador, com problemas comuns e limites da versão |
| [guia-por-papel.md](guia-por-papel.md) | Resumo de uma página por papel |
| [desktop/](desktop/guia-do-dono.md) | Programa Fila Gestão: [guia do dono](desktop/guia-do-dono.md), [métricas do Dashboard](desktop/metricas.md), [desempenho](desktop/desempenho.md) |
| [regras.md](regras.md) | Regras do anexo numeradas (`R-…`), decisões já tomadas e limitações externas reais |
| [matriz-transicoes.md](matriz-transicoes.md) | Máquinas de estado separadas (fila, participação, comanda, Pix, recebimento, estorno, reserva…) com ator, pré-condição, efeito, idempotência, auditoria e erro de cada transição (`T-…`) |
| [matriz-permissoes.md](matriz-permissoes.md) | Quem pode o quê: resumo por papel + tabela por operação gerada do contrato |
| [cenarios-aceite.md](cenarios-aceite.md) | Cenários `C-…` em "Dado / Quando / Então", incluindo disputas concorrentes |
| [contrato/openapi.yaml](contrato/openapi.yaml) | Contrato HTTP (OpenAPI 3.1) — fonte única para backend e frontend |
| [contrato/convencoes.md](contrato/convencoes.md) | Envelopes, erros (RFC 9457), paginação, IDs, dinheiro em centavos, datas UTC, idempotência, sessões e CSRF |
| [contrato/eventos-sse.md](contrato/eventos-sse.md) | Eventos SSE: versão, audiência, cursor pós-commit, snapshot e recuperação |
| [adr/](adr/README.md) | Decisões de arquitetura (ADR-0001…0037) |
| [configuracoes-da-loja.md](configuracoes-da-loja.md) | Cada configuração da loja e do aparelho: tipo, padrão, intervalo, quem altera, visibilidade, quando vale e efeito no que já existe (1.0.0) |
| [melhorias-1.0.0-impacto.md](melhorias-1.0.0-impacto.md) | Matriz de impacto das melhorias 1.0.0 (quantidades, produtos, custos, entrada/capacidade, desktop) |
| [notificacoes.md](notificacoes.md) | Notificações do cliente e da equipe: o que avisa, onde aparece, permissão, aviso de atraso, limites (sem push) |
| [configuracoes-e-identidade.md](configuracoes-e-identidade.md) | Minha conta, fotos (visibilidade, arquitetura, cache, medições), login com Google só para cliente (com passo a passo do Google Cloud), identidade da loja (16 paletas e contrastes), QR Code, matriz de sessão, ativação e volta atrás, checklist manual |
| [evidencias.md](evidencias.md) | **Revisão final (etapa 13):** o que foi executado, onde, quando e com que resultado; o que não foi testado e o que depende de fora |
| [rastreabilidade.md](rastreabilidade.md) | Cada regra → cenários → testes que os citam (gerado) |
| [bugs.md](bugs.md) | Defeitos encontrados, gravidade, reprodução e situação |
| [piloto.md](piloto.md) | Plano de piloto, critérios de parada e volta atrás |
| [operacao/](operacao/README.md) | Implantação, monitoramento e alertas, backup e restauração, rotinas de incidente e evidências; publicação da V1 em produção ([publicacao-v1.md](operacao/publicacao-v1.md), [variáveis](operacao/variaveis-v1.md), [backup e recuperação](operacao/backup-e-recuperacao-v1.md)) |
| [evidencias/melhorias-1.0.0.md](evidencias/melhorias-1.0.0.md) | Evidências das melhorias 1.0.0: testes, desempenho medido da busca, segundo plano do desktop, o que não foi validado |
| [retencao.md](retencao.md) | Matriz de retenção por categoria (o que o sistema faz hoje; prazos legais pendentes de validação) |
| [tools/verificar_contrato.py](tools/verificar_contrato.py) | Verificador de rastreabilidade regra → cenário → transição → operação |
| [tools/gerar_guia_pdf.py](tools/gerar_guia_pdf.py) | Gera o PDF do guia prático a partir de `guia-pratico.md` (Python `markdown` + Chrome/Chromium headless) |

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
4. Função nova ou tela alterada → atualizar [guia-pratico.md](guia-pratico.md) e gerar o PDF de novo (`python docs/tools/gerar_guia_pdf.py`).
