-- Melhorias 1.0.0 (ADR-0033) — quantidade por item da comanda, produtos na comanda e histórico
-- das quantidades. Uma linha por serviço (ou produto) com quantidade; o índice único
-- order_item_service_uq continua valendo.
--
-- Invariantes novas (protegidas pelo banco):
--   * 1 ≤ quantity ≤ 50 (o limite da loja, menor, é conferido no servidor só ao AUMENTAR);
--   * 0 ≤ charged_quantity ≤ performed_quantity ≤ quantity;
--   * charged ⇔ charged_quantity > 0 (a coluna antiga continua coerente para quem a lê);
--   * aceito ⇒ ainda falta unidade (performed_quantity < quantity); realizado ⇒ ≥ 1 unidade;
--     não realizado ⇒ nenhuma unidade;
--   * produto: sem duração (não entra na fila) e sem pontos nesta versão;
--   * total da comanda aberta = Σ preço × quantidade; com valor fechado = Σ preço × cobradas.
--
-- Dados existentes: quantidade 1; realizado → 1 unidade realizada; cobrado → 1 unidade cobrada.
-- Preço, duração e pontos históricos não mudam.

-- +goose Up

SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE order_item
    ADD COLUMN kind               text NOT NULL DEFAULT 'service' CHECK (kind IN ('service', 'product')),
    ADD COLUMN product_id         uuid,
    ADD COLUMN quantity           integer NOT NULL DEFAULT 1 CHECK (quantity BETWEEN 1 AND 50),
    ADD COLUMN performed_quantity integer NOT NULL DEFAULT 0,
    ADD COLUMN charged_quantity   integer NOT NULL DEFAULT 0,
    -- Unidades integralmente feitas segundo o profissional na interrupção (o dono decide depois).
    ADD COLUMN reported_quantity  integer;

-- Antes de trocar a regra do total: a função antiga só lê preço e "charged", que não mudam aqui.
UPDATE order_item SET performed_quantity = 1 WHERE status = 'performed';
UPDATE order_item SET charged_quantity = 1 WHERE charged;

ALTER TABLE order_item ALTER COLUMN service_id DROP NOT NULL;
ALTER TABLE order_item DROP CONSTRAINT order_item_duration_minutes_check;
ALTER TABLE order_item DROP CONSTRAINT order_item_points_check;
ALTER TABLE order_item
    ADD CONSTRAINT order_item_product_fk FOREIGN KEY (establishment_id, product_id) REFERENCES product (establishment_id, id),
    ADD CONSTRAINT order_item_kind_ref_check CHECK (
        (kind = 'service' AND service_id IS NOT NULL AND product_id IS NULL) OR
        (kind = 'product' AND product_id IS NOT NULL AND service_id IS NULL)),
    ADD CONSTRAINT order_item_duration_check CHECK (
        (kind = 'service' AND duration_minutes > 0) OR (kind = 'product' AND duration_minutes = 0)),
    ADD CONSTRAINT order_item_points_check CHECK (points >= 0 AND (kind = 'service' OR points = 0)),
    ADD CONSTRAINT order_item_quantities_check CHECK (
        performed_quantity BETWEEN 0 AND quantity AND charged_quantity BETWEEN 0 AND performed_quantity),
    ADD CONSTRAINT order_item_charged_quantity_check CHECK (charged = (charged_quantity > 0)),
    ADD CONSTRAINT order_item_reported_quantity_check CHECK (reported_quantity IS NULL OR reported_quantity BETWEEN 0 AND quantity),
    ADD CONSTRAINT order_item_status_quantity_check CHECK (
        (status <> 'accepted' OR performed_quantity < quantity) AND
        (status <> 'performed' OR performed_quantity >= 1) AND
        (status <> 'not_performed' OR performed_quantity = 0));
-- Um produto aparece no máximo uma vez por comanda (quantidade na mesma linha), como o serviço.
CREATE UNIQUE INDEX order_item_product_uq ON order_item (order_id, product_id) WHERE status <> 'removed' AND product_id IS NOT NULL;

-- Total sempre explicável item a item, agora com quantidade.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_order_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    oid     uuid;
    o       service_order%ROWTYPE;
    expect  bigint;
    charged integer;
BEGIN
    IF TG_TABLE_NAME = 'order_item' THEN
        oid := NEW.order_id;
    ELSE
        oid := NEW.id;
    END IF;
    SELECT * INTO o FROM service_order WHERE id = oid;
    IF o.status IN ('open', 'pending_review') THEN
        SELECT coalesce(sum(i.unit_price_cents * i.quantity), 0), count(*) FILTER (WHERE i.charged)
          INTO expect, charged FROM order_item i WHERE i.order_id = oid AND i.status <> 'removed';
        IF charged > 0 THEN
            RAISE EXCEPTION 'comanda % com item cobrado antes de fechar o valor', oid USING ERRCODE = '23514';
        END IF;
    ELSE
        SELECT coalesce(sum(i.unit_price_cents * i.charged_quantity), 0) INTO expect
          FROM order_item i WHERE i.order_id = oid AND i.charged;
    END IF;
    IF o.total_cents <> expect THEN
        RAISE EXCEPTION 'total da comanda % (%) difere da soma dos itens (%)', oid, o.total_cents, expect USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd

-- Histórico das quantidades (pedido, realizado/entregue, cobrado): toda mudança vira uma linha
-- imutável com origem, autor e versão da comanda. Retirar a linha registra "quantity → 0".
CREATE TABLE order_item_quantity_change (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL,
    order_id         uuid NOT NULL,
    item_id          uuid NOT NULL REFERENCES order_item (id),
    field            text NOT NULL CHECK (field IN ('quantity', 'performed_quantity', 'charged_quantity')),
    from_value       integer NOT NULL CHECK (from_value BETWEEN 0 AND 50),
    to_value         integer NOT NULL CHECK (to_value BETWEEN 0 AND 50),
    source           text NOT NULL CHECK (source IN ('customer', 'staff', 'change_request', 'completion', 'interruption',
                                                     'review', 'adjustment')),
    actor_kind       text NOT NULL CHECK (actor_kind IN ('visitor', 'customer', 'professional', 'owner', 'system')),
    actor_account_id uuid REFERENCES account (id),
    order_version    bigint NOT NULL CHECK (order_version >= 1),
    at               timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    CHECK (from_value <> to_value)
);
CREATE INDEX order_item_quantity_change_order_idx ON order_item_quantity_change (order_id, at, id);
CREATE TRIGGER order_item_quantity_change_no_delete BEFORE DELETE ON order_item_quantity_change FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER order_item_quantity_change_no_update BEFORE UPDATE ON order_item_quantity_change FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- Solicitação de mudança com quantidade: inclusões (serviço ou produto, com quantidade e o preço
-- visto pelo cliente) e nova quantidade de linhas existentes.
ALTER TABLE change_request_addition DROP CONSTRAINT change_request_addition_pkey;
ALTER TABLE change_request_addition DROP CONSTRAINT change_request_addition_duration_minutes_check;
ALTER TABLE change_request_addition DROP CONSTRAINT change_request_addition_points_check;
ALTER TABLE change_request_addition ALTER COLUMN service_id DROP NOT NULL;
ALTER TABLE change_request_addition
    ADD COLUMN kind       text NOT NULL DEFAULT 'service' CHECK (kind IN ('service', 'product')),
    ADD COLUMN product_id uuid,
    ADD COLUMN quantity   integer NOT NULL DEFAULT 1 CHECK (quantity BETWEEN 1 AND 50),
    ADD CONSTRAINT change_request_addition_product_fk FOREIGN KEY (establishment_id, product_id) REFERENCES product (establishment_id, id),
    ADD CONSTRAINT change_request_addition_kind_ref_check CHECK (
        (kind = 'service' AND service_id IS NOT NULL AND product_id IS NULL) OR
        (kind = 'product' AND product_id IS NOT NULL AND service_id IS NULL)),
    ADD CONSTRAINT change_request_addition_duration_check CHECK (
        (kind = 'service' AND duration_minutes > 0) OR (kind = 'product' AND duration_minutes = 0)),
    ADD CONSTRAINT change_request_addition_points_check CHECK (points >= 0 AND (kind = 'service' OR points = 0));
CREATE UNIQUE INDEX change_request_addition_service_uq ON change_request_addition (change_request_id, service_id) WHERE service_id IS NOT NULL;
CREATE UNIQUE INDEX change_request_addition_product_uq ON change_request_addition (change_request_id, product_id) WHERE product_id IS NOT NULL;

CREATE TABLE change_request_item_quantity (
    change_request_id uuid NOT NULL REFERENCES change_request (id),
    item_id           uuid NOT NULL REFERENCES order_item (id),
    quantity          integer NOT NULL CHECK (quantity BETWEEN 1 AND 50),
    PRIMARY KEY (change_request_id, item_id)
);
CREATE TRIGGER change_request_item_quantity_no_delete BEFORE DELETE ON change_request_item_quantity FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER change_request_item_quantity_no_update BEFORE UPDATE ON change_request_item_quantity FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- Correção de comanda fechada também por quantidade cobrada (T-O-11).
-- [{"item_id":"…","from":3,"to":2}]
ALTER TABLE order_adjustment ADD COLUMN quantity_changes jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(quantity_changes) = 'array');
ALTER TABLE order_adjustment DROP CONSTRAINT order_adjustment_check1;
ALTER TABLE order_adjustment ADD CONSTRAINT order_adjustment_effect_check
    CHECK (cardinality(uncharged_item_ids) + cardinality(added_item_ids) + jsonb_array_length(quantity_changes) >= 1);

-- +goose Down
SET CONSTRAINTS ALL IMMEDIATE;
-- Só é seguro voltar sem produtos e sem quantidades maiores que 1 (nada é convertido às cegas).
ALTER TABLE order_adjustment DROP CONSTRAINT order_adjustment_effect_check;
ALTER TABLE order_adjustment ADD CONSTRAINT order_adjustment_check1 CHECK ((cardinality(uncharged_item_ids) + cardinality(added_item_ids)) >= 1);
ALTER TABLE order_adjustment DROP COLUMN quantity_changes;
DROP TABLE change_request_item_quantity;
DROP INDEX change_request_addition_product_uq;
DROP INDEX change_request_addition_service_uq;
ALTER TABLE change_request_addition DROP CONSTRAINT change_request_addition_points_check,
    DROP CONSTRAINT change_request_addition_duration_check, DROP CONSTRAINT change_request_addition_kind_ref_check,
    DROP CONSTRAINT change_request_addition_product_fk, DROP COLUMN quantity, DROP COLUMN product_id, DROP COLUMN kind;
ALTER TABLE change_request_addition ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE change_request_addition ADD CONSTRAINT change_request_addition_points_check CHECK (points >= 0),
    ADD CONSTRAINT change_request_addition_duration_minutes_check CHECK (duration_minutes > 0),
    ADD PRIMARY KEY (change_request_id, service_id);
DROP TABLE order_item_quantity_change;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_order_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    oid     uuid;
    o       service_order%ROWTYPE;
    expect  bigint;
    charged integer;
BEGIN
    IF TG_TABLE_NAME = 'order_item' THEN
        oid := NEW.order_id;
    ELSE
        oid := NEW.id;
    END IF;
    SELECT * INTO o FROM service_order WHERE id = oid;
    IF o.status IN ('open', 'pending_review') THEN
        SELECT coalesce(sum(unit_price_cents), 0), count(*) FILTER (WHERE i.charged)
          INTO expect, charged FROM order_item i WHERE i.order_id = oid AND i.status <> 'removed';
        IF charged > 0 THEN
            RAISE EXCEPTION 'comanda % com item cobrado antes de fechar o valor', oid USING ERRCODE = '23514';
        END IF;
    ELSE
        SELECT coalesce(sum(unit_price_cents), 0) INTO expect FROM order_item i WHERE i.order_id = oid AND i.charged;
    END IF;
    IF o.total_cents <> expect THEN
        RAISE EXCEPTION 'total da comanda % (%) difere da soma dos itens (%)', oid, o.total_cents, expect USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
DROP INDEX order_item_product_uq;
ALTER TABLE order_item DROP CONSTRAINT order_item_status_quantity_check, DROP CONSTRAINT order_item_reported_quantity_check,
    DROP CONSTRAINT order_item_charged_quantity_check, DROP CONSTRAINT order_item_quantities_check,
    DROP CONSTRAINT order_item_points_check, DROP CONSTRAINT order_item_duration_check,
    DROP CONSTRAINT order_item_kind_ref_check, DROP CONSTRAINT order_item_product_fk;
ALTER TABLE order_item ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE order_item ADD CONSTRAINT order_item_points_check CHECK (points >= 0),
    ADD CONSTRAINT order_item_duration_minutes_check CHECK (duration_minutes > 0);
ALTER TABLE order_item DROP COLUMN reported_quantity, DROP COLUMN charged_quantity, DROP COLUMN performed_quantity,
    DROP COLUMN quantity, DROP COLUMN product_id, DROP COLUMN kind;
