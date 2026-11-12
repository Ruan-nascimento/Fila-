-- Etapa 02 — tabelas transversais da plataforma.
-- Donos: internal/platform/ratelimit (rate_limit), internal/platform/idempotency
-- (idempotency_record), internal/platform/mail (mail_outbox) e o módulo auditoria (audit_event).
-- Nenhuma delas guarda e-mail, IP, nick, senha ou token em claro.

-- +goose Up

-- Contadores de tentativas por janela fixa (R-CONTA-11, R-SEG-04).
-- bucket = "<regra>:<HMAC do assunto>"; o assunto (conta, IP, navegador) nunca é gravado.
CREATE TABLE rate_limit (
    bucket       text PRIMARY KEY CHECK (char_length(bucket) <= 200),
    window_start timestamptz NOT NULL,
    hits         integer NOT NULL CHECK (hits > 0),
    expires_at   timestamptz NOT NULL CHECK (expires_at > window_start)
);
CREATE INDEX rate_limit_expires_idx ON rate_limit (expires_at);

-- Respostas de comandos por Idempotency-Key (ADR-0009). Gravada na mesma transação do efeito.
-- Escopo = ator + operação; respostas com segredo nunca são guardadas (rotas fora da regra).
CREATE TABLE idempotency_record (
    actor_key       text NOT NULL CHECK (char_length(actor_key) <= 100),
    operation       text NOT NULL CHECK (char_length(operation) <= 100),
    idem_key        text NOT NULL CHECK (idem_key ~ '^[A-Za-z0-9_-]{16,128}$'),
    fingerprint     bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
    response_status integer NOT NULL CHECK (response_status BETWEEN 200 AND 299),
    response_body   bytea NOT NULL,
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL CHECK (expires_at > created_at),
    PRIMARY KEY (actor_key, operation, idem_key)
);
CREATE INDEX idempotency_record_expires_idx ON idempotency_record (expires_at);

-- Caixa de saída de e-mails (gravada na mesma transação que gera o e-mail).
-- payload = destinatário, assunto e corpo cifrados (AES-256-GCM, chave derivada de
-- FILA_SECRET_KEY). Depois do envio (ou desistência) o payload é apagado.
CREATE TABLE mail_outbox (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    kind            text NOT NULL CHECK (kind ~ '^[a-z_]{1,40}$'),
    payload         bytea,
    created_at      timestamptz NOT NULL,
    attempts        integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL,
    sent_at         timestamptz,
    failed_at       timestamptz,
    last_error      text CHECK (char_length(last_error) <= 200),
    CHECK (sent_at IS NULL OR failed_at IS NULL),
    CHECK ((sent_at IS NULL AND failed_at IS NULL) OR payload IS NULL)
);
CREATE INDEX mail_outbox_pending_idx ON mail_outbox (next_attempt_at)
    WHERE sent_at IS NULL AND failed_at IS NULL;

-- Auditoria mínima das transições (R-SEG-10, ADR-0015): ator por pseudônimo interno (UUID),
-- nunca e-mail, nick, telefone ou segredo. Sem chave estrangeira de propósito: o registro
-- sobrevive à anonimização e não impede apagar o perfil.
CREATE TABLE audit_event (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    establishment_id uuid,
    occurred_at      timestamptz NOT NULL,
    actor_kind       text NOT NULL CHECK (actor_kind IN
                         ('visitor', 'customer', 'professional', 'owner', 'provider', 'system', 'operator')),
    actor_id         uuid,
    action           text NOT NULL CHECK (action ~ '^T-[A-Z]{1,3}-[0-9]{2}$'),
    target_type      text NOT NULL CHECK (target_type ~ '^[a-z_]{1,40}$'),
    target_id        uuid NOT NULL,
    from_state       text CHECK (char_length(from_state) <= 40),
    to_state         text CHECK (char_length(to_state) <= 40),
    reason           text CHECK (char_length(reason) <= 500),
    request_id       text CHECK (char_length(request_id) <= 64)
);
CREATE INDEX audit_event_establishment_idx ON audit_event (establishment_id, occurred_at DESC)
    WHERE establishment_id IS NOT NULL;
CREATE INDEX audit_event_target_idx ON audit_event (target_type, target_id);

-- +goose Down
DROP TABLE audit_event;
DROP TABLE mail_outbox;
DROP TABLE idempotency_record;
DROP TABLE rate_limit;
