# Registros de decisão de arquitetura (ADR)

Cada ADR registra uma decisão: contexto, decisão, alternativas e consequências. Os ADRs 0001–0007 documentam decisões **já tomadas** no anexo de regras; os demais registram detalhes técnicos escolhidos na etapa 00.
Para mudar uma decisão, crie um novo ADR que substitua o anterior (não reescreva o histórico).

| ADR | Título | Situação |
|---|---|---|
| [ADR-0001](0001-monolito-modular-go.md) | Monólito modular em Go | Aceita (decisão 01 do anexo) |
| [ADR-0002](0002-postgresql-registro-oficial.md) | PostgreSQL como registro oficial | Aceita (decisão 02 do anexo) |
| [ADR-0003](0003-frontend-react-typescript-vite.md) | Frontend web React + TypeScript + Vite | Aceita (decisão 03 do anexo) |
| [ADR-0004](0004-http-comandos-sse-atualizacoes.md) | HTTP para comandos, SSE para atualizações | Aceita (decisão 04 do anexo) |
| [ADR-0005](0005-separacao-fila-atendimento-comanda-recebimento.md) | Separar fila, atendimento, comanda, recebimento e conferência | Aceita (decisão 05 do anexo) |
| [ADR-0006](0006-autorizacao-servidor-sessoes-cookie.md) | Autorização no servidor e sessões em cookie | Aceita (decisão 06 do anexo + detalhes da etapa 00) |
| [ADR-0007](0007-pix-confirmacao-autenticada-conferencia.md) | Pix confirmado só pelo provedor; conferência separada | Aceita (decisão 07 do anexo + detalhes da etapa 00) |
| [ADR-0008](0008-contrato-openapi-fonte-unica.md) | Contrato OpenAPI como fonte única | Aceita (etapa 00) |
| [ADR-0009](0009-idempotencia-e-concorrencia-otimista.md) | Idempotência por ator/operação e concorrência otimista | Aceita (etapa 00) |
| [ADR-0010](0010-outbox-transacional-e-cursor-pos-commit.md) | Outbox transacional e cursor de eventos pós-commit | Aceita (etapa 00) |
| [ADR-0011](0011-visitante-credencial-e-codigo.md) | Credencial de visitante por loja e código de recuperação | Aceita (etapa 00, aplicando prazos do anexo) |
| [ADR-0012](0012-razao-de-pontos-imutavel.md) | Razão de pontos com lançamentos imutáveis | Aceita (etapa 00) |
| [ADR-0013](0013-processos-controlados-do-dono.md) | Dono inicial e transferência por processo controlado | Aceita (etapa 00) |
| [ADR-0014](0014-identificadores-dinheiro-tempo.md) | Identificadores, dinheiro e tempo | Aceita (etapa 00) |
| [ADR-0015](0015-minimizacao-e-retencao-de-dados.md) | Separação de perfil e registros, e retenção | Aceita parcialmente (prazos legais pendentes) |
| [ADR-0016](0016-fundacao-ferramentas-e-configuracao.md) | Fundação: ferramentas, versões e configuração | Aceita (etapa 01) |
| [ADR-0017](0017-identidade-sessoes-e-limites.md) | Identidade: senhas, tokens, cookies, e-mail e limites de tentativa | Aceita (etapa 02) |
| [ADR-0018](0018-loja-equipe-catalogo.md) | Loja, equipe e catálogo: limites entre módulos e dependências de etapas futuras | Aceita (etapa 03) |
| [ADR-0019](0019-fila-transacional.md) | Fila transacional: travas, ordem FIFO, senhas e estimativa | Aceita (etapa 04) |
| [ADR-0020](0020-comanda-atendimento-e-outbox.md) | Comanda, atendimento e outbox de eventos | Aceita (etapa 05) |
| [ADR-0021](0021-sse-publicador-hub-e-cursor.md) | SSE: publicador, distribuidor por processo e cursor | Aceita (etapa 06) |
