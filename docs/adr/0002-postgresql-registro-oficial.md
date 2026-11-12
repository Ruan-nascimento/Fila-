# ADR-0002 — PostgreSQL como registro oficial

- Situação: Aceita (decisão 02 do anexo)
- Data: 2026-11-12

## Contexto

Fila e dinheiro não podem depender do navegador: atualizar ou fechar a página não pode apagar uma visita, e duas pessoas não podem disputar a mesma cadeira ou unidade de prêmio.

## Decisão

- PostgreSQL (versão suportada escolhida na etapa 01 pela documentação oficial) guarda todo estado verdadeiro.
- Invariantes protegidas **no banco**: índices únicos parciais (uma participação ativa por identidade/loja; uma chamada/atendimento por fila; uma cobrança Pix ativa por comanda; uma reserva ativa por conta/loja; um crédito por comanda), `CHECK` (preço > 0, pontos ≥ 0), chaves estrangeiras com `establishment_id` em todas as tabelas de negócio.
- Bloqueio de linha (`SELECT … FOR UPDATE`) onde há escolha pelo servidor (próximo da fila, estoque de prêmio) e versão otimista nos agregados (ADR-0009).
- Migrações versionadas; ambientes com bancos e credenciais separados; dados fictícios em teste.
- Backup e restauração testada antes do piloto; após restaurar, reconciliar pagamentos antes de religar o worker.

## Alternativas consideradas

- Estado em memória com persistência eventual: rejeitado (perde visitas em reinício).
- Mutex no processo Go: rejeitado como proteção principal (não vale com dois processos).

## Consequências

- Testes de integração usam PostgreSQL real (contêiner ou instância local), não mock.
- Todo SQL é parametrizado; dinheiro em `bigint` de centavos.
