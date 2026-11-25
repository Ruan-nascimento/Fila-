-- Fila 2.0, parte 2 (ADR-0039) — reserva com Pix (sinal abatido ou taxa adicional), avisos por
-- e-mail e recuperação da reserva pelo contato verificado.
--
-- Expansão compatível: nada existente muda de significado. Lojas continuam com reserva gratuita
-- (booking_payment_mode = 'free'); a cobrança só existe com a liberação do servidor
-- (FILA_AGENDA_COBRANCA) e o Pix operacional.
--
-- Invariantes protegidas pelo banco:
--   * cobrança Pix, recebimento, estorno e pendência apontam para UMA comanda OU UMA obrigação de
--     reserva (XOR), nunca as duas e nunca nenhuma (exceto a pendência órfã pós-restauração);
--   * uma obrigação por reserva; uma tentativa Pix ativa por obrigação;
--   * o sinal abatido entra na comanda por UM registro de crédito (booking_credit), e a comanda
--     só fica paga quando recebimentos principais + crédito = total (check_order_payment).

-- +goose Up

-- Configurações da agenda: política financeira da reserva (seção Agenda, mesma versão).
ALTER TABLE establishment_settings
    ADD COLUMN booking_payment_mode  text NOT NULL DEFAULT 'free' CHECK (booking_payment_mode IN ('free', 'deposit', 'fee')),
    ADD COLUMN booking_payment_cents bigint CHECK (booking_payment_cents BETWEEN 100 AND 100000),
    ADD COLUMN cancel_window_hours   integer NOT NULL DEFAULT 12 CHECK (cancel_window_hours BETWEEN 0 AND 168),
    ADD COLUMN late_cancel_policy    text NOT NULL DEFAULT 'refund' CHECK (late_cancel_policy IN ('refund', 'retain')),
    ADD COLUMN reminder_hours        integer NOT NULL DEFAULT 24 CHECK (reminder_hours BETWEEN 0 AND 72),
    ADD CONSTRAINT establishment_settings_booking_amount_check
        CHECK ((booking_payment_mode = 'free') = (booking_payment_cents IS NULL));

-- Reserva: "aguardando pagamento" segura o horário até hold_expires_at; sem pagamento vira "expirada".
ALTER TABLE appointment DROP CONSTRAINT appointment_status_check;
ALTER TABLE appointment ADD CONSTRAINT appointment_status_check CHECK (status IN
    ('held', 'confirmed', 'called', 'in_service', 'completed', 'interrupted', 'no_show', 'cancelled', 'expired'));
ALTER TABLE appointment DROP CONSTRAINT appointment_check5;
ALTER TABLE appointment ADD CONSTRAINT appointment_finished_check
    CHECK ((status IN ('completed', 'interrupted', 'no_show', 'cancelled', 'expired')) = (finished_at IS NOT NULL));
ALTER TABLE appointment ADD COLUMN hold_expires_at timestamptz;
ALTER TABLE appointment ADD CONSTRAINT appointment_hold_check CHECK (status <> 'held' OR hold_expires_at IS NOT NULL);
-- A retenção ativa também conta para "sem sobreposição do próprio cliente".
ALTER TABLE appointment DROP CONSTRAINT appointment_account_overlap;
ALTER TABLE appointment DROP CONSTRAINT appointment_visitor_overlap;
ALTER TABLE appointment ADD CONSTRAINT appointment_account_overlap EXCLUDE USING gist (
    establishment_id WITH =, account_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
    WHERE (account_id IS NOT NULL AND status IN ('held', 'confirmed', 'called', 'in_service'));
ALTER TABLE appointment ADD CONSTRAINT appointment_visitor_overlap EXCLUDE USING gist (
    establishment_id WITH =, visitor_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
    WHERE (visitor_id IS NOT NULL AND status IN ('held', 'confirmed', 'called', 'in_service'));
CREATE INDEX appointment_held_idx ON appointment (hold_expires_at) WHERE status = 'held';
-- Ocupação de retenção: guarda o prazo (sem now() em predicado: quem ocupa o mesmo intervalo
-- solta a retenção vencida sob a trava da cadeira, antes de gravar).
ALTER TABLE agenda_occupancy ADD COLUMN hold_until timestamptz;

-- Obrigação financeira da reserva (módulo pagamento). amount = valor aceito na reserva.
CREATE TABLE booking_payment (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id   uuid NOT NULL REFERENCES establishment (id),
    appointment_id     uuid NOT NULL UNIQUE,
    mode               text NOT NULL CHECK (mode IN ('deposit', 'fee')),
    amount_cents       bigint NOT NULL CHECK (amount_cents >= 100),
    status             text NOT NULL DEFAULT 'awaiting_payment'
                           CHECK (status IN ('awaiting_payment', 'paid', 'expired', 'cancelled')),
    hold_expires_at    timestamptz NOT NULL,
    -- Política aceita (cópia): janela de cancelamento grátis e o que acontece no atraso/falta.
    cancel_window_hours integer NOT NULL CHECK (cancel_window_hours BETWEEN 0 AND 168),
    late_cancel_policy  text NOT NULL CHECK (late_cancel_policy IN ('refund', 'retain')),
    policy_version     bigint NOT NULL,
    accepted_at        timestamptz NOT NULL,
    paid_at            timestamptz,
    -- Desfecho do valor pago: abatido na comanda, mantido (taxa ou retenção pela política) ou a devolver.
    outcome            text CHECK (outcome IN ('applied', 'kept', 'refund_due')),
    order_id           uuid,
    version            bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, appointment_id) REFERENCES appointment (establishment_id, id),
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    UNIQUE (establishment_id, id),
    UNIQUE (order_id),
    CHECK ((status = 'paid') = (paid_at IS NOT NULL)),
    CHECK (outcome IS NULL OR status = 'paid')
);
CREATE INDEX booking_payment_hold_idx ON booking_payment (hold_expires_at) WHERE status = 'awaiting_payment';

-- Destino da cobrança, do recebimento, do estorno e da pendência: comanda OU obrigação da reserva.
ALTER TABLE pix_charge ALTER COLUMN order_id DROP NOT NULL;
ALTER TABLE pix_charge ADD COLUMN booking_payment_id uuid;
ALTER TABLE pix_charge ADD CONSTRAINT pix_charge_booking_fk
    FOREIGN KEY (establishment_id, booking_payment_id) REFERENCES booking_payment (establishment_id, id);
ALTER TABLE pix_charge ADD CONSTRAINT pix_charge_target_check CHECK (num_nonnulls(order_id, booking_payment_id) = 1);
CREATE UNIQUE INDEX pix_charge_booking_active_uq ON pix_charge (booking_payment_id)
    WHERE booking_payment_id IS NOT NULL AND status IN ('creating', 'pending', 'unknown');
CREATE UNIQUE INDEX pix_charge_booking_idem_uq ON pix_charge (booking_payment_id, idem_key_hash) WHERE booking_payment_id IS NOT NULL;

ALTER TABLE receipt ALTER COLUMN order_id DROP NOT NULL;
ALTER TABLE receipt ADD COLUMN booking_payment_id uuid;
ALTER TABLE receipt ADD CONSTRAINT receipt_booking_fk
    FOREIGN KEY (establishment_id, booking_payment_id) REFERENCES booking_payment (establishment_id, id);
ALTER TABLE receipt ADD CONSTRAINT receipt_target_check CHECK (num_nonnulls(order_id, booking_payment_id) = 1);
-- Recebimento da reserva só por Pix confirmado pelo provedor.
ALTER TABLE receipt ADD CONSTRAINT receipt_booking_method_check CHECK (booking_payment_id IS NULL OR method = 'pix');
CREATE INDEX receipt_booking_idx ON receipt (booking_payment_id) WHERE booking_payment_id IS NOT NULL;
-- Um recebimento principal por obrigação (o resto é excesso: tardio, duplicado).
CREATE UNIQUE INDEX receipt_booking_principal_uq ON receipt (booking_payment_id)
    WHERE booking_payment_id IS NOT NULL AND classification = 'principal';

ALTER TABLE refund ALTER COLUMN order_id DROP NOT NULL;
ALTER TABLE refund ADD COLUMN booking_payment_id uuid;
ALTER TABLE refund ADD CONSTRAINT refund_booking_fk
    FOREIGN KEY (establishment_id, booking_payment_id) REFERENCES booking_payment (establishment_id, id);
ALTER TABLE refund ADD CONSTRAINT refund_target_check CHECK (num_nonnulls(order_id, booking_payment_id) = 1);
-- Devolução pedida pelo sistema (política da reserva) não tem conta de quem pediu.
ALTER TABLE refund ALTER COLUMN requested_by_account_id DROP NOT NULL;
ALTER TABLE refund ADD CONSTRAINT refund_requester_check CHECK (requested_by_account_id IS NOT NULL OR booking_payment_id IS NOT NULL);
-- Devolução parcial (ex.: sobra do sinal depois de atendimento interrompido) — o provedor só
-- estorna o valor integral: a parte é devolvida fora e confirmada pelo dono (revisão).
ALTER TABLE refund DROP CONSTRAINT refund_check3;
ALTER TABLE refund ADD CONSTRAINT refund_method_status_check
    CHECK (method = 'pix' OR status IN ('under_review', 'confirmed', 'failed'));
DROP INDEX refund_one_per_receipt_uq;
-- Integral: um estorno não-falho por recebimento. A devolução parcial da sobra do sinal é única
-- por obrigação (marcada partial).
ALTER TABLE refund ADD COLUMN partial boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX refund_one_per_receipt_uq ON refund (receipt_id) WHERE status <> 'failed';
ALTER TABLE refund ADD CONSTRAINT refund_partial_check CHECK (NOT partial OR (booking_payment_id IS NOT NULL AND status IN ('under_review', 'confirmed', 'failed')));

ALTER TABLE financial_issue ADD COLUMN booking_payment_id uuid;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_booking_fk
    FOREIGN KEY (establishment_id, booking_payment_id) REFERENCES booking_payment (establishment_id, id);
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_order_required;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_target_check
    CHECK (num_nonnulls(order_id, booking_payment_id) = 1 OR (kind = 'restored_orphan_charge' AND order_id IS NULL AND booking_payment_id IS NULL));
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_kind_check;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_kind_check CHECK (kind IN ('duplicate_receipt', 'late_receipt',
    'amount_mismatch', 'charge_outcome_unresolved', 'refund_under_review', 'active_pix_on_method_switch', 'adjustment_excess',
    'restored_orphan_charge', 'restored_refund_unrecorded', 'booking_late_payment', 'booking_refund_due', 'booking_notice_failed'));

-- Crédito do sinal na comanda (aplicação do recebimento original, sem nova entrada de dinheiro).
CREATE TABLE booking_credit (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id   uuid NOT NULL,
    booking_payment_id uuid NOT NULL UNIQUE,
    order_id           uuid NOT NULL UNIQUE,
    receipt_id         uuid NOT NULL UNIQUE REFERENCES receipt (id),
    amount_cents       bigint NOT NULL CHECK (amount_cents > 0),
    applied_at         timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, booking_payment_id) REFERENCES booking_payment (establishment_id, id),
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id)
);
CREATE TRIGGER booking_credit_no_delete BEFORE DELETE ON booking_credit FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER booking_credit_no_update BEFORE UPDATE ON booking_credit FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER booking_payment_no_delete BEFORE DELETE ON booking_payment FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- A comanda guarda o crédito aplicado (módulo atendimento): devido = total − crédito.
ALTER TABLE service_order ADD COLUMN credit_cents bigint NOT NULL DEFAULT 0 CHECK (credit_cents >= 0);
ALTER TABLE service_order ADD CONSTRAINT service_order_credit_check CHECK (credit_cents <= total_cents OR status = 'open');

-- Quitação: recebimentos principais da comanda + crédito do sinal = total; o crédito confere com
-- o registro de crédito; tudo conferido no COMMIT.
-- +goose StatementBegin
-- O gatilho é adiado (roda no COMMIT, uma vez por UPDATE da transação): confere o estado ATUAL da
-- comanda, não a linha de cada UPDATE intermediário (ex.: total fechado e, em seguida, o crédito).
CREATE OR REPLACE FUNCTION check_order_payment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    cur         service_order%ROWTYPE;
    principal   bigint;
    unconfirmed integer;
    credited    bigint;
BEGIN
    SELECT * INTO cur FROM service_order WHERE id = NEW.id;
    SELECT coalesce(sum(amount_cents), 0) INTO credited FROM booking_credit WHERE order_id = cur.id;
    IF credited <> cur.credit_cents THEN
        RAISE EXCEPTION 'comanda % com crédito % diferente do registrado %', cur.id, cur.credit_cents, credited USING ERRCODE = '23514';
    END IF;
    IF cur.status NOT IN ('awaiting_confirmation', 'settled') THEN
        RETURN NULL;
    END IF;
    SELECT coalesce(sum(r.amount_cents), 0), count(*) FILTER (WHERE c.receipt_id IS NULL)
      INTO principal, unconfirmed
      FROM receipt r LEFT JOIN receipt_conferral c ON c.receipt_id = r.id
     WHERE r.order_id = cur.id AND r.classification = 'principal';
    IF principal + cur.credit_cents <> cur.total_cents THEN
        RAISE EXCEPTION 'comanda % em % sem recebimento igual ao devido', cur.id, cur.status USING ERRCODE = '23514';
    END IF;
    IF cur.status = 'settled' AND unconfirmed > 0 THEN
        RAISE EXCEPTION 'comanda % paga com recebimento não conferido', cur.id USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd

-- Avisos da reserva (módulo agenda): um por (reserva, revisão, tipo); o worker revalida antes de
-- enviar e grava o e-mail na caixa de saída cifrada. "queued" = gravado; o envio real é o do
-- dispatcher de e-mail (aceito pelo provedor), nunca "lido".
CREATE TABLE appointment_notice (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL,
    appointment_id   uuid NOT NULL,
    revision         integer NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('confirmed', 'reminder', 'called', 'rescheduled', 'cancelled', 'no_show',
                         'expired', 'refund')),
    due_at           timestamptz NOT NULL,
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'queued', 'skipped', 'failed')),
    skip_reason      text CHECK (char_length(skip_reason) <= 80),
    attempts         integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_until      timestamptz,
    created_at       timestamptz NOT NULL,
    done_at          timestamptz,
    FOREIGN KEY (establishment_id, appointment_id) REFERENCES appointment (establishment_id, id),
    UNIQUE (appointment_id, revision, kind),
    CHECK ((status = 'pending') = (done_at IS NULL))
);
CREATE INDEX appointment_notice_due_idx ON appointment_notice (due_at) WHERE status = 'pending';
CREATE INDEX appointment_notice_appointment_idx ON appointment_notice (appointment_id, created_at);
CREATE TRIGGER appointment_notice_no_delete BEFORE DELETE ON appointment_notice FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- Contato de e-mail verificado do visitante (módulo identidade), no escopo da loja. O endereço
-- fica cifrado; a busca da recuperação usa um HMAC (nunca o endereço em claro num índice).
CREATE TABLE visitor_contact (
    visitor_id       uuid PRIMARY KEY REFERENCES visitor (id),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    email_sealed     bytea NOT NULL,
    email_hmac       bytea NOT NULL CHECK (octet_length(email_hmac) = 32),
    code_hmac        bytea CHECK (octet_length(code_hmac) = 32),
    code_expires_at  timestamptz,
    code_attempts    integer NOT NULL DEFAULT 0 CHECK (code_attempts BETWEEN 0 AND 10),
    verified_at      timestamptz,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    CHECK ((code_hmac IS NULL) = (code_expires_at IS NULL))
);
CREATE INDEX visitor_contact_lookup_idx ON visitor_contact (establishment_id, email_hmac) WHERE verified_at IS NOT NULL;

-- Acesso à reserva pelo e-mail (link de uso único, troca por sessão segura).
CREATE TABLE appointment_access_token (
    token_hash       bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    email_hmac       bytea NOT NULL CHECK (octet_length(email_hmac) = 32),
    expires_at       timestamptz NOT NULL,
    used_at          timestamptz,
    created_at       timestamptz NOT NULL
);
CREATE INDEX appointment_access_token_expiry_idx ON appointment_access_token (expires_at);

-- +goose Down
-- Só volta sem dinheiro de reserva gravado (nunca apaga cobrança, recebimento ou estorno).
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM booking_payment) THEN
        RAISE EXCEPTION 'Há obrigações de reserva gravadas: a cobrança da reserva não pode ser removida por migração.';
    END IF;
END
$$;
-- +goose StatementEnd
DROP TABLE appointment_access_token;
DROP TABLE visitor_contact;
DROP TABLE appointment_notice;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_order_payment() RETURNS trigger LANGUAGE plpgsql AS $$
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
ALTER TABLE service_order DROP CONSTRAINT service_order_credit_check;
ALTER TABLE service_order DROP COLUMN credit_cents;
DROP TABLE booking_credit;
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_kind_check;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_kind_check CHECK (kind IN ('duplicate_receipt', 'late_receipt',
    'amount_mismatch', 'charge_outcome_unresolved', 'refund_under_review', 'active_pix_on_method_switch', 'adjustment_excess',
    'restored_orphan_charge', 'restored_refund_unrecorded'));
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_target_check;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_order_required CHECK (order_id IS NOT NULL OR kind = 'restored_orphan_charge');
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_booking_fk;
ALTER TABLE financial_issue DROP COLUMN booking_payment_id;
ALTER TABLE refund DROP CONSTRAINT refund_partial_check;
ALTER TABLE refund DROP COLUMN partial;
ALTER TABLE refund DROP CONSTRAINT refund_method_status_check;
ALTER TABLE refund ADD CONSTRAINT refund_check3 CHECK (method = 'pix' OR status IN ('under_review', 'confirmed', 'failed'));
ALTER TABLE refund DROP CONSTRAINT refund_requester_check;
ALTER TABLE refund ALTER COLUMN requested_by_account_id SET NOT NULL;
ALTER TABLE refund DROP CONSTRAINT refund_target_check;
ALTER TABLE refund DROP CONSTRAINT refund_booking_fk;
ALTER TABLE refund DROP COLUMN booking_payment_id;
ALTER TABLE refund ALTER COLUMN order_id SET NOT NULL;
DROP INDEX receipt_booking_principal_uq;
DROP INDEX receipt_booking_idx;
ALTER TABLE receipt DROP CONSTRAINT receipt_booking_method_check;
ALTER TABLE receipt DROP CONSTRAINT receipt_target_check;
ALTER TABLE receipt DROP CONSTRAINT receipt_booking_fk;
ALTER TABLE receipt DROP COLUMN booking_payment_id;
ALTER TABLE receipt ALTER COLUMN order_id SET NOT NULL;
DROP INDEX pix_charge_booking_idem_uq;
DROP INDEX pix_charge_booking_active_uq;
ALTER TABLE pix_charge DROP CONSTRAINT pix_charge_target_check;
ALTER TABLE pix_charge DROP CONSTRAINT pix_charge_booking_fk;
ALTER TABLE pix_charge DROP COLUMN booking_payment_id;
ALTER TABLE pix_charge ALTER COLUMN order_id SET NOT NULL;
DROP TABLE booking_payment;
ALTER TABLE agenda_occupancy DROP COLUMN hold_until;
DROP INDEX appointment_held_idx;
ALTER TABLE appointment DROP CONSTRAINT appointment_account_overlap;
ALTER TABLE appointment DROP CONSTRAINT appointment_visitor_overlap;
ALTER TABLE appointment ADD CONSTRAINT appointment_account_overlap EXCLUDE USING gist (
    establishment_id WITH =, account_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
    WHERE (account_id IS NOT NULL AND status IN ('confirmed', 'called', 'in_service'));
ALTER TABLE appointment ADD CONSTRAINT appointment_visitor_overlap EXCLUDE USING gist (
    establishment_id WITH =, visitor_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
    WHERE (visitor_id IS NOT NULL AND status IN ('confirmed', 'called', 'in_service'));
ALTER TABLE appointment DROP CONSTRAINT appointment_hold_check;
ALTER TABLE appointment DROP COLUMN hold_expires_at;
ALTER TABLE appointment DROP CONSTRAINT appointment_finished_check;
ALTER TABLE appointment ADD CONSTRAINT appointment_check5
    CHECK ((status IN ('completed', 'interrupted', 'no_show', 'cancelled')) = (finished_at IS NOT NULL));
ALTER TABLE appointment DROP CONSTRAINT appointment_status_check;
ALTER TABLE appointment ADD CONSTRAINT appointment_status_check CHECK (status IN
    ('confirmed', 'called', 'in_service', 'completed', 'interrupted', 'no_show', 'cancelled'));
ALTER TABLE establishment_settings
    DROP CONSTRAINT establishment_settings_booking_amount_check,
    DROP COLUMN reminder_hours, DROP COLUMN late_cancel_policy, DROP COLUMN cancel_window_hours,
    DROP COLUMN booking_payment_cents, DROP COLUMN booking_payment_mode;
