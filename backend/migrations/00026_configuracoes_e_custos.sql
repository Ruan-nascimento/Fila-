-- Melhorias 1.0.0 (ADR-0034) — configurações da loja por seção, custos de recebimento
-- (estimativas cadastradas pelo dono), modalidade do cartão e custo registrado por recebimento.
--
-- Sem linha em establishment_settings, valem os padrões (o servidor lê com os mesmos valores):
-- nenhuma loja muda de comportamento só por esta migração, exceto o que o ADR declara.
-- Recebimentos antigos ficam sem custo registrado ("desconhecido"): nada é fabricado.

-- +goose Up

CREATE TABLE establishment_settings (
    establishment_id                uuid PRIMARY KEY REFERENCES establishment (id),
    -- Catálogo
    max_service_quantity            integer NOT NULL DEFAULT 10 CHECK (max_service_quantity BETWEEN 1 AND 50),
    products_enabled                boolean NOT NULL DEFAULT true,
    catalog_version                 bigint NOT NULL DEFAULT 1 CHECK (catalog_version >= 1),
    -- Filas e atendimento
    visitors_enabled                boolean NOT NULL DEFAULT true,
    call_tolerance_minutes          integer NOT NULL DEFAULT 10 CHECK (call_tolerance_minutes BETWEEN 1 AND 60),
    default_queue_capacity          integer CHECK (default_queue_capacity BETWEEN 1 AND 200),
    professionals_control_admission boolean NOT NULL DEFAULT true,
    queue_version                   bigint NOT NULL DEFAULT 1 CHECK (queue_version >= 1),
    -- Pagamentos (Pix só vale com a integração operacional; conferido no servidor)
    cash_enabled                    boolean NOT NULL DEFAULT true,
    card_enabled                    boolean NOT NULL DEFAULT true,
    pix_enabled                     boolean NOT NULL DEFAULT true,
    payments_version                bigint NOT NULL DEFAULT 1 CHECK (payments_version >= 1),
    updated_at                      timestamptz NOT NULL,
    CHECK (cash_enabled OR card_enabled OR pix_enabled)
);

-- Custo de recebimento por modalidade: cada mudança é uma NOVA versão (linha imutável). A versão
-- de maior número é a vigente; "cleared" volta a "não configurado". Percentual em pontos-base
-- (1 pb = 0,01%; 250 = 2,5%), componente fixo em centavos POR TRANSAÇÃO.
CREATE TABLE payment_fee_config (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    method           text NOT NULL CHECK (method IN ('debit', 'credit', 'pix')),
    version          integer NOT NULL CHECK (version >= 1),
    cleared          boolean NOT NULL DEFAULT false,
    percent_bp       integer NOT NULL CHECK (percent_bp BETWEEN 0 AND 3000),
    fixed_cents      bigint NOT NULL CHECK (fixed_cents BETWEEN 0 AND 10000),
    effective_from   timestamptz NOT NULL,
    created_by       uuid NOT NULL REFERENCES account (id),
    created_at       timestamptz NOT NULL,
    UNIQUE (establishment_id, method, version),
    UNIQUE (establishment_id, id),
    CHECK (NOT cleared OR (percent_bp = 0 AND fixed_cents = 0))
);
CREATE TRIGGER payment_fee_config_no_delete BEFORE DELETE ON payment_fee_config FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER payment_fee_config_no_update BEFORE UPDATE ON payment_fee_config FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- Modalidade do cartão. Recebimentos antigos ficam nulos ("cartão não especificado").
ALTER TABLE receipt ADD COLUMN card_type text CHECK (card_type IN ('debit', 'credit'));
ALTER TABLE receipt ADD CONSTRAINT receipt_card_type_method_check CHECK (method = 'card' OR card_type IS NULL);

-- Custo registrado no recebimento (o recebimento é imutável, então o custo fica ao lado).
-- not_configured: modalidade sem custo cadastrado; estimated: calculado pela configuração vigente;
-- actual: informado pelo provedor; not_applicable: dinheiro. Sem linha = recebimento anterior
-- a esta versão (custo desconhecido).
CREATE TABLE receipt_fee (
    receipt_id       uuid PRIMARY KEY REFERENCES receipt (id),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    status           text NOT NULL CHECK (status IN ('not_configured', 'estimated', 'actual', 'not_applicable')),
    fee_config_id    uuid REFERENCES payment_fee_config (id),
    percent_bp       integer CHECK (percent_bp BETWEEN 0 AND 3000),
    fixed_cents      bigint CHECK (fixed_cents BETWEEN 0 AND 10000),
    estimated_cents  bigint CHECK (estimated_cents >= 0),
    actual_cents     bigint CHECK (actual_cents >= 0),
    created_at       timestamptz NOT NULL,
    actual_at        timestamptz,
    CHECK (status <> 'estimated' OR (fee_config_id IS NOT NULL AND estimated_cents IS NOT NULL
                                     AND percent_bp IS NOT NULL AND fixed_cents IS NOT NULL)),
    CHECK ((status = 'actual') = (actual_cents IS NOT NULL)),
    CHECK ((actual_cents IS NULL) = (actual_at IS NULL))
);
CREATE INDEX receipt_fee_establishment_idx ON receipt_fee (establishment_id, created_at);
CREATE TRIGGER receipt_fee_no_delete BEFORE DELETE ON receipt_fee FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- Estorno: taxa devolvida pelo provedor, quando informada (nulo = desconhecido).
ALTER TABLE refund ADD COLUMN fee_returned_cents bigint CHECK (fee_returned_cents >= 0);

-- +goose Down
ALTER TABLE refund DROP COLUMN fee_returned_cents;
DROP TABLE receipt_fee;
ALTER TABLE receipt DROP CONSTRAINT receipt_card_type_method_check;
ALTER TABLE receipt DROP COLUMN card_type;
DROP TABLE payment_fee_config;
DROP TABLE establishment_settings;
