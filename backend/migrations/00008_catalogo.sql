-- Etapa 03 — catálogo de serviços e habilitações. Dono: módulo catalogo.
-- Dinheiro em centavos (bigint). Cada versão do serviço fica registrada em service_revision:
-- mudanças valem só para inclusões futuras e o histórico do catálogo nunca é reescrito.

-- +goose Up

CREATE TABLE service (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    price_cents      bigint NOT NULL CHECK (price_cents BETWEEN 1 AND 10000000),
    duration_minutes integer NOT NULL CHECK (duration_minutes BETWEEN 1 AND 480),
    points           integer NOT NULL CHECK (points BETWEEN 0 AND 100000),
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    UNIQUE (establishment_id, id)
);
CREATE INDEX service_establishment_idx ON service (establishment_id, status, name);

-- Profissionais habilitados (R-CMD-01). Chaves compostas garantem a mesma loja no banco.
CREATE TABLE service_professional (
    establishment_id uuid NOT NULL,
    service_id       uuid NOT NULL,
    professional_id  uuid NOT NULL,
    PRIMARY KEY (service_id, professional_id),
    FOREIGN KEY (establishment_id, service_id) REFERENCES service (establishment_id, id),
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id)
);
CREATE INDEX service_professional_professional_idx ON service_professional (professional_id);

-- Uma linha por versão do serviço (preço, duração, pontos, situação e habilitações vigentes).
CREATE TABLE service_revision (
    service_id       uuid NOT NULL REFERENCES service (id),
    version          bigint NOT NULL CHECK (version >= 1),
    name             text NOT NULL,
    price_cents      bigint NOT NULL CHECK (price_cents > 0),
    duration_minutes integer NOT NULL,
    points           integer NOT NULL,
    status           text NOT NULL,
    professional_ids uuid[] NOT NULL,
    changed_at       timestamptz NOT NULL,
    changed_by       uuid,
    PRIMARY KEY (service_id, version)
);

CREATE TRIGGER service_no_delete BEFORE DELETE ON service FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER service_revision_no_delete BEFORE DELETE ON service_revision FOR EACH ROW EXECUTE FUNCTION forbid_delete();
-- Revisões são imutáveis.
-- +goose StatementBegin
CREATE FUNCTION forbid_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'linhas de % são imutáveis', TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER service_revision_immutable BEFORE UPDATE ON service_revision FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- +goose Down
DROP TABLE service_revision;
DROP FUNCTION forbid_update();
DROP TABLE service_professional;
DROP TABLE service;
