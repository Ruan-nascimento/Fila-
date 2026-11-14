# Registro de defeitos (etapas 11–13)

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
| B-12-01 | Média | A posição na fila ("você é o 4º") não andava sozinha quando alguém da frente era chamado; só com "Atualizar" | Duas pessoas na fila, tela da segunda aberta; chamar a primeira → a segunda continua "2º" | **Corrigido** (etapa 12). Teste: `stream.test.tsx` "a posição anda…"; exercício de implantação |
| B-12-02 | Média | Implantar uma versão nova recriava o contêiner do Caddy: a porta pública ficava fechada ~3 s | Tráfego contínuo durante `implantar.sh` → 12 de 37 leituras falharam (conexão recusada) | **Corrigido**: o Caddy fica no ar; o site é publicado num volume. Exercício repetido: 0 falhas |
| B-12-03 | Média | Quem só olha a página da loja (sem conta nem visitante) contava como "uma credencial" por IP: no máximo 6 pessoas no mesmo Wi-Fi | 7 navegadores anônimos no mesmo IP abrindo a loja → o 7º recebe 429 no tempo real | **Corrigido**: para anônimos vale só o teto por IP (100). Teste: `TestStreamTimeoutsHeartbeatAndLimits` |
| B-12-04 | Baixa | A configuração recusava as variáveis de implantação (`FILA_IMAGEM_*` e outras) no terminal do operador | Rodar `fila-admin` com o ambiente do `implantar.sh` carregado | **Corrigido**. Testes: `TestDeployVarsAreIgnored`, `TestDeployVarsMatchDeployFiles` |
| B-12-05 | Baixa | `implantar.sh` podia ficar preso na conferência final sem limite de tempo | Porta pública que aceita conexão e não responde (visto com o IPv6 do Docker Desktop) | **Corrigido** (etapa 13): `curl --max-time 5`, opção IPv4 |
| B-11-01 | Média | Comanda desatualizada na tela do cliente quando participação e comanda mudavam juntas | Concluir atendimento: a tela mostrava "concluído" e a comanda "Aberta" | **Corrigido** (etapa 11), com teste |
| B-11-02 | Média | Sem internet, a tela não avisava que o tempo real tinha parado | Desligar a rede do aparelho | **Corrigido** (etapa 11), com teste |
| B-11-03 | Baixa | Erro antigo ficava ao lado do QR do Pix depois de a rede voltar | Rede caindo na geração do Pix | **Corrigido** (etapa 11), com teste |
| B-11-04 | Baixa | Cliente que saía no meio de um pedido virava "erro 500" no log | Fechar a aba durante uma requisição | **Corrigido** (etapa 11): 499 informativo |

## Defeitos dos próprios testes (etapa 13)

| Id | Defeito | Situação |
|---|---|---|
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
