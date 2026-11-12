package identidade

import (
	"context"
	"time"

	"github.com/google/uuid"

	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
)

// Visões de resposta. Os nomes e formatos seguem os esquemas do contrato (SessionView,
// AccountView, MembershipView, VisitorView, VisitorSessionCreated, Invite, PublicEstablishment);
// o teste de contrato confere que decodificam nos tipos gerados sem campos a mais.

type accountView struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Email  string    `json:"email"`
	Status string    `json:"status"`
}

type sessionInfo struct {
	Kind             string     `json:"kind"`
	ExpiresAt        time.Time  `json:"expires_at"`
	ReauthValidUntil *time.Time `json:"reauth_valid_until"`
}

type membershipView struct {
	EstablishmentID   uuid.UUID  `json:"establishment_id"`
	EstablishmentName string     `json:"establishment_name"`
	Role              string     `json:"role"`
	ProfessionalID    *uuid.UUID `json:"professional_id"`
}

type visitorView struct {
	ID              uuid.UUID `json:"id"`
	EstablishmentID uuid.UUID `json:"establishment_id"`
	Nick            string    `json:"nick"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type sessionView struct {
	Account     *accountView     `json:"account"`
	Session     *sessionInfo     `json:"session"`
	Visitors    []visitorView    `json:"visitors"`
	Memberships []membershipView `json:"memberships"`
	CSRFToken   *string          `json:"csrf_token"`
}

type inviteView struct {
	ID             uuid.UUID `json:"id"`
	ProfessionalID uuid.UUID `json:"professional_id"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type publicEstablishmentView struct {
	ID       uuid.UUID `json:"id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Timezone string    `json:"timezone"`
}

type messageView struct {
	Message string `json:"message"`
}

// sessionView monta a visão da sessão do principal.
func (s *Service) buildSessionView(ctx context.Context, q database.Querier, p *Principal) (sessionView, error) {
	v := sessionView{Visitors: []visitorView{}, Memberships: []membershipView{}}
	if p.Session != nil {
		v.Account = &accountView{ID: p.Session.AccountID, Name: p.Session.AccountName,
			Email: p.Session.AccountEmail, Status: p.Session.AccountStatus}
		v.Session = &sessionInfo{Kind: p.Session.Kind, ExpiresAt: p.Session.ExpiresAt}
		if r := p.Session.ReauthValidUntil; r != nil && r.After(s.clock.Now()) {
			v.Session.ReauthValidUntil = r
		}
		ms, err := estabelecimento.ActiveMemberships(ctx, q, p.Session.AccountID)
		if err != nil {
			return v, err
		}
		for _, m := range ms {
			v.Memberships = append(v.Memberships, membershipView{EstablishmentID: m.EstablishmentID,
				EstablishmentName: m.EstablishmentName, Role: m.Role, ProfessionalID: m.ProfessionalID})
		}
	}
	for _, vc := range p.Visitors {
		v.Visitors = append(v.Visitors, visitorView{ID: vc.VisitorID, EstablishmentID: vc.EstablishmentID,
			Nick: vc.Nick, ExpiresAt: vc.ExpiresAt})
	}
	if tok := s.csrfToken(p); tok != "" {
		v.CSRFToken = &tok
	}
	return v, nil
}
