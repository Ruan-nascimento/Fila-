-- Etapa 02 — loja, profissional e vínculo de equipe (mínimo para dono inicial, convites e
-- sessões de equipe). Dono: módulo estabelecimento. A etapa 03 acrescenta fila, catálogo,
-- configuração da loja e o cadastro de profissionais pela interface (migrações novas).

-- +goose Up

CREATE TABLE establishment (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    slug       text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{1,48}[a-z0-9])$'),
    timezone   text NOT NULL CHECK (char_length(timezone) BETWEEN 1 AND 64),
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
    version    bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE professional (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    display_name     text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 40),
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    -- Permite chaves estrangeiras compostas que garantem "mesma loja" no banco.
    UNIQUE (establishment_id, id)
);

-- Vínculo de equipe: só concede papel com sessão de contexto staff (ADR-0006).
-- account_id aponta para a conta (módulo identidade); a exclusão de conta (etapa 07)
-- anonimiza o perfil sem apagar a linha, então a chave estrangeira continua válida.
CREATE TABLE staff_membership (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    account_id       uuid NOT NULL REFERENCES account (id),
    role             text NOT NULL CHECK (role IN ('owner', 'professional')),
    professional_id  uuid,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id),
    CHECK ((role = 'professional') = (professional_id IS NOT NULL))
);
-- Uma conta tem no máximo um vínculo ativo por papel em cada loja.
CREATE UNIQUE INDEX staff_membership_role_uq ON staff_membership (establishment_id, account_id, role)
    WHERE status = 'active';
-- Um profissional tem no máximo uma conta com acesso ativo.
CREATE UNIQUE INDEX staff_membership_professional_uq ON staff_membership (professional_id)
    WHERE status = 'active' AND professional_id IS NOT NULL;
CREATE INDEX staff_membership_account_idx ON staff_membership (account_id) WHERE status = 'active';

-- +goose Down
DROP TABLE staff_membership;
DROP TABLE professional;
DROP TABLE establishment;
