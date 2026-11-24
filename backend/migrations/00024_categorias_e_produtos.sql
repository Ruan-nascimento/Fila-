-- Melhorias 1.0.0 (ADR-0032) — categorias do catálogo e catálogo simples de produtos.
-- Aditiva: serviços existentes ficam sem categoria (continuam acessíveis) e com ordem 0.
-- Produto não tem estoque: a disponibilidade é a situação ativa/inativa definida pelo dono.

-- +goose Up

-- Categoria pertence à loja e a um tipo de item (serviço ou produto). Desativar não apaga itens.
CREATE TABLE catalog_category (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    kind             text NOT NULL CHECK (kind IN ('service', 'product')),
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    display_order    integer NOT NULL DEFAULT 0 CHECK (display_order BETWEEN 0 AND 9999),
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    UNIQUE (establishment_id, id)
);
-- Nome único por loja e tipo, sem diferenciar maiúsculas.
CREATE UNIQUE INDEX catalog_category_name_uq ON catalog_category (establishment_id, kind, lower(name));
CREATE TRIGGER catalog_category_no_delete BEFORE DELETE ON catalog_category FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- Serviço: categoria opcional (mesma loja garantida pela chave composta) e ordem de exibição.
ALTER TABLE service
    ADD COLUMN category_id   uuid,
    ADD COLUMN display_order integer NOT NULL DEFAULT 0 CHECK (display_order BETWEEN 0 AND 9999),
    ADD CONSTRAINT service_category_fk FOREIGN KEY (establishment_id, category_id) REFERENCES catalog_category (establishment_id, id);

-- Foto de produto reaproveita a infraestrutura de mídia (pública, da loja).
ALTER TABLE media_asset DROP CONSTRAINT media_asset_purpose_check;
ALTER TABLE media_asset ADD CONSTRAINT media_asset_purpose_check
    CHECK (purpose IN ('avatar', 'professional_photo', 'logo', 'product_photo'));

CREATE TABLE product (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    description      text CHECK (char_length(description) BETWEEN 1 AND 200),
    category_id      uuid,
    price_cents      bigint NOT NULL CHECK (price_cents BETWEEN 1 AND 10000000),
    image_asset_id   uuid REFERENCES media_asset (id),
    display_order    integer NOT NULL DEFAULT 0 CHECK (display_order BETWEEN 0 AND 9999),
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    UNIQUE (establishment_id, id),
    FOREIGN KEY (establishment_id, category_id) REFERENCES catalog_category (establishment_id, id)
);
-- Listagem da loja: situação, ordem e nome (desempate pelo id).
CREATE INDEX product_establishment_idx ON product (establishment_id, status, display_order, name, id);

-- Histórico do catálogo de produtos (como service_revision): imutável.
CREATE TABLE product_revision (
    product_id  uuid NOT NULL REFERENCES product (id),
    version     bigint NOT NULL CHECK (version >= 1),
    name        text NOT NULL,
    description text,
    category_id uuid,
    price_cents bigint NOT NULL CHECK (price_cents > 0),
    status      text NOT NULL,
    changed_at  timestamptz NOT NULL,
    changed_by  uuid,
    PRIMARY KEY (product_id, version)
);
CREATE TRIGGER product_no_delete BEFORE DELETE ON product FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER product_revision_no_delete BEFORE DELETE ON product_revision FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER product_revision_immutable BEFORE UPDATE ON product_revision FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- +goose Down
DROP TABLE product_revision;
DROP TABLE product;
ALTER TABLE media_asset DROP CONSTRAINT media_asset_purpose_check;
ALTER TABLE media_asset ADD CONSTRAINT media_asset_purpose_check CHECK (purpose IN ('avatar', 'professional_photo', 'logo'));
ALTER TABLE service DROP CONSTRAINT service_category_fk, DROP COLUMN display_order, DROP COLUMN category_id;
DROP TABLE catalog_category;
