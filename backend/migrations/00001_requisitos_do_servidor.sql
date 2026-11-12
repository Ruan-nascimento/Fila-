-- Migração inicial da etapa 01.
-- Não cria tabelas de negócio (elas chegam a partir da etapa 02). Garante que o servidor
-- atende aos requisitos da fundação, para falhar cedo e com mensagem clara:
--   * PostgreSQL 18 ou superior (usamos uuidv7() nativo para identificadores — ADR-0014);
--   * banco criado com codificação UTF8 (nicks e nomes com acentos).

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF current_setting('server_version_num')::int < 180000 THEN
        RAISE EXCEPTION 'PostgreSQL 18 ou superior é obrigatório (encontrado: %)', current_setting('server_version');
    END IF;
    IF pg_encoding_to_char((SELECT encoding FROM pg_database WHERE datname = current_database())) <> 'UTF8' THEN
        RAISE EXCEPTION 'O banco precisa usar codificação UTF8';
    END IF;
END
$$;
-- +goose StatementEnd

-- uuidv7() precisa existir; esta chamada falha se não existir.
SELECT uuidv7();

-- +goose Down
-- Nada a desfazer: a migração só verifica requisitos.
SELECT 1;
