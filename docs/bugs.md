# Registro de defeitos (etapas 11–13 e revisão)

Defeitos achados pelos testes e exercícios, com gravidade, como reproduzir e situação. Corrigido só conta quando há teste que falharia sem a correção.

## Gravidade

| Gravidade | Significa |
|---|---|
| **Crítica** | dinheiro errado, acesso a dado de outra pessoa ou loja, perda de dados confirmados — impede o piloto |
| **Alta** | dado pessoal guardado além do prometido, ação importante sem funcionar, risco financeiro contornável |
| **Média** | tela desatualizada ou indisponibilidade curta, sem perda de dado |
| **Baixa** | incômodo, mensagem ruim, problema só de ambiente de teste |

## Defeitos encontrados

| Id | Gravidade | Defeito | Como reproduzir (antes da correção) | Situação |
|---|---|---|---|---|
| B-13-01 | **Alta** (privacidade) | Depois de uma exclusão de conta **concluída**, nome e nick da pessoa ainda ficavam por até 24 h: (a) nos eventos de tempo real da equipe (`event_log`), (b) nas respostas guardadas para repetição: as da própria pessoa (nome, e-mail) e as da equipe que a atendeu (nick) (`idempotency_record`) | Conta com visita atendida e reserva; pedir exclusão; worker conclui; procurar o nome em todas as colunas do banco → `event_log.payload` (20 linhas) e `idempotency_record.response_body` (5 linhas) | **Corrigido** (etapa 13): a anonimização troca nome, e-mail e nicks por "Cliente" nesses registros e apaga as respostas guardadas da própria pessoa. Teste: `TestDeletionLeavesNoPersonalDataAnywhere`, que varre as 135 colunas de texto/JSON/binárias |
| B-13-02 | **Alta** (integridade/privacidade; achado na revisão da etapa 13, regressão da correção de B-13-01) | A limpeza de B-13-01 trocava o **texto** do nome, e-mail e nicks da pessoa excluída por "Cliente" em **qualquer** lugar dos eventos e respostas guardados. Se o nome coincidisse com outro valor (outra pessoa com o mesmo nick, ou um nick igual a um estado como "waiting"), os dados de terceiros eram corrompidos: estados viravam "Cliente" e o nick de outra cliente sumia na tela da equipe e nas repetições | Duas contas com o nome "waiting" na mesma fila; a equipe chama; a dona cancela uma; ela pede exclusão; o worker conclui → 18 registros com `"status": "Cliente"` em `event_log`/`idempotency_record` | **Corrigido** (revisão): a troca agora é por **identidade**: só o campo `nick` de objetos cujo `id`/`participation_id` é uma visita da pessoa, e `holder_display_name` de reservas dela (`internal/platform/jsonscrub`). Testes: `TestDeletionScrubTouchesOnlyTheDeletedPerson` (integração), `TestRewriteOnlyTheTargetPerson` (unidade) e `TestDeletionLeavesNoPersonalDataAnywhere`. Mutações conferidas: sem troca → falha a varredura; troca sem olhar o id → falha o teste de escopo |
| B-13-03 | Média (privacidade, janela de milissegundos; achado na revisão por leitura de código) | Corrida entre a exclusão e o publicador de eventos: o publicador lê a outbox **sem trava**; se a exclusão apagasse ou limpasse uma linha nesse meio-tempo, a versão antiga (com o nick ou a visão da própria pessoa) ia para o `event_log` **depois** da limpeza e ficava até 24 h | Determinística no teste: transação de limpeza aberta, publicador rodando em paralelo → publicava a versão antiga | **Corrigido**: a exclusão segura a trava do publicador (`event_stream_state`) antes de mexer nos eventos (`outbox.HoldPublisher`), na mesma ordem de travas do publicador (sem impasse). Teste: `TestHoldPublisherSerializesCleanup`; mutação (tirar a trava) → o teste falha |
| B-12-01 | Média | A posição na fila ("você é o 4º") não andava sozinha quando alguém da frente era chamado; só com "Atualizar" | Duas pessoas na fila, tela da segunda aberta; chamar a primeira → a segunda continua "2º" | **Corrigido** (etapa 12). Teste: `stream.test.tsx` "a posição anda…"; exercício de implantação |
| B-12-02 | Média | Implantar uma versão nova recriava o contêiner do Caddy: a porta pública ficava fechada ~3 s | Tráfego contínuo durante `implantar.sh` → 12 de 37 leituras falharam (conexão recusada) | **Corrigido**: o Caddy fica no ar; o site é publicado num volume. Exercício repetido: 0 falhas |
| B-12-03 | Média | Quem só olha a página da loja (sem conta nem visitante) contava como "uma credencial" por IP: no máximo 6 pessoas no mesmo Wi-Fi | 7 navegadores anônimos no mesmo IP abrindo a loja → o 7º recebe 429 no tempo real | **Corrigido**: para anônimos vale só o teto por IP (100). Teste: `TestStreamTimeoutsHeartbeatAndLimits` |
| B-12-04 | Baixa | A configuração recusava as variáveis de implantação (`FILA_IMAGEM_*` e outras) no terminal do operador | Rodar `fila-admin` com o ambiente do `implantar.sh` carregado | **Corrigido**. Testes: `TestDeployVarsAreIgnored`, `TestDeployVarsMatchDeployFiles` |
| B-12-05 | Baixa | `implantar.sh` podia ficar preso na conferência final sem limite de tempo | Porta pública que aceita conexão e não responde (visto com o IPv6 do Docker Desktop) | **Corrigido** (etapa 13): `curl --max-time 5`, opção IPv4 |
| B-11-01 | Média | Comanda desatualizada na tela do cliente quando participação e comanda mudavam juntas | Concluir atendimento: a tela mostrava "concluído" e a comanda "Aberta" | **Corrigido** (etapa 11), com teste |
| B-11-02 | Média | Sem internet, a tela não avisava que o tempo real tinha parado | Desligar a rede do aparelho | **Corrigido** (etapa 11), com teste |
| B-11-03 | Baixa | Erro antigo ficava ao lado do QR do Pix depois de a rede voltar | Rede caindo na geração do Pix | **Corrigido** (etapa 11), com teste |
| B-11-04 | Baixa | Cliente que saía no meio de um pedido virava "erro 500" no log | Fechar a aba durante uma requisição | **Corrigido** (etapa 11): 499 informativo |

### Configurações, fotos, Google e QR (ADR-0028)

| Id | Gravidade | Defeito | Como reproduzir | Situação |
|---|---|---|---|---|
| B-28-01 | Média (acessibilidade/layout) | Em 320 px, "Minha conta" tinha rolagem horizontal: o campo de arquivo tem largura própria maior que a coluna | Celular 320 px → `/conta` com fotos ligadas → `scrollWidth` 443 > 320 | **Corrigido** (`w-full min-w-0` no campo). Teste e2e `320 px: sem rolagem horizontal nas telas novas` e varredura 320–1440 px |
| B-28-02 | Média (layout) | Em 768 px, a prévia de identidade e o cartão do QR ficavam lado a lado e estouravam a coluna da gestão | Tablet 768 px → Configurações da loja → `scrollWidth` 771 | **Corrigido**: lado a lado só a partir de 1280 px. `e2e/telas.spec.ts` deixou de acusar a tela |
| B-28-03 | Média (desempenho) | Foto 12 MP com EXIF de rotação: o worker girava a imagem inteira pixel a pixel (5,6 s e ~137 MiB por imagem) | `MEDIA_FOTO=foto-12mp.jpg go test ./internal/midia -run Measured -v` | **Corrigido**: recorte e redução no original e rotação só da variante; variantes menores a partir da maior (1,4 s, ~42 MiB). Teste de correção das 8 orientações: `TestProcessAppliesOrientationEveryCase` |
| B-28-04 | Baixa (anterior a este trabalho) | Página da loja no celular desloca o layout ao carregar filas e serviços (CLS ≈ 0,17–0,20), com ou sem imagens | Build de produção, 390 px, `PerformanceObserver('layout-shift')` | **Aberto**: não faz parte deste escopo. As imagens não contribuem (medido: 0). Sugestão: reservar a altura das áreas de filas e serviços enquanto carregam |
| B-28-05 | Baixa (testes, anterior a este trabalho) | Na suíte e2e existente, 5 testes falham também na revisão de partida neste ambiente: 3 dependem do simulador de Pix (`E2E_PIX_CONTROL`), `telas do cliente` procura "Seus pontos nesta loja" na aba Fila (o redesign moveu para a aba Pontos) e `telas da dona` esgota o tempo nas capturas | Rodar `e2e/telas.spec.ts` e `e2e/jornadas.spec.ts` sem simulador de Pix, na revisão `5bc96f0` | **Aberto**: mesmas falhas antes e depois (comparado lado a lado) |

## Defeitos dos próprios testes (etapa 13 e revisão)

| Id | Defeito | Situação |
|---|---|---|
| T-R-01 | A ferramenta de rastreabilidade creditava um id de cenário citado em comentário **dentro** de um teste ao teste **seguinte** (C-CMD-05 e C-ATD-02 apareciam em `TestCompleteFreesChair`, que não os confere) | Corrigido: comentário mais recuado que a abertura do teste conta para o próprio teste; função auxiliar não herda o teste anterior. Os dois cenários continuam cobertos, agora pelo teste certo (`TestChangeRequests`) |
| T-R-02 | Quatro cenários estavam listados como cobertos, mas os testes não conferiam todas as frases: C-EST-04 (chamar com fila pausada, fechar depois de cancelar, concluir após fechar), C-EXC-04 (alerta de **monitoramento**, só a tela era testada), C-FID-07 (ausência de campo de desconto), C-SSE-01 (transação desfeita sem evento: o teste existia, mas não estava ligado) | Corrigido: os testes agora conferem cada frase do cenário |
| T-13-01 | O roteiro do piloto resolvia "a primeira" pendência da tela quando havia duas; no Edge, a lista se atualizava em outra ordem e o teste falhava | Corrigido: o teste mira o cartão do Pix tardio e confere a nota no servidor. Passa no Chrome e no Edge |
| T-13-02 | O teste que confere as variáveis de implantação lia nomes sem dígitos (`FILA_CURL_IPV4` virava `FILA_CURL_IPV`) | Corrigido na expressão; pacote reexecutado |
| T-13-03 | A 1ª versão da varredura de autorização chamava "sair" como dado próprio e deslogava os intrusos, que passavam a receber só 401 | Corrigido: só leituras próprias são chamadas, e o teste exige que cada intruso continue logado e que haja 403/404 |

## Achados que NÃO são defeito (conferidos)

| Achado | Conclusão |
|---|---|
| Na varredura de autorização, "minha visita", "meus pontos", "catálogo de prêmios" e listas de filas e comandas responderam 200 para os intrusos | Devolvem só os dados do próprio chamador ou uma lista filtrada; a varredura confere que nenhum id alheio aparece |
| 19 rotas validam o corpo antes da autorização (respondem 400 com corpo vazio) | Não vaza nada; com corpo válido e a versão real do recurso, todas recusam (403/404) — provado na varredura |
| E-mails nunca entregues (envio desligado em produção) | Vencem em 48 h e o conteúdo cifrado é apagado (`maintenance`) |
| `govulncheck`: GO-2026-5932 em `golang.org/x/crypto/openpgp` | Pacote não importado pelo projeto (só Argon2 do x/crypto); correção só em versão de desenvolvimento. Acompanhar no Dependabot |
| 503 de "servidor cheio" no SSE aparece como erro no log | Proposital: é serviço degradado e deve contar nos alertas |

## Riscos abertos (não são defeitos do código)

Lista completa em [evidencias.md](evidencias.md) §8. Os que bloqueiam o piloto em produção:

1. **Sem provedor de e-mail:** em produção, convites de equipe, confirmação de conta e redefinição de senha **não chegam**. Sem isso, nem a dona consegue ativar o acesso. Bloqueio externo: contratar provedor e configurar credenciais.
2. **AbacatePay real (sandbox) não validado**, por falta de chave de teste. Até lá, piloto só com dinheiro e cartão (Pix desligado).
