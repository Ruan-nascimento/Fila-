-- Sessão única do dono (ADR-0027). Dono: módulo identidade.
--
-- owner_session_slot: UM registro por conta com vínculo de dono. A linha é a autoridade: só a
-- sessão apontada por session_id exerce privilégios de dono. Login, troca e logout travam a linha
-- (SELECT … FOR UPDATE) e mutações de dono a travam em modo compartilhado (FOR SHARE) no começo da
-- transação: troca e mutação ficam serializadas por conta. session_id é UNIQUE e a conta é a chave
-- primária: duas sessões de dono para a mesma conta são impossíveis no banco.
-- owner_session_challenge: desafio curto (2 min) da tela "continuar neste aparelho". Guarda só
-- hashes (do desafio e do cookie da tentativa) e a geração da vaga que a pessoa viu.

-- +goose Up

ALTER TABLE account_session DROP CONSTRAINT account_session_revoke_reason_check;
ALTER TABLE account_session ADD CONSTRAINT account_session_revoke_reason_check CHECK (revoke_reason IN
    ('logout', 'password_changed', 'password_reset', 'replaced', 'account_deletion', 'membership_ended', 'restore',
     'owner_session_replaced', 'owner_session_migration'));

CREATE TABLE owner_session_slot (
    account_id  uuid PRIMARY KEY REFERENCES account (id),
    -- NULL = vaga livre (logout comprovado). Sessão revogada ou vencida também conta como livre:
    -- a validade é conferida junto com account_session a cada uso.
    session_id  uuid UNIQUE REFERENCES account_session (id),
    -- Cresce a cada troca de ocupante: um desafio emitido contra a geração N não vale na N+1.
    generation  bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    updated_at  timestamptz NOT NULL
);

CREATE TABLE owner_session_challenge (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id          uuid NOT NULL REFERENCES account (id),
    token_hash          bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    attempt_hash        bytea NOT NULL CHECK (octet_length(attempt_hash) = 32),
    replaces_session_id uuid NOT NULL REFERENCES account_session (id),
    generation          bigint NOT NULL,
    created_at          timestamptz NOT NULL,
    expires_at          timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at         timestamptz,
    outcome             text CHECK (outcome IN ('confirmed', 'cancelled', 'stale')),
    result_session_id   uuid REFERENCES account_session (id),
    CHECK ((consumed_at IS NULL) = (outcome IS NULL)),
    CHECK ((outcome = 'confirmed') = (result_session_id IS NOT NULL))
);
CREATE INDEX owner_session_challenge_account_idx ON owner_session_challenge (account_id, created_at DESC);

-- Política para as sessões anteriores a esta migração (documentada no ADR-0027): para cada conta
-- com vínculo ativo de dono, a sessão de equipe válida usada mais recentemente fica com a vaga;
-- as demais sessões de equipe dessa conta são encerradas. Sessões de cliente e de quem não é dono
-- não mudam.
INSERT INTO owner_session_slot (account_id, session_id, generation, updated_at)
SELECT DISTINCT ON (s.account_id) s.account_id, s.id, 1, now()
FROM account_session s
WHERE s.kind = 'staff' AND s.revoked_at IS NULL AND s.absolute_expires_at > now()
  AND EXISTS (SELECT 1 FROM staff_membership m
              WHERE m.account_id = s.account_id AND m.role = 'owner' AND m.status = 'active')
ORDER BY s.account_id, s.last_used_at DESC, s.created_at DESC, s.id DESC;

UPDATE account_session s SET revoked_at = now(), revoke_reason = 'owner_session_migration'
WHERE s.kind = 'staff' AND s.revoked_at IS NULL
  AND EXISTS (SELECT 1 FROM staff_membership m
              WHERE m.account_id = s.account_id AND m.role = 'owner' AND m.status = 'active')
  AND NOT EXISTS (SELECT 1 FROM owner_session_slot o WHERE o.session_id = s.id);

-- Processos da API que já estejam no ar reconferem as conexões SSE abertas.
SELECT pg_notify('fila_revogacao', '');

-- +goose Down
DROP TABLE owner_session_challenge;
DROP TABLE owner_session_slot;
UPDATE account_session SET revoke_reason = 'replaced' WHERE revoke_reason IN ('owner_session_replaced', 'owner_session_migration');
ALTER TABLE account_session DROP CONSTRAINT account_session_revoke_reason_check;
ALTER TABLE account_session ADD CONSTRAINT account_session_revoke_reason_check CHECK (revoke_reason IN
    ('logout', 'password_changed', 'password_reset', 'replaced', 'account_deletion', 'membership_ended', 'restore'));
