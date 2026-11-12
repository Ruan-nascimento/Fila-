-- Etapa 02 — contas, tokens de e-mail e sessões. Dono: módulo identidade.
-- Segredos (senha, tokens) nunca ficam em claro: senha em Argon2id, tokens em SHA-256.

-- +goose Up

CREATE TABLE account (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    name                text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    email               text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    -- Forma usada para unicidade (minúsculas, NFC). Unicidade só entre contas não excluídas.
    email_normalized    text NOT NULL CHECK (email_normalized = lower(email_normalized)),
    password_hash       text NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
    status              text NOT NULL CHECK (status IN ('pending_verification', 'active', 'deletion_processing', 'deleted')),
    email_verified_at   timestamptz,
    password_changed_at timestamptz NOT NULL,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    CHECK (status = 'pending_verification' OR email_verified_at IS NOT NULL)
);
CREATE UNIQUE INDEX account_email_uq ON account (email_normalized) WHERE status <> 'deleted';

-- Tokens de uso único enviados por e-mail (verificação: 24 h; redefinição de senha: 30 min).
CREATE TABLE account_token (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id  uuid NOT NULL REFERENCES account (id),
    purpose     text NOT NULL CHECK (purpose IN ('email_verification', 'password_reset')),
    token_hash  bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at timestamptz,
    revoked_at  timestamptz,
    CHECK (consumed_at IS NULL OR revoked_at IS NULL)
);
CREATE INDEX account_token_account_idx ON account_token (account_id, purpose)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

-- Sessões de conta (cookie __Host-fila_session). O banco guarda só o SHA-256 do token.
-- kind: customer (7 dias sem uso ou 30 dias desde o login) ou staff (12 horas desde o login).
CREATE TABLE account_session (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id           uuid NOT NULL REFERENCES account (id),
    token_hash           bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    kind                 text NOT NULL CHECK (kind IN ('customer', 'staff')),
    created_at           timestamptz NOT NULL,
    last_used_at         timestamptz NOT NULL,
    absolute_expires_at  timestamptz NOT NULL CHECK (absolute_expires_at > created_at),
    idle_timeout_seconds integer CHECK (idle_timeout_seconds > 0),
    reauth_valid_until   timestamptz,
    revoked_at           timestamptz,
    revoke_reason        text CHECK (revoke_reason IN
                             ('logout', 'password_changed', 'password_reset', 'replaced', 'account_deletion', 'membership_ended')),
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL)),
    CHECK ((kind = 'staff') = (idle_timeout_seconds IS NULL))
);
CREATE INDEX account_session_active_idx ON account_session (account_id) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE account_session;
DROP TABLE account_token;
DROP TABLE account;
