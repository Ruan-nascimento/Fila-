-- Melhorias 1.0.0 (ADR-0036) — texto de busca já normalizado (minúsculas, sem acento), calculado
-- uma vez na escrita em vez de a cada busca. Medição antes: translate() em 20 000 linhas levava
-- ~160 ms por coluna (p95 de 329 ms na busca de comandas). Mesma regra de platform/busca.FoldSQL.
-- Colunas GERADAS: anonimizar o nick (exclusão de conta) atualiza a busca junto.

-- +goose Up
ALTER TABLE participation ADD COLUMN search_text text GENERATED ALWAYS AS (
    translate(lower(ticket_code || ' ' || nick), 'áàâãäéèêëíìîïóòôõöúùûüçñÁÀÂÃÄÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑ',
              'aaaaaeeeeiiiiooooouuuucnaaaaaeeeeiiiiooooouuuucn')) STORED;
ALTER TABLE order_item ADD COLUMN search_name text GENERATED ALWAYS AS (
    translate(lower(name), 'áàâãäéèêëíìîïóòôõöúùûüçñÁÀÂÃÄÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑ',
              'aaaaaeeeeiiiiooooouuuucnaaaaaeeeeiiiiooooouuuucn')) STORED;

-- +goose Down
ALTER TABLE order_item DROP COLUMN search_name;
ALTER TABLE participation DROP COLUMN search_text;
