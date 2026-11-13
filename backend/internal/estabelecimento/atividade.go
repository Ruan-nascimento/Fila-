package estabelecimento

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// QueueInfo é o que o módulo fila precisa saber de uma fila.
type QueueInfo struct {
	ID                 uuid.UUID
	EstablishmentID    uuid.UUID
	ProfessionalID     uuid.UUID
	ProfessionalName   string
	ProfessionalActive bool
	Status             string
	Letter             string
	Timezone           string
}

const queueInfoSelect = `
	SELECT q.id, q.establishment_id, p.id, p.display_name, p.status = 'active', q.status, p.queue_letter, e.timezone
	FROM queue q JOIN professional p ON p.id = q.professional_id JOIN establishment e ON e.id = q.establishment_id
	WHERE q.establishment_id = $1 AND q.id = ANY($2) AND e.status = 'active'
	ORDER BY q.id`

// LockQueues trava as filas DESTA loja (FOR UPDATE), sempre em ordem de id para não haver
// impasse (deadlock) entre comandos que travam duas filas (transferência). Entrada, pausa,
// chamada e transferência na mesma fila ficam assim em fila indiana. Fila de outra loja → ErrNotFound.
func LockQueues(ctx context.Context, q database.Querier, estID uuid.UUID, ids ...uuid.UUID) (map[uuid.UUID]QueueInfo, error) {
	return loadQueues(ctx, q, estID, ids, " FOR UPDATE OF q")
}

// GetQueue lê a fila DESTA loja sem travar.
func GetQueue(ctx context.Context, q database.Querier, estID, queueID uuid.UUID) (QueueInfo, error) {
	m, err := loadQueues(ctx, q, estID, []uuid.UUID{queueID}, "")
	if err != nil {
		return QueueInfo{}, err
	}
	return m[queueID], nil
}

func loadQueues(ctx context.Context, q database.Querier, estID uuid.UUID, ids []uuid.UUID, suffix string) (map[uuid.UUID]QueueInfo, error) {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	sorted = slices.Compact(sorted)
	rows, err := q.Query(ctx, queueInfoSelect+suffix, estID, sorted)
	if err != nil {
		return nil, fmt.Errorf("ler filas: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (QueueInfo, error) {
		var v QueueInfo
		err := r.Scan(&v.ID, &v.EstablishmentID, &v.ProfessionalID, &v.ProfessionalName, &v.ProfessionalActive, &v.Status, &v.Letter, &v.Timezone)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	if len(list) != len(sorted) {
		return nil, ErrNotFound
	}
	out := make(map[uuid.UUID]QueueInfo, len(list))
	for _, v := range list {
		out[v.ID] = v
	}
	return out, nil
}

// QueueOfProfessional devolve a fila do profissional.
func QueueOfProfessional(ctx context.Context, q database.Querier, professionalID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM queue WHERE professional_id = $1`, professionalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// StaffEntry é uma pessoa na fila, como a equipe vê (StaffQueue.entries).
type StaffEntry struct {
	ParticipationID         uuid.UUID  `json:"participation_id"`
	OrderID                 uuid.UUID  `json:"order_id"`
	TicketCode              string     `json:"ticket_code"`
	Nick                    string     `json:"nick"`
	Status                  string     `json:"status"`
	ServiceNames            []string   `json:"service_names"`
	CalledAt                *time.Time `json:"called_at"`
	ToleranceEndsAt         *time.Time `json:"tolerance_ends_at"`
	HasPendingChangeRequest bool       `json:"has_pending_change_request"`
	Version                 int64      `json:"version"`
}

// PublicSnapshot é o que o público vê de uma fila: só senhas, situação, quantidade e estimativa.
type PublicSnapshot struct {
	WaitingCount    int
	Tickets         []PublicTicket
	EstimateMinutes int
}

// PublicTicket é uma senha visível ao público.
type PublicTicket struct {
	TicketCode string `json:"ticket_code"`
	Status     string `json:"status"`
}

// QueueActivity fornece as pessoas de cada fila (módulo fila). nil = filas vazias.
type QueueActivity interface {
	StaffEntries(ctx context.Context, q database.Querier, queueIDs []uuid.UUID) (map[uuid.UUID][]StaffEntry, error)
	PublicSnapshots(ctx context.Context, q database.Querier, queueIDs []uuid.UUID, now time.Time) (map[uuid.UUID]PublicSnapshot, error)
	// QueueChanged grava na outbox as visões atuais da fila (pública e da equipe), na transação tx.
	QueueChanged(ctx context.Context, tx pgx.Tx, estID, queueID uuid.UUID) error
}

// queueChanged grava o evento da fila se o módulo de fila estiver ligado.
func (s *Service) queueChanged(ctx context.Context, tx pgx.Tx, estID, queueID uuid.UUID) error {
	if s.Activity == nil {
		return nil
	}
	return s.Activity.QueueChanged(ctx, tx, estID, queueID)
}

// UnavailableSignals conta os profissionais com sinal de indisponibilidade ligado (painel do dono).
func UnavailableSignals(ctx context.Context, q database.Querier, estID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM queue WHERE establishment_id = $1 AND unavailable`, estID).Scan(&n)
	return n, err
}
