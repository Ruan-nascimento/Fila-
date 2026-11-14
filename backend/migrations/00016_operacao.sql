-- Etapa 12 — operação e recuperação (módulo operacao, ADR-0026).
--   * operation_state: linha única com a trava do worker (manutenção ou banco restaurado), o
--     banco ao qual o estado está vinculado (detecta restauração lógica: um banco restaurado
--     tem outro oid) e o último sinal de vida do worker (≈ instante do backup, depois de restaurar).
--   * Pendências financeiras achadas na conciliação pós-restauração: cobrança paga no provedor
--     cuja comanda não existe no banco restaurado (sem comanda) e estorno feito no provedor que o
--     banco restaurado não conhece.
--   * Cobrança Pix recriada a partir do provedor depois de restaurar: created_by = 'restore'.
--   * Sessões encerradas pela restauração: revoke_reason = 'restore'.

-- +goose Up

SET CONSTRAINTS ALL IMMEDIATE;

CREATE TABLE operation_state (
    singleton          boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    -- oid do banco em que o estado foi gravado; NULL até o primeiro worker vincular.
    bound_database_oid oid,
    hold_reason        text CHECK (hold_reason IN ('maintenance', 'restore', 'restore_detected')),
    hold_note          text CHECK (char_length(hold_note) BETWEEN 3 AND 300),
    hold_since         timestamptz,
    reconciled_at      timestamptz,
    worker_seen_at     timestamptz,
    updated_at         timestamptz NOT NULL,
    CHECK ((hold_reason IS NULL) = (hold_since IS NULL))
);
INSERT INTO operation_state (singleton, updated_at) VALUES (true, now());
CREATE TRIGGER operation_state_no_delete BEFORE DELETE ON operation_state FOR EACH ROW EXECUTE FUNCTION forbid_delete();

ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_kind_check;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_kind_check CHECK (kind IN ('duplicate_receipt', 'late_receipt',
    'amount_mismatch', 'charge_outcome_unresolved', 'refund_under_review', 'active_pix_on_method_switch', 'adjustment_excess',
    'restored_orphan_charge', 'restored_refund_unrecorded'));
-- Só a cobrança órfã (comanda inexistente no banco restaurado) fica sem comanda.
ALTER TABLE financial_issue ALTER COLUMN order_id DROP NOT NULL;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_order_required
    CHECK (order_id IS NOT NULL OR kind = 'restored_orphan_charge');
-- Sem comanda, a FK composta não confere a loja: FK própria.
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_establishment_fk
    FOREIGN KEY (establishment_id) REFERENCES establishment (id);
-- Identificador da cobrança no provedor (para o dono localizar no painel do provedor). Não é dado pessoal.
ALTER TABLE financial_issue ADD COLUMN provider_reference text CHECK (char_length(provider_reference) BETWEEN 1 AND 100);

ALTER TABLE pix_charge DROP CONSTRAINT pix_charge_created_by_check;
ALTER TABLE pix_charge ADD CONSTRAINT pix_charge_created_by_check
    CHECK (created_by IN ('visitor', 'customer', 'professional', 'owner', 'restore'));

-- Depois de restaurar, todas as sessões são encerradas: uma sessão encerrada DEPOIS do backup
-- (saída, troca de senha) voltaria a valer no banco restaurado.
ALTER TABLE account_session DROP CONSTRAINT account_session_revoke_reason_check;
ALTER TABLE account_session ADD CONSTRAINT account_session_revoke_reason_check CHECK (revoke_reason IN
    ('logout', 'password_changed', 'password_reset', 'replaced', 'account_deletion', 'membership_ended', 'restore'));

-- +goose Down
SET CONSTRAINTS ALL IMMEDIATE;
-- Reversão só é possível se nenhuma linha usa o que esta versão criou (a reversão falha em vez de apagar).
ALTER TABLE account_session DROP CONSTRAINT account_session_revoke_reason_check;
ALTER TABLE account_session ADD CONSTRAINT account_session_revoke_reason_check CHECK (revoke_reason IN
    ('logout', 'password_changed', 'password_reset', 'replaced', 'account_deletion', 'membership_ended'));
ALTER TABLE pix_charge DROP CONSTRAINT pix_charge_created_by_check;
ALTER TABLE pix_charge ADD CONSTRAINT pix_charge_created_by_check
    CHECK (created_by IN ('visitor', 'customer', 'professional', 'owner'));
ALTER TABLE financial_issue DROP COLUMN provider_reference;
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_establishment_fk;
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_order_required;
ALTER TABLE financial_issue ALTER COLUMN order_id SET NOT NULL;
ALTER TABLE financial_issue DROP CONSTRAINT financial_issue_kind_check;
ALTER TABLE financial_issue ADD CONSTRAINT financial_issue_kind_check CHECK (kind IN ('duplicate_receipt', 'late_receipt',
    'amount_mismatch', 'charge_outcome_unresolved', 'refund_under_review', 'active_pix_on_method_switch', 'adjustment_excess'));
DROP TRIGGER operation_state_no_delete ON operation_state;
DROP TABLE operation_state;
