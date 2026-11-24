package estabelecimento

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Entrada na fila separada da operação (ADR-0035). Fechar a ENTRADA só impede novas
// participações: quem já está continua, a equipe segue chamando, atendendo e recebendo, e a
// ordem não muda. Uma desistência nunca reabre a entrada; só a equipe autorizada reabre.
// Reabrir não ignora a capacidade: se a fila continuar lotada, novas entradas esperam vaga.

func (s *Service) closeAdmission(w http.ResponseWriter, r *http.Request) error {
	return s.changeAdmission(w, r, "closeAdmission", AdmissionClosed, "T-Q-06")
}

func (s *Service) reopenAdmission(w http.ResponseWriter, r *http.Request) error {
	return s.changeAdmission(w, r, "reopenAdmission", AdmissionOpen, "T-Q-07")
}

// changeAdmission: dono em qualquer fila da loja; profissional só na própria fila e só com a
// permissão "Profissionais podem controlar a entrada da própria fila" ligada.
func (s *Service) changeAdmission(w http.ResponseWriter, r *http.Request, op, to, action string) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Member(r, estID)
	if err != nil {
		return err
	}
	queueID, err := pathID(r, "queueId")
	if err != nil {
		return err
	}
	var req versionRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: op, Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadQueue(ctx, tx, estID, queueID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		st, err := LoadSettings(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		if !AdmissionAllowedFor(actor, cur.Professional.ID, st) {
			if !actor.IsOwner() && (actor.ProfessionalID == nil || *actor.ProfessionalID != cur.Professional.ID) {
				return idempotency.Result{}, httpserver.ErrNotFound() // fila de outro profissional
			}
			return idempotency.Result{}, httpserver.ErrForbidden("O dono da loja não liberou o controle da entrada pelos profissionais.")
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(s.withEntries(ctx, tx, cur))
		}
		if cur.Admission.Status == to {
			detail := "A entrada desta fila já está fechada."
			if to == AdmissionOpen {
				detail = "A entrada desta fila já está aberta."
			}
			return idempotency.Result{}, invalidTransition(detail, s.withEntries(ctx, tx, cur))
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE queue SET admission = $2, admission_changed_at = $3, version = version + 1, updated_at = $3
			WHERE id = $1`, queueID, to, now); err != nil {
			return idempotency.Result{}, err
		}
		return s.queueCommandDone(ctx, tx, actor, estID, queueID, action, cur.Admission.Status, to)
	})
}

type capacityRequest struct {
	ExpectedVersion *int64      `json:"expected_version"`
	Capacity        optionalInt `json:"capacity"`
}

// setQueueCapacity (T-Q-08): só o dono. null = segue o padrão da loja. Reduzir abaixo de quem já
// está na fila não tira ninguém: só impede novas entradas até liberar vaga.
func (s *Service) setQueueCapacity(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	queueID, err := pathID(r, "queueId")
	if err != nil {
		return err
	}
	var req capacityRequest
	body, err := httpserver.DecodeJSON(r, &req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := v.Version("/expected_version", req.ExpectedVersion)
	v.Required("/capacity", !req.Capacity.Set)
	if req.Capacity.Value != nil {
		c := int64(*req.Capacity.Value)
		v.IntRange("/capacity", &c, MinQueueCapacity, MaxQueueCapacity)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: "setQueueCapacity", Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		cur, err := loadQueue(ctx, tx, estID, queueID, true)
		if err != nil {
			return idempotency.Result{}, err
		}
		if cur.Version != expected {
			return idempotency.Result{}, versionConflict(s.withEntries(ctx, tx, cur))
		}
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `UPDATE queue SET capacity = $2, version = version + 1, updated_at = $3 WHERE id = $1`,
			queueID, req.Capacity.Value, now); err != nil {
			return idempotency.Result{}, err
		}
		return s.queueCommandDone(ctx, tx, actor, estID, queueID, "T-Q-08", capacityTag(cur.Admission.CapacityOverride),
			capacityTag(req.Capacity.Value))
	})
}

func capacityTag(c *int) string {
	if c == nil {
		return "padrao_da_loja"
	}
	return "limite_" + strconv.Itoa(*c)
}

// withEntries completa a visão da fila (entradas, entrada e carga) para respostas de conflito.
func (s *Service) withEntries(ctx context.Context, tx pgx.Tx, v staffQueueView) staffQueueView {
	views := []staffQueueView{v}
	if err := s.fillEntries(ctx, tx, views); err != nil {
		return v
	}
	return views[0]
}

// queueCommandDone grava a auditoria e os eventos da fila e devolve a visão atual.
func (s *Service) queueCommandDone(ctx context.Context, tx pgx.Tx, actor acesso.Actor, estID, queueID uuid.UUID,
	action, from, to string) (idempotency.Result, error) {
	kind := auditoria.ActorProfessional
	if actor.IsOwner() {
		kind = auditoria.ActorOwner
	}
	if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: s.Clock.Now(), ActorKind: kind,
		ActorID: auditoria.Ptr(actor.AccountID), Action: action, TargetType: "queue", TargetID: queueID,
		FromState: from, ToState: to, RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
		return idempotency.Result{}, err
	}
	if err := s.queueChanged(ctx, tx, estID, queueID); err != nil {
		return idempotency.Result{}, err
	}
	next, err := loadQueue(ctx, tx, estID, queueID, false)
	if err != nil {
		return idempotency.Result{}, err
	}
	views := []staffQueueView{next}
	if err := s.fillEntries(ctx, tx, views); err != nil {
		return idempotency.Result{}, err
	}
	return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[staffQueueView]{Data: views[0]}}, nil
}
