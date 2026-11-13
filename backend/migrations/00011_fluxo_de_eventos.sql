-- Etapa 06 — fluxo de eventos (platform/outbox): event_log com posição densa atribuída DEPOIS do
-- commit (ADR-0010/0021). A outbox (00010) é a fila de entrada; o publicador do worker move para
-- cá só linhas de transações já terminadas, em ordem, e apaga da outbox na mesma transação.

-- +goose Up

-- Estado do fluxo: uma linha. A época identifica esta "linha do tempo" de posições; muda quando o
-- banco é restaurado de backup (posições poderiam se repetir), invalidando cursores antigos.
-- O bloqueio desta linha (FOR UPDATE) serializa os publicadores: a posição cresce na ordem de commit.
CREATE TABLE event_stream_state (
    id            smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    epoch         uuid NOT NULL DEFAULT gen_random_uuid(),
    last_position bigint NOT NULL DEFAULT 0 CHECK (last_position >= 0),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
INSERT INTO event_stream_state (id) VALUES (1);

CREATE TABLE event_log (
    position         bigint PRIMARY KEY CHECK (position >= 1),
    event_id         uuid NOT NULL UNIQUE,
    establishment_id uuid NOT NULL,
    audience         text NOT NULL CHECK (audience IN ('public', 'visit', 'account', 'professional', 'owner')),
    professional_id  uuid,
    account_id       uuid,
    visitor_id       uuid,
    type             text NOT NULL,
    resource_type    text NOT NULL,
    resource_id      uuid NOT NULL,
    resource_version bigint NOT NULL CHECK (resource_version >= 0),
    payload          jsonb NOT NULL,
    occurred_at      timestamptz NOT NULL,
    published_at     timestamptz NOT NULL
);
-- Replay de uma conexão: eventos da loja depois do cursor, em ordem.
CREATE INDEX event_log_establishment_idx ON event_log (establishment_id, position);
-- Retenção (24 h): apagar por idade.
CREATE INDEX event_log_published_idx ON event_log (published_at);

-- +goose Down
DROP TABLE event_log;
DROP TABLE event_stream_state;
