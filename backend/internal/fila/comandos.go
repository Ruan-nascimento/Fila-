package fila

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// ---------------------------------------------------------------------------------------------
// callNext (T-P-02): o servidor escolhe o primeiro aguardando (FIFO). Sem versão esperada: a
// disputa é resolvida pelo bloqueio da fila e pelo índice único "uma cadeira por fila".

func (s *Service) callNext(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	queueID, err := pathID(r, "queueId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	ctx := r.Context()
	qi, err := estabelecimento.GetQueue(ctx, s.Pool, estID, queueID)
	if errors.Is(err, estabelecimento.ErrNotFound) {
		return hs.ErrNotFound()
	}
	if err != nil {
		return err
	}
	if !actor.IsOwner() && (actor.ProfessionalID == nil || *actor.ProfessionalID != qi.ProfessionalID) {
		return hs.ErrForbidden("Você só pode chamar clientes da sua própria fila.")
	}
	var empty struct{}
	body, err := hs.DecodeJSON(r, &empty)
	if err != nil {
		return err
	}
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "callNext", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		queues, err := estabelecimento.LockQueues(ctx, tx, estID, queueID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if queues[queueID].Status != estabelecimento.QueueOpen {
			return idempotency.Result{}, conflict("queue_not_open", "Fila não está aberta",
				"Com a fila pausada ou fechada não é possível chamar.", nil)
		}
		busy, err := scanRow(tx.QueryRow(ctx, rowSelect+` WHERE queue_id = $1 AND status IN ('called', 'in_service')`, queueID))
		if err == nil {
			view, verr := s.visit(ctx, tx, busy, true)
			if verr != nil {
				return idempotency.Result{}, verr
			}
			return idempotency.Result{}, conflict("professional_busy", "Cadeira ocupada",
				"Já há um cliente chamado ou em atendimento nesta fila.", view)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return idempotency.Result{}, err
		}
		next, err := scanRow(tx.QueryRow(ctx, rowSelect+` WHERE queue_id = $1 AND status = 'waiting' ORDER BY position LIMIT 1 FOR UPDATE`, queueID))
		if errors.Is(err, pgx.ErrNoRows) {
			return idempotency.Result{}, conflict("no_waiting_participant", "Ninguém aguardando",
				"Não há clientes aguardando nesta fila.", nil)
		}
		if err != nil {
			return idempotency.Result{}, err
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `
			UPDATE participation SET status = 'called', called_at = $2, tolerance_ends_at = $3, version = version + 1, updated_at = $2
			WHERE id = $1`, next.ID, now, now.Add(Tolerance)); err != nil {
			if database.IsUniqueViolation(err, "participation_chair_uq") {
				return idempotency.Result{}, conflict("professional_busy", "Cadeira ocupada", "Já há um cliente chamado nesta fila.", nil)
			}
			return idempotency.Result{}, err
		}
		return s.finish(ctx, tx, next.EstablishmentID, next.ID, role{staff: actor, isStaff: true}, "T-P-02", Waiting, Called, "", http.StatusOK)
	})
}

// ---------------------------------------------------------------------------------------------
// Comandos sobre uma participação: startService, markNoShow, cancelParticipation, transfer.

type versionReason struct {
	ExpectedVersion *int64  `json:"expected_version"`
	Reason          *string `json:"reason"`
}

// lockForCommand trava, nesta ordem, a(s) fila(s) e a participação (ordem fixa = sem impasse).
// Se a participação mudou de fila entre a leitura e o bloqueio (transferência simultânea), a
// versão terá mudado e o chamador devolve 409 version_conflict.
func (s *Service) lockForCommand(ctx context.Context, tx pgx.Tx, estID, partID uuid.UUID, extraQueues ...uuid.UUID) (row, map[uuid.UUID]estabelecimento.QueueInfo, error) {
	before, err := load(ctx, tx, estID, partID, false)
	if err != nil {
		return row{}, nil, err
	}
	queues, err := estabelecimento.LockQueues(ctx, tx, estID, append([]uuid.UUID{before.QueueID}, extraQueues...)...)
	if errors.Is(err, estabelecimento.ErrNotFound) {
		return row{}, nil, hs.ErrNotFound()
	}
	if err != nil {
		return row{}, nil, err
	}
	p, err := load(ctx, tx, estID, partID, true)
	if err == nil && p.QueueID != before.QueueID {
		// Mudou de fila entre a leitura e o bloqueio (transferência simultânea): a versão também
		// mudou, então quem pediu recebe conflito com os dados atuais.
		p.Version = -1
	}
	return p, queues, err
}

// participationCommand é a parte comum: autoriza, valida versão e roda a transição.
func (s *Service) participationCommand(w http.ResponseWriter, r *http.Request, op string, a allow,
	body any, run func(tx pgx.Tx, p row, who role, queues map[uuid.UUID]estabelecimento.QueueInfo) (idempotency.Result, error),
	expected func() int64, extraQueue func() *uuid.UUID) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	partID, err := pathID(r, "participationId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	p, err := load(ctx, s.Pool, estID, partID, false)
	if err != nil {
		return err
	}
	qi, err := estabelecimento.GetQueue(ctx, s.Pool, estID, p.QueueID)
	if err != nil {
		return err
	}
	who, err := s.authorize(r, p, qi.ProfessionalID, a)
	if err != nil {
		return err
	}
	raw, err := hs.DecodeJSON(r, body)
	if err != nil {
		return err
	}
	if err := validateBody(body, who); err != nil {
		return err
	}
	key := "participacao:" + partID.String()
	if who.isStaff {
		key = actorKey(who.staff)
	} else if acct, vis := s.Customers.CustomerIdentities(r, estID); p.owns(acct) {
		key = identityKey(*acct)
	} else if p.owns(vis) {
		key = identityKey(*vis)
	}
	cmd := idempotency.Command{ActorKey: key, Operation: op, Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		var extra []uuid.UUID
		if extraQueue != nil {
			if q := extraQueue(); q != nil {
				extra = append(extra, *q)
			}
		}
		cur, queues, err := s.lockForCommand(ctx, tx, estID, partID, extra...)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected() {
			view, verr := s.visit(ctx, tx, cur, who.isStaff)
			if verr != nil {
				return idempotency.Result{}, verr
			}
			return idempotency.Result{}, conflict("version_conflict", "A visita mudou",
				"Outra pessoa alterou esta participação. Confira os dados atualizados.", view)
		}
		return run(tx, cur, who, queues)
	})
}

// validateBody aplica as validações comuns de versão e motivo.
func validateBody(body any, who role) error {
	var v validate.Collector
	switch b := body.(type) {
	case *versionReason:
		v.Version("/expected_version", b.ExpectedVersion)
		if b.Reason != nil {
			_, fe := validate.Text("/reason", *b.Reason, 3, 500)
			v.Add(fe)
		}
	case *versionOnly:
		v.Version("/expected_version", b.ExpectedVersion)
	case *transferRequest:
		v.Version("/expected_version", b.ExpectedVersion)
		v.Required("/target_queue_id", b.TargetQueueID == nil)
		v.Required("/reason", b.Reason == nil)
		if b.Reason != nil {
			_, fe := validate.Text("/reason", *b.Reason, 3, 500)
			v.Add(fe)
		}
		v.Required("/customer_consent", b.Consent == nil)
		if b.Consent != nil {
			v.Required("/customer_consent/given", b.Consent.Given == nil)
			v.Required("/customer_consent/method", b.Consent.Method == nil)
			if b.Consent.Method != nil && *b.Consent.Method != "in_person_verbal" {
				v.Add(&hs.FieldError{Field: "/customer_consent/method", Code: hs.FieldNotAllowed})
			}
			if b.Consent.Note != nil {
				_, fe := validate.Text("/customer_consent/note", *b.Consent.Note, 0, 300)
				v.Add(fe)
			}
		}
	}
	return v.Err()
}

type versionOnly struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

// startService (T-P-03): chamado → em atendimento, mesmo após os 10 minutos (enquanto não
// marcado ausente) e com a fila pausada ou fechada (R-FILA-06, R-FILA-11).
func (s *Service) startService(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	return s.participationCommand(w, r, "startService", allow{professional: true, owner: true}, &body,
		func(tx pgx.Tx, p row, who role, _ map[uuid.UUID]estabelecimento.QueueInfo) (idempotency.Result, error) {
			ctx := r.Context()
			if p.Status != Called {
				return s.invalid(ctx, tx, p, who, "Só é possível iniciar o atendimento de quem foi chamado.")
			}
			now := s.Clock.Now()
			if _, err := tx.Exec(ctx, `UPDATE participation SET status = 'in_service', started_at = $2, version = version + 1, updated_at = $2 WHERE id = $1`,
				p.ID, now); err != nil {
				return idempotency.Result{}, err
			}
			return s.finish(ctx, tx, p.EstablishmentID, p.ID, who, "T-P-03", Called, InService, "", http.StatusOK)
		}, func() int64 { return deref(body.ExpectedVersion) }, nil)
}

// markNoShow (T-P-06): chamado → ausente, só depois dos 10 minutos (relógio do servidor).
// Encerra a comanda sem cobrança; sem punição de pontos (R-FID-06); voltar = nova entrada.
func (s *Service) markNoShow(w http.ResponseWriter, r *http.Request) error {
	var body versionOnly
	return s.participationCommand(w, r, "markNoShow", allow{professional: true, owner: true}, &body,
		func(tx pgx.Tx, p row, who role, _ map[uuid.UUID]estabelecimento.QueueInfo) (idempotency.Result, error) {
			ctx := r.Context()
			if p.Status != Called {
				return s.invalid(ctx, tx, p, who, "Só é possível marcar ausência de quem foi chamado.")
			}
			now := s.Clock.Now()
			if now.Before(*p.ToleranceEndsAt) {
				view, err := s.visit(ctx, tx, p, true)
				if err != nil {
					return idempotency.Result{}, err
				}
				return idempotency.Result{}, conflict("tolerance_not_elapsed", "Ainda dentro da tolerância",
					"A ausência só pode ser marcada 10 minutos depois da chamada.", view)
			}
			return s.end(ctx, tx, p, who, NoShow, atendimento.ClosedNoShow, "T-P-06", "")
		}, func() int64 { return deref(body.ExpectedVersion) }, nil)
}

// cancelParticipation: desistência do dono da visita (T-P-04) ou cancelamento pelo dono da loja
// com motivo obrigatório (T-P-05). Só aguardando ou chamado; depois de iniciado, só interrupção
// pela equipe (etapa 05). Sem multa e sem perda de pontos (R-FILA-12).
func (s *Service) cancelParticipation(w http.ResponseWriter, r *http.Request) error {
	var body versionReason
	return s.participationCommand(w, r, "cancelParticipation", allow{customer: true, owner: true}, &body,
		func(tx pgx.Tx, p row, who role, _ map[uuid.UUID]estabelecimento.QueueInfo) (idempotency.Result, error) {
			ctx := r.Context()
			reason := ""
			if body.Reason != nil {
				reason, _ = validate.Text("/reason", *body.Reason, 3, 500)
			}
			if who.isStaff && reason == "" {
				return idempotency.Result{}, hs.ErrValidation(hs.FieldError{Field: "/reason", Code: hs.FieldRequired,
					Message: "informe o motivo do cancelamento"})
			}
			if p.Status != Waiting && p.Status != Called {
				return s.invalid(ctx, tx, p, who, "Só é possível desistir enquanto aguarda ou depois de chamado; em atendimento, fale com a equipe.")
			}
			if who.isStaff {
				return s.end(ctx, tx, p, who, Cancelled, atendimento.ClosedByOwner, "T-P-05", reason)
			}
			return s.end(ctx, tx, p, who, Cancelled, atendimento.ClosedByCustomer, "T-P-04", "")
		}, func() int64 { return deref(body.ExpectedVersion) }, nil)
}

// end encerra a participação sem atendimento e a comanda sem cobrança (T-O-05).
func (s *Service) end(ctx context.Context, tx pgx.Tx, p row, who role, to, closure, action, reason string) (idempotency.Result, error) {
	now := s.Clock.Now()
	var endReason *string
	if reason != "" {
		endReason = &reason
	}
	if _, err := tx.Exec(ctx, `UPDATE participation SET status = $2, finished_at = $3, end_reason = $4, version = version + 1, updated_at = $3 WHERE id = $1`,
		p.ID, to, now, endReason); err != nil {
		return idempotency.Result{}, err
	}
	audit := s.auditBase(ctx, p, who)
	if err := atendimento.CloseNoCharge(ctx, tx, p.EstablishmentID, p.OrderID, closure, now, audit); err != nil && !errors.Is(err, atendimento.ErrNotOpen) {
		return idempotency.Result{}, err
	}
	if p.VisitorID != nil && s.VisitClosed != nil {
		if err := s.VisitClosed(ctx, tx, *p.VisitorID, now); err != nil {
			return idempotency.Result{}, err
		}
	}
	return s.finish(ctx, tx, p.EstablishmentID, p.ID, who, action, p.Status, to, reason, http.StatusOK)
}

type transferRequest struct {
	ExpectedVersion *int64     `json:"expected_version"`
	TargetQueueID   *uuid.UUID `json:"target_queue_id"`
	Reason          *string    `json:"reason"`
	Consent         *struct {
		Given  *bool   `json:"given"`
		Method *string `json:"method"`
		Note   *string `json:"note"`
	} `json:"customer_consent"`
}

// transferParticipation (T-P-07): só o dono; consentimento do cliente registrado; só quem está
// aguardando; destino aberto, profissional ativo e habilitado para TODOS os serviços aceitos;
// entra no FIM do destino com nova senha; preços aceitos mantidos. Não existe reordenação.
func (s *Service) transferParticipation(w http.ResponseWriter, r *http.Request) error {
	var body transferRequest
	return s.participationCommand(w, r, "transferParticipation", allow{owner: true}, &body,
		func(tx pgx.Tx, p row, who role, queues map[uuid.UUID]estabelecimento.QueueInfo) (idempotency.Result, error) {
			ctx := r.Context()
			if body.Consent.Given == nil || !*body.Consent.Given {
				return idempotency.Result{}, &hs.Error{Status: 400, Code: "transfer_consent_required", Title: "Falta a concordância do cliente",
					Detail: "Registre que o cliente concordou com a transferência."}
			}
			target := *body.TargetQueueID
			if p.Status != Waiting {
				return s.invalid(ctx, tx, p, who, "Só é possível transferir quem está aguardando.")
			}
			if target == p.QueueID {
				return s.invalid(ctx, tx, p, who, "O cliente já está nesta fila.")
			}
			tq := queues[target]
			if tq.Status != estabelecimento.QueueOpen || !tq.ProfessionalActive {
				return idempotency.Result{}, conflict("queue_not_open", "Fila de destino não está aberta",
					"Só é possível transferir para uma fila aberta.", nil)
			}
			ids, err := atendimento.ServiceIDs(ctx, tx, p.OrderID)
			if err != nil {
				return idempotency.Result{}, err
			}
			enabled, err := catalogo.EnabledServiceIDs(ctx, tx, p.EstablishmentID, tq.ProfessionalID)
			if err != nil {
				return idempotency.Result{}, err
			}
			for _, sid := range ids {
				if !enabled[sid] {
					return idempotency.Result{}, conflict("transfer_target_incompatible", "Profissional de destino incompatível",
						tq.ProfessionalName+" não faz todos os serviços escolhidos pelo cliente.", nil)
				}
			}
			now := s.Clock.Now()
			pos, day, number, err := nextSlot(ctx, tx, tq, now)
			if err != nil {
				return idempotency.Result{}, err
			}
			code := ticketCode(tq.Letter, number)
			if _, err := tx.Exec(ctx, `
				UPDATE participation SET queue_id = $2, position = $3, ticket_day = $4, ticket_number = $5, ticket_code = $6,
				       version = version + 1, updated_at = $7
				WHERE id = $1`, p.ID, target, pos, day, number, code, now); err != nil {
				return idempotency.Result{}, err
			}
			reason, _ := validate.Text("/reason", *body.Reason, 3, 500)
			var note *string
			if body.Consent.Note != nil && *body.Consent.Note != "" {
				n, _ := validate.Text("/note", *body.Consent.Note, 0, 300)
				note = &n
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO participation_transfer (participation_id, from_queue_id, to_queue_id, from_ticket_code, to_ticket_code,
				                                    reason, consent_method, consent_note, by_account_id, at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				p.ID, p.QueueID, target, p.TicketCode, code, reason, *body.Consent.Method, note, who.staff.AccountID, now); err != nil {
				return idempotency.Result{}, err
			}
			return s.finish(ctx, tx, p.EstablishmentID, p.ID, who, "T-P-07", Waiting, Waiting, reason, http.StatusOK)
		}, func() int64 { return deref(body.ExpectedVersion) }, func() *uuid.UUID { return body.TargetQueueID })
}

// ---------------------------------------------------------------------------------------------

func (s *Service) invalid(ctx context.Context, tx pgx.Tx, p row, who role, detail string) (idempotency.Result, error) {
	view, err := s.visit(ctx, tx, p, who.isStaff)
	if err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{}, conflict("invalid_transition", "Ação não permitida agora", detail, view)
}

func (s *Service) auditBase(ctx context.Context, p row, who role) auditoria.Event {
	e := auditoria.Event{RequestID: hs.RequestIDFrom(ctx)}
	switch {
	case who.isStaff:
		e.ActorKind, e.ActorID = who.auditKind(), auditoria.Ptr(who.staff.AccountID)
	case p.AccountID != nil:
		e.ActorKind, e.ActorID = auditoria.ActorCustomer, p.AccountID
	default:
		e.ActorKind, e.ActorID = auditoria.ActorVisitor, p.VisitorID
	}
	return e
}

// finish grava a auditoria da transição e devolve a visita atualizada.
func (s *Service) finish(ctx context.Context, tx pgx.Tx, estID, partID uuid.UUID, who role, action, from, to, reason string, status int) (idempotency.Result, error) {
	p, err := load(ctx, tx, estID, partID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	e := s.auditBase(ctx, p, who)
	e.EstablishmentID, e.OccurredAt, e.Action = &estID, s.Clock.Now(), action
	e.TargetType, e.TargetID, e.FromState, e.ToState, e.Reason = "participation", partID, from, to, reason
	if err := auditoria.Record(ctx, tx, e); err != nil {
		return idempotency.Result{}, err
	}
	view, err := s.visit(ctx, tx, p, who.isStaff)
	if err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: status, Body: hs.Data[visitView]{Data: view}}, nil
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
