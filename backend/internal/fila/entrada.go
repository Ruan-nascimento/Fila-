package fila

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
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
	QueueID    *uuid.UUID   `json:"queue_id"`
	ServiceIDs *[]uuid.UUID `json:"service_ids"`
}

// createParticipation (T-P-01 + T-O-01): entra na fila com 1–10 serviços. Preço, total, papel e
// identidade vêm do servidor; o corpo só tem fila e serviços.
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
	v.Required("/service_ids", req.ServiceIDs == nil)
	var serviceIDs []uuid.UUID
	if req.ServiceIDs != nil {
		serviceIDs = *req.ServiceIDs
		switch {
		case len(serviceIDs) == 0:
			v.Add(&hs.FieldError{Field: "/service_ids", Code: hs.FieldTooShort, Message: "escolha pelo menos um serviço"})
		case len(serviceIDs) > maxServices:
			v.Add(&hs.FieldError{Field: "/service_ids", Code: hs.FieldTooLong})
		}
		for i, sid := range serviceIDs {
			if slices.Index(serviceIDs, sid) != i {
				v.Add(&hs.FieldError{Field: "/service_ids/" + strconv.Itoa(i), Code: "duplicate"})
			}
		}
	}
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
		if qi.Status != estabelecimento.QueueOpen || !qi.ProfessionalActive {
			return idempotency.Result{}, conflict("queue_not_open", "Fila não está aberta",
				"Esta fila não está recebendo entradas agora.", nil)
		}
		snaps, err := catalogo.ServicesForEntry(ctx, tx, estID, serviceIDs)
		if err != nil {
			return idempotency.Result{}, err
		}
		services := make([]catalogo.Snapshot, 0, len(serviceIDs))
		for i, sid := range serviceIDs {
			sn, ok := snaps[sid]
			switch {
			case !ok:
				return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/service_ids/" + strconv.Itoa(i),
					Code: hs.FieldNotAllowed, Message: "serviço não encontrado nesta loja"})
			case !sn.Active:
				return idempotency.Result{}, conflict("service_inactive", "Serviço indisponível",
					fmt.Sprintf("O serviço %q não está disponível no momento.", sn.Name), nil)
			case !sn.Enabled(qi.ProfessionalID):
				return idempotency.Result{}, conflict("service_not_eligible", "Serviço não feito por este profissional",
					fmt.Sprintf("%s não faz o serviço %q. Escolha outro profissional ou serviço.", qi.ProfessionalName, sn.Name), nil)
			}
			services = append(services, sn)
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
		// Elegibilidade de pontos (R-FID-03): só conta com programa ligado. O programa de pontos
		// chega na etapa 10 e começa desligado (R-FID-01); até lá ninguém é elegível. O valor é
		// copiado (snapshot) para a participação e para a comanda.
		const loyaltyEligible = false
		if _, err := atendimento.CreateOrder(ctx, tx, atendimento.NewOrder{ID: orderID, EstablishmentID: estID,
			ParticipationID: partID, ProfessionalID: qi.ProfessionalID, Identity: orderIdentity(*id),
			LoyaltyEligible: loyaltyEligible, Services: services, Now: now}, audit); err != nil {
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
