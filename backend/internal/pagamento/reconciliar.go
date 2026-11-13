package pagamento

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/platform/database"
)

// Reconciliação (T-PX-04, R-PAG-09/16): o worker consulta o provedor — com a chave do backend,
// consulta autenticada — para cada tentativa ativa cuja hora de verificar chegou. Não depende da
// aba do cliente. Nada aqui confia em navegador, imagem ou clique.
//
//   - "creating" parada / "unknown": procura pelo externalId (= id da tentativa). Achou → adota.
//     Não achou → repete a criação com o MESMO externalId (o provedor devolve a cobrança pendente
//     existente em vez de criar outra; é a garantia remota). Nunca cria com outro externalId.
//   - "pending": consulta pelo id do provedor até pagar, expirar, cancelar ou falhar.
//
// Espera entre consultas cresce (backoff) até um teto; resultado desconhecido que persiste é
// registrado no log como erro (a pendência "charge_outcome_unresolved" para a dona é da etapa 09).

const (
	reconcileBatch       = 20
	reconcileLease       = 2 * time.Minute // outra instância não pega a mesma tentativa enquanto isso
	maxBackoff           = 5 * time.Minute
	unresolvedAfterTries = 12
)

// backoff devolve a espera até a próxima consulta: 5 s, 10 s, 20 s… até 5 min.
func backoff(attempts int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}

// ReconcileOnce verifica as tentativas devidas e devolve quantas processou.
func (s *Service) ReconcileOnce(ctx context.Context) (int, error) {
	if s.Provider == nil {
		return 0, nil
	}
	now := s.Clock.Now()
	var ids []uuid.UUID
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE pix_charge SET next_check_at = $2
			WHERE id IN (SELECT id FROM pix_charge
			             WHERE status IN ('creating', 'pending', 'unknown') AND next_check_at <= $1
			             ORDER BY next_check_at LIMIT $3 FOR UPDATE SKIP LOCKED)
			RETURNING id`, now, now.Add(reconcileLease), reconcileBatch)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.reconcile(ctx, id); err != nil {
			s.Log.Error("falha ao reconciliar cobrança Pix", "tentativa", id.String(), "erro", err.Error())
		}
	}
	n, err := s.reconcileRefunds(ctx)
	return len(ids) + n, err
}

// reconcile consulta o provedor (fora de transação) e aplica o resultado.
func (s *Service) reconcile(ctx context.Context, id uuid.UUID) error {
	c, err := scanCharge(s.Pool.QueryRow(ctx, chargeSelect+` WHERE id = $1`, id))
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.Timeout+2*time.Second)
	defer cancel()
	var pc Charge
	var callErr error
	if c.ProviderID != nil {
		pc, callErr = s.Provider.GetPix(callCtx, *c.ProviderID)
	} else {
		var found []Charge
		found, callErr = s.Provider.FindPix(callCtx, c.ID.String())
		switch {
		case callErr != nil:
		case len(found) > 1:
			// Não deveria acontecer (o provedor deduplica pelo externalId). Não escolhe às cegas.
			s.Log.Error("várias cobranças no provedor para a mesma tentativa", "tentativa", c.ID.String(), "quantidade", len(found))
			callErr = ErrIntegrity
		case len(found) == 1:
			pc = found[0]
		case c.Status == ChargeCreating || c.Status == ChargeUnknown:
			// Nenhuma cobrança com este externalId: repete a criação com o MESMO externalId.
			pc, callErr = s.Provider.CreatePix(callCtx, CreatePix{ExternalID: c.ID.String(), AmountCents: c.amount,
				ExpiresIn: s.ExpiresIn, Description: "Atendimento na barbearia",
				Metadata: map[string]string{"charge_id": c.ID.String(), "order_id": c.OrderID.String(), "establishment_id": c.EstablishmentID.String()}})
			if errors.Is(callErr, ErrRejected) {
				// O provedor recusou a criação e nada existe com este externalId: falhou de vez.
				return s.finish(ctx, c, nil, ChargeFailed)
			}
		}
	}
	if callErr != nil {
		return s.retryLater(ctx, c, callErr)
	}
	return s.finish(ctx, c, &pc, "")
}

// finish aplica o estado do provedor (ou marca a falha) e agenda a próxima consulta se continuar ativa.
func (s *Service) finish(ctx context.Context, c chargeRow, pc *Charge, failTo string) error {
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		lp, err := atendimento.LockForPayment(ctx, tx, c.EstablishmentID, c.OrderID)
		if err != nil {
			return err
		}
		cur, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE id = $1 FOR UPDATE`, c.ID))
		if err != nil {
			return err
		}
		now := s.Clock.Now()
		if failTo != "" {
			if cur.Status == ChargeCreating || cur.Status == ChargeUnknown {
				return s.setStatus(ctx, tx, lp, &cur, failTo, "T-PX-08", now)
			}
			return nil
		}
		// Estados finais não voltam atrás (ex.: "expirado" chegando depois de "pago").
		if cur.Status == ChargePaid || cur.Status == ChargeExpired || cur.Status == ChargeCancelled || cur.Status == ChargeFailed {
			if pc.Status == ProviderPaid && cur.Status != ChargePaid {
				// Pago depois de marcado como resolvido: registra (como excesso, se for o caso).
				return s.paid(ctx, tx, lp, &cur, *pc, "provider_query", now)
			}
			return nil
		}
		if err := s.apply(ctx, tx, lp, &cur, *pc, "provider_query", now); err != nil {
			if errors.Is(err, ErrIntegrity) {
				s.Log.Error("resposta do provedor não confere; cobrança segue em verificação", "tentativa", c.ID.String(), "erro", err.Error())
				return s.schedule(ctx, tx, cur, now)
			}
			return err
		}
		return s.schedule(ctx, tx, cur, now)
	})
}

// retryLater conta a tentativa e agenda nova consulta (resultado ainda desconhecido).
func (s *Service) retryLater(ctx context.Context, c chargeRow, callErr error) error {
	s.Log.Warn("consulta ao provedor sem resposta conclusiva; nova tentativa agendada", "tentativa", c.ID.String(), "motivo", callErr.Error())
	return database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		cur, err := scanCharge(tx.QueryRow(ctx, chargeSelect+` WHERE id = $1 FOR UPDATE`, c.ID))
		if err != nil {
			return err
		}
		return s.schedule(ctx, tx, cur, s.Clock.Now())
	})
}

// schedule conta a consulta e marca a próxima (só para tentativas ainda ativas).
func (s *Service) schedule(ctx context.Context, tx pgx.Tx, c chargeRow, now time.Time) error {
	if c.Status != ChargeCreating && c.Status != ChargePending && c.Status != ChargeUnknown {
		return nil
	}
	attempts := c.Attempts + 1
	next := now.Add(backoff(attempts))
	if c.Status == ChargePending && c.ExpiresAt != nil && next.After(c.ExpiresAt.Add(30*time.Second)) && now.Before(*c.ExpiresAt) {
		next = c.ExpiresAt.Add(30 * time.Second) // confere logo depois de expirar
	}
	if c.Status != ChargePending && attempts == unresolvedAfterTries {
		// Alerta: segue consultando (com espera máxima), mas a dona passa a ver a pendência.
		s.Log.Error("cobrança Pix continua com resultado desconhecido (charge_outcome_unresolved)", "tentativa", c.ID.String(),
			"consultas", attempts)
		amt := c.amount
		if _, err := s.openIssue(ctx, tx, c.EstablishmentID, c.OrderID, IssueChargeUnresolved, "pix_charge", c.ID, &amt, now); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE pix_charge SET check_attempts = $2, next_check_at = $3 WHERE id = $1`, c.ID, attempts, next)
	return err
}

// Run reconcilia a cada intervalo até ctx terminar.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := s.ReconcileOnce(ctx); err != nil && ctx.Err() == nil {
			s.Log.Error("reconciliação de pagamentos falhou", "erro", err.Error())
		} else if n >= reconcileBatch {
			continue // há mais na fila: segue sem esperar
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
