# ADR-0026 — Operação: trava do worker, conciliação pós-restauração, diário de exclusões e métricas internas

- Situação: Aceita (etapa 12)
- Data: 2026-11-14

## Contexto

A etapa 12 exige restaurar backup sem que o banco "volte no tempo" para o mundo externo. Depois de restaurar:

- o worker não pode retomar tarefas sobre um estado antigo: vencer reservas, estornar, reenviar e-mails, criar Pix;
- pagamentos e estornos feitos depois do backup continuam valendo no provedor;
- pedidos de exclusão feitos depois do backup não podem ser esquecidos;
- sessões encerradas depois do backup não podem voltar a valer.

Além disso, a operação precisa de três coisas:

- métricas de erro, latência, conexões SSE e filas internas, sem dependência nova e sem dado pessoal;
- troca da chave do servidor sem invalidar códigos e protocolos já emitidos;
- segredos entregues pela plataforma, não escritos no código.

## Decisão

1. **Módulo `operacao`, dono de `operation_state`.** Essa tabela tem uma única linha com:
   - a trava do worker e o motivo: `maintenance`, `restore` ou `restore_detected`;
   - quando a conciliação terminou;
   - o último sinal de vida do worker;
   - o **oid do banco** em que o estado foi gravado.

   Uma restauração lógica (`pg_restore` num banco novo) ou uma cópia criam um banco com outro oid. O worker percebe sozinho e grava a trava `restore_detected`.
2. **Todas as tarefas do worker rodam sob um supervisor.**
   - Com trava, nada roda: o supervisor cancela as tarefas e espera a rodada atual terminar.
   - Os indicadores continuam sendo lidos, para o alerta aparecer.
   - A API não é travada: ela não produz efeitos externos sozinha, fora a criação de Pix pedida por alguém.
3. **Conciliação pós-restauração (`fila-admin restauracao reconciliar`).** Os passos rodam nesta ordem, e cada um pode ser repetido:
   1. época nova do SSE;
   2. todas as sessões e links de e-mail não usados são encerrados;
   3. pagamentos com o provedor;
   4. exclusões do diário são reaplicadas.

   No passo de pagamentos:
   - As tentativas não finais do banco são **consultadas**, sem nunca criar cobrança. Se o provedor não tem nada com o nosso id, a tentativa vira falha.
   - As cobranças **pagas** e **estornadas** no provedor desde o último sinal de vida do worker menos 1 dia são comparadas com o banco. Elas vêm de `GET /transparents/list` com `startDate` e paginação `after`.
   - Pagamento que o banco não tem → registrado pelo caminho normal, recriando a tentativa com o mesmo id quando a comanda existe.
   - Comanda que não existe → pendência `restored_orphan_charge`, com a referência do provedor e sem comanda.
   - Estorno que o banco não tem → pendência `restored_refund_unrecorded`.

   A trava só sai com a conciliação concluída **depois** do início da trava. Declarar restauração de novo invalida a conciliação anterior.
4. **Diário de exclusões fora do banco.** O worker copia, em segundos, cada pedido de exclusão para `FILA_DELETION_JOURNAL_DIR`, uma linha JSON com fsync. Cada linha tem só ids, o HMAC do protocolo, o sufixo e a data. A conciliação recria os pedidos que o banco restaurado não tem, com o mesmo id e protocolo, e aplica os mesmos efeitos do pedido original.
5. **Métricas em formato Prometheus, escritas à mão** (`platform/metrics`), num endereço separado (`FILA_METRICS_ADDR`) que nunca é publicado.
   - Os rótulos só usam conjuntos pequenos: rota-modelo, método, status e tipo de pendência.
   - A API expõe requisições, latência, recusas, conexões SSE e o pool do banco.
   - O worker expõe os indicadores de negócio.
   - `fila-admin verificar` aplica os mesmos limiares e serve para quem não tem Prometheus (saída 3 = alerta).
6. **Proteção da API.** Limite geral por IP em memória, por processo (balde de fichas). Tetos SSE por IP e por processo. Os limites contra adivinhação continuam no banco.
7. **Segredos e chaves.**
   - `NOME_FILE=/caminho` para os segredos (Docker e Kubernetes secrets).
   - `FILA_SECRET_KEY_PREVIOUS` só reconhece o que já foi emitido.
   - Produção só aceita o endereço oficial da AbacatePay.

## Alternativas consideradas

- **Restaurar e religar tudo direto**, confiando no worker normal: ele criaria Pix para tentativas do passado e não saberia de pagamentos feitos fora do banco. Descartada.
- **Trava só manual**, sem detectar o oid: depende de o operador lembrar. Mantivemos as duas; a restauração física (que mantém o oid) exige a trava manual, que está documentada.
- **Biblioteca `prometheus/client_golang`:** padrão de mercado, mas é uma dependência a mais e só usaríamos contadores, medidores e histogramas simples. A troca é simples, porque os nomes seguem a convenção.
- **Limite geral por IP no banco:** valeria entre processos, mas custaria uma escrita por requisição. O proxy e o banco já cobrem o que importa (tentativas de senha e código).
- **Diário de exclusões em outra tabela do mesmo banco:** seria restaurado junto e voltaria no tempo. Descartada.

## Consequências

- Depois de restaurar, todos entram de novo. Pagamentos do provedor nunca se perdem: viram recebimento ou pendência visível ao dono.
- A janela de perda (RPO) das exclusões depende do diário estar em outro disco ou volume que sobreviva ao incidente. Se o servidor inteiro se perde, vale a última cópia externa do diário.
- Visitas em fila feitas depois do backup se perdem e são refeitas no balcão. Credenciais de visitante não são encerradas (risco baixo, documentado).
- Com N processos da API, o limite geral efetivo por IP é N × o configurado.
- Métricas e alertas dependem de um Prometheus e de um canal de aviso configurados pela operação. Sem eles, `fila-admin verificar` em agendamento e os logs do worker mostram os alertas.
