-- Etapa 05 — pedido, serviços e atendimento (módulo atendimento) e outbox de eventos (platform/outbox).
-- Invariantes protegidas pelo banco (não só pelo código):
--   * no máximo uma solicitação de alteração pendente por comanda (R-CMD-07);
--   * item cobrado é sempre item realizado; comanda aberta não tem item cobrado;
--   * total da comanda = soma dos itens (aceitos enquanto aberta/em revisão; cobrados depois de
--     fechado o valor), conferido no COMMIT por gatilho de restrição (R-CMD-02, aceite da etapa);
--   * valor fechado (total_frozen_at) só existe fora de "aberta" e "em revisão".
-- Conflito com a etapa 04 (descrito no ADR-0020): comandas encerradas sem cobrança guardavam o
-- total dos serviços aceitos. Agora o valor de uma comanda encerrada é o que foi cobrado (zero),
-- com instante de fechamento; a migração corrige as linhas existentes sem apagar itens.

-- +goose Up

-- A FK participação↔comanda é adiada (DEFERRABLE): sem isto, o UPDATE de comandas existentes
-- deixa eventos de gatilho pendentes e o ALTER TABLE seguinte falha (SQLSTATE 55006) em bancos
-- que já têm comandas da etapa 04. Vale só para a transação desta migração.
SET CONSTRAINTS ALL IMMEDIATE;

-- Comanda ----------------------------------------------------------------------------------------

-- Profissional do atendimento (escopo own_attendance): o da fila na entrada; muda junto com a
-- transferência. Snapshot de elegibilidade de pontos (R-FID-03) copiado da participação.
ALTER TABLE service_order
    ADD COLUMN professional_id  uuid REFERENCES professional (id),
    ADD COLUMN loyalty_eligible boolean NOT NULL DEFAULT false,
    ADD COLUMN interrupted_at   timestamptz,
    ADD COLUMN review_note      text CHECK (char_length(review_note) BETWEEN 3 AND 500);

UPDATE service_order o SET professional_id = q.professional_id, loyalty_eligible = p.loyalty_eligible
FROM participation p JOIN queue q ON q.id = p.queue_id
WHERE p.order_id = o.id;

ALTER TABLE service_order ALTER COLUMN professional_id SET NOT NULL;
CREATE INDEX service_order_list_idx ON service_order (establishment_id, created_at DESC, id DESC);
CREATE INDEX service_order_professional_idx ON service_order (establishment_id, professional_id, created_at DESC, id DESC);

-- Encerradas sem cobrança antes desta etapa: valor cobrado = 0, fechado no encerramento.
UPDATE service_order SET total_cents = 0, total_frozen_at = closed_at
WHERE status = 'closed_no_charge' AND total_frozen_at IS NULL;

-- Correção para zero (T-O-11) também encerra sem cobrança, com motivo próprio.
ALTER TABLE service_order DROP CONSTRAINT service_order_closure_reason_check;
ALTER TABLE service_order ADD CONSTRAINT service_order_closure_reason_check CHECK (closure_reason IN
    ('cancelled_by_customer', 'cancelled_by_owner', 'cancelled_by_account_deletion', 'no_show',
     'interruption_zero_total', 'adjustment_zero_total'));
ALTER TABLE service_order ADD CONSTRAINT service_order_frozen_check
    CHECK ((status IN ('open', 'pending_review')) = (total_frozen_at IS NULL));
ALTER TABLE service_order ADD CONSTRAINT service_order_review_check
    CHECK (status <> 'pending_review' OR interrupted_at IS NOT NULL);

-- Itens: resultado informado na interrupção, motivo de "não realizado" e instante de retirada.
ALTER TABLE order_item
    ADD COLUMN reported_outcome text CHECK (reported_outcome IN ('fully_performed', 'partially_performed', 'not_started')),
    ADD COLUMN outcome_reason   text CHECK (char_length(outcome_reason) BETWEEN 3 AND 500),
    ADD COLUMN removed_at       timestamptz,
    ADD CONSTRAINT order_item_charged_check CHECK (NOT charged OR status = 'performed'),
    ADD CONSTRAINT order_item_removed_check CHECK ((status = 'removed') = (removed_at IS NOT NULL)),
    ADD CONSTRAINT order_item_performed_check CHECK (status <> 'performed' OR performed_at IS NOT NULL);
CREATE INDEX order_item_order_idx ON order_item (order_id);

-- Total sempre explicável item a item. Conferido no COMMIT (DEFERRABLE), porque um comando muda
-- itens e total em vários passos dentro da mesma transação.
-- +goose StatementBegin
CREATE FUNCTION check_order_total() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE CONSTRAINT TRIGGER service_order_total_check AFTER INSERT OR UPDATE ON service_order
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_order_total();
CREATE CONSTRAINT TRIGGER order_item_total_check AFTER INSERT OR UPDATE ON order_item
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_order_total();

-- Solicitação de alteração (T-CR-*). Inclusões guardam o snapshot do catálogo no pedido: é o
-- preço que o cliente viu e que vale se a equipe aprovar.
CREATE TABLE change_request (
    id                    uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id      uuid NOT NULL,
    order_id              uuid NOT NULL,
    status                text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'voided')),
    remove_item_ids       uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(remove_item_ids) <= 10),
    version               bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at            timestamptz NOT NULL,
    decided_at            timestamptz,
    decided_by_role       text CHECK (decided_by_role IN ('owner', 'professional')),
    decided_by_account_id uuid REFERENCES account (id),
    decision_reason       text CHECK (char_length(decision_reason) BETWEEN 3 AND 500),
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    CHECK ((status = 'pending') = (decided_at IS NULL)),
    CHECK ((status IN ('approved', 'rejected')) = (decided_by_role IS NOT NULL AND decided_by_account_id IS NOT NULL)),
    CHECK (status <> 'rejected' OR decision_reason IS NOT NULL)
);
-- R-CMD-07: uma pendente por comanda.
CREATE UNIQUE INDEX change_request_one_pending_uq ON change_request (order_id) WHERE status = 'pending';
CREATE INDEX change_request_order_idx ON change_request (order_id, created_at);

CREATE TABLE change_request_addition (
    change_request_id uuid NOT NULL REFERENCES change_request (id),
    establishment_id  uuid NOT NULL,
    service_id        uuid NOT NULL,
    name              text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    unit_price_cents  bigint NOT NULL CHECK (unit_price_cents > 0),
    duration_minutes  integer NOT NULL CHECK (duration_minutes > 0),
    points            integer NOT NULL CHECK (points >= 0),
    PRIMARY KEY (change_request_id, service_id),
    FOREIGN KEY (establishment_id, service_id) REFERENCES service (establishment_id, id)
);

-- Correção auditada de comanda com valor fechado (T-O-11): só por item, nunca valor livre.
CREATE TABLE order_adjustment (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id     uuid NOT NULL,
    order_id             uuid NOT NULL,
    reason               text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500),
    previous_total_cents bigint NOT NULL CHECK (previous_total_cents >= 0),
    new_total_cents      bigint NOT NULL CHECK (new_total_cents >= 0),
    uncharged_item_ids   uuid[] NOT NULL,
    added_item_ids       uuid[] NOT NULL,
    from_version         bigint NOT NULL,
    to_version           bigint NOT NULL CHECK (to_version > from_version),
    by_account_id        uuid NOT NULL REFERENCES account (id),
    at                   timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    CHECK (cardinality(uncharged_item_ids) + cardinality(added_item_ids) >= 1)
);
CREATE INDEX order_adjustment_order_idx ON order_adjustment (order_id, at);

CREATE TRIGGER change_request_no_delete BEFORE DELETE ON change_request FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER change_request_addition_no_delete BEFORE DELETE ON change_request_addition FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER change_request_addition_no_update BEFORE UPDATE ON change_request_addition FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER order_adjustment_no_delete BEFORE DELETE ON order_adjustment FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER order_adjustment_no_update BEFORE UPDATE ON order_adjustment FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- Fila: contador de atividade (versão dos eventos de fila) -----------------------------------------
-- Cresce a cada mudança de quem está na fila. A versão de um evento de fila é
-- versão da fila (abrir/pausar/fechar) + atividade: as duas só crescem, então a soma também.
ALTER TABLE queue_counter ADD COLUMN activity_seq bigint NOT NULL DEFAULT 0 CHECK (activity_seq >= 0);

-- Outbox de eventos (ADR-0010) --------------------------------------------------------------------
-- Gravada na MESMA transação da mudança: se o comando desfaz, o evento some junto. Um registro
-- por audiência, já com a visão projetada para ela. O publicador e o event_log (cursor) são da
-- etapa 06; até lá as linhas só se acumulam (sem entrega).
CREATE TABLE outbox (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id         uuid NOT NULL UNIQUE DEFAULT uuidv7(),
    xact_id          xid8 NOT NULL DEFAULT pg_current_xact_id(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    audience         text NOT NULL CHECK (audience IN ('public', 'visit', 'account', 'professional', 'owner')),
    -- Filtros de entrega: profissional da audiência "professional"; dono da visita em "visit"/"account".
    professional_id  uuid,
    account_id       uuid,
    visitor_id       uuid,
    type             text NOT NULL CHECK (type ~ '^[a-z_]+(\.[a-z_]+)+$'),
    resource_type    text NOT NULL CHECK (resource_type ~ '^[a-z_]+$'),
    resource_id      uuid NOT NULL,
    resource_version bigint NOT NULL CHECK (resource_version >= 0),
    payload          jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at      timestamptz NOT NULL,
    CHECK ((audience = 'professional') = (professional_id IS NOT NULL)),
    CHECK ((audience IN ('visit', 'account')) = (num_nonnulls(account_id, visitor_id) = 1)),
    CHECK (audience <> 'account' OR account_id IS NOT NULL)
);
CREATE INDEX outbox_xact_idx ON outbox (xact_id, id);

-- +goose Down
SET CONSTRAINTS ALL IMMEDIATE;
DROP TABLE outbox;
ALTER TABLE queue_counter DROP COLUMN activity_seq;
DROP TABLE order_adjustment;
DROP TABLE change_request_addition;
DROP TABLE change_request;
DROP TRIGGER order_item_total_check ON order_item;
DROP TRIGGER service_order_total_check ON service_order;
DROP FUNCTION check_order_total();
DROP INDEX order_item_order_idx;
ALTER TABLE order_item DROP CONSTRAINT order_item_performed_check, DROP CONSTRAINT order_item_removed_check,
    DROP CONSTRAINT order_item_charged_check, DROP COLUMN removed_at, DROP COLUMN outcome_reason, DROP COLUMN reported_outcome;
ALTER TABLE service_order DROP CONSTRAINT service_order_review_check, DROP CONSTRAINT service_order_frozen_check;
ALTER TABLE service_order DROP CONSTRAINT service_order_closure_reason_check;
ALTER TABLE service_order ADD CONSTRAINT service_order_closure_reason_check CHECK (closure_reason IN
    ('cancelled_by_customer', 'cancelled_by_owner', 'cancelled_by_account_deletion', 'no_show', 'interruption_zero_total'));
DROP INDEX service_order_professional_idx;
DROP INDEX service_order_list_idx;
ALTER TABLE service_order DROP COLUMN review_note, DROP COLUMN interrupted_at, DROP COLUMN loyalty_eligible, DROP COLUMN professional_id;
