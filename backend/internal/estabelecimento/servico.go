package estabelecimento

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fila/internal/acesso"
	"fila/internal/platform/clock"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
)

// Occupancy informa quantas participações uma fila/profissional tem. É fornecida pelo módulo
// fila a partir da etapa 04; enquanto não existem participações, o valor é nil e as contagens
// são zero (não há ninguém para proteger).
type Occupancy interface {
	// PendingInQueue: participações aguardando ou chamadas (bloqueiam o fechamento, T-Q-04).
	PendingInQueue(ctx context.Context, q database.Querier, queueID uuid.UUID) (int, error)
	// ActiveForProfessional: aguardando, chamadas ou em atendimento (bloqueiam a desativação, T-PR-02).
	ActiveForProfessional(ctx context.Context, q database.Querier, professionalID uuid.UUID) (int, error)
}

// InviteInfo é o convite pendente exibido na lista de profissionais (esquema Invite).
type InviteInfo struct {
	ID             uuid.UUID `json:"id"`
	ProfessionalID uuid.UUID `json:"professional_id"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// Deps são as dependências do módulo.
type Deps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Log   *slog.Logger
	Auth  acesso.Authorizer
	// Occupancy é opcional até a etapa 04.
	Occupancy Occupancy
	// PendingInvites devolve o convite pendente de cada profissional da loja (módulo identidade).
	PendingInvites func(ctx context.Context, q database.Querier, establishmentID uuid.UUID) (map[uuid.UUID]InviteInfo, error)
	// AccessRevoked avisa o módulo identidade que estas contas perderam o vínculo de equipe:
	// ele encerra sessões de equipe e convites pendentes (T-PR-02).
	AccessRevoked func(ctx context.Context, q database.Querier, accountIDs []uuid.UUID, professionalID uuid.UUID, now time.Time) error
}

// Service implementa loja, equipe e filas.
type Service struct {
	Deps
	idem *idempotency.Runner
}

// New cria o serviço.
func New(d Deps) *Service {
	return &Service{Deps: d, idem: idempotency.New(d.Pool, d.Clock, d.Log)}
}

func (s *Service) pendingInQueue(ctx context.Context, q database.Querier, queueID uuid.UUID) (int, error) {
	if s.Occupancy == nil {
		return 0, nil
	}
	return s.Occupancy.PendingInQueue(ctx, q, queueID)
}

func (s *Service) activeForProfessional(ctx context.Context, q database.Querier, profID uuid.UUID) (int, error) {
	if s.Occupancy == nil {
		return 0, nil
	}
	return s.Occupancy.ActiveForProfessional(ctx, q, profID)
}

// pathID lê um UUID do caminho; inválido vira 404 (não revela formato interno).
func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, httpserver.ErrNotFound()
	}
	return id, nil
}

func versionConflict(current any) error {
	return &httpserver.Error{Status: 409, Code: "version_conflict", Title: "Os dados mudaram",
		Detail: "Outra pessoa alterou este registro. Confira os dados atualizados e tente de novo.", Current: current}
}

func invalidTransition(detail string, current any) error {
	return &httpserver.Error{Status: 409, Code: "invalid_transition", Title: "Ação não permitida agora",
		Detail: detail, Current: current}
}

func actorKey(a acesso.Actor) string { return "conta:" + a.AccountID.String() }
