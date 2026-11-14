# Evidências da etapa 12: o que foi verificado, onde e com que números

Tudo aqui foi **executado** em 2026-11-14. O que não foi executado está na seção 6.

## 1. Ambiente usado

| Item | Valor |
|---|---|
| Máquina | notebook Windows 11, Intel Core i5-1155G7 (4 núcleos), 16 GB |
| Docker | Docker Desktop 29.9.3 (8 CPUs e 7,6 GB para os contêineres) |
| Homologação | `deploy/compose.yaml` com `FILA_ENV=homologacao`, Caddy com TLS interno em `https://localhost:8443`, PostgreSQL 18.7 com TLS obrigatório, papéis `fila_migrador`/`fila_app`/`fila_backup`, segredos em arquivos gerados na hora (fora do repositório) |
| Pix | simulador local (mesmo formato da API v2 da AbacatePay) com chave de teste; **a AbacatePay real (sandbox ou produção) não foi usada**, porque não há chave disponível |
| Imagens | `fila-backend` (distroless, 78 MB), `fila-web` (publicação do site), `caddy:2.11.9-alpine`, `postgres:18.7`, `prom/prometheus:v3.15.1`, todas fixadas por digest |

**Não é o servidor de produção:** cliente e servidor na mesma máquina, e o Docker Desktop encaminha portas do Windows para os contêineres. Os números servem como referência; refaça no servidor real.

## 2. Implantação, TLS, segredos e banco

| Verificação | Resultado |
|---|---|
| Primeira implantação pelo `deploy/implantar.sh` (banco vazio → 16 migrações → permissões → API → worker → site → saúde pela porta pública) | ok em 11 s |
| TLS no site | `https://localhost:8443` com HSTS, CSP, X-Frame-Options, nosniff, Referrer-Policy; o cabeçalho `Server` foi removido |
| Banco sem TLS | recusado (`pg_hba.conf rejects connection … no encryption`); a aplicação conecta com TLS 1.3 |
| Papel da aplicação | `CREATE TABLE` negado (`permission denied for schema public`); `migrate permissoes -verificar` limpo. **Toda a suíte de integração** (backend) passa conectada com um papel assim, localmente e no CI |
| Navegador real na homologação (Chrome 157 e Edge 157) | 18/18 (21 telas × 6 larguras + 4 jornadas), passando por Caddy, TLS, CSP e o papel restrito; **nenhuma violação de CSP** |
| Logs depois da suíte inteira (API 824 linhas, worker, Caddy) | 0 `ERROR`, 0 respostas 5xx, nenhum e-mail, token, senha, chave ou nick |
| Produção com Pix simulado | a imagem recusa subir (saída 2): chave de teste e endereço que não é o oficial. Teste local e no CI (job "Implantação") |

## 3. Implantar sem perder a fila (`operacao.spec.ts`, teste 1)

1. Loja com 3 pessoas na fila pela API e uma 4ª (Lia) pelo **celular no navegador**.
2. Implantação de uma versão nova com tráfego contínuo de leitura.
3. No meio da troca, Beto chama e inicia o primeiro atendimento: comandos repetidos com a mesma chave até dar certo.

| Medida | Resultado |
|---|---|
| Duração da implantação | 15 s (backup 1 s, migrações, troca da API em ~6 s, worker, site) |
| Leituras durante a troca | 40, **0 falhas** (todas 200); a mais lenta levou 4,2 s, segurada pelo Caddy enquanto a API trocava |
| Fila | ninguém sumiu: o primeiro em atendimento, os outros aguardando |
| Tela da Lia | passou de "4º" para "3º" **sozinha**, sem "Reconectando" |

O exercício também **achou dois problemas**, corrigidos nesta etapa:

- A posição na fila não atualizava sozinha quando alguém da frente era chamado (defeito das etapas 06/11). Correção na tela da visita, com teste.
- A primeira versão do script trocava o contêiner do Caddy e deixava a porta pública fechada por ~3 s (12 leituras falharam). Agora o Caddy fica no ar e só o site é publicado.

## 4. Worker morto no meio do trabalho (`operacao.spec.ts`, teste 2)

O worker foi morto com `SIGKILL` (sem encerramento). Com ele morto:

- um Pix foi pago no provedor;
- uma reserva de prêmio venceu;
- uma conta pediu exclusão;
- um e-mail de redefinição de senha foi pedido.

Nada andou. Ao religar:

| Medida | Resultado |
|---|---|
| Pix confirmado | 16,2 s depois de religar |
| Tudo em dia (Pix, pontos da reserva devolvidos, exclusão concluída, e-mail entregue) | 16,3 s |
| Efeito único | 1 recebimento para o Pix, 1 liberação para a reserva |

## 5. Backup e restauração (`operacao.spec.ts`, teste 3, e medição com volume)

| Medida | Banco pequeno (homologação) | Banco com ~1 GB (2 milhões de linhas sintéticas de auditoria) |
|---|---|---|
| Backup (dump, conferência, cifra, HMAC) | 1,4 s (327 KB cifrado) | 15 s (85 MB cifrado) |
| Restauração do banco (pg_restore) | 1 s | 33 s |
| **API no ar** depois de começar a restaurar | **14 s** | **51 s** |
| Conciliação | 1 s | ~2 s |
| **Worker liberado** | **17 s** | **54 s** |

Resultado da conciliação no exercício:

- 1 pagamento registrado, com a tentativa recriada a partir do provedor;
- 1 exclusão reaplicada pelo diário, com o mesmo protocolo e concluída depois de liberar;
- 56 sessões encerradas;
- época do SSE trocada.

A entrada na fila feita depois do backup **não existe** depois de restaurar: é a perda (RPO) esperada.

**Monitoramento:** com o worker travado, `fila-admin verificar` saiu com 3 e `FilaWorkerTravadoRestauracao`.

## 6. Monitoramento e capacidade

| Verificação | Resultado |
|---|---|
| Prometheus coletando API e worker | `up` nos dois; métricas de requisições e da trava presentes |
| Alerta real | worker parado → `FilaWorkerForaDoAr` (crítico, operador, rotina worker-parado) disparou em **140 s** (regra de 2 min + coleta) e sumiu ao religar |
| Limite por IP (produção, 600/min) | carga de um único IP por 20 s: 498 atendidas (rajada + taxa), **87 378 recusadas com 429**, 0 erros de rede, servidor pronto depois |
| Leituras com limite aberto (só para medir) | **1 806 req/s** de leitura da fila pública com 50 clientes; p50 26 ms, p95 40 ms, p99 53 ms; 0 erros |
| SSE simultâneas (pela rede interna do Docker, via Caddy) | **1 900 abertas, 0 falhas**; API 204 MB, Caddy 294 MB; saúde respondendo em 26 ms durante a carga |
| Acima do teto (2 100 pedidas) | 2 000 abertas, **100 recusadas com 503**, como configurado |
| Limitação **do ambiente** | vindas do Windows pelo encaminhamento de portas do Docker Desktop, só ~508 conexões abriram e novas conexões TLS falharam. Pela rede interna, 1 900 funcionaram: é limite do Docker Desktop, não da aplicação. **Medir no servidor real.** |

Também nesta medição: quem só olha a página da loja (sem credencial) era limitado a 6 conexões **por IP**, ou seja, 6 pessoas no Wi-Fi da barbearia. Corrigido: para anônimos vale só o teto por IP (100).

## 7. Testes automatizados da etapa

| Tipo | O que prova |
|---|---|
| Integração (banco real) | troca de chave sem perder códigos e protocolos; tetos SSE; trava, cópia de banco detectada e liberação só com conciliação; supervisor para e religa tarefas; worker travado não faz nada; restauração ponta a ponta com o simulador (órfã, estorno, tentativa que nunca chegou, sessões, diário, repetição); comandos do operador com auditoria; papel restrito; indicadores e alertas |
| Unidade | métricas no formato Prometheus; limite por IP; segredos por arquivo; homologação × produção; Pix oficial só em produção; listagem paginada da AbacatePay; paridade alertas × Prometheus |
| Componente (Vitest) | pendência sem comanda; posição anda com o evento público |
| Mutação | 35 defeitos introduzidos de propósito nas partes críticas (34 no backend: chaves, segredos, limites, métricas, trava, conciliação, diário, alertas; 1 no frontend: posição na fila). Todos foram detectados; 4 só depois de reforçar os testes que os deixavam passar |
| CI | tudo acima, mais a suíte inteira com o papel restrito; shellcheck; compose; promtool; build das imagens; site sem segredo; imagem recusa produção com Pix simulado; executáveis sem pacote de teste |

## 8. O que NÃO foi verificado

- O **servidor de produção** (não existe ainda): números de capacidade e de recuperação lá.
- **TLS público (Let's Encrypt):** só o TLS interno foi exercitado. O ACME depende de domínio e DNS reais.
- **AbacatePay real** (sandbox e produção): conciliação, listagem paginada e webhook só contra o simulador. O checklist de [pagamentos-abacatepay.md](../pagamentos-abacatepay.md) segue pendente.
- **Envio de alertas a pessoas** (Alertmanager e canal): bloqueio externo.
- **Cópia dos backups para fora do servidor:** depende da hospedagem.
- **Banco gerenciado** (sem o perfil `banco-local`) e **restauração física / PITR**: documentadas, não exercitadas.
- **Duas instâncias da API:** não configuradas. Com uma, a troca segura as requisições por alguns segundos.
- Os blocos `Down` das migrações (reversão usada é aplicação anterior + backup).
