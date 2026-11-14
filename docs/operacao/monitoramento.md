# Monitoramento e alertas

## 1. De onde vêm os números

- **API** (`FILA_METRICS_ADDR`, porta interna 9090, nunca publicada):
  - `fila_http_requests_total{route,method,status}`;
  - `fila_http_request_duration_seconds` (latência, sem o SSE);
  - `fila_http_rate_limited_total`;
  - `fila_sse_connections`;
  - pool do banco (`fila_db_pool_*`);
  - processo (`fila_go_*`, `fila_process_start_time_seconds`).
- **Worker** (mesma porta interna): indicadores de negócio lidos do banco a cada 15 s, **mesmo com o worker travado**:
  - atraso da outbox;
  - Pix com resultado desconhecido;
  - pendências financeiras por tipo (divergências);
  - estornos parados;
  - exclusões em processamento e perto da meta;
  - reservas vencidas não liberadas;
  - e-mails parados ou desistidos;
  - trava do worker e sinal de vida.
- **`fila-admin verificar`:** mesma avaliação, para quem não tem Prometheus (saída 3 = alerta). Também confere a idade do último backup (`-backups`).

Os rótulos nunca têm id, IP, e-mail ou caminho real; testado em `TestChainWithLimitsAndObserves`.

## 2. Alertas

Definidos em [`deploy/monitoramento/alertas.yml`](../../deploy/monitoramento/alertas.yml) (validado com `promtool` no CI). O teste `TestPrometheusRulesMatchEvaluate` garante os mesmos nomes e limiares de `fila-admin verificar`, e que cada rotina citada existe em [rotinas.md](rotinas.md).

| Alerta | Quando | Severidade | Responsável | Rotina |
|---|---|---|---|---|
| FilaApiForaDoAr | coleta da API falha por 1 min | crítico | operador | [api-fora](rotinas.md#api-fora) |
| FilaWorkerForaDoAr | coleta do worker falha por 2 min | crítico | operador | [worker-parado](rotinas.md#worker-parado) |
| FilaErros5xx | > 1% de 5xx (e > 0,05/s) por 5 min | crítico | operador | [erros-5xx](rotinas.md#erros-5xx) |
| FilaLatenciaAlta | p95 > 1 s por 10 min | aviso | operador | [lentidao](rotinas.md#lentidao) |
| FilaSSEPertoDoTeto | > 1600 conexões SSE por 5 min | aviso | operador | [lentidao](rotinas.md#lentidao) |
| FilaPoolDoBancoCheio | > 50 esperas por conexão em 5 min | aviso | operador | [lentidao](rotinas.md#lentidao) |
| FilaAbusoLimiteGeral | > 500 recusas por IP em 10 min | aviso | operador | [abuso](rotinas.md#abuso) |
| FilaIndicadoresSemLeitura | worker sem ler o banco há 2 min | crítico | operador | [worker-parado](rotinas.md#worker-parado) |
| FilaWorkerTravadoRestauracao | trava de restauração ativa | crítico | operador | [restauracao](rotinas.md#restauracao) |
| FilaWorkerEmManutencaoLonga | manutenção há > 2 h | aviso | operador | [manutencao](rotinas.md#manutencao) |
| FilaWorkerSemSinal | sinal de vida no banco > 2 min (sem trava) | crítico | operador | [worker-parado](rotinas.md#worker-parado) |
| FilaOutboxAtrasada | evento sem publicar há > 60 s | crítico | operador | [worker-parado](rotinas.md#worker-parado) |
| FilaPixDesconhecido | Pix desconhecido há > 15 min | aviso | operador e dono | [provedor-indisponivel](rotinas.md#provedor-indisponivel) |
| FilaDivergenciaFinanceiraAntiga | pendência financeira aberta há > 3 dias | aviso | dono da loja | [divergencia-financeira](rotinas.md#divergencia-financeira) |
| FilaDivergenciaPosRestauracao | pendência `restored_*` aberta | crítico | operador e dono | [restauracao](rotinas.md#restauracao) |
| FilaEstornoParado | estorno sem confirmação há > 24 h | aviso | operador | [provedor-indisponivel](rotinas.md#provedor-indisponivel) |
| FilaExclusaoSemProcessamento | exclusão sem reavaliação há > 15 min | crítico | operador | [worker-parado](rotinas.md#worker-parado) |
| FilaExclusaoPertoDaMeta | exclusão há ≥ 25 dias | aviso | operador e dono | [exclusao-atrasada](rotinas.md#exclusao-atrasada) |
| FilaReservaVencidaPresa | reserva vencida há > 5 min e não liberada | crítico | operador | [worker-parado](rotinas.md#worker-parado) |
| FilaEmailParado | e-mail esperando há > 15 min | aviso | operador | [email](rotinas.md#email) |
| FilaEmailFalhou | e-mail desistido nas últimas 24 h | aviso | operador | [email](rotinas.md#email) |
| FilaBackupAtrasado / FilaBackupAusente (só `verificar -backups`) | sem backup conferido há > 26 h | crítico | operador | [backup](rotinas.md#backup) |

**Sem Prometheus**, o próprio worker registra no log quando o conjunto de alertas muda (`alertas internos ativos`), e `fila-admin verificar` em agendamento mostra os detalhes.

## 3. O que falta (bloqueio externo)

O **envio** dos alertas para uma pessoa (e-mail, Telegram, SMS) exige um Alertmanager ou serviço equivalente **e as credenciais do canal**. Isso não existe hoje e não foi inventado aqui. Até lá, os alertas aparecem:

- no Prometheus (`http://127.0.0.1:9091/alerts` por túnel SSH);
- no log do worker;
- na saída de `fila-admin verificar`, cujo agendamento pode mandar e-mail pelo próprio servidor.

O dono da loja vê as pendências que são dele em Gestão → Pendências (etapa 09), sem depender disso.

## 4. Verificado

Detalhes em [evidencias.md](evidencias.md):

- o Prometheus coletou API e worker na homologação (`up`);
- `FilaWorkerForaDoAr` disparou 140 s depois de o worker parar, com severidade, responsável e rotina, e voltou ao normal sozinho quando ele voltou;
- os testes de `fila-admin verificar` cobrem: worker nunca visto, backup ausente, backup velho, tudo em dia e manutenção curta.
