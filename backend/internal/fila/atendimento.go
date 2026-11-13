package fila

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/estabelecimento"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Estados finais do atendimento.
const (
	Completed   = "completed"
	Interrupted = "interrupted"
)

type completeRequest struct {
	ExpectedVersion      *int64                     `json:"expected_version"`
	OrderExpectedVersion *int64                     `json:"order_expected_version"`
	Items                *[]atendimento.ItemOutcome `json:"items"`
}

type interruptRequest struct {
	ExpectedVersion      *int64                     `json:"expected_version"`
	OrderExpectedVersion *int64                     `json:"order_expected_version"`
	Reason               *string                    `json:"reason"`
	Items                *[]atendimento.ItemOutcome `json:"items"`
}

func validateClosing(v *validate.Collector, b any) {
	switch b := b.(type) {
	case *completeRequest:
		v.Version("/expected_version", b.ExpectedVersion)
		v.Version("/order_expected_version", b.OrderExpectedVersion)
		atendimento.ValidateOutcomes(v, b.Items, []string{atendimento.OutcomePerformed, atendimento.OutcomeNotPerformed}, true)
	case *interruptRequest:
		v.Version("/expected_version", b.ExpectedVersion)
		v.Version("/order_expected_version", b.OrderExpectedVersion)
		v.Required("/reason", b.Reason == nil)
		if b.Reason != nil {
			_, fe := validate.Text("/reason", *b.Reason, 3, 500)
			v.Add(fe)
		}
		atendimento.ValidateOutcomes(v, b.Items, []string{atendimento.OutcomeFully, atendimento.OutcomePartially, atendimento.OutcomeNotStarted}, false)
	}
}

// withVisit troca a comanda em "current" de um 409 pela visita inteira (participação + comanda),
// que é o recurso destes comandos.
func (s *Service) withVisit(ctx context.Context, tx pgx.Tx, p row, who role, err error) (idempotency.Result, error) {
	var he *hs.Error
	if errors.As(err, &he) && he.Status == http.StatusConflict {
		view, verr := s.visit(ctx, tx, p, who.isStaff)
		if verr != nil {
			return idempotency.Result{}, verr
		}
		he.Current = view
	}
	return idempotency.Result{}, err
}

// completeService (T-P-08 + T-O-04): em atendimento → concluído. Exige solicitações resolvidas e
// o resultado de cada item; fecha o valor e libera a cadeira na mesma transação. O pagamento
// segue separado: o profissional já pode chamar o próximo (R-CMD-11, R-ARQ-07).
func (s *Service) completeService(w http.ResponseWriter, r *http.Request) error {
	var body completeRequest
	return s.participationCommand(w, r, "completeService", allow{professional: true, owner: true}, &body,
		func(tx pgx.Tx, p row, who role, _ map[uuid.UUID]estabelecimento.QueueInfo) (idempotency.Result, error) {
			ctx := r.Context()
			if p.Status != InService {
				return s.invalid(ctx, tx, p, who, "Só é possível concluir um atendimento em andamento.")
			}
			now := s.Clock.Now()
			if err := atendimento.Complete(ctx, tx, atendimento.Closing{EstablishmentID: p.EstablishmentID, OrderID: p.OrderID,
				ExpectedVersion: *body.OrderExpectedVersion, Items: *body.Items, Now: now}, s.auditBase(ctx, p, who)); err != nil {
				return s.withVisit(ctx, tx, p, who, err)
			}
			if _, err := tx.Exec(ctx, `UPDATE participation SET status = 'completed', finished_at = $2, version = version + 1, updated_at = $2 WHERE id = $1`,
				p.ID, now); err != nil {
				return idempotency.Result{}, err
			}
			return s.finish(ctx, tx, p.EstablishmentID, p.ID, who, "T-P-08", InService, Completed, "", http.StatusOK, p.QueueID)
		}, func() int64 { return deref(body.ExpectedVersion) }, nil)
}

// interruptService (T-P-09 + T-O-06 + T-CR-04): em atendimento → interrompido, com motivo e o
// resultado de cada item. Libera a cadeira; a comanda aguarda a revisão do dono; sem pontos.
func (s *Service) interruptService(w http.ResponseWriter, r *http.Request) error {
	var body interruptRequest
	return s.participationCommand(w, r, "interruptService", allow{professional: true, owner: true}, &body,
		func(tx pgx.Tx, p row, who role, _ map[uuid.UUID]estabelecimento.QueueInfo) (idempotency.Result, error) {
			ctx := r.Context()
			if p.Status != InService {
				return s.invalid(ctx, tx, p, who, "Só é possível interromper um atendimento em andamento.")
			}
			reason, _ := validate.Text("/reason", *body.Reason, 3, 500)
			now := s.Clock.Now()
			if err := atendimento.Interrupt(ctx, tx, atendimento.Closing{EstablishmentID: p.EstablishmentID, OrderID: p.OrderID,
				ExpectedVersion: *body.OrderExpectedVersion, Items: *body.Items, Reason: reason, Now: now}, s.auditBase(ctx, p, who)); err != nil {
				return s.withVisit(ctx, tx, p, who, err)
			}
			if _, err := tx.Exec(ctx, `UPDATE participation SET status = 'interrupted', finished_at = $2, end_reason = $3, version = version + 1, updated_at = $2 WHERE id = $1`,
				p.ID, now, reason); err != nil {
				return idempotency.Result{}, err
			}
			return s.finish(ctx, tx, p.EstablishmentID, p.ID, who, "T-P-09", InService, Interrupted, reason, http.StatusOK, p.QueueID)
		}, func() int64 { return deref(body.ExpectedVersion) }, nil)
}
