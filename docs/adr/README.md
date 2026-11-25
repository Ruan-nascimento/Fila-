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
| [ADR-0022](0022-exclusao-de-conta-e-historico.md) | Exclusão de conta e histórico | Aceita (etapa 07) |
| [ADR-0023](0023-pagamentos-pix-reconciliacao.md) | Pagamentos: Pix em duas fases, reconciliação por externalId e recebimento presencial | Aceita (etapa 08) |
| [ADR-0024](0024-webhook-estorno-pendencias.md) | Webhook autenticado, estorno em três momentos e pendências financeiras | Aceita (etapa 09) |
| [ADR-0025](0025-fidelidade-extrato-reservas.md) | Fidelidade: extrato materializado pelo banco, crédito na liquidação e reservas de 24 h | Aceita (etapa 10) |
| [ADR-0026](0026-operacao-trava-restauracao-metricas.md) | Operação: trava do worker, conciliação pós-restauração, diário de exclusões e métricas internas | Aceita (etapa 12) |
| [ADR-0027](0027-sessao-unica-do-dono-e-dashboard-desktop.md) | Sessão única do dono (vaga por conta), instalação desktop com prova DPoP e módulo analítico somente leitura | Aceita |
| [ADR-0028](0028-configuracoes-fotos-google-identidade-qr.md) | Configurações pessoais, fotos (módulo midia), Google só para cliente (OIDC + PKCE), identidade da loja (16 paletas) e QR Code | Aceita |
| [ADR-0029](0029-mudar-de-lugar-na-fila-e-endereco-acompanha-o-nome.md) | Mudar de lugar na fila (T-P-11) e endereço da loja que acompanha o nome, com endereços antigos válidos | Aceita |
| [ADR-0030](0030-notificacoes-pelo-fluxo-sse.md) | Notificações derivadas do fluxo SSE (sem push), permissão por aparelho, plugin nativo no desktop e aviso de atraso do cliente | Aceita |
| [ADR-0031](0031-painel-do-cliente-e-diretorio-de-barbearias.md) | Painel do cliente (minhas filas em todas as lojas) e diretório de barbearias por distância, com busca e paginação | Aceita |
| [ADR-0032](0032-produtos-simples-e-categorias.md) | Catálogo simples de produtos e categorias do catálogo (1.0.0) | Aceita (substitui parcialmente R-ESC-03) |
| [ADR-0033](0033-quantidades-na-comanda.md) | Quantidades na comanda, execução parcial e grupos numa única participação | Aceita |
| [ADR-0034](0034-configuracoes-da-loja-e-custos-de-recebimento.md) | Configurações da loja por seção e custos de recebimento | Aceita |
| [ADR-0035](0035-entrada-capacidade-e-tolerancia.md) | Entrada na fila separada da operação, capacidade e tolerância configurável | Aceita (substitui R-FILA-09/10/18) |
| [ADR-0036](0036-pesquisa-e-organizacao-das-listas.md) | Pesquisa e organização das listas | Aceita |
| [ADR-0037](0037-desktop-em-segundo-plano.md) | Programa desktop em segundo plano (bandeja, instância única) | Aceita |
| [ADR-0038](0038-agenda-hibrida-e-uma-cadeira.md) | Agenda gratuita, operação híbrida e uma cadeira por profissional (Fila 2.0) | Aceita |
| [ADR-0039](0039-reserva-com-pix-avisos-e-recuperacao.md) | Reserva com Pix (sinal ou taxa), avisos por e-mail e recuperação da reserva (Fila 2.0, parte 2) | Aceita |
| [ADR-0040](0040-agenda-em-escala-indicadores-e-recuperacao.md) | Agenda em escala: leituras sem N+1, invalidação pública, indicadores do dono e recuperação após parada do worker (Fila 2.0, parte 3) | Aceita |
