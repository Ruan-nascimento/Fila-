-- Etapa 08 — pagamentos (módulo pagamento): cobrança Pix, recebimento e conferência como registros
-- distintos (ADR-0005, ADR-0007, ADR-0023). Invariantes protegidas pelo banco:
--   * no máximo UMA tentativa Pix ativa (criando/pendente/desconhecida) por comanda (R-PAG-10);
--   * um recebimento por cobrança Pix (aviso repetido não duplica, R-PAG-08);
--   * dinheiro: entregue ≥ valor, troco = entregue − valor; cartão: só referência sem dado de cartão;
--   * comanda "aguardando conferência" ou "paga" só com recebimentos principais = valor devido, e
--     "paga" só com todos conferidos (R-PAG-13) — conferido no COMMIT;
--   * recebimento e conferência nunca são apagados nem alterados.

-- +goose Up

SET CONSTRAINTS ALL IMMEDIATE;

-- Meio escolhido (T-O-12). A comanda é do módulo atendimento; só ele escreve esta coluna.
ALTER TABLE service_order ADD COLUMN payment_method text CHECK (payment_method IN ('pix', 'cash', 'card'));

CREATE TABLE pix_charge (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id    uuid NOT NULL,
    order_id            uuid NOT NULL,
    status              text NOT NULL DEFAULT 'creating' CHECK (status IN
                            ('creating', 'pending', 'unknown', 'paid', 'expired', 'cancelled', 'failed')),
    amount_cents        bigint NOT NULL CHECK (amount_cents >= 100),
    provider            text NOT NULL CHECK (provider IN ('abacatepay')),
    test_mode           boolean NOT NULL,
    provider_charge_id  text UNIQUE CHECK (char_length(provider_charge_id) BETWEEN 1 AND 100),
    br_code             text CHECK (char_length(br_code) BETWEEN 1 AND 1000),
    expires_at          timestamptz,
    -- Idempotency-Key do pedido (hash): a mesma intenção devolve a mesma tentativa.
    idem_key_hash       bytea NOT NULL CHECK (octet_length(idem_key_hash) = 32),
    created_by          text NOT NULL CHECK (created_by IN ('visitor', 'customer', 'professional', 'owner')),
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    resolved_at         timestamptz,
    cancel_requested_at timestamptz,
    check_attempts      integer NOT NULL DEFAULT 0 CHECK (check_attempts >= 0),
    next_check_at       timestamptz,
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    UNIQUE (order_id, idem_key_hash),
    UNIQUE (establishment_id, id),
    CHECK ((status IN ('paid', 'expired', 'cancelled', 'failed')) = (resolved_at IS NOT NULL)),
    CHECK (status NOT IN ('pending', 'paid') OR provider_charge_id IS NOT NULL)
);
-- R-PAG-10: uma tentativa ativa por comanda.
CREATE UNIQUE INDEX pix_charge_one_active_uq ON pix_charge (order_id) WHERE status IN ('creating', 'pending', 'unknown');
CREATE INDEX pix_charge_check_idx ON pix_charge (next_check_at) WHERE status IN ('creating', 'pending', 'unknown');
CREATE INDEX pix_charge_order_idx ON pix_charge (order_id, created_at);

CREATE TABLE receipt (
    id                       uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id         uuid NOT NULL,
    order_id                 uuid NOT NULL,
    method                   text NOT NULL CHECK (method IN ('pix', 'cash', 'card')),
    amount_cents             bigint NOT NULL CHECK (amount_cents > 0),
    classification           text NOT NULL CHECK (classification IN ('principal', 'excess')),
    pix_charge_id            uuid UNIQUE,
    -- De onde veio a evidência: consulta autenticada ao provedor, aviso do provedor (etapa 09) ou
    -- registro presencial da equipe.
    evidence                 text NOT NULL CHECK (evidence IN ('provider_query', 'provider_webhook', 'in_person')),
    registered_by            text NOT NULL CHECK (registered_by IN ('provider', 'professional', 'owner')),
    registered_by_account_id uuid REFERENCES account (id),
    registered_at            timestamptz NOT NULL,
    cash_tendered_cents      bigint,
    change_cents             bigint,
    card_reference           text CHECK (card_reference ~ '^[A-Za-z0-9-]{1,40}$'),
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    FOREIGN KEY (establishment_id, pix_charge_id) REFERENCES pix_charge (establishment_id, id),
    CHECK ((method = 'pix') = (pix_charge_id IS NOT NULL)),
    CHECK ((method = 'pix') = (registered_by = 'provider')),
    CHECK ((method = 'pix') = (evidence <> 'in_person')),
    CHECK ((registered_by = 'provider') = (registered_by_account_id IS NULL)),
    CHECK (method = 'cash' OR (cash_tendered_cents IS NULL AND change_cents IS NULL)),
    CHECK (method <> 'cash' OR (cash_tendered_cents >= amount_cents AND change_cents = cash_tendered_cents - amount_cents)),
    CHECK (method = 'card' OR card_reference IS NULL)
);
CREATE INDEX receipt_order_idx ON receipt (order_id, registered_at);

-- Conferência: registro SEPARADO do recebimento, feito pela equipe (R-PAG-04/05).
CREATE TABLE receipt_conferral (
    receipt_id              uuid PRIMARY KEY REFERENCES receipt (id),
    establishment_id        uuid NOT NULL REFERENCES establishment (id),
    conferred_at            timestamptz NOT NULL,
    conferred_by_role       text NOT NULL CHECK (conferred_by_role IN ('owner', 'professional')),
    conferred_by_account_id uuid NOT NULL REFERENCES account (id)
);

CREATE TRIGGER pix_charge_no_delete BEFORE DELETE ON pix_charge FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER receipt_no_delete BEFORE DELETE ON receipt FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER receipt_no_update BEFORE UPDATE ON receipt FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER receipt_conferral_no_delete BEFORE DELETE ON receipt_conferral FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER receipt_conferral_no_update BEFORE UPDATE ON receipt_conferral FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- Comanda paga ou aguardando conferência só com recebimento de verdade (conferido no COMMIT).
-- +goose StatementBegin
CREATE FUNCTION check_order_payment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    principal  bigint;
    unconfirmed integer;
BEGIN
    IF NEW.status NOT IN ('awaiting_confirmation', 'settled') THEN
        RETURN NULL;
    END IF;
    SELECT coalesce(sum(r.amount_cents), 0), count(*) FILTER (WHERE c.receipt_id IS NULL)
      INTO principal, unconfirmed
      FROM receipt r LEFT JOIN receipt_conferral c ON c.receipt_id = r.id
     WHERE r.order_id = NEW.id AND r.classification = 'principal';
    IF principal <> NEW.total_cents THEN
        RAISE EXCEPTION 'comanda % em % sem recebimento igual ao devido', NEW.id, NEW.status USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'settled' AND unconfirmed > 0 THEN
        RAISE EXCEPTION 'comanda % paga com recebimento não conferido', NEW.id USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER service_order_payment_check AFTER INSERT OR UPDATE ON service_order
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_order_payment();

-- +goose Down
SET CONSTRAINTS ALL IMMEDIATE;
DROP TRIGGER service_order_payment_check ON service_order;
DROP FUNCTION check_order_payment();
DROP TABLE receipt_conferral;
DROP TABLE receipt;
DROP TABLE pix_charge;
ALTER TABLE service_order DROP COLUMN payment_method;
