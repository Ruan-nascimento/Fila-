package fidelidade

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/platform/clock"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
)

// HoldFor é a validade de uma reserva de prêmio (R-FID-10).
const HoldFor = 24 * time.Hour

// Deps são as dependências do módulo.
type Deps struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Log       *slog.Logger
	Auth      acesso.Authorizer
	Customers acesso.Customers
}

// Service implementa programa, extrato, prêmios e reservas.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço e liga o histórico do cliente aos pontos creditados.
func New(d Deps) *Service {
	atendimento.RegisterPointsCredited(PointsCredited)
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	const est = "/v1/establishments/{establishmentId}"
	self := []hs.Grant{{Actor: "customer", Scope: "self"}}
	owner := []hs.Grant{hs.GrantOwner}
	staff := []hs.Grant{{Actor: "professional", Scope: "establishment_member"}, hs.GrantOwner}
	return []hs.Route{
		{Operation: "getLoyaltyProgram", Method: http.MethodGet, Path: est + "/loyalty-program", Grants: owner, Handler: s.getProgram},
		{Operation: "updateLoyaltyProgram", Method: http.MethodPut, Path: est + "/loyalty-program", Grants: owner, Handler: s.updateProgram},
		{Operation: "listRewards", Method: http.MethodGet, Path: est + "/rewards", Grants: owner, Handler: s.listRewards},
		{Operation: "createReward", Method: http.MethodPost, Path: est + "/rewards", Grants: owner, Handler: s.createReward},
		{Operation: "updateReward", Method: http.MethodPatch, Path: est + "/rewards/{rewardId}", Grants: owner, Handler: s.updateReward},
		{Operation: "deactivateReward", Method: http.MethodPost, Path: est + "/rewards/{rewardId}/deactivate", Grants: owner,
			Handler: s.deactivateReward},
		{Operation: "getMyLoyalty", Method: http.MethodGet, Path: "/v1/me/loyalty/{establishmentId}", Grants: self, Handler: s.getMyLoyalty},
		{Operation: "listMyLoyaltyLedger", Method: http.MethodGet, Path: "/v1/me/loyalty/{establishmentId}/ledger", Grants: self,
			Handler: s.listMyLedger},
		{Operation: "listCustomerRewards", Method: http.MethodGet, Path: est + "/reward-catalog", Grants: self, Handler: s.listCustomerRewards},
		{Operation: "createRewardReservation", Method: http.MethodPost, Path: est + "/reward-reservations", Grants: self,
			Handler: s.createReservation},
		{Operation: "listRewardReservations", Method: http.MethodGet, Path: est + "/reward-reservations", Grants: staff,
			Handler: s.listReservations},
		{Operation: "cancelRewardReservation", Method: http.MethodPost, Path: est + "/reward-reservations/{reservationId}/cancel",
			Grants: self, Handler: s.cancelReservation},
		{Operation: "deliverRewardReservation", Method: http.MethodPost, Path: est + "/reward-reservations/{reservationId}/deliver",
			Grants: staff, Handler: s.deliverReservation},
		{Operation: "revertRewardDelivery", Method: http.MethodPost, Path: est + "/reward-reservations/{reservationId}/revert-delivery",
			Grants: owner, Handler: s.revertDelivery},
	}
}

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, hs.ErrNotFound()
	}
	return id, nil
}

func conflict(code, title, detail string, current any) error {
	return &hs.Error{Status: 409, Code: code, Title: title, Detail: detail, Current: current}
}
