package fila

import (
	"net/http"

	hs "fila/internal/platform/httpserver"
)

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	const est = "/v1/establishments/{establishmentId}"
	const part = est + "/participations/{participationId}"
	visitOwner := []hs.Grant{{Actor: "visitor", Scope: "visit_owner"}, {Actor: "customer", Scope: "visit_owner"}}
	staff := []hs.Grant{{Actor: "professional", Scope: "own_queue"}, hs.GrantOwner}
	return []hs.Route{
		{Operation: "createParticipation", Method: http.MethodPost, Path: est + "/participations",
			Grants: []hs.Grant{{Actor: "visitor", Scope: "self"}, {Actor: "customer", Scope: "self"}}, Handler: s.createParticipation},
		{Operation: "getCurrentVisit", Method: http.MethodGet, Path: est + "/my-visit", Grants: visitOwner, Handler: s.getCurrentVisit},
		{Operation: "getParticipation", Method: http.MethodGet, Path: part, Grants: append(append([]hs.Grant{}, visitOwner...), staff...), Handler: s.getParticipation},
		{Operation: "cancelParticipation", Method: http.MethodPost, Path: part + "/cancel",
			Grants: append(append([]hs.Grant{}, visitOwner...), hs.GrantOwner), Handler: s.cancelParticipation},
		{Operation: "startService", Method: http.MethodPost, Path: part + "/start", Grants: staff, Handler: s.startService},
		{Operation: "completeService", Method: http.MethodPost, Path: part + "/complete", Grants: staff, Handler: s.completeService},
		{Operation: "interruptService", Method: http.MethodPost, Path: part + "/interrupt", Grants: staff, Handler: s.interruptService},
		{Operation: "markNoShow", Method: http.MethodPost, Path: part + "/no-show", Grants: staff, Handler: s.markNoShow},
		{Operation: "transferParticipation", Method: http.MethodPost, Path: part + "/transfer", Grants: []hs.Grant{hs.GrantOwner}, Handler: s.transferParticipation},
		{Operation: "callNext", Method: http.MethodPost, Path: est + "/queues/{queueId}/call-next", Grants: staff, Handler: s.callNext},
	}
}
