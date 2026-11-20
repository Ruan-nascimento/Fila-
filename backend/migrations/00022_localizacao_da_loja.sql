-- Localização da barbearia (opcional), para o cliente encontrar as mais próximas. O dono informa
-- na configuração da loja (pela localização do aparelho, na própria barbearia, ou digitando).
-- É informação pública da loja, como o endereço. A localização do CLIENTE nunca é gravada: vai
-- só na consulta de busca e não aparece em log (o log registra a rota, não a query).
--
-- Dono: módulo estabelecimento. Aditivo: colunas opcionais, nenhuma linha reescrita.

-- +goose Up
ALTER TABLE establishment
    ADD COLUMN latitude  double precision CHECK (latitude BETWEEN -90 AND 90),
    ADD COLUMN longitude double precision CHECK (longitude BETWEEN -180 AND 180),
    ADD CONSTRAINT establishment_location_ck CHECK ((latitude IS NULL) = (longitude IS NULL));

-- +goose Down
ALTER TABLE establishment DROP CONSTRAINT establishment_location_ck, DROP COLUMN longitude, DROP COLUMN latitude;
