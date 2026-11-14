# Rotinas de operação

Passo a passo para cada situação. Cada alerta (`deploy/monitoramento/alertas.yml` e `fila-admin verificar`) aponta para uma seção daqui. Os comandos usam o atalho:

```bash
DC="docker compose -f deploy/compose.yaml --env-file /etc/fila/producao.env --profile ferramentas --profile banco-local"
```

**Papéis**

- **Operador:** quem cuida do servidor; hoje, o responsável técnico.
- **Dono da loja:** decide sobre dinheiro e atendimento. Vê as pendências em Gestão → Pendências.

Nenhuma rotina apaga dados. Em dúvida, **trave o worker** (`fila-admin manutencao iniciar`): a API continua atendendo e nada é feito por trás até você soltar.

---

<a id="incidente"></a>
## 1. Incidente: algo está errado e não sei o quê

1. **Registre a hora** e o que se vê: tela, mensagem e o código da requisição (`X-Request-Id`, que aparece nas telas de erro).
2. **Veja o estado:**
   - `$DC run --rm fila-admin verificar -backups /backups` mostra os alertas ativos e o que fazer;
   - `$DC ps` mostra quem está de pé.
3. **Logs:**
   - `$DC logs --since 30m api worker | grep '"level":"ERROR"'`;
   - procure pelo `request_id` do usuário.

   Os logs não têm e-mail, nome, nick, senha nem token. Mesmo assim, não os copie para fora do servidor sem necessidade.
4. **Se há risco a dinheiro ou a dados** (pagamento sendo marcado errado, dados de uma loja aparecendo em outra):
   - trave o worker: `$DC run --rm fila-admin manutencao iniciar -motivo "incidente <hora>"`;
   - se for preciso, pare a API: `$DC stop api`. O site mostra indisponível; as filas ficam no banco e nada se perde.
5. **Avise o dono da loja:** o atendimento segue no balcão. Dinheiro e cartão são registrados depois.
6. **Corrija** com uma implantação normal, de correção ou de volta à versão anterior (seção [Falha de migração](#falha-de-migracao)), e solte a trava.
7. **Depois, escreva um resumo:** o que aconteceu, o impacto, a causa e o que muda para não repetir. Sem dados pessoais no resumo.

<a id="api-fora"></a>
## 2. API fora do ar (`FilaApiForaDoAr`)

1. Rode `$DC ps api` e `$DC logs --tail 50 api`.
2. `configuração inválida`: um segredo ou variável mudou. A mensagem lista as variáveis e nunca mostra valores. Corrija o arquivo em `/etc/fila` e rode `$DC up -d api`.
3. `banco inacessível`: veja a seção [Banco fora](#banco-fora).
4. Contêiner reiniciando em loop: confira a memória e o disco (`df -h`, `free -m`). A API sobe mesmo sem banco (`/healthz/ready` fica 503), então um loop de reinício é sinal de configuração.

<a id="banco-fora"></a>
## 3. Banco fora do ar

1. Rode `$DC ps postgres` e `$DC logs --tail 50 postgres`.
2. Disco cheio é a causa mais comum. Libere espaço sem apagar backups recentes nem o diário de exclusões.
3. Quando o banco volta, API e worker se recuperam sozinhos: `/healthz/ready` volta a 200 e as tarefas retomam.
4. Se o banco não volta (arquivos corrompidos), siga [Restauração](#restauracao).

<a id="worker-parado"></a>
## 4. Worker parado ou atrasado

Alertas: `FilaWorkerForaDoAr`, `FilaWorkerSemSinal`, `FilaOutboxAtrasada`, `FilaExclusaoSemProcessamento`, `FilaReservaVencidaPresa`, `FilaIndicadoresSemLeitura`.

O efeito é que as telas não se atualizam sozinhas, o Pix não é confirmado e e-mails, exclusões e vencimentos de reserva param. **Nada se perde:** tudo fica no banco e é feito quando o worker volta (cada tarefa é retomável e não repete efeito).

1. Rode `$DC ps worker` e `$DC logs --tail 100 worker`.
2. **Travado de propósito?** `$DC run --rm fila-admin restauracao status`:
   - trava de manutenção esquecida → `manutencao encerrar`;
   - trava de restauração → seção [Restauração](#restauracao).
3. **Parado:** `$DC up -d worker`. Em até 1 minuto `verificar` não deve mostrar mais o alerta.
4. **Reserva vencida presa ou exclusão sem processamento com o worker de pé:** procure `ERROR` no log da tarefa correspondente (`"tarefa":"fidelidade"` ou `"tarefa":"exclusao"`).

<a id="erros-5xx"></a>
## 5. Erros do servidor (`FilaErros5xx`)

1. Rode `$DC logs --since 15m api | grep '"status":5'` e veja a rota (`route`) e o `request_id`.
2. Procure o `ERROR` com o mesmo `request_id`, que mostra a causa sem dados pessoais.
3. Se começou com uma implantação, volte à versão anterior (seção [Falha de migração](#falha-de-migracao), item 1).

<a id="lentidao"></a>
## 6. Lentidão ou capacidade

Alertas: `FilaLatenciaAlta`, `FilaSSEPertoDoTeto`, `FilaPoolDoBancoCheio`.

1. Veja CPU, memória e disco (`docker stats --no-stream`) e o pool do banco (`fila_db_pool_acquired_connections` perto de `fila_db_pool_max_connections`).
2. Pool cheio: aumente `FILA_DB_MAX_CONNS`, respeitando o `max_connections` do PostgreSQL (padrão 100), e rode `$DC up -d api worker`.
3. Conexões SSE perto do teto: é tráfego real ou robô? Veja `FilaAbusoLimiteGeral`. Os limites verificados estão em [evidencias.md](evidencias.md).

<a id="abuso"></a>
## 7. Abuso (`FilaAbusoLimiteGeral`)

1. O limite por IP já recusa o excesso (429). Veja se o site continua respondendo para os outros.
2. Se for ataque de volume que o servidor não aguenta, o bloqueio por IP fica no firewall do servidor ou no provedor de hospedagem. **Não há firewall de aplicação configurado** (capacidade externa).
3. Tentativas de senha, código e cadastro já têm limite próprio no banco.

<a id="provedor-indisponivel"></a>
## 8. Provedor de Pix indisponível

Alertas: `FilaPixDesconhecido`, `FilaEstornoParado`.

Sinais: tentativas Pix "desconhecidas" acumulando e estornos sem confirmação.

1. **Confira a página de status da AbacatePay** e teste do servidor: `curl -sS -o /dev/null -w '%{http_code}' https://api.abacatepay.com/v2/transparents/list` (401 sem chave = está no ar).
2. **Oriente a loja:** receber em **dinheiro ou cartão**, pela troca de meio na comanda. O Pix ainda válido continua valendo: se o cliente pagar os dois, o sistema registra os dois e abre pendência de devolução. Nada é apagado.
3. **Nada a fazer no sistema.** O worker reconsulta sozinho, com espera crescente, e só confirma Pix por consulta autenticada. Quando o provedor volta, os pagamentos aparecem.
4. **Depois de 12 consultas sem resposta**, a pendência `charge_outcome_unresolved` aparece para o dono. Ele confere no painel da AbacatePay e resolve com nota.

<a id="divergencia-financeira"></a>
## 9. Divergência financeira (`FilaDivergenciaFinanceiraAntiga`)

**Responsável: dono da loja.** Em Gestão → Pendências, cada pendência diz o que fazer: devolver excesso, conferir o valor ou registrar o estorno. O operador só acompanha. Pendência resolvida exige nota e nunca apaga recebimento.

<a id="exclusao-atrasada"></a>
## 10. Exclusão de conta perto da meta (`FilaExclusaoPertoDaMeta`)

A meta operacional é de 30 dias; não é prazo legal.

1. Gestão → Exclusões (dono) mostra o que segura a exclusão: atendimento em curso, pagamento pendente ou fila do profissional.
2. **O dono resolve o bloqueio**; o worker conclui sozinho na rodada seguinte.

<a id="email"></a>
## 11. E-mail parado ou falhando

Alertas: `FilaEmailParado`, `FilaEmailFalhou`.

**Hoje não há provedor de e-mail contratado** (bloqueio externo). Em produção, `FILA_MAIL_MODE=disabled`: verificação de conta, redefinição de senha e convites **não chegam**. Na homologação, os e-mails viram arquivos.

Com provedor configurado (etapa futura): confira as credenciais e a cota. O worker tenta 8 vezes com espera e desiste depois de 48 h sem guardar o conteúdo.

<a id="backup"></a>
## 12. Backup atrasado ou ausente

Alertas: `FilaBackupAtrasado`, `FilaBackupAusente`.

1. Rode `$DC run --rm backup` à mão e leia a saída.
2. Falhas comuns:
   - disco cheio: libere espaço sem apagar backups da última semana;
   - senha do papel `fila_backup` trocada: arquivo `segredos/backup/pg_senha_backup`;
   - chave de cifra ausente: `segredos/backup/backup_chave`.
3. Confira o agendamento (cron) descrito em [backup-restauracao.md](backup-restauracao.md).

<a id="restauracao"></a>
## 13. Restauração de backup

Passo a passo completo, com tempos medidos: [backup-restauracao.md](backup-restauracao.md).

**Resumo:**

1. `deploy/restaurar.sh <ambiente> <backup> "<motivo>"` restaura, migra, trava o worker e sobe a API.
2. `$DC run --rm fila-admin restauracao reconciliar -diario /var/lib/fila/diario`.
3. O **dono** confere as pendências `restored_*`.
4. `$DC run --rm fila-admin restauracao liberar`.

<a id="manutencao"></a>
## 14. Manutenção planejada (`FilaWorkerEmManutencaoLonga`)

1. Combine o horário com o dono: fora do horário de atendimento.
2. `$DC run --rm fila-admin manutencao iniciar -motivo "<o quê>"`. As tarefas param em até 10 s e a API segue no ar.
3. Faça a manutenção (ex.: atualizar o sistema operacional, trocar disco).
4. `$DC run --rm fila-admin manutencao encerrar`. As tarefas voltam e fazem o que ficou parado.

O alerta dispara se a trava passar de 2 horas: provavelmente alguém esqueceu de soltar.

<a id="falha-de-migracao"></a>
## 15. Falha de migração ou implantação ruim

**Política de migração:**

- só para frente;
- cada migração é compatível com a versão ANTERIOR da aplicação (só acrescenta; remover algo exige duas versões);
- não há "down" automático em produção.

**A implantação falhou ANTES de trocar a API** (a migração deu erro):

- A migração roda numa transação: o banco fica como estava (`migrate status` mostra a última versão aplicada). A versão antiga continua no ar.
- Corrija a migração **numa nova versão** (migração aplicada nunca é editada) e implante de novo.

**A nova versão está ruim, mas a migração foi aplicada:**

- Volte a APLICAÇÃO: `FILA_SEM_BUILD=1 deploy/implantar.sh /etc/fila/producao.env <versao-anterior>`. O esquema novo é compatível com a versão anterior.

**A migração estragou dados** (raro; a revisão de código existe para isso):

1. Trave: `fila-admin manutencao iniciar`.
2. Restaure o backup feito **pelo próprio `implantar.sh` antes de migrar**: [Restauração](#restauracao). A conciliação com o provedor recupera pagamentos feitos depois desse backup.
3. Implante a versão anterior.

<a id="rotacao-de-segredo"></a>
## 16. Troca (rotação) de segredo

Os segredos são arquivos em `/etc/fila/segredos/<grupo>/`. Troque o arquivo e reinicie quem o usa. Nunca coloque segredo em variável de ambiente no compose, no `.env` do repositório ou em log.

| Segredo | Como trocar | Efeito |
|---|---|---|
| **Chave do servidor** (`app/secret_key`) | 1. Gere uma nova: `fila-admin gerar-chave`.<br>2. Ponha a ATUAL em `FILA_SECRET_KEY_PREVIOUS` (via `app/secret_key_previous` + `FILA_SECRET_KEY_PREVIOUS_FILE` no compose) e a nova em `secret_key`.<br>3. `$DC up -d api worker`.<br>4. **Depois de 30 dias**, remova a anterior. | Ninguém perde nada: o já emitido (códigos de visitante, protocolos de exclusão, e-mails na fila) continua reconhecido pela chave anterior. Protocolos de exclusão mais antigos que a remoção da anterior deixam de ser consultáveis (a exclusão segue normalmente). Teste: `TestSecretKeyRotationKeepsIssuedCredentials`. |
| **Senha do papel da aplicação** (`fila_app`) | 1. `ALTER ROLE fila_app PASSWORD '<nova>'` (como `fila_dba`).<br>2. Atualize `app/database_url` e `banco/pg_senha_app`.<br>3. `$DC up -d api worker`. | Entre o passo 1 e o 3, novas conexões falham. Faça fora do horário: dura segundos. |
| **Senhas `fila_migrador` / `fila_backup`** | Igual, atualizando `migrador/database_url` ou `backup/pg_senha_backup`. | Nenhum efeito no atendimento. |
| **Chave da AbacatePay** | Gere a nova no painel da AbacatePay, troque `app/abacatepay_api_key`, rode `$DC up -d api worker` e **depois** revogue a antiga no painel. | Cobranças antigas continuam consultáveis (mesma conta). |
| **Secret do webhook** | Troque no painel da AbacatePay e em `app/abacatepay_webhook_secret` ao mesmo tempo. | Avisos com o secret antigo recebem 401 e não são repetidos. O worker confirma os Pix pela consulta, então nada se perde. |
| **Chave de cifra dos backups** | **Não descarte a antiga** enquanto houver backup cifrado com ela (retenção). Guarde-a junto com os backups antigos, num cofre fora do servidor. | Restaurar um backup antigo exige a chave daquela época. |

<a id="verificacao-periodica"></a>
## 17. Verificação diária (5 minutos)

- `fila-admin verificar -backups /backups` sai com 0. Em agendamento (cron), saída 3 deve avisar o operador.
- Ver no painel do dono se há pendências antigas.
- Uma vez por mês: exercício de restauração em ambiente isolado ([backup-restauracao.md](backup-restauracao.md)).
