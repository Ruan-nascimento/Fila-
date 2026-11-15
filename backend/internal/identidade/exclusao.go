package identidade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	"fila/internal/platform/mail"
	"fila/internal/platform/validate"
)

// Estados da conta ligados à exclusão (AccountStatus).
const (
	StatusActive             = "active"
	StatusDeletionProcessing = "deletion_processing"
	StatusDeleted            = "deleted"
	// DeletedName substitui o nome da conta anonimizada (não é dado de ninguém).
	DeletedName = "Conta excluída"
	// VisitorNickRemoved substitui o nick do visitante depois da necessidade operacional.
	VisitorNickRemoved = "Visitante"
)

// ErrAccountNotFound indica que a conta não existe.
var ErrAccountNotFound = errors.New("conta não encontrada")

// BeginDeletion (T-AC-03, parte da identidade): trava a conta e, se ainda ativa, passa para
// deletion_processing, revoga TODAS as sessões (inclusive SSE abertos, pelo aviso de revogação)
// e os tokens ainda não usados. Devolve o estado ANTERIOR: se não era "active", nada muda
// (repetição ou outra solicitação em andamento).
func (s *Service) BeginDeletion(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, now time.Time) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM account WHERE id = $1 FOR UPDATE`, accountID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAccountNotFound
	}
	if err != nil || status != StatusActive {
		return status, err
	}
	if _, err := tx.Exec(ctx, `UPDATE account SET status = 'deletion_processing', updated_at = $2 WHERE id = $1`, accountID, now); err != nil {
		return status, err
	}
	if _, err := revokeSessions(ctx, tx, accountID, nil, "account_deletion", now); err != nil {
		return status, err
	}
	if _, err := tx.Exec(ctx, `UPDATE account_token SET revoked_at = $2
		WHERE account_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, accountID, now); err != nil {
		return status, err
	}
	return status, nil
}

// MailDeletionProtocol envia ao titular o protocolo da solicitação (a resposta HTTP pode se
// perder; o e-mail é a segunda via). O e-mail fica cifrado na caixa de saída até a entrega.
func (s *Service) MailDeletionProtocol(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, protocol string, now time.Time) error {
	var to string
	if err := tx.QueryRow(ctx, `SELECT email FROM account WHERE id = $1`, accountID).Scan(&to); err != nil {
		return err
	}
	body := fmt.Sprintf(`Olá.

Recebemos o pedido de exclusão da sua conta na Fila. Suas sessões foram encerradas.

Protocolo: %s

Guarde o protocolo para consultar a situação em %s
Se houver atendimento em andamento ou pagamento pendente, a equipe da loja resolve antes;
a exclusão só aparece como concluída depois disso. A meta é concluir em até 30 dias
(meta da operação, não prazo legal).`, protocol, s.cfg.PublicBaseURL+"/exclusao")
	return s.outbox.Enqueue(ctx, tx, "exclusao_conta", mail.Message{To: to, Subject: "Pedido de exclusão de conta", Body: body}, now)
}

// AnonymizeAccount (T-AC-04, parte da identidade): sobrescreve nome, e-mail e senha; a conta
// continua como pseudônimo (status deleted) para os registros que apontam para ela. Convites
// aceitos por ela ou ainda pendentes para o e-mail dela perdem o e-mail. Idempotente: numa conta
// já excluída não muda nada. Devolve contagens por categoria (evidência sem dados pessoais).
func (s *Service) AnonymizeAccount(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, now time.Time) (map[string]int64, error) {
	var status, emailNorm string
	err := tx.QueryRow(ctx, `SELECT status, email_normalized FROM account WHERE id = $1 FOR UPDATE`, accountID).Scan(&status, &emailNorm)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	out := map[string]int64{"perfil": 0, "convites": 0, "tokens": 0}
	if status == StatusDeleted {
		return out, nil
	}
	if status != StatusDeletionProcessing {
		return nil, fmt.Errorf("conta em estado %q não pode ser anonimizada", status)
	}
	// O endereço substituto deriva do pseudônimo (não identifica ninguém) e usa o domínio
	// reservado .invalid (nunca entrega).
	placeholder := "excluida-" + accountID.String() + "@conta.invalid"
	if _, err := tx.Exec(ctx, `
		UPDATE account SET name = $2, email = $3, email_normalized = $3, password_hash = '$argon2id$conta-excluida',
		       status = 'deleted', updated_at = $4
		WHERE id = $1`, accountID, DeletedName, placeholder, now); err != nil {
		return nil, err
	}
	out["perfil"] = 1
	tag, err := tx.Exec(ctx, `
		UPDATE invite SET email = 'convite@conta.invalid',
		       status = CASE WHEN status = 'pending' THEN 'revoked' ELSE status END,
		       revoked_at = CASE WHEN status = 'pending' THEN $3 ELSE revoked_at END
		WHERE (accepted_account_id = $1 OR lower(email) = $2) AND email <> 'convite@conta.invalid'`,
		accountID, emailNorm, now)
	if err != nil {
		return nil, err
	}
	out["convites"] = tag.RowsAffected()
	// Tokens guardam só hash e datas; ficam revogados (sem dado pessoal).
	tag, err = tx.Exec(ctx, `UPDATE account_token SET revoked_at = $2
		WHERE account_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, accountID, now)
	if err != nil {
		return nil, err
	}
	out["tokens"] = tag.RowsAffected()
	return out, nil
}

// ScrubVisitorNicks substitui o nick de visitantes cujo acesso já expirou há mais de `after`
// (necessidade operacional encerrada; matriz de retenção). Repetível.
func ScrubVisitorNicks(ctx context.Context, q database.Querier, expiredBefore time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE visitor SET nick = $2 WHERE absolute_expires_at < $1 AND nick <> $2`,
		expiredBefore, VisitorNickRemoved)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// AccountStatus devolve o estado da conta (usado para não reabrir efeitos de exclusão).
func AccountStatus(ctx context.Context, q database.Querier, accountID uuid.UUID) (string, error) {
	var status string
	err := q.QueryRow(ctx, `SELECT status FROM account WHERE id = $1`, accountID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAccountNotFound
	}
	return status, err
}

// OperatorInviteOwner (T-OP-02, operador): convida um novo dono para uma loja que já existe —
// o caminho para transferir a administração antes de o dono atual excluir a conta. Identidade do
// pedido confirmada fora do sistema; auditado com ator operator e motivo.
func (s *Service) OperatorInviteOwner(ctx context.Context, estID uuid.UUID, estName, email, reason string) (time.Time, error) {
	addr, _, fe := validate.Email("/email", email)
	if fe != nil {
		return time.Time{}, fmt.Errorf("e-mail inválido")
	}
	why, fe := validate.Text("/motivo", reason, 3, 500)
	if fe != nil {
		return time.Time{}, fmt.Errorf("informe o motivo (3 a 500 caracteres)")
	}
	now := s.clock.Now()
	var expires time.Time
	err := database.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		inv, token, err := insertInvite(ctx, tx, estID, estabelecimento.RoleOwner, nil, addr, nil, now)
		if err != nil {
			return err
		}
		if err := s.mailInvite(ctx, tx, addr, estName, estabelecimento.RoleOwner, token, now); err != nil {
			return err
		}
		expires = inv.ExpiresAt
		return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOperator,
			Action: "T-OP-02", TargetType: "invite", TargetID: inv.ID, ToState: "pending", Reason: why})
	})
	return expires, err
}
