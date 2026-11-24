# ADR-0036 — Pesquisa e organização das listas

- Situação: Aceita
- Data: 2026-11-21

## Decisão

1. **Onde há busca**: serviços e produtos (cliente e gestão), profissionais, prêmios, comandas. Fila da gestão: só localizar (não altera a ordem). Nada de busca decorativa.
2. **Normalização única** (`platform/busca`): minúsculas, sem acento, espaços repetidos e das pontas removidos; curingas do LIKE escapados. A mesma regra no Go, no SQL (`translate(lower(...))`) e no frontend (`normalize('NFD')`).
3. **Local × servidor**: listas carregadas por inteiro e limitadas (serviços, produtos até 500, categorias até 50, profissionais, prêmios) filtram no aparelho, na hora. **Comandas são paginadas**: a busca (`q`, até 60 caracteres, senha/nick/item) é sempre no servidor, nunca só na página atual. O frontend usa ~250 ms de espera, cancela a requisição anterior (`AbortController`) e descarta resposta fora de ordem.
4. **Texto de busca normalizado gravado na escrita** (migration 00028): colunas geradas `participation.search_text` (senha + nick) e `order_item.search_name`, com a mesma regra de `platform/busca`. Medido: normalizar na consulta custava ~160 ms por coluna em 20 mil linhas (p95 329 ms, acima da meta de 300 ms); com a coluna gerada, p95 64 ms ([evidências](../evidencias/melhorias-1.0.0.md)). Sem índice novo, sem extensão do PostgreSQL e sem mecanismo externo de busca: o filtro roda dentro da loja (e do profissional), sobre os índices existentes por loja e data.
5. **Organização**: categorias como chips com rolagem local (sem rolar a página na horizontal); "sem categoria" sempre acessível; filtro ativos/inativos e ordenação por posição, nome, preço ou duração na gestão; reordenar por campo de posição (sem depender de arrastar).
