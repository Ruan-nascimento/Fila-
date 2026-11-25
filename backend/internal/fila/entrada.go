package fila

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/fidelidade"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/ratelimit"
	"fila/internal/platform/validate"
)

// Limites de entrada (R-VIS-10, R-SEG-04): por identidade E por origem, nunca só por IP.
var (
	ruleEntryIdentity = ratelimit.Rule{Name: "fila_entrada_identidade", Limit: 10, Window: time.Hour}
	ruleEntryIP       = ratelimit.Rule{Name: "fila_entrada_ip", Limit: 60, Window: time.Hour}
)

type entryRequest struct {
	QueueID    *uuid.UUID                 `json:"queue_id"`
	ServiceIDs *[]uuid.UUID               `json:"service_ids"`
	Items      *[]atendimento.LineRequest `json:"items"`
	// AcknowledgeOwnAppointment: a pessoa viu que a espera prevista invade um horário marcado dela
	// nesta loja e quer entrar mesmo assim (a reserva continua; ela decide se cancela).
	AcknowledgeOwnAppointment *bool `json:"acknowledge_own_appointment"`
}

// Mensagens da entrada (ADR-0035): lotação e bloqueio manual são situações diferentes.
const (
	msgQueueFull    = "A fila está cheia no momento. Tente novamente quando uma vaga for liberada."
	msgEntryClosed  = "A entrada na fila foi encerrada por enquanto. Quem já está na fila será atendido normalmente."
	msgQueuePaused  = "A fila está pausada e não está recebendo entradas agora."
	msgQueueNotOpen = "Esta fila não está recebendo entradas agora."
)

// entryConflict traduz a situação da entrada num 409 com código próprio.
func entryConflict(state string) error {
	switch state {
	case estabelecimento.EntryFull:
		return conflict("queue_full", "Fila cheia", msgQueueFull, nil)
	case estabelecimento.EntryClosedManual:
		return conflict("queue_entry_closed", "Entrada encerrada", msgEntryClosed, nil)
	case estabelecimento.EntryOperationPaused:
		return conflict("queue_not_open", "Fila pausada", msgQueuePaused, nil)
	}
	return conflict("queue_not_open", "Fila não está aberta", msgQueueNotOpen, nil)
}

// admissionState calcula, com a fila JÁ TRAVADA (FOR UPDATE), se ela aceita mais uma
// participação: a contagem de pendentes é feita dentro da mesma transação, então duas pessoas
// disputando a última vaga entram uma de cada vez e só uma passa.
func admissionState(ctx context.Context, q database.Querier, qi estabelecimento.QueueInfo, st estabelecimento.Settings) (string, error) {
	if !qi.ProfessionalActive {
		return estabelecimento.EntryOperationClosed, nil
	}
	capacity := qi.EffectiveCapacity(st)
	pending := 0
	if capacity != nil {
		if err := q.QueryRow(ctx, `SELECT count(*) FROM participation WHERE queue_id = $1 AND status IN ('waiting', 'called')`,
			qi.ID).Scan(&pending); err != nil {
			return "", err
		}
	}
	return estabelecimento.EntryState(qi.Status, qi.Admission, capacity, pending), nil
}

// createParticipation (T-P-01 + T-O-01): entra na fila com 1–10 serviços (com quantidade) e,
// se a loja oferece, até 10 produtos como complemento. Nunca só produtos. Preço, total, papel e
// identidade vêm do servidor; o corpo só tem fila e itens. Um pedido = UMA participação (uma
// posição), mesmo com várias unidades: os serviços são feitos em sequência pelo profissional.
func (s *Service) createParticipation(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	account, visitor := s.Customers.CustomerIdentities(r, estID)
	id := account
	if id == nil {
		id = visitor
	}
	if id == nil {
		return hs.ErrUnauthenticated()
	}
	var req entryRequest
	body, err := hs.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/queue_id", req.QueueID == nil)
	if req.ServiceIDs == nil && req.Items == nil {
		v.Required("/service_ids", true)
	}
	wants := atendimento.RequestedLines(&v, req.ServiceIDs, req.Items, 1)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	subject := estID.String() + "|" + identityKey(*id)
	if err := s.allow(ctx, ruleEntryIdentity, subject); err != nil {
		return err
	}
	if err := s.allow(ctx, ruleEntryIP, estID.String()+"|"+hs.ClientIP(r, s.TrustForwardedFor)); err != nil {
		return err
	}

	cmd := idempotency.Command{ActorKey: identityKey(*id), Operation: "createParticipation", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.Clock.Now()
		// Serializa as entradas da MESMA identidade (abas simultâneas): a segunda espera a primeira
		// e então vê a participação já criada. O índice único do banco é a última barreira.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "fila-entrada|"+subject); err != nil {
			return idempotency.Result{}, err
		}
		if cur, err := s.activeOf(ctx, tx, estID, *id); err != nil {
			return idempotency.Result{}, err
		} else if cur != nil {
			view, err := s.visit(ctx, tx, *cur, false)
			if err != nil {
				return idempotency.Result{}, err
			}
			return idempotency.Result{}, conflict("active_participation_exists", "Você já está na fila",
				"Você já tem uma participação em andamento nesta loja.", view)
		}
		pending, err := atendimento.PendingOrderExists(ctx, tx, estID, orderIdentity(*id))
		if err != nil {
			return idempotency.Result{}, err
		}
		if pending {
			return idempotency.Result{}, conflict("pending_order_exists", "Há uma comanda pendente",
				"Resolva o pagamento da visita anterior antes de entrar de novo.", nil)
		}
		queues, err := estabelecimento.LockQueues(ctx, tx, estID, *req.QueueID)
		if errors.Is(err, estabelecimento.ErrNotFound) {
			return idempotency.Result{}, hs.ErrNotFound()
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		qi := queues[*req.QueueID]
		st, err := estabelecimento.LoadSettings(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		// Visitante desligado pela loja: nenhuma entrada NOVA de visitante (quem já está segue).
		if id.Kind == acesso.IdentityVisitor && !st.VisitorsEnabled {
			return idempotency.Result{}, conflict("visitors_disabled", "Entrada como visitante desligada",
				"Esta barbearia não aceita entrada como visitante no momento. Entre com a sua conta.", nil)
		}
		if state, err := admissionState(ctx, tx, qi, st); err != nil {
			return idempotency.Result{}, err
		} else if state != estabelecimento.EntryAccepting {
			return idempotency.Result{}, entryConflict(state)
		}
		// Forma de atendimento (ADR-0038): "com horário marcado" fecha só a ENTRADA na fila.
		mode := s.mode(st)
		if mode == estabelecimento.ModeSchedule {
			return idempotency.Result{}, conflict("queue_entry_schedule_only", "Esta barbearia atende com horário marcado",
				"Agora a entrada é só com horário marcado. Escolha um horário na agenda.", nil)
		}
		lines, err := atendimento.ResolveNewLines(ctx, tx, estID, qi.ProfessionalID, qi.ProfessionalName, wants, st)
		if err != nil {
			return idempotency.Result{}, err
		}
		// "Os dois": a projeção de hoje (cadeira + fila FIFO + horários marcados) precisa atender a
		// nova entrada dentro do expediente; serializado pela trava da fila, como a reserva.
		if mode == estabelecimento.ModeHybrid && s.Agenda != nil {
			mins := 0
			for _, l := range lines {
				if l.Snap.Kind != "product" {
					mins += l.Snap.DurationMinutes * l.Quantity
				}
			}
			ack := req.AcknowledgeOwnAppointment != nil && *req.AcknowledgeOwnAppointment
			if err := s.Agenda.CheckQueueEntry(ctx, tx, estID, qi, st, mins, id, ack); err != nil {
				return idempotency.Result{}, err
			}
		}

		pos, day, number, err := nextSlot(ctx, tx, qi, now)
		if err != nil {
			return idempotency.Result{}, err
		}
		partID, _ := uuid.NewV7()
		orderID, _ := uuid.NewV7()
		audit := auditoria.Event{ActorKind: auditoria.ActorCustomer, ActorID: auditoria.Ptr(id.ID()), RequestID: hs.RequestIDFrom(ctx)}
		if id.Kind == acesso.IdentityVisitor {
			audit.ActorKind = auditoria.ActorVisitor
		}
		// Elegibilidade de pontos (R-FID-02/03): só conta (sessão de conta só existe com e-mail
		// verificado) e só com o programa ligado NA ENTRADA. O valor é copiado (snapshot) para a
		// participação e para a comanda: desligar depois não retira o crédito prometido, e um
		// visitante que cria conta depois não ganha pontos desta visita.
		loyaltyEligible := false
		if id.Kind == acesso.IdentityAccount {
			if loyaltyEligible, err = fidelidade.ProgramEnabled(ctx, tx, estID); err != nil {
				return idempotency.Result{}, err
			}
		}
		if _, err := atendimento.CreateOrder(ctx, tx, atendimento.NewOrder{ID: orderID, EstablishmentID: estID,
			ParticipationID: partID, ProfessionalID: qi.ProfessionalID, Identity: orderIdentity(*id),
			LoyaltyEligible: loyaltyEligible, Lines: lines, Now: now}, audit); err != nil {
			return idempotency.Result{}, err
		}
		oi := orderIdentity(*id)
		_, err = tx.Exec(ctx, `
			INSERT INTO participation (id, establishment_id, queue_id, order_id, account_id, visitor_id, nick, loyalty_eligible,
			                           position, ticket_day, ticket_number, ticket_code, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)`,
			partID, estID, qi.ID, orderID, oi.AccountID, oi.VisitorID, id.Name, loyaltyEligible, pos, day, number,
			ticketCode(qi.Letter, number), now)
		if database.IsUniqueViolation(err, "participation_active_account_uq") || database.IsUniqueViolation(err, "participation_active_visitor_uq") {
			return idempotency.Result{}, conflict("active_participation_exists", "Você já está na fila",
				"Você já tem uma participação em andamento nesta loja.", nil)
		}
		if err != nil {
			return idempotency.Result{}, fmt.Errorf("criar participação: %w", err)
		}
		if id.Kind == acesso.IdentityVisitor && s.VisitStarted != nil {
			if err := s.VisitStarted(ctx, tx, id.VisitorID); err != nil {
				return idempotency.Result{}, err
			}
		}
		audit.EstablishmentID, audit.OccurredAt, audit.Action = &estID, now, "T-P-01"
		audit.TargetType, audit.TargetID, audit.ToState = "participation", partID, Waiting
		if err := auditoria.Record(ctx, tx, audit); err != nil {
			return idempotency.Result{}, err
		}
		p, err := load(ctx, tx, estID, partID, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		if err := s.recordParticipation(ctx, tx, p); err != nil {
			return idempotency.Result{}, err
		}
		if err := s.QueueChanged(ctx, tx, estID, qi.ID); err != nil {
			return idempotency.Result{}, err
		}
		view, err := s.visit(ctx, tx, p, false)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusCreated, Body: hs.Data[visitView]{Data: view}}, nil
	})
}

// activeOf devolve a participação ativa da identidade na loja (ou nil).
func (s *Service) activeOf(ctx context.Context, q database.Querier, estID uuid.UUID, id acesso.Identity) (*row, error) {
	col := "account_id"
	if id.Kind == acesso.IdentityVisitor {
		col = "visitor_id"
	}
	p, err := scanRow(q.QueryRow(ctx, rowSelect+` WHERE establishment_id = $1 AND `+col+` = $2 AND status = ANY($3)`,
		estID, id.ID(), activeStatuses))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// nextSlot atribui a próxima posição FIFO e a senha do dia (no fuso da loja). Roda com a fila
// travada (LockQueues), então duas entradas nunca recebem a mesma posição ou senha.
func nextSlot(ctx context.Context, q database.Querier, qi estabelecimento.QueueInfo, now time.Time) (pos int64, day time.Time, number int, err error) {
	loc, err := time.LoadLocation(qi.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	day = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	if _, err = q.Exec(ctx, `INSERT INTO queue_counter (queue_id) VALUES ($1) ON CONFLICT DO NOTHING`, qi.ID); err != nil {
		return
	}
	err = q.QueryRow(ctx, `
		UPDATE queue_counter SET last_position = last_position + 1,
		       ticket_seq = CASE WHEN ticket_day = $2 THEN ticket_seq + 1 ELSE 1 END,
		       ticket_day = $2
		WHERE queue_id = $1 RETURNING last_position, ticket_seq`, qi.ID, day).Scan(&pos, &number)
	return
}

func ticketCode(letter string, n int) string { return fmt.Sprintf("%s%03d", letter, n) }

func (s *Service) allow(ctx context.Context, rule ratelimit.Rule, subject string) error {
	ok, retry, err := s.Limiter.Allow(ctx, rule, subject)
	if err != nil {
		return err
	}
	if !ok {
		return hs.ErrRateLimited(retry)
	}
	return nil
}
