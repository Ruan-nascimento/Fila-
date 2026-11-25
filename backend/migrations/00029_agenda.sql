-- Fila 2.0, parte 1 (ADR-0038) — agenda gratuita, operação híbrida e uma cadeira por profissional.
--
-- Nada muda para as lojas existentes: o modo padrão é "fila" (service_mode = 'queue'); a agenda
-- só aparece depois da adesão explícita do dono. As tabelas novas pertencem ao módulo agenda.
--
-- Invariantes protegidas pelo banco:
--   * dois compromissos/bloqueios ativos do MESMO profissional nunca se sobrepõem
--     (restrição de exclusão GiST em agenda_occupancy, intervalos semiabertos [início, fim));
--   * o mesmo cliente não tem duas reservas ativas sobrepostas na mesma loja, mesmo com
--     profissionais diferentes (exclusão em appointment por conta e por visitante);
--   * uma reserva vira no máximo UMA visita (participation_id único), e a visita sabe a origem;
--   * itens da reserva guardam preço, duração e quantidade aceitos (cópia; o catálogo pode mudar).
--
-- btree_gist é uma extensão "trusted" desde o PostgreSQL 13: o dono do banco (fila_migrador, ou o
-- dono do banco no Neon) consegue instalá-la sem superusuário. Não há now() em nenhum predicado.

-- +goose Up

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Configurações da agenda (seção própria em establishment_settings, ADR-0034).
ALTER TABLE establishment_settings
    ADD COLUMN service_mode               text NOT NULL DEFAULT 'queue' CHECK (service_mode IN ('queue', 'schedule', 'hybrid')),
    ADD COLUMN slot_step_minutes          integer NOT NULL DEFAULT 15 CHECK (slot_step_minutes BETWEEN 5 AND 120),
    ADD COLUMN buffer_minutes             integer NOT NULL DEFAULT 0 CHECK (buffer_minutes BETWEEN 0 AND 60),
    ADD COLUMN min_lead_minutes           integer NOT NULL DEFAULT 30 CHECK (min_lead_minutes BETWEEN 0 AND 1440),
    ADD COLUMN horizon_days               integer NOT NULL DEFAULT 30 CHECK (horizon_days BETWEEN 1 AND 180),
    -- nulo = herda a tolerância de chamada da loja (call_tolerance_minutes)
    ADD COLUMN schedule_tolerance_minutes integer CHECK (schedule_tolerance_minutes BETWEEN 1 AND 60),
    ADD COLUMN checkin_window_minutes     integer NOT NULL DEFAULT 15 CHECK (checkin_window_minutes BETWEEN 0 AND 60),
    ADD COLUMN max_future_appointments    integer NOT NULL DEFAULT 3 CHECK (max_future_appointments BETWEEN 1 AND 20),
    ADD COLUMN professionals_can_block    boolean NOT NULL DEFAULT false,
    ADD COLUMN agenda_version             bigint NOT NULL DEFAULT 1 CHECK (agenda_version >= 1);

-- Jornada semanal: uma VERSÃO por loja (professional_id nulo) ou por profissional, válida a partir
-- de effective_from (data local da loja). Versões são imutáveis: a vigente numa data é a de maior
-- effective_from ≤ data e, na mesma data, a gravada por último. Profissional sem versão própria (ou
-- com inherits_store) segue a da loja. Mudar a jornada de hoje não reescreve a de ontem.
CREATE TABLE agenda_schedule (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    professional_id  uuid,
    effective_from   date NOT NULL,
    inherits_store   boolean NOT NULL DEFAULT false,
    created_by       uuid NOT NULL REFERENCES account (id),
    created_at       timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id),
    CHECK (NOT inherits_store OR professional_id IS NOT NULL)
);

-- Turnos de cada dia da semana (0 = domingo). Minutos locais [início, fim), dentro do mesmo dia:
-- um turno que atravessa a meia-noite é gravado como dois trechos. Pausa/almoço = intervalo entre
-- turnos. modality (só no modo "os dois"): fila, agenda ou compartilhado.
CREATE TABLE agenda_shift (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    schedule_id  uuid NOT NULL REFERENCES agenda_schedule (id),
    weekday      smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_minute integer NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute   integer NOT NULL CHECK (end_minute BETWEEN 1 AND 1440),
    modality     text NOT NULL DEFAULT 'both' CHECK (modality IN ('queue', 'schedule', 'both')),
    CHECK (start_minute < end_minute),
    EXCLUDE USING gist (schedule_id WITH =, weekday WITH =, int4range(start_minute, end_minute) WITH &&)
);
CREATE INDEX agenda_schedule_lookup_idx ON agenda_schedule (establishment_id, professional_id, effective_from DESC, created_at DESC);

-- Exceções com data: folga/feriado (dia inteiro: minutos nulos) ou bloqueio de um trecho. Loja
-- inteira (professional_id nulo) ou um profissional. Motivo interno (nunca vai ao cliente).
CREATE TABLE agenda_exception (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    professional_id  uuid,
    local_date       date NOT NULL,
    start_minute     integer CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute       integer CHECK (end_minute BETWEEN 1 AND 1440),
    reason           text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 200),
    created_by       uuid NOT NULL REFERENCES account (id),
    created_at       timestamptz NOT NULL,
    removed_at       timestamptz,
    removed_by       uuid REFERENCES account (id),
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id),
    CHECK ((start_minute IS NULL) = (end_minute IS NULL)),
    CHECK (start_minute IS NULL OR start_minute < end_minute),
    CHECK ((removed_at IS NULL) = (removed_by IS NULL)),
    UNIQUE (establishment_id, id)
);
CREATE INDEX agenda_exception_day_idx ON agenda_exception (establishment_id, local_date) WHERE removed_at IS NULL;

-- Reserva (compromisso marcado). starts_at/ends_at são instantes absolutos; local_date e timezone
-- guardam a referência da loja no momento da reserva. ends_at já inclui a margem de preparação.
CREATE TABLE appointment (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id       uuid NOT NULL REFERENCES establishment (id),
    professional_id        uuid NOT NULL,
    account_id             uuid REFERENCES account (id),
    visitor_id             uuid REFERENCES visitor (id),
    -- Nome exibido à equipe (nick do visitante ou nome da conta no momento da reserva).
    nick                   text NOT NULL CHECK (char_length(nick) BETWEEN 1 AND 60),
    origin                 text NOT NULL CHECK (origin IN ('online', 'counter')),
    status                 text NOT NULL DEFAULT 'confirmed' CHECK (status IN
                               ('confirmed', 'called', 'in_service', 'completed', 'interrupted', 'no_show', 'cancelled')),
    cancelled_by           text CHECK (cancelled_by IN ('customer', 'store')),
    local_date             date NOT NULL,
    timezone               text NOT NULL,
    starts_at              timestamptz NOT NULL,
    ends_at                timestamptz NOT NULL,
    service_minutes        integer NOT NULL CHECK (service_minutes BETWEEN 1 AND 1440),
    buffer_minutes         integer NOT NULL CHECK (buffer_minutes BETWEEN 0 AND 60),
    -- Política congelada na reserva (mudar a configuração depois não muda este compromisso).
    tolerance_minutes      integer NOT NULL CHECK (tolerance_minutes BETWEEN 1 AND 60),
    checkin_window_minutes integer NOT NULL CHECK (checkin_window_minutes BETWEEN 0 AND 60),
    policy_version         bigint NOT NULL,
    price_cents            bigint NOT NULL CHECK (price_cents >= 0),
    called_at              timestamptz,
    deadline_at            timestamptz,
    arrived_at             timestamptz,
    arrival_signal_at      timestamptz,
    store_late_at          timestamptz,
    participation_id       uuid UNIQUE REFERENCES participation (id),
    end_reason             text CHECK (char_length(end_reason) <= 500),
    revision               integer NOT NULL DEFAULT 1 CHECK (revision >= 1),
    version                bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at             timestamptz NOT NULL,
    updated_at             timestamptz NOT NULL,
    finished_at            timestamptz,
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id),
    CHECK (num_nonnulls(account_id, visitor_id) = 1),
    CHECK (starts_at < ends_at),
    CHECK ((status = 'cancelled') = (cancelled_by IS NOT NULL)),
    CHECK ((called_at IS NULL) = (deadline_at IS NULL)),
    CHECK (status NOT IN ('called', 'in_service', 'completed', 'interrupted', 'no_show') OR
           (called_at IS NOT NULL AND participation_id IS NOT NULL)),
    CHECK ((status IN ('completed', 'interrupted', 'no_show', 'cancelled')) = (finished_at IS NOT NULL)),
    UNIQUE (establishment_id, id),
    -- O mesmo cliente não marca dois horários sobrepostos na mesma loja (com qualquer profissional).
    CONSTRAINT appointment_account_overlap EXCLUDE USING gist (
        establishment_id WITH =, account_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
        WHERE (account_id IS NOT NULL AND status IN ('confirmed', 'called', 'in_service')),
    CONSTRAINT appointment_visitor_overlap EXCLUDE USING gist (
        establishment_id WITH =, visitor_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
        WHERE (visitor_id IS NOT NULL AND status IN ('confirmed', 'called', 'in_service'))
);
CREATE INDEX appointment_professional_period_idx ON appointment (establishment_id, professional_id, starts_at);
CREATE INDEX appointment_account_idx ON appointment (account_id, starts_at DESC, id DESC) WHERE account_id IS NOT NULL;
CREATE INDEX appointment_visitor_idx ON appointment (visitor_id, starts_at DESC, id DESC) WHERE visitor_id IS NOT NULL;
-- Tarefas do worker: chamadas vencidas (falta) e confirmadas que a loja não chamou (atraso da loja).
CREATE INDEX appointment_called_deadline_idx ON appointment (deadline_at) WHERE status = 'called';
CREATE INDEX appointment_confirmed_start_idx ON appointment (starts_at) WHERE status = 'confirmed';

-- Itens aceitos na reserva (cópia do catálogo). Produto tem duração zero.
CREATE TABLE appointment_item (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL,
    appointment_id   uuid NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('service', 'product')),
    service_id       uuid,
    product_id       uuid,
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    unit_price_cents bigint NOT NULL CHECK (unit_price_cents > 0),
    duration_minutes integer NOT NULL,
    points           integer NOT NULL CHECK (points >= 0),
    quantity         integer NOT NULL CHECK (quantity BETWEEN 1 AND 50),
    FOREIGN KEY (establishment_id, appointment_id) REFERENCES appointment (establishment_id, id),
    FOREIGN KEY (establishment_id, service_id) REFERENCES service (establishment_id, id),
    FOREIGN KEY (establishment_id, product_id) REFERENCES product (establishment_id, id),
    CHECK ((kind = 'service') = (service_id IS NOT NULL) AND (kind = 'product') = (product_id IS NOT NULL)),
    CHECK ((kind = 'service' AND duration_minutes > 0) OR (kind = 'product' AND duration_minutes = 0))
);
CREATE INDEX appointment_item_appointment_idx ON appointment_item (appointment_id);

-- Calendário de ocupações da cadeira: compromissos e bloqueios ativos. É a última barreira contra
-- sobreposição (quem escreve já trava a fila do profissional; ver ADR-0038 "ordem de travas").
-- Uma reserva chamada/em atendimento continua com a MESMA ocupação (não conflita consigo mesma).
CREATE TABLE agenda_occupancy (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL,
    professional_id  uuid NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('appointment', 'block')),
    appointment_id   uuid UNIQUE,
    exception_id     uuid,
    period           tstzrange NOT NULL,
    released_at      timestamptz,
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id),
    FOREIGN KEY (establishment_id, appointment_id) REFERENCES appointment (establishment_id, id),
    FOREIGN KEY (establishment_id, exception_id) REFERENCES agenda_exception (establishment_id, id),
    CHECK ((kind = 'appointment') = (appointment_id IS NOT NULL) AND (kind = 'block') = (exception_id IS NOT NULL)),
    CHECK (NOT isempty(period) AND NOT lower_inf(period) AND NOT upper_inf(period)
           AND lower_inc(period) AND NOT upper_inc(period)),
    CONSTRAINT agenda_occupancy_no_overlap EXCLUDE USING gist (professional_id WITH =, period WITH &&)
        WHERE (released_at IS NULL)
);
CREATE UNIQUE INDEX agenda_occupancy_block_uq ON agenda_occupancy (exception_id, professional_id) WHERE exception_id IS NOT NULL;

-- Histórico de transições da reserva (imutável): quem, quando, de onde para onde e por quê.
CREATE TABLE appointment_event (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL,
    appointment_id   uuid NOT NULL,
    action           text NOT NULL,
    from_status      text,
    to_status        text NOT NULL,
    actor_kind       text NOT NULL CHECK (actor_kind IN ('customer', 'visitor', 'owner', 'professional', 'system')),
    actor_id         uuid,
    reason           text CHECK (char_length(reason) <= 500),
    starts_at        timestamptz NOT NULL,
    professional_id  uuid NOT NULL,
    revision         integer NOT NULL,
    at               timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, appointment_id) REFERENCES appointment (establishment_id, id)
);
CREATE INDEX appointment_event_appointment_idx ON appointment_event (appointment_id, at, id);

-- A visita sabe se veio da fila ou de uma reserva chamada (indicadores não contam duas vezes).
ALTER TABLE participation ADD COLUMN origin text NOT NULL DEFAULT 'queue' CHECK (origin IN ('queue', 'appointment'));

CREATE TRIGGER agenda_schedule_no_delete BEFORE DELETE ON agenda_schedule FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER agenda_shift_no_delete BEFORE DELETE ON agenda_shift FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER agenda_exception_no_delete BEFORE DELETE ON agenda_exception FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER appointment_no_delete BEFORE DELETE ON appointment FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER appointment_item_no_delete BEFORE DELETE ON appointment_item FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER agenda_occupancy_no_delete BEFORE DELETE ON agenda_occupancy FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER appointment_event_no_delete BEFORE DELETE ON appointment_event FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER appointment_event_no_update BEFORE UPDATE ON appointment_event FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- +goose Down
-- Só volta sem nenhuma reserva gravada (nunca apaga compromisso de cliente).
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM appointment) THEN
        RAISE EXCEPTION 'Há reservas gravadas: a agenda não pode ser removida por migração.';
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE participation DROP COLUMN origin;
DROP TABLE appointment_event;
DROP TABLE agenda_occupancy;
DROP TABLE appointment_item;
DROP TABLE appointment;
DROP TABLE agenda_exception;
DROP TABLE agenda_shift;
DROP TABLE agenda_schedule;
ALTER TABLE establishment_settings
    DROP COLUMN agenda_version, DROP COLUMN professionals_can_block, DROP COLUMN max_future_appointments,
    DROP COLUMN checkin_window_minutes, DROP COLUMN schedule_tolerance_minutes, DROP COLUMN horizon_days,
    DROP COLUMN min_lead_minutes, DROP COLUMN buffer_minutes, DROP COLUMN slot_step_minutes, DROP COLUMN service_mode;
