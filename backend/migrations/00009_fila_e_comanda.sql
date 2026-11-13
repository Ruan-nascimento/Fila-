-- Etapa 04 — participação na fila (módulo fila) e comanda aberta na entrada (módulo atendimento).
-- Invariantes protegidas pelo banco (não só pelo código):
--   * uma participação ativa (aguardando/chamada/em atendimento) por identidade e loja (R-FILA-02);
--   * no máximo uma pessoa chamada/em atendimento por fila = por profissional (R-FILA-14);
--   * posição FIFO e senha do dia únicas por fila (R-FILA-05);
--   * cada serviço no máximo uma vez por comanda (R-CMD-04); preço em centavos (bigint).

-- +goose Up

-- Comanda (módulo atendimento). Criada junto com a participação (T-O-01).
CREATE TABLE service_order (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    participation_id uuid NOT NULL UNIQUE,
    account_id       uuid REFERENCES account (id),
    visitor_id       uuid REFERENCES visitor (id),
    status           text NOT NULL DEFAULT 'open' CHECK (status IN
                         ('open', 'pending_review', 'awaiting_payment', 'awaiting_confirmation', 'settled', 'closed_no_charge')),
    closure_reason   text CHECK (closure_reason IN
                         ('cancelled_by_customer', 'cancelled_by_owner', 'cancelled_by_account_deletion', 'no_show', 'interruption_zero_total')),
    total_cents      bigint NOT NULL CHECK (total_cents >= 0),
    total_frozen_at  timestamptz,
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    closed_at        timestamptz,
    CHECK (num_nonnulls(account_id, visitor_id) = 1),
    CHECK ((status = 'closed_no_charge') = (closure_reason IS NOT NULL)),
    CHECK ((status IN ('settled', 'closed_no_charge')) = (closed_at IS NOT NULL)),
    UNIQUE (establishment_id, id)
);
-- Comanda pendente bloqueia nova entrada (R-FILA-03): busca rápida por identidade.
CREATE INDEX service_order_pending_account_idx ON service_order (establishment_id, account_id)
    WHERE account_id IS NOT NULL AND status IN ('open', 'pending_review', 'awaiting_payment');
CREATE INDEX service_order_pending_visitor_idx ON service_order (establishment_id, visitor_id)
    WHERE visitor_id IS NOT NULL AND status IN ('open', 'pending_review', 'awaiting_payment');

-- Itens com cópia (snapshot) do serviço aceito: mudanças no catálogo não reescrevem a visita (R-CMD-05).
CREATE TABLE order_item (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL,
    order_id         uuid NOT NULL,
    service_id       uuid NOT NULL,
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    unit_price_cents bigint NOT NULL CHECK (unit_price_cents > 0),
    duration_minutes integer NOT NULL CHECK (duration_minutes > 0),
    points           integer NOT NULL CHECK (points >= 0),
    status           text NOT NULL DEFAULT 'accepted' CHECK (status IN ('accepted', 'performed', 'not_performed', 'removed')),
    charged          boolean NOT NULL DEFAULT false,
    added_at         timestamptz NOT NULL,
    performed_at     timestamptz,
    FOREIGN KEY (establishment_id, order_id) REFERENCES service_order (establishment_id, id),
    FOREIGN KEY (establishment_id, service_id) REFERENCES service (establishment_id, id)
);
CREATE UNIQUE INDEX order_item_service_uq ON order_item (order_id, service_id) WHERE status <> 'removed';

-- Sequências por fila (módulo fila): posição FIFO e senha do dia, atribuídas sob bloqueio.
CREATE TABLE queue_counter (
    queue_id      uuid PRIMARY KEY REFERENCES queue (id),
    last_position bigint NOT NULL DEFAULT 0 CHECK (last_position >= 0),
    ticket_day    date,
    ticket_seq    integer NOT NULL DEFAULT 0 CHECK (ticket_seq >= 0)
);

CREATE TABLE participation (
    id                uuid PRIMARY KEY,
    establishment_id  uuid NOT NULL,
    queue_id          uuid NOT NULL,
    order_id          uuid NOT NULL UNIQUE REFERENCES service_order (id),
    account_id        uuid REFERENCES account (id),
    visitor_id        uuid REFERENCES visitor (id),
    -- Nick (visitante) ou nome da conta no momento da entrada; dado pessoal, só para a equipe.
    nick              text NOT NULL CHECK (char_length(nick) BETWEEN 1 AND 30),
    loyalty_eligible  boolean NOT NULL,
    status            text NOT NULL DEFAULT 'waiting' CHECK (status IN
                          ('waiting', 'called', 'in_service', 'completed', 'cancelled', 'no_show', 'interrupted')),
    position          bigint NOT NULL CHECK (position >= 1),
    ticket_day        date NOT NULL,
    ticket_number     integer NOT NULL CHECK (ticket_number >= 1),
    ticket_code       text NOT NULL CHECK (ticket_code ~ '^[A-Z][0-9]{3,}$'),
    called_at         timestamptz,
    tolerance_ends_at timestamptz,
    started_at        timestamptz,
    finished_at       timestamptz,
    end_reason        text CHECK (char_length(end_reason) <= 500),
    version           bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, queue_id) REFERENCES queue (establishment_id, id),
    CHECK (num_nonnulls(account_id, visitor_id) = 1),
    -- Tolerância fixa de 10 minutos, contada pelo servidor a partir da chamada (R-FILA-09).
    CHECK ((called_at IS NULL) = (tolerance_ends_at IS NULL)),
    CHECK (tolerance_ends_at IS NULL OR tolerance_ends_at = called_at + interval '10 minutes'),
    CHECK (status NOT IN ('called', 'in_service', 'no_show') OR called_at IS NOT NULL),
    CHECK (status NOT IN ('in_service', 'completed', 'interrupted') OR started_at IS NOT NULL),
    CHECK ((status IN ('completed', 'cancelled', 'no_show', 'interrupted')) = (finished_at IS NOT NULL)),
    UNIQUE (queue_id, position),
    UNIQUE (queue_id, ticket_day, ticket_number)
);
-- R-FILA-02: uma participação ativa por identidade e loja.
CREATE UNIQUE INDEX participation_active_account_uq ON participation (establishment_id, account_id)
    WHERE account_id IS NOT NULL AND status IN ('waiting', 'called', 'in_service');
CREATE UNIQUE INDEX participation_active_visitor_uq ON participation (establishment_id, visitor_id)
    WHERE visitor_id IS NOT NULL AND status IN ('waiting', 'called', 'in_service');
-- R-FILA-14: uma cadeira por profissional (uma fila por profissional).
CREATE UNIQUE INDEX participation_chair_uq ON participation (queue_id) WHERE status IN ('called', 'in_service');
CREATE INDEX participation_waiting_idx ON participation (queue_id, position) WHERE status = 'waiting';

-- A comanda aponta para a participação (as duas nascem na mesma transação).
ALTER TABLE service_order ADD CONSTRAINT service_order_participation_fk
    FOREIGN KEY (participation_id) REFERENCES participation (id) DEFERRABLE INITIALLY DEFERRED;

-- Histórico de transferências (R-FILA-13): motivo e consentimento registrados.
CREATE TABLE participation_transfer (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    participation_id uuid NOT NULL REFERENCES participation (id),
    from_queue_id    uuid NOT NULL REFERENCES queue (id),
    to_queue_id      uuid NOT NULL REFERENCES queue (id),
    from_ticket_code text NOT NULL,
    to_ticket_code   text NOT NULL,
    reason           text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500),
    consent_method   text NOT NULL CHECK (consent_method IN ('in_person_verbal')),
    consent_note     text CHECK (char_length(consent_note) <= 300),
    by_account_id    uuid NOT NULL REFERENCES account (id),
    at               timestamptz NOT NULL,
    CHECK (from_queue_id <> to_queue_id)
);
CREATE INDEX participation_transfer_participation_idx ON participation_transfer (participation_id, at);

CREATE TRIGGER service_order_no_delete BEFORE DELETE ON service_order FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER order_item_no_delete BEFORE DELETE ON order_item FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER participation_no_delete BEFORE DELETE ON participation FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER participation_transfer_no_delete BEFORE DELETE ON participation_transfer FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- +goose Down
DROP TABLE participation_transfer;
ALTER TABLE service_order DROP CONSTRAINT service_order_participation_fk;
DROP TABLE participation;
DROP TABLE queue_counter;
DROP TABLE order_item;
DROP TABLE service_order;
