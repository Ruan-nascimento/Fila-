# Desempenho das consultas do Dashboard

Medido em 18/11/2026 com `FILA_PERF=1 go -C backend test -run TestDesempenho -v ./internal/analitico/`. O teste cria um banco temporário, semeia os dados, mede cada consulta 3 vezes (vale a melhor) e mostra o plano da consulta de caixa. A saída completa está em [evidencias/desktop-desempenho.txt](../evidencias/desktop-desempenho.txt).

## Ambiente e volume

- PostgreSQL 18.6 (Docker local), Linux, mesmo computador da API. Não é o servidor de produção: os números servem para comparar consultas entre si, não como promessa de latência.
- Dados: 1 loja, 4 profissionais, 3 serviços e um ano de movimento.
  - 60.000 participações concluídas e 60.000 comandas liquidadas.
  - 80.000 itens: 1 ou 2 por comanda.
  - 60.000 recebimentos conferidos e 6.000 estornos efetivados.

## Resultados (ms)

| Consulta | 30 dias | 366 dias |
|---|---|---|
| Resumo (4 indicadores + comparação) | 41,9 | 257 |
| Série caixa | 5,5 | 68,6 |
| Série ticket | 4,7 | 59,3 |
| Série movimento | 15,4 | 169 |
| Série espera (mediana e p90) | 12,1 | 140 |
| Série duração | 8,6 | 127 |
| Meios de pagamento | 2,3 | 22,2 |
| Desfechos | 3,9 | 49,4 |
| **Profissionais** | **76** | **446** |
| Serviços | 34,3 | 220 |
| Horários de pico | 11,4 | 139 |
| Pendências (fotografia) | 21,1 | 25,9 |
| Recorrência | 4,8 | 49,3 |

## Gargalos observados e o que foi feito

1. **JOIN com a comanda em toda consulta de caixa.** A primeira versão juntava `receipt` e `service_order` mesmo sem filtro de profissional. O plano mostrava `Seq Scan on service_order` nas 60.000 linhas. Agora a comanda só entra por `EXISTS` quando há filtro.

   | Consulta | Antes | Depois |
   |---|---|---|
   | Série caixa, 30 dias | 34 ms | 5,5 ms |
   | Resumo, 30 dias | 166 ms | 42 ms |
   | Meios de pagamento, 30 dias | 57 ms | 2,3 ms |

   O plano novo usa só `Bitmap Index Scan on receipt_est_registered_idx`.
2. **Índices da migração 00018** (loja + instante de referência de cada métrica). Sem eles, as mesmas séries de 30 dias levaram:

   | Série (30 dias) | Sem índices | Com índices |
   |---|---|---|
   | Caixa | 25,9 ms | 5,5 ms |
   | Ticket | 20 ms | 4,7 ms |
   | Movimento | 42,9 ms | 15,4 ms |
   | Espera | 30,1 ms | 12,1 ms |
   | Duração | 27,2 ms | 8,6 ms |

   O ganho cresce com o histórico: sem índice, cada consulta percorre a tabela inteira.
3. **Ainda lento:** o detalhamento por profissional em 366 dias (446 ms). Ele usa subconsultas por profissional, com a mediana calculada várias vezes. Fica sob demanda: só carrega ao abrir a aba "Profissionais e serviços". Se a loja crescer, o próximo passo é uma única agregação agrupada por fila.

## Prioridade do balcão

- As consultas analíticas são somente leitura (`REPEATABLE READ READ ONLY`) e não travam linhas.
- Não há recálculo a cada mudança de posição na fila: eventos `queue.*` não invalidam nada.
- As mudanças próximas são agrupadas (cerca de 3 s) antes de avisar o Dashboard, que recarrega só as áreas afetadas da aba aberta.

**Observação de ambiente.** O publicador de eventos espera as transações de escrita mais antigas de todo o servidor PostgreSQL. Durante a semeadura deste teste, uma transação de escrita de 5 minutos, rodando no mesmo servidor em outro banco, atrasou os eventos SSE do ambiente de teste da janela. Por isso, rode a medição num servidor separado do ambiente de homologação. As consultas do Dashboard não abrem transação de escrita.
