package identidade

import (
	"context"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"fila/internal/acesso"
	"fila/internal/platform/database"
)

// CustomerIdentities implementa acesso.Customers: conta (sessão de conta em qualquer contexto,
// "agindo como cliente") e visitante desta loja, a partir dos cookies.
func (s *Service) CustomerIdentities(r *http.Request, estID uuid.UUID) (account, visitor *acesso.Identity) {
	p := PrincipalFrom(r.Context())
	if p.Session != nil {
		account = &acesso.Identity{Kind: acesso.IdentityAccount, AccountID: p.Session.AccountID, Name: shortName(p.Session.AccountName)}
	}
	if v := p.VisitorIn(estID); v != nil {
		visitor = &acesso.Identity{Kind: acesso.IdentityVisitor, VisitorID: v.VisitorID, Name: v.Nick}
	}
	return account, visitor
}

var _ acesso.Customers = (*Service)(nil)

// shortName cabe no limite de nick (30 caracteres) usado na fila.
func shortName(name string) string {
	if utf8.RuneCountInString(name) <= 30 {
		return name
	}
	return string([]rune(name)[:30])
}

// SetActiveVisit liga a verificação de visita ativa/pendente do visitante (T-VS-01), fornecida
// pelo módulo fila. Chamado na montagem da API, antes de atender pedidos.
func (s *Service) SetActiveVisit(f ActiveVisitFunc) { s.activeVisit = f }

// VisitStarted marca que o visitante tem visita em andamento: o prazo de "24 h após encerrar"
// de uma visita anterior deixa de valer (a nova visita precisa do acesso até terminar).
func (s *Service) VisitStarted(ctx context.Context, q database.Querier, visitorID uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE visitor SET visit_closed_at = NULL WHERE id = $1`, visitorID)
	return err
}

// VisitClosed registra o encerramento da visita do visitante (comanda encerrada): a credencial
// e o código passam a valer só por mais 24 horas (R-VIS-03). Chamado na transação que encerra.
func (s *Service) VisitClosed(ctx context.Context, q database.Querier, visitorID uuid.UUID, at time.Time) error {
	_, err := q.Exec(ctx, `UPDATE visitor SET visit_closed_at = $2 WHERE id = $1`, visitorID, at)
	return err
}
