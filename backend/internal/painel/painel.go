// Package painel monta os contadores do painel do dono (Pendencies): comandas a receber e a
// conferir, interrupções a revisar, cobranças desconhecidas, pendências financeiras, estornos em
// revisão, exclusões em processamento e sinais de indisponibilidade.
//
// Fronteiras (ADR-0001): não tem tabela. Cada número vem da função exportada pelo módulo dono
// do dado; aqui só se juntam (somente leitura).
package painel

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/estabelecimento"
	"fila/internal/exclusao"
	"fila/internal/pagamento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// Pendencies segue o esquema Pendencies.
type Pendencies struct {
	OrdersAwaitingPayment      int `json:"orders_awaiting_payment"`
	OrdersAwaitingConfirmation int `json:"orders_awaiting_confirmation"`
	InterruptionsToReview      int `json:"interruptions_to_review"`
	ChargesUnknown             int `json:"charges_unknown"`
	OpenFinancialIssues        int `json:"open_financial_issues"`
	RefundsUnderReview         int `json:"refunds_under_review"`
	DeletionRequestsProcessing int `json:"deletion_requests_processing"`
	UnavailableSignals         int `json:"unavailable_signals"`
}

// Count lê os contadores da loja (na transação/consulta q).
func Count(ctx context.Context, q database.Querier, estID uuid.UUID) (Pendencies, error) {
	var p Pendencies
	o, err := atendimento.PendencyCounts(ctx, q, estID)
	if err != nil {
		return p, err
	}
	f, err := pagamento.PendencyCounts(ctx, q, estID)
	if err != nil {
		return p, err
	}
	del, err := exclusao.ProcessingInShop(ctx, q, estID)
	if err != nil {
		return p, err
	}
	un, err := estabelecimento.UnavailableSignals(ctx, q, estID)
	if err != nil {
		return p, err
	}
	return Pendencies{OrdersAwaitingPayment: o.AwaitingPayment, OrdersAwaitingConfirmation: o.AwaitingConfirmation,
		InterruptionsToReview: o.PendingReview, ChargesUnknown: f.ChargesUnknown, OpenFinancialIssues: f.OpenFinancialIssues,
		RefundsUnderReview: f.RefundsUnderReview, DeletionRequestsProcessing: del, UnavailableSignals: un}, nil
}

// Record grava pendencies.updated (audiência dono) com os contadores atuais, na transação do
// comando que os mudou. A versão é o instante em microssegundos (cresce a cada mudança).
func Record(ctx context.Context, tx pgx.Tx, estID uuid.UUID, now time.Time) error {
	p, err := Count(ctx, tx, estID)
	if err != nil {
		return err
	}
	return outbox.Record(ctx, tx, outbox.Event{EstablishmentID: estID, Type: "pendencies.updated", ResourceType: "establishment",
		ResourceID: estID, ResourceVersion: now.UnixMicro(), Audience: outbox.Audience{Kind: outbox.Owner}, Data: p, OccurredAt: now})
}

// Service atende GET /v1/establishments/{id}/pendencies.
type Service struct {
	Pool *pgxpool.Pool
	Auth acesso.Authorizer
}

// Routes devolve a operação do contrato.
func (s *Service) Routes() []hs.Route {
	return []hs.Route{{Operation: "listPendencies", Method: http.MethodGet, Path: "/v1/establishments/{establishmentId}/pendencies",
		Grants: []hs.Grant{hs.GrantOwner}, Handler: s.listPendencies}}
}

func (s *Service) listPendencies(w http.ResponseWriter, r *http.Request) error {
	estID, err := uuid.Parse(r.PathValue("establishmentId"))
	if err != nil {
		return hs.ErrNotFound()
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	if !actor.IsOwner() {
		return hs.ErrForbidden("Somente o dono vê o painel de pendências.")
	}
	ctx := r.Context()
	var out struct {
		Data   Pendencies `json:"data"`
		Cursor string     `json:"cursor"`
	}
	// Contadores e cursor no mesmo instante: quem continua pelo SSE a partir do cursor não perde mudança.
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		if out.Data, err = Count(ctx, tx, estID); err != nil {
			return err
		}
		out.Cursor, err = outbox.CurrentCursor(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	hs.WriteJSON(w, http.StatusOK, out)
	return nil
}
