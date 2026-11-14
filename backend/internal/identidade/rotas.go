package identidade

import (
	"net/http"

	hs "fila/internal/platform/httpserver"
)

// Routes devolve as operações do contrato implementadas por este módulo.
func (s *Service) Routes() []hs.Route {
	pub := []hs.Grant{hs.GrantPublic}
	return []hs.Route{
		{Operation: "getSession", Method: http.MethodGet, Path: "/v1/session", Grants: pub, Handler: s.getSession},
		{Operation: "registerAccount", Method: http.MethodPost, Path: "/v1/auth/register", Grants: pub, Handler: s.register},
		{Operation: "verifyEmail", Method: http.MethodPost, Path: "/v1/auth/email-verification/confirm", Grants: pub, Handler: s.verifyEmail},
		{Operation: "resendVerification", Method: http.MethodPost, Path: "/v1/auth/email-verification/resend", Grants: pub, Handler: s.resendVerification},
		{Operation: "login", Method: http.MethodPost, Path: "/v1/auth/login", Grants: pub, Handler: s.login},
		{Operation: "logout", Method: http.MethodPost, Path: "/v1/auth/logout", Grants: hs.GrantAnyAccount, Handler: s.logout},
		{Operation: "requestPasswordReset", Method: http.MethodPost, Path: "/v1/auth/password-reset/request", Grants: pub, Handler: s.requestPasswordReset},
		{Operation: "confirmPasswordReset", Method: http.MethodPost, Path: "/v1/auth/password-reset/confirm", Grants: pub, Handler: s.confirmPasswordReset},
		{Operation: "updateMyProfile", Method: http.MethodPatch, Path: "/v1/me", Grants: hs.GrantAnyAccount, Handler: s.updateMyProfile},
		{Operation: "changePassword", Method: http.MethodPost, Path: "/v1/auth/password/change", Grants: hs.GrantAnyAccount, Handler: s.changePassword},
		{Operation: "reauthenticate", Method: http.MethodPost, Path: "/v1/auth/reauthenticate", Grants: hs.GrantAnyAccount, Handler: s.reauthenticate},
		{Operation: "acceptInvite", Method: http.MethodPost, Path: "/v1/auth/invites/accept", Grants: pub, Handler: s.acceptInvite},
		{Operation: "createInvite", Method: http.MethodPost, Path: "/v1/establishments/{establishmentId}/professionals/{professionalId}/invites",
			Grants: []hs.Grant{hs.GrantOwner}, Handler: s.createInvite},
		{Operation: "revokeInvite", Method: http.MethodPost, Path: "/v1/establishments/{establishmentId}/invites/{inviteId}/revoke",
			Grants: []hs.Grant{hs.GrantOwner}, Handler: s.revokeInvite},
		{Operation: "createVisitorSession", Method: http.MethodPost, Path: "/v1/establishments/{establishmentId}/visitor-sessions", Grants: pub, Handler: s.createVisitorSession},
		{Operation: "recoverVisitorSession", Method: http.MethodPost, Path: "/v1/establishments/{establishmentId}/visitor-sessions/recover", Grants: pub, Handler: s.recoverVisitorSession},
		{Operation: "endVisitorSession", Method: http.MethodPost, Path: "/v1/visitor-sessions/current/end", Grants: []hs.Grant{{Actor: "visitor", Scope: "self"}}, Handler: s.endVisitorSession},
		{Operation: "getPublicEstablishmentBySlug", Method: http.MethodGet, Path: "/v1/public/slugs/{slug}", Grants: pub, Handler: s.publicEstablishment},
	}
}
