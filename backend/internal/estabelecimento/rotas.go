package estabelecimento

import (
	"net/http"

	hs "fila/internal/platform/httpserver"
)

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	owner := []hs.Grant{hs.GrantOwner}
	member := []hs.Grant{{Actor: "professional", Scope: "establishment_member"}, hs.GrantOwner}
	// Perfil profissional próprio: quem atende (profissional, ou dono que também atende).
	self := []hs.Grant{{Actor: "professional", Scope: "self"}, {Actor: "owner", Scope: "self"}}
	const est = "/v1/establishments/{establishmentId}"
	return []hs.Route{
		{Operation: "getEstablishment", Method: http.MethodGet, Path: est, Grants: member, Handler: s.getEstablishment},
		{Operation: "updateEstablishment", Method: http.MethodPatch, Path: est, Grants: owner, Handler: s.updateEstablishment},
		{Operation: "setEstablishmentLogo", Method: http.MethodPut, Path: est + "/logo", Grants: owner, Handler: s.setLogo},
		{Operation: "removeEstablishmentLogo", Method: http.MethodPost, Path: est + "/logo/remove", Grants: owner, Handler: s.removeLogo},
		{Operation: "getMyProfessionalProfile", Method: http.MethodGet, Path: est + "/my-professional-profile", Grants: self, Handler: s.getMyProfessionalProfile},
		{Operation: "setMyProfessionalPhoto", Method: http.MethodPut, Path: est + "/my-professional-profile/photo", Grants: self, Handler: s.setMyProfessionalPhoto},
		{Operation: "removeMyProfessionalPhoto", Method: http.MethodPost, Path: est + "/my-professional-profile/photo/remove", Grants: self, Handler: s.removeMyProfessionalPhoto},
		{Operation: "removeProfessionalPhoto", Method: http.MethodPost, Path: est + "/professionals/{professionalId}/photo/remove", Grants: owner, Handler: s.removeProfessionalPhoto},
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
		{Operation: "closeAdmission", Method: http.MethodPost, Path: est + "/queues/{queueId}/close-admission",
			Grants: []hs.Grant{{Actor: "professional", Scope: "own_queue"}, hs.GrantOwner}, Handler: s.closeAdmission},
		{Operation: "reopenAdmission", Method: http.MethodPost, Path: est + "/queues/{queueId}/reopen-admission",
			Grants: []hs.Grant{{Actor: "professional", Scope: "own_queue"}, hs.GrantOwner}, Handler: s.reopenAdmission},
		{Operation: "setQueueCapacity", Method: http.MethodPut, Path: est + "/queues/{queueId}/capacity", Grants: owner, Handler: s.setQueueCapacity},
		{Operation: "getEstablishmentSettings", Method: http.MethodGet, Path: est + "/settings", Grants: owner, Handler: s.getSettings},
		{Operation: "updateCatalogSettings", Method: http.MethodPatch, Path: est + "/settings/catalog", Grants: owner, Handler: s.updateCatalogSettings},
		{Operation: "updateQueueSettings", Method: http.MethodPatch, Path: est + "/settings/queue", Grants: owner, Handler: s.updateQueueSettings},
		{Operation: "updatePaymentSettings", Method: http.MethodPatch, Path: est + "/settings/payments", Grants: owner, Handler: s.updatePaymentSettings},
		{Operation: "updateAgendaSettings", Method: http.MethodPatch, Path: est + "/settings/agenda", Grants: owner, Handler: s.updateAgendaSettings},
		{Operation: "getAgendaImpact", Method: http.MethodGet, Path: est + "/settings/agenda/impact", Grants: owner, Handler: s.getAgendaImpact},
		{Operation: "signalAvailability", Method: http.MethodPost, Path: est + "/queues/{queueId}/availability-signal",
			Grants: []hs.Grant{{Actor: "professional", Scope: "own_queue"}}, Handler: s.signalAvailability},
		{Operation: "listPublicEstablishments", Method: http.MethodGet, Path: "/v1/public/establishments",
			Grants: []hs.Grant{hs.GrantPublic}, Handler: s.listPublicEstablishments},
		{Operation: "listPublicQueues", Method: http.MethodGet, Path: "/v1/public/establishments/{establishmentId}/queues",
			Grants: []hs.Grant{hs.GrantPublic}, Handler: s.listPublicQueues},
	}
}
