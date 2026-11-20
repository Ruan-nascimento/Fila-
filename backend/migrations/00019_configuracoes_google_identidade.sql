-- Configurações pessoais, fotos, login com Google (só cliente) e identidade da loja (ADR-0028).
-- Tudo aditivo: colunas novas são opcionais ou têm padrão compatível; nenhuma sessão é encerrada
-- e nenhum dado existente é reescrito.
--
-- Donos: módulo identidade (account, account_session, account_token, external_identity,
-- oidc_transaction), módulo estabelecimento (establishment, professional) e módulo midia
-- (media_asset).

-- +goose Up

-- ---------------------------------------------------------------- arquivos de imagem (midia)
-- Metadados e referências; os bytes ficam no armazenamento de objetos (nunca no banco, em SSE
-- ou em JSON de perfil). A finalidade decide a visibilidade: avatar é sempre privado; foto
-- profissional e logo são públicas. Uma imagem privada nunca vira pública trocando o id: a
-- publicação cria outro registro (cópia controlada).
CREATE TABLE media_asset (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_account_id uuid NOT NULL REFERENCES account (id),
    establishment_id uuid REFERENCES establishment (id),
    purpose          text NOT NULL CHECK (purpose IN ('avatar', 'professional_photo', 'logo')),
    visibility       text NOT NULL CHECK (visibility IN ('private', 'public')),
    -- processing → ready | failed; ready → attached; attached → released; * → purged (arquivos apagados).
    state            text NOT NULL CHECK (state IN ('processing', 'ready', 'failed', 'attached', 'released', 'purged')),
    source_bytes     integer NOT NULL CHECK (source_bytes BETWEEN 1 AND 5242880),
    -- Chave aleatória do original em staging privado (apagado depois do processamento).
    staging_key      text CHECK (staging_key ~ '^staging/[0-9a-f-]{36}$'),
    -- Recorte normalizado (0..1) sobre a imagem já orientada; nulo = enquadramento padrão.
    crop_x           double precision CHECK (crop_x >= 0 AND crop_x < 1),
    crop_y           double precision CHECK (crop_y >= 0 AND crop_y < 1),
    crop_size        double precision CHECK (crop_size > 0 AND crop_size <= 1),
    -- [{"size":128,"width":128,"height":128,"bytes":1234,"key":"v/…/128.jpg","mime":"image/jpeg"}]
    variants         jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(variants) = 'array'),
    failure_reason   text CHECK (failure_reason IN ('invalid_image', 'unsupported_format', 'dimensions', 'processing_error')),
    attempts         integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 10),
    lease_until      timestamptz,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    ready_at         timestamptz,
    attached_at      timestamptz,
    released_at      timestamptz,
    purged_at        timestamptz,
    CHECK ((purpose = 'avatar') = (visibility = 'private')),
    CHECK ((purpose = 'avatar') = (establishment_id IS NULL)),
    CHECK ((state = 'failed') = (failure_reason IS NOT NULL)),
    CHECK (num_nulls(crop_x, crop_y, crop_size) IN (0, 3)),
    CHECK (state NOT IN ('ready', 'attached', 'released') OR jsonb_array_length(variants) > 0)
);
-- Fila de processamento (worker) e limpeza.
CREATE INDEX media_asset_processing_idx ON media_asset (created_at) WHERE state = 'processing';
CREATE INDEX media_asset_cleanup_idx ON media_asset (updated_at) WHERE state IN ('ready', 'failed', 'released');
CREATE INDEX media_asset_owner_idx ON media_asset (owner_account_id) WHERE state <> 'purged';

-- ---------------------------------------------------------------- conta (identidade)
-- Conta só com Google não tem senha: nada de hash aleatório fingindo uma senha.
ALTER TABLE account ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE account
    ADD COLUMN appearance      text NOT NULL DEFAULT 'system' CHECK (appearance IN ('system', 'light', 'dark')),
    ADD COLUMN avatar_asset_id uuid REFERENCES media_asset (id),
    -- Versão do perfil (nome, aparência, foto): impede sobrescrita silenciosa entre abas.
    ADD COLUMN profile_version bigint NOT NULL DEFAULT 1 CHECK (profile_version >= 1);

-- Como a sessão foi aberta. Sessão de equipe só por senha: o Google é exclusivo da jornada de
-- cliente e nunca desbloqueia rotas administrativas (garantido também pelo banco).
ALTER TABLE account_session
    ADD COLUMN auth_method text NOT NULL DEFAULT 'password' CHECK (auth_method IN ('password', 'google'));
ALTER TABLE account_session ADD CONSTRAINT account_session_staff_password_ck
    CHECK (kind <> 'staff' OR auth_method = 'password');

-- Código de reautenticação por e-mail (conta sem senha): 10 min, preso à conta e à sessão.
ALTER TABLE account_token DROP CONSTRAINT account_token_purpose_check;
ALTER TABLE account_token ADD CONSTRAINT account_token_purpose_check
    CHECK (purpose IN ('email_verification', 'password_reset', 'reauth_code'));
ALTER TABLE account_token ADD COLUMN session_id uuid REFERENCES account_session (id);
ALTER TABLE account_token ADD CONSTRAINT account_token_reauth_session_ck
    CHECK ((purpose = 'reauth_code') = (session_id IS NOT NULL));

-- Identidades externas. A chave é provedor + emissor + sub (o e-mail muda; o sub não).
-- E-mail guardado só para mostrar "conectado como …" ao titular; apagado na exclusão.
CREATE TABLE external_identity (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id    uuid NOT NULL REFERENCES account (id),
    provider      text NOT NULL CHECK (provider IN ('google')),
    issuer        text NOT NULL CHECK (char_length(issuer) BETWEEN 1 AND 200),
    subject       text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 255),
    email         text CHECK (char_length(email) BETWEEN 3 AND 254),
    created_at    timestamptz NOT NULL,
    last_login_at timestamptz
);
CREATE UNIQUE INDEX external_identity_subject_uq ON external_identity (provider, issuer, subject);
CREATE UNIQUE INDEX external_identity_account_uq ON external_identity (account_id, provider);

-- Transações OpenID Connect (10 min, uso único). Só hashes do state, do nonce e do cookie de
-- correlação; o verificador PKCE fica cifrado (AES-GCM com subchave própria).
CREATE TABLE oidc_transaction (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    state_hash       bytea NOT NULL UNIQUE CHECK (octet_length(state_hash) = 32),
    browser_hash     bytea NOT NULL CHECK (octet_length(browser_hash) = 32),
    nonce_hash       bytea NOT NULL CHECK (octet_length(nonce_hash) = 32),
    verifier_sealed  bytea NOT NULL,
    intent           text NOT NULL CHECK (intent IN ('login', 'link')),
    return_to        text CHECK (char_length(return_to) BETWEEN 1 AND 300),
    establishment_id uuid REFERENCES establishment (id),
    account_id       uuid REFERENCES account (id),
    session_id       uuid REFERENCES account_session (id),
    created_at       timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL CHECK (expires_at > created_at),
    consumed_at      timestamptz,
    CHECK ((intent = 'link') = (account_id IS NOT NULL AND session_id IS NOT NULL))
);
CREATE INDEX oidc_transaction_browser_idx ON oidc_transaction (browser_hash) WHERE consumed_at IS NULL;
CREATE INDEX oidc_transaction_expires_idx ON oidc_transaction (expires_at);

-- ---------------------------------------------------------------- loja (estabelecimento)
-- Paleta por identificador de catálogo (nunca CSS livre nem hexadecimal arbitrário).
ALTER TABLE establishment
    ADD COLUMN palette            text NOT NULL DEFAULT 'purple' CHECK (palette IN ('purple', 'violet', 'indigo', 'blue', 'sky',
                                      'cyan', 'teal', 'emerald', 'green', 'lime', 'amber', 'orange', 'red', 'rose', 'pink', 'fuchsia')),
    ADD COLUMN public_description text CHECK (char_length(public_description) BETWEEN 1 AND 280),
    ADD COLUMN public_address     text CHECK (char_length(public_address) BETWEEN 1 AND 200),
    -- E.164 (+5511999998888): publicado na página da loja; nunca copiado do contato do dono.
    ADD COLUMN public_phone       text CHECK (public_phone ~ '^\+[1-9][0-9]{7,14}$'),
    ADD COLUMN logo_asset_id      uuid REFERENCES media_asset (id);

-- Foto pública do profissional: publicação explícita, independente do avatar pessoal.
ALTER TABLE professional ADD COLUMN photo_asset_id uuid REFERENCES media_asset (id);

-- +goose Down
ALTER TABLE professional DROP COLUMN photo_asset_id;
ALTER TABLE establishment DROP COLUMN logo_asset_id, DROP COLUMN public_phone, DROP COLUMN public_address,
    DROP COLUMN public_description, DROP COLUMN palette;
DROP TABLE oidc_transaction;
DROP TABLE external_identity;
ALTER TABLE account_token DROP CONSTRAINT account_token_reauth_session_ck;
DELETE FROM account_token WHERE purpose = 'reauth_code';
ALTER TABLE account_token DROP COLUMN session_id;
ALTER TABLE account_token DROP CONSTRAINT account_token_purpose_check;
ALTER TABLE account_token ADD CONSTRAINT account_token_purpose_check CHECK (purpose IN ('email_verification', 'password_reset'));
ALTER TABLE account_session DROP CONSTRAINT account_session_staff_password_ck;
ALTER TABLE account_session DROP COLUMN auth_method;
ALTER TABLE account DROP COLUMN profile_version, DROP COLUMN avatar_asset_id, DROP COLUMN appearance;
-- Contas só com Google não têm como voltar ao esquema antigo (senha obrigatória): ficam bloqueadas.
UPDATE account SET password_hash = '$argon2id$sem-senha' WHERE password_hash IS NULL;
ALTER TABLE account ALTER COLUMN password_hash SET NOT NULL;
DROP TABLE media_asset;
