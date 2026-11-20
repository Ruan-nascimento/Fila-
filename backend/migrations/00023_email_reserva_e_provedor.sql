-- Entrega de e-mail por provedor externo (Resend). O envio deixa de acontecer dentro de uma
-- transação longa: o worker RESERVA a mensagem por pouco tempo (lease_until), envia fora da
-- transação e confirma individualmente. Se o worker cair depois do envio aceito e antes da
-- confirmação, a reserva vence e outra tentativa reenvia com a MESMA chave de idempotência
-- (o id da mensagem), que o provedor deduplica dentro da janela dele.
--
-- provider_message_id: identificador devolvido pelo provedor (ajuda a localizar a mensagem no
-- painel do provedor). Não é dado pessoal nem permite ler o conteúdo.
--
-- Dono: internal/platform/mail. Aditivo: colunas opcionais, nenhuma linha reescrita.

-- +goose Up
ALTER TABLE mail_outbox
    ADD COLUMN lease_until         timestamptz,
    ADD COLUMN provider_message_id text CHECK (char_length(provider_message_id) <= 100);

-- +goose Down
ALTER TABLE mail_outbox DROP COLUMN provider_message_id, DROP COLUMN lease_until;
