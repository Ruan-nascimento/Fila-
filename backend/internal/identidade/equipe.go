package identidade

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
)

// PendingInvites devolve o convite pendente (não vencido) de cada profissional da loja.
// Usado pela lista de profissionais do módulo estabelecimento.
func (s *Service) PendingInvites(ctx context.Context, q database.Querier, estID uuid.UUID) (map[uuid.UUID]estabelecimento.InviteInfo, error) {
	rows, err := q.Query(ctx, `
		SELECT id, professional_id, expires_at FROM invite
		WHERE establishment_id = $1 AND role = 'professional' AND status = 'pending' AND expires_at > $2`,
		estID, s.clock.Now())
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (estabelecimento.InviteInfo, error) {
		v := estabelecimento.InviteInfo{Status: "pending"}
		err := r.Scan(&v.ID, &v.ProfessionalID, &v.ExpiresAt)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]estabelecimento.InviteInfo, len(list))
	for _, v := range list {
		out[v.ProfessionalID] = v
	}
	return out, nil
}

// AccessRevoked é chamado (na mesma transação) quando um profissional é desativado (T-PR-02):
//   - convites pendentes dele deixam de valer;
//   - contas que ficaram sem nenhum vínculo de equipe têm as sessões de equipe encerradas.
//
// Contas que ainda têm outro vínculo mantêm a sessão; a autorização confere os vínculos a cada
// pedido, então o acesso à fila desativada já termina imediatamente (R-PERM-03).
func (s *Service) AccessRevoked(ctx context.Context, q database.Querier, accountIDs []uuid.UUID, professionalID uuid.UUID, now time.Time) error {
	rows, err := q.Query(ctx, `UPDATE invite SET status = 'revoked', revoked_at = $2
		WHERE professional_id = $1 AND status = 'pending' RETURNING id, establishment_id`, professionalID, now)
	if err != nil {
		return err
	}
	type rev struct{ id, est uuid.UUID }
	revoked, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rev, error) {
		var x rev
		err := r.Scan(&x.id, &x.est)
		return x, err
	})
	if err != nil {
		return err
	}
	for _, x := range revoked {
		if err := auditoria.Record(ctx, q, auditoria.Event{EstablishmentID: auditoria.Ptr(x.est), OccurredAt: now,
			ActorKind: auditoria.ActorSystem, Action: "T-IN-03", TargetType: "invite", TargetID: x.id,
			FromState: "pending", ToState: "revoked", Reason: "profissional desativado"}); err != nil {
			return err
		}
	}
	for _, acc := range accountIDs {
		ms, err := estabelecimento.ActiveMemberships(ctx, q, acc)
		if err != nil {
			return err
		}
		if len(ms) > 0 {
			continue
		}
		if _, err := q.Exec(ctx, `UPDATE account_session SET revoked_at = $2, revoke_reason = 'membership_ended'
			WHERE account_id = $1 AND kind = 'staff' AND revoked_at IS NULL`, acc, now); err != nil {
			return err
		}
	}
	return nil
}
