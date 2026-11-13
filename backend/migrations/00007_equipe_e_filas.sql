-- Etapa 03 — profissionais completos e fila de cada profissional. Dono: módulo estabelecimento.
-- Migração compatível com a etapa 02: a tabela professional já existe (mínima); profissionais
-- já cadastrados recebem letra e fila automaticamente.

-- +goose Up

-- Letra da fila (prefixo das senhas, ex.: A017), única por loja.
ALTER TABLE professional ADD COLUMN queue_letter text CHECK (queue_letter ~ '^[A-Z]$');
UPDATE professional p SET queue_letter = chr(64 + n.rn::int)
FROM (SELECT id, row_number() OVER (PARTITION BY establishment_id ORDER BY created_at, id) AS rn FROM professional) n
WHERE n.id = p.id;
ALTER TABLE professional ALTER COLUMN queue_letter SET NOT NULL;
ALTER TABLE professional ADD CONSTRAINT professional_letter_uq UNIQUE (establishment_id, queue_letter);

-- Motivo de fim do vínculo: permite religar só o que a desativação do profissional desligou.
ALTER TABLE staff_membership ADD COLUMN ended_reason text
    CHECK (ended_reason IN ('professional_deactivated', 'account_deletion'));
ALTER TABLE staff_membership ADD CONSTRAINT staff_membership_ended_reason_ck
    CHECK (status = 'inactive' OR ended_reason IS NULL);

-- Uma fila por profissional (R-FILA-01), criada fechada.
CREATE TABLE queue (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id   uuid NOT NULL,
    professional_id    uuid NOT NULL UNIQUE,
    status             text NOT NULL DEFAULT 'closed' CHECK (status IN ('open', 'paused', 'closed')),
    version            bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    -- Sinal de indisponibilidade do profissional para o dono (T-Q-05); não muda o estado.
    unavailable        boolean NOT NULL DEFAULT false,
    unavailable_note   text CHECK (char_length(unavailable_note) <= 200),
    unavailable_since  timestamptz,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    FOREIGN KEY (establishment_id, professional_id) REFERENCES professional (establishment_id, id),
    UNIQUE (establishment_id, id),
    CHECK (unavailable OR (unavailable_note IS NULL AND unavailable_since IS NULL))
);
INSERT INTO queue (establishment_id, professional_id, created_at, updated_at)
SELECT establishment_id, id, created_at, created_at FROM professional;

-- Histórico não se apaga (R-FILA-08, R-CMD-03): desativar é mudar o estado, nunca DELETE.
-- +goose StatementBegin
CREATE FUNCTION forbid_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'linhas de % não podem ser apagadas; use desativação', TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER establishment_no_delete BEFORE DELETE ON establishment FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER professional_no_delete BEFORE DELETE ON professional FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER queue_no_delete BEFORE DELETE ON queue FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- +goose Down
DROP TRIGGER queue_no_delete ON queue;
DROP TRIGGER professional_no_delete ON professional;
DROP TRIGGER establishment_no_delete ON establishment;
DROP TABLE queue;
DROP FUNCTION forbid_delete();
ALTER TABLE staff_membership DROP CONSTRAINT staff_membership_ended_reason_ck;
ALTER TABLE staff_membership DROP COLUMN ended_reason;
ALTER TABLE professional DROP CONSTRAINT professional_letter_uq;
ALTER TABLE professional DROP COLUMN queue_letter;
