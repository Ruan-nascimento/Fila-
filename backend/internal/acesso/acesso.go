// Package acesso define o contrato de autorização usado pelos módulos de domínio.
//
// Quem decide o ator é o módulo identidade (cookies → sessão → vínculos). Os outros módulos só
// recebem um Authorizer e pedem "este pedido é do dono desta loja?" — nunca leem papel do corpo
// da requisição (R-PERM-06). A interface fica aqui, sem dependências de domínio, para evitar
// ciclo entre identidade e estabelecimento/catálogo (ADR-0018).
package acesso

import (
	"net/http"

	"github.com/google/uuid"
)

// Papéis de equipe.
const (
	RoleOwner        = "owner"
	RoleProfessional = "professional"
)

// Actor é a equipe autorizada para a loja da rota.
type Actor struct {
	AccountID       uuid.UUID
	EstablishmentID uuid.UUID
	// Role é "owner" quando a conta tem vínculo de dono na loja; senão "professional".
	Role string
	// ProfessionalID do vínculo de profissional (nil para dono sem fila própria).
	ProfessionalID *uuid.UUID
}

// IsOwner informa se o ator é dono da loja.
func (a Actor) IsOwner() bool { return a.Role == RoleOwner }

// Authorizer autoriza pedidos de equipe. Regras comuns (R-PERM-01/05):
//   - sem sessão → 401;
//   - sem vínculo ativo com a loja → 404 (não revela que a loja existe);
//   - com vínculo, mas sessão de cliente ou papel insuficiente → 403.
type Authorizer interface {
	// Owner exige sessão de equipe com vínculo de dono na loja (escopo "establishment").
	Owner(r *http.Request, establishmentID uuid.UUID) (Actor, error)
	// Member exige sessão de equipe com qualquer vínculo ativo na loja (dono ou profissional).
	Member(r *http.Request, establishmentID uuid.UUID) (Actor, error)
	// RequireReauth exige confirmação de senha nos últimos 10 minutos (x-reauth).
	RequireReauth(r *http.Request) error
}
