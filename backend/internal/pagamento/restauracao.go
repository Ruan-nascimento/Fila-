package pagamento

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/atendimento"
	"fila/internal/auditoria"
	"fila/internal/platform/database"
)

// Pendências abertas pela conciliação pós-restauração (etapa 12).
const (
	IssueRestoredOrphan           = "restored_orphan_charge"
	IssueRestoredRefundUnrecorded = "restored_refund_unrecorded"
)

// RestoreReport resume a conciliação com o provedor depois de restaurar um backup. Só contagens
// e identificadores técnicos: nada de dado pessoal.
type RestoreReport struct {
	Since            time.Time
	LocalChecked     int      // tentativas não finais do banco restaurado consultadas no provedor
	LocalUnresolved  int      // continuaram sem resposta conclusiva (o worker segue consultando)
	ProviderPaid     int      // cobranças pagas no provedor desde a data
	ProviderRefunded int      // cobranças estornadas no provedor desde a data
	Recreated        int      // cobranças que o banco restaurado não conhecia, recriadas a partir do provedor
	PaymentsApplied  int      // pagamentos registrados agora (o banco restaurado "não sabia")
	Orphans          int      // pagas no provedor sem comanda no banco restaurado → pendência
	RefundIssues     int      // estornadas no provedor sem estorno no banco → pendência
	Unlinked         []string // ids do provedor sem vínculo com o sistema (sem loja): tratar à mão
}

// ReconcileAfterRestore confere o banco restaurado com o provedor ANTES de o worker voltar:
//
//  1. Tentativas não finais do banco (criando, pendente, desconhecida) são CONSULTADAS. Ao
//     contrário do worker normal, nunca se cria cobrança aqui: se o provedor não tem nada com o
//     nosso id, a tentativa é marcada como falha (o cliente gera outro Pix se precisar).
//  2. Cobranças PAGAS e ESTORNADAS no provedor desde `since` são comparadas com o banco:
//     - existe aqui e o pagamento não está registrado → registra (como o worker faria);
//     - não existe aqui, mas a comanda existe → recria a tentativa (created_by 'restore') com o
//     mesmo id e registra o pagamento pelo caminho normal (principal, excesso, pendências);
//     - nem a comanda existe → pendência `restored_orphan_charge` para o dono (devolver ou
//     registrar à mão); nada é inventado;
//     - estornada no provedor e sem estorno aqui → pendência `restored_refund_unrecorded`.
//
// Repetível: rodar de novo não duplica recebimento, pontos nem pendência (índices únicos).
// Qualquer falha de comunicação com o provedor devolve erro e a trava continua.
func (s *Service) ReconcileAfterRestore(ctx context.Context, since time.Time) (RestoreReport, error) {
	rep := RestoreReport{Since: since}
	if s.Provider == nil {
		return rep, errors.New("pix desligado nesta configuração: configure o provedor para conciliar (ou confirme que nunca houve Pix)")
	}
	paid, err := s.listAll(ctx, since, ProviderPaid)
	if err != nil {
		return rep, err
	}
	refunded, err := s.listAll(ctx, since, ProviderRefunded)
	if err != nil {
		return rep, err
	}
	rep.ProviderPaid, rep.ProviderRefunded = len(paid), len(refunded)

	// 1. Tentativas não finais do banco restaurado (só consulta).
	rows, err := s.Pool.Query(ctx, `SELECT id FROM pix_charge WHERE status IN ('creating', 'pending', 'unknown') ORDER BY created_at`)
	if err != nil {
		return rep, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return rep, err
	}
	for _, id := range ids {
		rep.LocalChecked++
		active, err := s.reconcileQueryOnly(ctx, id)
		if err != nil {
			return rep, fmt.Errorf("consultar tentativa %s: %w", id, err)
		}
		if active {
			rep.LocalUnresolved++
		}
	}

	// 2. O que o provedor sabe e o banco restaurado talvez não.
	for _, pc := range append(paid, refunded...) {
		if err := s.restoreOne(ctx, pc, &rep); err != nil {
			return rep, fmt.Errorf("conciliar cobrança %s do provedor: %w", pc.ProviderID, err)
		}
	}
	return rep, nil
}

func (s *Service) listAll(ctx context.Context, since time.Time, status string) ([]Charge, error) {
	callCtx, cancel := context.WithTimeout(ctx, 10*s.Timeout+10*time.Second)
	defer cancel()
	return s.Provider.ListPixSince(callCtx, since, status)
}

// reconcileQueryOnly consulta uma tentativa não final sem nunca criar cobrança. Devolve se ela
// continua ativa (pendente ou desconhecida).
func (s *Service) reconcileQueryOnly(ctx context.Context, id uuid.UUID) (bool, error) {
	c, err := scanCharge(s.Pool.QueryRow(ctx, chargeSelect+` WHERE id = $1`, id))
	if err != nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.Timeout+2*time.Second)
	defer cancel()
	var pc Charge
	if c.ProviderID != nil {
		pc, err = s.Provider.GetPix(callCtx, *c.ProviderID)
	} else {
		var found []Charge
		found, err = s.Provider.FindPix(callCtx, c.ID.String())
		switch {
		case err != nil:
		case len(found) > 1:
			err = ErrIntegrity
		case len(found) == 1:
			pc = found[0]
		default:
			// Nada no provedor com o nosso id: a criação nunca chegou lá. Não se cria agora.
			return false, s.finish(ctx, c, nil, ChargeFailed)
		}
	}
	if errors.Is(err, ErrNotFound) && c.ProviderID != nil {
		return true, s.retryLater(ctx, c, err) // o worker segue consultando depois de liberar
	}
	if err != nil {
		return false, err
	}
	if err := s.finish(ctx, c, &pc, ""); err != nil {
		return false, err
	}
	var st string
	if err := s.Pool.QueryRow(ctx, `SELECT status FROM pix_charge WHERE id = $1`, id).Scan(&st); err != nil {
		return false, err
	}
	return st == ChargePending || st == ChargeUnknown || st == ChargeCreating, nil
}

func (s *Service) restoreOne(ctx context.Context, pc Charge, rep *RestoreReport) error {
	if pc.DevMode != s.Provider.TestMode() {
		return fmt.Errorf("%w: ambiente da cobrança diferente do da chave", ErrIntegrity)
	}
	chargeID, errC := uuid.Parse(pc.Metadata["charge_id"])
	orderID, errO := uuid.Parse(pc.Metadata["order_id"])
	estID, errE := uuid.Parse(pc.Metadata["establishment_id"])

	// Já existe aqui? Pelo id do provedor ou pelo nosso id (metadado).
	var local *chargeRow
	c, err := scanCharge(s.Pool.QueryRow(ctx, chargeSelect+` WHERE provider_charge_id = $1 OR id = $2`, pc.ProviderID, chargeID))
	switch {
	case err == nil:
		local = &c
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	if local == nil && (errC != nil || errO != nil || errE != nil) {
		// Cobrança sem os nossos metadados: não foi criada por este sistema (ou foi criada à mão).
		rep.Unlinked = append(rep.Unlinked, pc.ProviderID)
		s.Log.Error("cobrança do provedor sem vínculo com o sistema; tratar à mão", "provedor_id", pc.ProviderID)
		return nil
	}
	asPaid := pc
	asPaid.Status = ProviderPaid // estornada = foi paga antes; o pagamento é registrado primeiro

	if local == nil {
		created, orphan, err := s.recreateCharge(ctx, chargeID, estID, orderID, pc)
		if err != nil {
			return err
		}
		if orphan {
			rep.Orphans++
			return nil
		}
		if created {
			rep.Recreated++
		}
		c, err := scanCharge(s.Pool.QueryRow(ctx, chargeSelect+` WHERE id = $1`, chargeID))
		if err != nil {
			return err
		}
		local = &c
	}
	applied, err := s.applyRestored(ctx, *local, asPaid)
	if err != nil {
		return err
	}
	if applied {
		rep.PaymentsApplied++
	}
	if pc.Status == ProviderRefunded {
		opened, err := s.refundUnrecorded(ctx, local.ID, pc)
		if err != nil {
			return err
		}
		if opened {
			rep.RefundIssues++
		}
	}
	return nil
}

// recreateCharge grava a tentativa que o banco restaurado perdeu. Se a comanda tem outra tentativa
// ativa, a recriada entra como expirada (o pagamento vira tardio/excesso pelo caminho normal e o
// dono vê a pendência); se a comanda nem existe, abre a pendência órfã.
func (s *Service) recreateCharge(ctx context.Context, chargeID, estID, orderID uuid.UUID, pc Charge) (created, orphan bool, err error) {
	err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		now := s.Clock.Now()
		lp, err := atendimento.LockForPayment(ctx, tx, estID, orderID)
		if errors.Is(err, atendimento.ErrOrderNotFound) {
			orphan = true
			amount := pc.AmountCents
			// A loja precisa existir no banco restaurado (FK); se não existir, a conciliação para.
			_, err := s.openIssueRef(ctx, tx, estID, nil, IssueRestoredOrphan, "pix_charge", chargeID, &amount, &pc.ProviderID, now)
			return err
		}
		if err != nil {
			return err
		}
		active, err := activeCharge(ctx, tx, lp.ID, true)
		if err != nil {
			return err
		}
		status, resolved := ChargeUnknown, (*time.Time)(nil)
		if active != nil {
			status, resolved = ChargeExpired, &now
		}
		key := sha256.Sum256([]byte("restauracao:" + chargeID.String()))
		tag, err := tx.Exec(ctx, `
			INSERT INTO pix_charge (id, establishment_id, order_id, status, amount_cents, provider, test_mode, provider_charge_id,
			                        br_code, expires_at, idem_key_hash, created_by, created_at, updated_at, resolved_at, next_check_at)
			VALUES ($1, $2, $3, $4, $5, 'abacatepay', $6, $7, nullif($8, ''), $9, $10, 'restore', $11, $11, $12, $11)
			ON CONFLICT (id) DO NOTHING`,
			chargeID, estID, lp.ID, status, pc.AmountCents, pc.DevMode, pc.ProviderID, pc.BRCode, pc.ExpiresAt, key[:], now, resolved)
		if err != nil {
			return err
		}
		created = tag.RowsAffected() == 1
		if created {
			return auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorSystem,
				Action: "T-PX-01", TargetType: "pix_charge", TargetID: chargeID, ToState: status + ":restaurada"})
		}
		return nil
	})
	return created, orphan, err
}

// applyRestored registra o pagamento confirmado pelo provedor (consulta autenticada) se o banco
// ainda não o tem. Devolve se registrou agora.
func (s *Service) applyRestored(ctx context.Context, c chargeRow, pc Charge) (bool, error) {
	var before int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM receipt WHERE pix_charge_id = $1`, c.ID).Scan(&before); err != nil {
		return false, err
	}
	if before > 0 && c.Status == ChargePaid {
		return false, nil
	}
	if err := s.finish(ctx, c, &pc, ""); err != nil {
		return false, err
	}
	var after int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM receipt WHERE pix_charge_id = $1`, c.ID).Scan(&after); err != nil {
		return false, err
	}
	return after > before, nil
}

// refundUnrecorded abre a pendência quando o provedor diz ESTORNADA e o banco não tem estorno
// (confirmado ou em andamento) para o recebimento desta cobrança.
func (s *Service) refundUnrecorded(ctx context.Context, chargeID uuid.UUID, pc Charge) (bool, error) {
	opened := false
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var estID, orderID, receiptID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT establishment_id, order_id, id FROM receipt WHERE pix_charge_id = $1`, chargeID).
			Scan(&estID, &orderID, &receiptID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // sem recebimento (comanda órfã): a pendência órfã já cobre
		}
		if err != nil {
			return err
		}
		var known bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM refund WHERE receipt_id = $1 AND status <> 'failed')`, receiptID).
			Scan(&known); err != nil {
			return err
		}
		if known {
			return nil // o worker confirma pela consulta (REFUNDED) depois de liberar
		}
		amount := pc.AmountCents
		opened, err = s.openIssueRef(ctx, tx, estID, &orderID, IssueRestoredRefundUnrecorded, "pix_charge", chargeID, &amount,
			&pc.ProviderID, s.Clock.Now())
		return err
	})
	return opened, err
}
