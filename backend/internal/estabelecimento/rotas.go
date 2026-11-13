package estabelecimento

import (
	"net/http"

	hs "fila/internal/platform/httpserver"
)

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	owner := []hs.Grant{hs.GrantOwner}
	member := []hs.Grant{{Actor: "professional", Scope: "establishment_member"}, hs.GrantOwner}
	const est = "/v1/establishments/{establishmentId}"
	return []hs.Route{
		{Operation: "getEstablishment", Method: http.MethodGet, Path: est, Grants: member, Handler: s.getEstablishment},
		{Operation: "updateEstablishment", Method: http.MethodPatch, Path: est, Grants: owner, Handler: s.updateEstablishment},
		{Operation: "listProfessionals", Method: http.MethodGet, Path: est + "/professionals", Grants: owner, Handler: s.listProfessionals},
		{Operation: "createProfessional", Method: http.MethodPost, Path: est + "/professionals", Grants: owner, Handler: s.createProfessional},
		{Operation: "updateProfessional", Method: http.MethodPatch, Path: est + "/professionals/{professionalId}", Grants: owner, Handler: s.updateProfessional},
		{Operation: "deactivateProfessional", Method: http.MethodPost, Path: est + "/professionals/{professionalId}/deactivate", Grants: owner, Handler: s.deactivateProfessional},
		{Operation: "reactivateProfessional", Method: http.MethodPost, Path: est + "/professionals/{professionalId}/reactivate", Grants: owner, Handler: s.reactivateProfessional},
		{Operation: "listStaffQueues", Method: http.MethodGet, Path: est + "/queues",
			Grants: []hs.Grant{{Actor: "professional", Scope: "own_queue"}, hs.GrantOwner}, Handler: s.listStaffQueues},
		{Operation: "openQueue", Method: http.MethodPost, Path: est + "/queues/{queueId}/open", Grants: owner, Handler: s.openQueue},
		{Operation: "pauseQueue", Method: http.MethodPost, Path: est + "/queues/{queueId}/pause", Grants: owner, Handler: s.pauseQueue},
		{Operation: "closeQueue", Method: http.MethodPost, Path: est + "/queues/{queueId}/close", Grants: owner, Handler: s.closeQueue},
		{Operation: "signalAvailability", Method: http.MethodPost, Path: est + "/queues/{queueId}/availability-signal",
			Grants: []hs.Grant{{Actor: "professional", Scope: "own_queue"}}, Handler: s.signalAvailability},
		{Operation: "listPublicQueues", Method: http.MethodGet, Path: "/v1/public/establishments/{establishmentId}/queues",
			Grants: []hs.Grant{hs.GrantPublic}, Handler: s.listPublicQueues},
	}
}
