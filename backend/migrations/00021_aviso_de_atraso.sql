-- Aviso de atraso: o cliente avisa pelo app que vai se atrasar (T-P-12). A equipe vê o aviso na
-- fila e recebe uma notificação; a pessoa continua no mesmo lugar até a equipe decidir (por
-- exemplo, descer uma posição com moveParticipation). Uma nova chamada limpa o aviso.
--
-- Dono: módulo fila. Aditivo: coluna opcional, nenhuma linha reescrita.

-- +goose Up
ALTER TABLE participation ADD COLUMN late_notice_at timestamptz;

-- +goose Down
ALTER TABLE participation DROP COLUMN late_notice_at;
