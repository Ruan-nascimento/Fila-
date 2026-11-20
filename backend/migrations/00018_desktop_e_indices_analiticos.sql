-- Instalações desktop autorizadas e índices do Dashboard (ADR-0027). Dono: módulo identidade
-- (tabelas desktop_*); os índices servem às consultas somente leitura do módulo analitico.
--
-- Ativação por código de uso único (fila-admin), instalação com a chave pública (JWK P-256 e a
-- impressão digital RFC 7638), vínculo da instalação a uma sessão de dono já existente e provas
-- DPoP (RFC 9449) já vistas, para recusar reutilização.

-- +goose Up

CREATE TABLE desktop_activation_code (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id      uuid NOT NULL REFERENCES account (id),
    code_hash       bytea NOT NULL UNIQUE CHECK (octet_length(code_hash) = 32),
    label           text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 60),
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at     timestamptz,
    revoked_at      timestamptz,
    installation_id uuid,
    CHECK ((consumed_at IS NULL) = (installation_id IS NULL)),
    CHECK (consumed_at IS NULL OR revoked_at IS NULL)
);

CREATE TABLE desktop_installation (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id    uuid NOT NULL REFERENCES account (id),
    label         text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 60),
    -- Impressão digital SHA-256 da chave pública (RFC 7638), em base64url: é o "jkt" do DPoP.
    jkt           text NOT NULL UNIQUE CHECK (jkt ~ '^[A-Za-z0-9_-]{43}$'),
    public_jwk    jsonb NOT NULL CHECK (jsonb_typeof(public_jwk) = 'object'),
    created_at    timestamptz NOT NULL,
    last_seen_at  timestamptz,
    revoked_at    timestamptz,
    revoke_reason text CHECK (revoke_reason IN ('revoked_by_admin', 'replaced', 'account_deletion')),
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);
CREATE INDEX desktop_installation_account_idx ON desktop_installation (account_id) WHERE revoked_at IS NULL;
ALTER TABLE desktop_activation_code ADD CONSTRAINT desktop_activation_code_installation_fk
    FOREIGN KEY (installation_id) REFERENCES desktop_installation (id);

-- Ticket de 60 s emitido pela gestão (cookie da sessão de dono) e trocado pela instalação (prova
-- DPoP) por um token de acesso preso à chave. Sem cópia de cookie para JavaScript ou arquivo.
CREATE TABLE desktop_link_ticket (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    session_id  uuid NOT NULL REFERENCES account_session (id),
    ticket_hash bytea NOT NULL UNIQUE CHECK (octet_length(ticket_hash) = 32),
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at timestamptz
);
CREATE INDEX desktop_link_ticket_session_idx ON desktop_link_ticket (session_id);

-- O vínculo é subordinado à sessão: vale só enquanto a sessão for a ocupante da vaga de dono.
CREATE TABLE desktop_binding (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    installation_id uuid NOT NULL REFERENCES desktop_installation (id),
    session_id      uuid NOT NULL REFERENCES account_session (id),
    token_hash      bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL CHECK (expires_at > created_at),
    revoked_at      timestamptz
);
CREATE INDEX desktop_binding_session_idx ON desktop_binding (session_id) WHERE revoked_at IS NULL;

-- jti de provas DPoP aceitas (janela curta). Repetir a mesma prova é recusado pela chave primária.
CREATE TABLE desktop_proof_seen (
    jkt        text NOT NULL,
    jti        text NOT NULL CHECK (char_length(jti) BETWEEN 16 AND 100),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (jkt, jti)
);
CREATE INDEX desktop_proof_seen_expiry_idx ON desktop_proof_seen (expires_at);

-- Índices das consultas analíticas (medidos em docs/desktop/desempenho.md: sem eles, cada série
-- percorria todas as linhas da tabela; com eles, só a loja e o intervalo pedidos).
CREATE INDEX receipt_est_registered_idx ON receipt (establishment_id, registered_at);
CREATE INDEX refund_est_resolved_idx ON refund (establishment_id, resolved_at) WHERE status = 'confirmed';
CREATE INDEX participation_est_created_idx ON participation (establishment_id, created_at);
CREATE INDEX participation_est_called_idx ON participation (establishment_id, called_at) WHERE called_at IS NOT NULL;
CREATE INDEX participation_est_finished_idx ON participation (establishment_id, finished_at) WHERE finished_at IS NOT NULL;
CREATE INDEX service_order_est_closed_idx ON service_order (establishment_id, closed_at) WHERE status = 'settled';
CREATE INDEX order_item_est_performed_idx ON order_item (establishment_id, performed_at) WHERE status = 'performed';
CREATE INDEX loyalty_ledger_est_occurred_idx ON loyalty_ledger (establishment_id, occurred_at);

-- +goose Down
DROP INDEX loyalty_ledger_est_occurred_idx;
DROP INDEX order_item_est_performed_idx;
DROP INDEX service_order_est_closed_idx;
DROP INDEX participation_est_finished_idx;
DROP INDEX participation_est_called_idx;
DROP INDEX participation_est_created_idx;
DROP INDEX refund_est_resolved_idx;
DROP INDEX receipt_est_registered_idx;
DROP TABLE desktop_proof_seen;
DROP TABLE desktop_binding;
DROP TABLE desktop_link_ticket;
ALTER TABLE desktop_activation_code DROP CONSTRAINT desktop_activation_code_installation_fk;
DROP TABLE desktop_installation;
DROP TABLE desktop_activation_code;
