-- Fila 2.0, parte 3 — índice parcial para a tarefa "a loja não chamou" (worker da agenda).
--
-- Medido no ensaio de carga (docs/operacao/agenda-2.0.md): com ~40 mil reservas, a rodada lia todas
-- as confirmadas (11,6 mil blocos, 15 ms a cada 30 s) porque o prazo é uma expressão. O índice só
-- guarda as confirmadas ainda não sinalizadas, e a consulta limita por starts_at <= agora.
-- Só expansão (CREATE INDEX): compatível com a API e o worker da versão anterior durante o deploy.

-- +goose Up
CREATE INDEX appointment_store_late_due_idx ON appointment (starts_at)
    WHERE status = 'confirmed' AND store_late_at IS NULL;

-- +goose Down
DROP INDEX appointment_store_late_due_idx;
