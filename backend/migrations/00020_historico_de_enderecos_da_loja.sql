-- Endereços (slugs) antigos da loja. Quando o dono troca o nome da barbearia, o link e o QR Code
-- passam a usar o endereço novo; os endereços antigos ficam guardados aqui para que links e QR
-- Codes já impressos continuem abrindo a mesma loja (o navegador é levado ao endereço atual).
--
-- Dono: módulo estabelecimento. Aditivo: nenhuma linha existente é reescrita.

-- +goose Up
CREATE TABLE establishment_slug_alias (
    slug             text PRIMARY KEY CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{1,48}[a-z0-9])$'),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    retired_at       timestamptz NOT NULL
);
CREATE INDEX establishment_slug_alias_est_idx ON establishment_slug_alias (establishment_id);

-- +goose Down
DROP TABLE establishment_slug_alias;
