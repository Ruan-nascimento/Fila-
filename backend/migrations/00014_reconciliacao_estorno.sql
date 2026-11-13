-- Etapa 09 — reconciliação, estorno e pendências financeiras (módulo pagamento, ADR-0024).
-- Invariantes protegidas pelo banco:
--   * aviso do provedor autenticado é gravado uma única vez por id do evento (deduplicação);
--   * no máximo UM estorno não-falho por recebimento (integral no MVP: ativo ou confirmado);
--   * estorno confirmado tem evidência (provedor ou documentação do dono) e não volta atrás;
--   * uma pendência por (tipo, origem); pendência resolvida tem nota; nada disso é apagado.

-- +goose Up

SET CONSTRAINTS ALL IMMEDIATE;

-- Avisos do provedor (webhook). Só identificadores e o necessário para auditoria: o corpo bruto
-- traz nome, e-mail e documento do pagador e NÃO é guardado (só o hash).
CREATE TABLE provider_event (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    provider           text NOT NULL CHECK (provider IN ('abacatepay')),
    event_id           text NOT NULL CHECK (char_length(event_id) BETWEEN 1 AND 100),
    event_type         text NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 60),
    authenticated      boolean NOT NULL,
    dev_mode           boolean,
    provider_charge_id text CHECK (char_length(provider_charge_id) BETWEEN 1 AND 100),
    charge_status      text CHECK (char_length(charge_status) BETWEEN 1 AND 30),
    amount_cents       bigint,
    body_sha256        bytea NOT NULL CHECK (octet_length(body_sha256) = 32),
    received_at        timestamptz NOT NULL,
    -- applied: mudou estado; duplicate_effect: efeito já existia; ignored: evento sem efeito no MVP;
    -- unknown_charge: cobrança não é nossa; mismatch: não confere (valor/pedido/ambiente);
    -- check_scheduled: não autenticado → consulta autenticada agendada.
    outcome            text NOT NULL CHECK (outcome IN
                           ('applied', 'duplicate_effect', 'ignored', 'unknown_charge', 'mismatch', 'check_scheduled'))
);
-- Deduplicação: o mesmo aviso autenticado (mesmo id, inclusive nas retentativas) só uma vez.
CREATE UNIQUE INDEX provider_event_dedup_uq ON provider_event (provider, event_id) WHERE authenticated;
CREATE INDEX provider_event_charge_idx ON provider_event (provider_charge_id, received_at);

CREATE TABLE refund (
    id                       uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id         uuid NOT NULL,
    order_id                 uuid NOT NULL,
    receipt_id               uuid NOT NULL REFERENCES receipt (id),
    method                   text NOT NULL CHECK (method IN ('pix', 'cash', 'card')),
    amount_cents             bigint NOT NULL CHECK (amount_cents > 0),
    status                   text NOT NULL CHECK (status IN
                                 ('requested', 'processing', 'unknown', 'under_review', 'confirmed', 'failed')),
    reason                   text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500),
    requested_at             timestamptz NOT NULL,
    requested_by_account_id  uuid NOT NULL REFERENCES account (id),
    provider_refund_id       text CHECK (char_length(provider_refund_id) BETWEEN 1 AND 100),
    evidence                 text CHECK (evidence IN ('provider', 'owner_documented')),
    evidence_note            text CHECK (char_length(evidence_note) BETWEEN 3 AND 500),
    external_reference       text CHECK (char_length(external_reference) BETWEEN 1 AND 80),
    resolved_at              timestamptz,
    resolved_by_account_id   uuid REFERENCES account (id),
    check_attempts           integer NOT NULL DEFAULT 0 CHECK (check_attempts >= 0),
    next_check_at            timestamptz,
    updated_at               timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    CHECK ((status IN ('confirmed', 'failed')) = (resolved_at IS NOT NULL)),
    CHECK (status <> 'confirmed' OR evidence IS NOT NULL),
    CHECK (evidence <> 'owner_documented' OR (evidence_note IS NOT NULL AND resolved_by_account_id IS NOT NULL)),
    CHECK (method = 'pix' OR status IN ('under_review', 'confirmed', 'failed'))
);
-- Integral no MVP: um estorno não-falho por recebimento (o recebido ainda não devolvido é zero
-- depois dele). Estorno falho não conta e permite novo pedido.
CREATE UNIQUE INDEX refund_one_per_receipt_uq ON refund (receipt_id) WHERE status <> 'failed';
CREATE INDEX refund_check_idx ON refund (next_check_at) WHERE status IN ('requested', 'processing', 'unknown');
CREATE INDEX refund_order_idx ON refund (order_id, requested_at);

-- Estorno resolvido (confirmado/falho) não muda mais; nenhum estorno é apagado.
-- +goose StatementBegin
CREATE FUNCTION refund_final_is_final() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('confirmed', 'failed') THEN
        RAISE EXCEPTION 'estorno % já resolvido (%)', OLD.id, OLD.status USING ERRCODE = '23514';
    END IF;
    IF NEW.amount_cents <> OLD.amount_cents OR NEW.receipt_id <> OLD.receipt_id OR NEW.order_id <> OLD.order_id THEN
        RAISE EXCEPTION 'estorno % não pode mudar de valor nem de recebimento', OLD.id USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER refund_final BEFORE UPDATE ON refund FOR EACH ROW EXECUTE FUNCTION refund_final_is_final();
CREATE TRIGGER refund_no_delete BEFORE DELETE ON refund FOR EACH ROW EXECUTE FUNCTION forbid_delete();

CREATE TABLE financial_issue (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id       uuid NOT NULL,
    order_id               uuid NOT NULL,
    kind                   text NOT NULL CHECK (kind IN ('duplicate_receipt', 'late_receipt', 'amount_mismatch',
                               'charge_outcome_unresolved', 'refund_under_review', 'active_pix_on_method_switch',
                               'adjustment_excess')),
    status                 text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    amount_cents           bigint CHECK (amount_cents >= 0),
    source_type            text NOT NULL CHECK (source_type IN ('pix_charge', 'receipt', 'refund')),
    source_id              uuid NOT NULL,
    created_at             timestamptz NOT NULL,
    resolved_at            timestamptz,
    resolved_by_account_id uuid REFERENCES account (id),
    resolution_note        text CHECK (char_length(resolution_note) BETWEEN 3 AND 500),
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    CHECK ((status = 'resolved') = (resolved_at IS NOT NULL)),
    CHECK (status = 'open' OR (resolution_note IS NOT NULL AND resolved_by_account_id IS NOT NULL))
);
-- UQ(tipo, origem): o mesmo problema não abre duas pendências (aviso repetido, worker repetido).
CREATE UNIQUE INDEX financial_issue_once_uq ON financial_issue (kind, source_id);
CREATE INDEX financial_issue_est_idx ON financial_issue (establishment_id, status, created_at DESC, id DESC);
CREATE TRIGGER financial_issue_no_delete BEFORE DELETE ON financial_issue FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER provider_event_no_delete BEFORE DELETE ON provider_event FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- +goose Down
SET CONSTRAINTS ALL IMMEDIATE;
DROP TABLE financial_issue;
DROP TABLE refund;
DROP FUNCTION refund_final_is_final();
DROP TABLE provider_event;
