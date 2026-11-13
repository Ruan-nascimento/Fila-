-- Etapa 07 — exclusão de conta (módulo exclusao). Uma solicitação por conta (ÚNICA-VEZ, R-EXC-08).
-- O protocolo entregue ao titular nunca é guardado: só o HMAC (para consultar) e um sufixo curto
-- (para a equipe e a repetição se referirem a ele sem conhecê-lo).
-- A conta continua existindo como pseudônimo (status 'deleted', perfil sobrescrito): comandas,
-- itens, correções e auditoria seguem apontando para o mesmo identificador, sem dado pessoal.

-- +goose Up

CREATE TABLE deletion_request (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id       uuid NOT NULL UNIQUE REFERENCES account (id),
    protocol_hmac    bytea NOT NULL UNIQUE CHECK (octet_length(protocol_hmac) = 32),
    protocol_suffix  text NOT NULL CHECK (protocol_suffix ~ '^[A-Z2-7]{6}$'),
    status           text NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'completed')),
    -- Profissionais que a conta representava (vínculos já encerrados): a fila deles precisa ser
    -- esvaziada pelo dono antes de concluir (professional_queue_has_participants).
    professional_ids uuid[] NOT NULL DEFAULT '{}',
    requested_at     timestamptz NOT NULL,
    checked_at       timestamptz,
    completed_at     timestamptz,
    -- Evidência da anonimização: só categorias, contagens e datas. Nunca nome, e-mail ou nick.
    evidence         jsonb CHECK (evidence IS NULL OR jsonb_typeof(evidence) = 'object'),
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
    CHECK (status <> 'completed' OR evidence IS NOT NULL)
);
CREATE INDEX deletion_request_processing_idx ON deletion_request (requested_at) WHERE status = 'processing';

-- Bloqueios atuais por loja (recalculados pelo worker e na solicitação). A loja vê só os seus.
CREATE TABLE deletion_request_block (
    request_id       uuid NOT NULL REFERENCES deletion_request (id),
    establishment_id uuid NOT NULL REFERENCES establishment (id),
    blocker          text NOT NULL CHECK (blocker IN
                         ('service_in_progress', 'financial_pending', 'professional_queue_has_participants')),
    PRIMARY KEY (request_id, establishment_id, blocker)
);
CREATE INDEX deletion_request_block_establishment_idx ON deletion_request_block (establishment_id);

CREATE TRIGGER deletion_request_no_delete BEFORE DELETE ON deletion_request FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- +goose Down
DROP TABLE deletion_request_block;
DROP TABLE deletion_request;
