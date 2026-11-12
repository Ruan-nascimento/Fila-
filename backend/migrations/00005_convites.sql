-- Etapa 02 — convites de acesso (profissional pelo dono; dono inicial pelo operador).
-- Dono: módulo identidade. Token de uso único em SHA-256; validade de 24 horas.

-- +goose Up

CREATE TABLE invite (
    id                    uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id      uuid NOT NULL REFERENCES establishment (id),
    role                  text NOT NULL CHECK (role IN ('owner', 'professional')),
    professional_id       uuid,
    -- E-mail convidado: necessário para ligar o convite à conta certa no aceite.
    email                 text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    token_hash            bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    status                text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked')),
    -- Conta do dono que convidou; nulo quando o convite veio do operador (fila-admin).
    created_by_account_id uuid REFERENCES account (id),
    created_at            timestamptz NOT NULL,
    expires_at            timestamptz NOT NULL CHECK (expires_at > created_at),
    accepted_at           timestamptz,
    accepted_account_id   uuid REFERENCES account (id),
    revoked_at            timestamptz,
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id),
    CHECK ((role = 'professional') = (professional_id IS NOT NULL)),
    CHECK ((status = 'accepted') = (accepted_at IS NOT NULL AND accepted_account_id IS NOT NULL)),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
-- No máximo um convite pendente por profissional (o novo revoga o anterior, T-IN-01).
CREATE UNIQUE INDEX invite_pending_professional_uq ON invite (professional_id)
    WHERE status = 'pending' AND professional_id IS NOT NULL;
CREATE INDEX invite_establishment_idx ON invite (establishment_id, created_at DESC);

-- +goose Down
DROP TABLE invite;
