-- Etapa 10 — pontos e prêmios (módulo fidelidade, ADR-0012, ADR-0025). Invariantes no banco:
--   * extrato de pontos só de inserção, com chave única de origem por loja (crédito uma vez,
--     reversão uma vez, liberação uma vez…);
--   * saldo e retido materializados pelo próprio banco a partir do extrato (gatilho), retido nunca
--     negativo e reserva (hold) nunca maior que o disponível;
--   * estoque de prêmio limitado explicado por movimentos e nunca negativo (última unidade);
--   * uma reserva ativa por conta e loja; reserva guarda as condições aceitas.

-- +goose Up

SET CONSTRAINTS ALL IMMEDIATE;

-- Programa por loja. Sem linha = desligado (R-FID-01).
CREATE TABLE loyalty_program (
    establishment_id uuid PRIMARY KEY REFERENCES establishment (id),
    enabled          boolean NOT NULL,
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at       timestamptz NOT NULL
);

-- Saldo materializado por conta e loja: só o gatilho do extrato escreve aqui.
CREATE TABLE loyalty_account (
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    account_id       uuid NOT NULL REFERENCES account (id),
    balance          bigint NOT NULL DEFAULT 0,
    held             bigint NOT NULL DEFAULT 0 CHECK (held >= 0),
    version          bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (establishment_id, account_id)
);

CREATE TABLE loyalty_ledger (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    account_id       uuid NOT NULL REFERENCES account (id),
    kind             text NOT NULL CHECK (kind IN ('credit', 'credit_reversal', 'hold', 'hold_release', 'consume',
                         'consume_reversal', 'account_closure')),
    balance_delta    bigint NOT NULL,
    held_delta       bigint NOT NULL,
    source_type      text NOT NULL CHECK (source_type IN ('order', 'reservation', 'account')),
    source_id        uuid NOT NULL,
    note             text CHECK (char_length(note) <= 500),
    occurred_at      timestamptz NOT NULL,
    -- Sinais coerentes com o tipo (o extrato explica o saldo, ADR-0012).
    CHECK (CASE kind
        WHEN 'credit' THEN balance_delta > 0 AND held_delta = 0
        WHEN 'credit_reversal' THEN balance_delta < 0 AND held_delta = 0
        WHEN 'hold' THEN balance_delta = 0 AND held_delta > 0
        WHEN 'hold_release' THEN balance_delta = 0 AND held_delta < 0
        WHEN 'consume' THEN balance_delta < 0 AND held_delta = balance_delta
        WHEN 'consume_reversal' THEN balance_delta > 0 AND held_delta = 0
        WHEN 'account_closure' THEN held_delta = 0
    END),
    -- "Uma única vez" (ÚNICA-VEZ): mesma origem e mesmo tipo, um lançamento só.
    UNIQUE (establishment_id, source_type, source_id, kind)
);
CREATE INDEX loyalty_ledger_account_idx ON loyalty_ledger (establishment_id, account_id, occurred_at DESC, id DESC);

-- +goose StatementBegin
CREATE FUNCTION loyalty_ledger_apply() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    b bigint;
    h bigint;
BEGIN
    -- UPDATE primeiro: num INSERT ... ON CONFLICT o PostgreSQL confere o CHECK (retido >= 0) na
    -- linha PROPOSTA antes de ver o conflito, e um lançamento negativo seria recusado à toa.
    UPDATE loyalty_account SET balance = balance + NEW.balance_delta, held = held + NEW.held_delta, version = version + 1
     WHERE establishment_id = NEW.establishment_id AND account_id = NEW.account_id
    RETURNING balance, held INTO b, h;
    IF NOT FOUND THEN
        INSERT INTO loyalty_account (establishment_id, account_id, balance, held, version)
        VALUES (NEW.establishment_id, NEW.account_id, NEW.balance_delta, NEW.held_delta, 1)
        RETURNING balance, held INTO b, h;
    END IF;
    -- Reserva só com saldo disponível (R-FID-08/14): retido nunca passa do saldo.
    IF NEW.kind = 'hold' AND b - h < 0 THEN
        RAISE EXCEPTION 'pontos insuficientes para reservar' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER loyalty_ledger_apply AFTER INSERT ON loyalty_ledger FOR EACH ROW EXECUTE FUNCTION loyalty_ledger_apply();
CREATE TRIGGER loyalty_ledger_no_update BEFORE UPDATE ON loyalty_ledger FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER loyalty_ledger_no_delete BEFORE DELETE ON loyalty_ledger FOR EACH ROW EXECUTE FUNCTION forbid_delete();

CREATE TABLE reward (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id   uuid NOT NULL REFERENCES establishment (id),
    name               text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    description        text NOT NULL CHECK (char_length(description) BETWEEN 1 AND 500),
    usage_instructions text NOT NULL CHECK (char_length(usage_instructions) BETWEEN 1 AND 500),
    cost_points        integer NOT NULL CHECK (cost_points BETWEEN 1 AND 1000000),
    stock_kind         text NOT NULL CHECK (stock_kind IN ('unlimited', 'limited')),
    -- Só o gatilho dos movimentos muda este número; nunca negativo (última unidade).
    units_available    integer CHECK (units_available >= 0),
    status             text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version            bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    CHECK ((stock_kind = 'limited') = (units_available IS NOT NULL)),
    UNIQUE (establishment_id, id)
);
CREATE INDEX reward_est_idx ON reward (establishment_id, status, name);

-- Movimentos de estoque dos prêmios limitados: explicam units_available.
CREATE TABLE reward_stock_movement (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    reward_id   uuid NOT NULL REFERENCES reward (id),
    kind        text NOT NULL CHECK (kind IN ('set_by_owner', 'reserve', 'release', 'delivery_reverted')),
    delta       integer NOT NULL,
    -- Origem: reserva (reserve/release/delivery_reverted) ou a própria versão do prêmio (owner).
    source_id   uuid NOT NULL,
    occurred_at timestamptz NOT NULL,
    UNIQUE (reward_id, source_id, kind)
);
-- +goose StatementBegin
CREATE FUNCTION reward_stock_apply() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE reward SET units_available = units_available + NEW.delta WHERE id = NEW.reward_id AND stock_kind = 'limited';
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER reward_stock_apply AFTER INSERT ON reward_stock_movement FOR EACH ROW EXECUTE FUNCTION reward_stock_apply();
CREATE TRIGGER reward_stock_no_update BEFORE UPDATE ON reward_stock_movement FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER reward_stock_no_delete BEFORE DELETE ON reward_stock_movement FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER reward_no_delete BEFORE DELETE ON reward FOR EACH ROW EXECUTE FUNCTION forbid_delete();

CREATE TABLE reward_reservation (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id            uuid NOT NULL,
    account_id                  uuid NOT NULL REFERENCES account (id),
    -- Nome exibido à equipe na entrega (trocado por "Cliente" na exclusão da conta).
    holder_name                 text NOT NULL CHECK (char_length(holder_name) BETWEEN 1 AND 80),
    reward_id                   uuid NOT NULL,
    -- Condições aceitas (R-FID-12): não mudam com o catálogo.
    accepted_name               text NOT NULL,
    accepted_description        text NOT NULL,
    accepted_usage_instructions text NOT NULL,
    accepted_cost_points        integer NOT NULL CHECK (accepted_cost_points >= 1),
    accepted_reward_version     bigint NOT NULL,
    limited_stock               boolean NOT NULL,
    status                      text NOT NULL DEFAULT 'active' CHECK (status IN
                                    ('active', 'cancelled', 'expired', 'delivered', 'delivery_reverted')),
    created_at                  timestamptz NOT NULL,
    expires_at                  timestamptz NOT NULL,
    resolved_at                 timestamptz,
    delivered_by_role           text CHECK (delivered_by_role IN ('owner', 'professional')),
    delivered_by_account_id     uuid REFERENCES account (id),
    cancelled_by                text CHECK (cancelled_by IN ('customer', 'system_deletion')),
    reverted_reason             text CHECK (char_length(reverted_reason) BETWEEN 3 AND 500),
    version                     bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    FOREIGN KEY (establishment_id, reward_id) REFERENCES reward (establishment_id, id),
    CHECK (expires_at > created_at),
    CHECK ((status = 'active') = (resolved_at IS NULL)),
    CHECK ((status IN ('delivered', 'delivery_reverted')) = (delivered_by_account_id IS NOT NULL)),
    CHECK ((status = 'delivery_reverted') = (reverted_reason IS NOT NULL))
);
-- R-FID-10: uma reserva ativa por conta e loja.
CREATE UNIQUE INDEX reward_reservation_one_active_uq ON reward_reservation (establishment_id, account_id) WHERE status = 'active';
CREATE INDEX reward_reservation_expiry_idx ON reward_reservation (expires_at) WHERE status = 'active';
CREATE INDEX reward_reservation_est_idx ON reward_reservation (establishment_id, created_at DESC, id DESC);
CREATE TRIGGER reward_reservation_no_delete BEFORE DELETE ON reward_reservation FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- +goose Down
SET CONSTRAINTS ALL IMMEDIATE;
DROP TABLE reward_reservation;
DROP TABLE reward_stock_movement;
DROP FUNCTION reward_stock_apply();
DROP TABLE reward;
DROP TABLE loyalty_ledger;
DROP FUNCTION loyalty_ledger_apply();
DROP TABLE loyalty_account;
DROP TABLE loyalty_program;
