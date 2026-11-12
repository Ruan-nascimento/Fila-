-- Etapa 02 — visitante (ADR-0011, ADR-0017). Dono: módulo identidade.
-- O cookie identifica o NAVEGADOR (visitor_browser); cada credencial liga o navegador a um
-- visitante de uma loja (no máximo uma ativa por loja). Não existe coluna de conta: associar
-- visita de visitante a conta é impossível por construção (R-VIS-09).

-- +goose Up

CREATE TABLE visitor_browser (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    token_hash   bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at   timestamptz NOT NULL,
    last_used_at timestamptz NOT NULL,
    revoked_at   timestamptz
);

CREATE TABLE visitor (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id        uuid NOT NULL REFERENCES establishment (id),
    -- Nick é dado pessoal e não autentica (R-VIS-01); removido após a necessidade operacional (etapa 07).
    nick                    text NOT NULL CHECK (char_length(nick) BETWEEN 1 AND 30),
    created_at              timestamptz NOT NULL,
    -- 30 dias desde a criação (R-VIS-02/04); recuperar não estende o prazo.
    absolute_expires_at     timestamptz NOT NULL CHECK (absolute_expires_at > created_at),
    -- Encerramento da comanda (preenchido pelos módulos de comanda/pagamento a partir da etapa 05):
    -- credencial e código valem até 24 h depois (R-VIS-03).
    visit_closed_at         timestamptz,
    -- HMAC-SHA256 do código de recuperação (chave derivada de FILA_SECRET_KEY). Nunca o código.
    recovery_code_hmac      bytea NOT NULL UNIQUE CHECK (octet_length(recovery_code_hmac) = 32),
    recovery_code_issued_at timestamptz NOT NULL,
    UNIQUE (id, establishment_id)
);

CREATE TABLE visitor_credential (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    visitor_id       uuid NOT NULL,
    establishment_id uuid NOT NULL,
    browser_id       uuid NOT NULL REFERENCES visitor_browser (id),
    created_at       timestamptz NOT NULL,
    last_used_at     timestamptz NOT NULL,
    revoked_at       timestamptz,
    revoke_reason    text CHECK (revoke_reason IN ('ended', 'recovered_elsewhere', 'replaced')),
    FOREIGN KEY (visitor_id, establishment_id) REFERENCES visitor (id, establishment_id),
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);
-- Um navegador tem no máximo uma credencial ativa por loja.
CREATE UNIQUE INDEX visitor_credential_active_uq ON visitor_credential (browser_id, establishment_id)
    WHERE revoked_at IS NULL;
CREATE INDEX visitor_credential_visitor_idx ON visitor_credential (visitor_id) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE visitor_credential;
DROP TABLE visitor;
DROP TABLE visitor_browser;
