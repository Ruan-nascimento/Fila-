-- Melhorias 1.0.0 (ADR-0035) — entrada na fila separada da operação, capacidade opcional e
-- tolerância configurável gravada em cada chamada.
--
--   * queue.status continua sendo a OPERAÇÃO (aberta, pausada, encerrada);
--   * queue.admission é a política de NOVAS entradas (aberta | fechada manualmente);
--   * a lotação é calculada (aguardando + chamado ≥ capacidade efetiva), nunca gravada;
--   * capacidade efetiva = queue.capacity, ou o padrão da loja; nula = sem limite (como antes).
--
-- Chamadas existentes ganham tolerance_minutes = 10 (a regra fixa de antes): ninguém tem o prazo
-- mudado por esta migração.

-- +goose Up

ALTER TABLE queue
    ADD COLUMN admission            text NOT NULL DEFAULT 'open' CHECK (admission IN ('open', 'closed')),
    ADD COLUMN admission_changed_at timestamptz,
    ADD COLUMN capacity             integer CHECK (capacity BETWEEN 1 AND 200);

ALTER TABLE participation ADD COLUMN tolerance_minutes integer CHECK (tolerance_minutes BETWEEN 1 AND 60);
UPDATE participation SET tolerance_minutes = 10 WHERE called_at IS NOT NULL;
ALTER TABLE participation DROP CONSTRAINT participation_check2;
-- tolerance_minutes nulo numa chamada = os 10 minutos fixos de antes. Isso mantém o código da
-- versão anterior funcionando com este banco (volta da API/worker sem voltar o esquema).
ALTER TABLE participation
    ADD CONSTRAINT participation_tolerance_check
        CHECK (tolerance_ends_at IS NULL OR tolerance_ends_at = called_at + make_interval(mins => coalesce(tolerance_minutes, 10)));

-- Contagem de vagas ocupadas (aguardando + chamado) por fila, feita com a fila travada.
CREATE INDEX participation_pending_idx ON participation (queue_id) WHERE status IN ('waiting', 'called');

-- +goose Down
DROP INDEX participation_pending_idx;
ALTER TABLE participation DROP CONSTRAINT participation_tolerance_check;
-- Só volta se todas as chamadas usaram 10 minutos; senão falha (não se reescreve prazo de ninguém).
ALTER TABLE participation ADD CONSTRAINT participation_check2
    CHECK (tolerance_ends_at IS NULL OR tolerance_ends_at = called_at + interval '10 minutes');
ALTER TABLE participation DROP COLUMN tolerance_minutes;
ALTER TABLE queue DROP COLUMN capacity, DROP COLUMN admission_changed_at, DROP COLUMN admission;
