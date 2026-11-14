package exclusao

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/estabelecimento"
	"fila/internal/identidade"
	"fila/internal/platform/database"
)

// Diário de exclusões (etapa 12, ADR-0026). Um backup anterior a um pedido de exclusão traria a
// conta de volta SEM o pedido (docs/retencao.md). Por isso cada pedido é copiado, em segundos,
// para um arquivo FORA do banco (FILA_DELETION_JOURNAL_DIR, em outro disco/volume). Depois de
// restaurar, `fila-admin restauracao reconciliar -diario <pasta>` recria os pedidos que o banco
// restaurado não tem, com o MESMO protocolo (o titular continua consultando pelo protocolo dele).
//
// Conteúdo de cada linha: ids técnicos, HMAC do protocolo, sufixo e data. Sem nome, e-mail ou nick.
// O arquivo só cresce (uma linha por pedido) e é lido inteiro na partida.

// JournalFile é o nome do arquivo dentro da pasta do diário.
const JournalFile = "exclusoes.jsonl"

// JournalEntry é uma linha do diário.
type JournalEntry struct {
	V               int         `json:"v"`
	RequestID       uuid.UUID   `json:"request_id"`
	AccountID       uuid.UUID   `json:"account_id"`
	ProtocolHMAC    string      `json:"protocol_hmac"` // hex
	ProtocolSuffix  string      `json:"protocol_suffix"`
	ProfessionalIDs []uuid.UUID `json:"professional_ids"`
	RequestedAt     time.Time   `json:"requested_at"`
}

// Journal copia os pedidos de exclusão para o arquivo do diário.
type Journal struct {
	Dir  string
	seen map[uuid.UUID]bool
	last time.Time
}

// ReadJournal lê todas as linhas do diário (pasta inexistente = diário vazio).
func ReadJournal(dir string) ([]JournalEntry, error) {
	f, err := os.Open(filepath.Join(dir, JournalFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines [][]byte
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, append([]byte(nil), sc.Bytes()...))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var out []JournalEntry
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		var e JournalEntry
		if err := json.Unmarshal(line, &e); err != nil {
			if i == len(lines)-1 {
				// Última linha cortada (queda no meio da escrita): ignorada; é regravada na próxima rodada.
				break
			}
			return nil, fmt.Errorf("diário de exclusões: linha %d ilegível", i+1)
		}
		out = append(out, e)
	}
	return out, nil
}

// Export grava no diário os pedidos que ainda não estão lá. Repetível; cada linha é escrita e
// sincronizada com o disco (fsync) antes de contar como gravada.
func (j *Journal) Export(ctx context.Context, q database.Querier) (int, error) {
	if j.seen == nil {
		if err := os.MkdirAll(j.Dir, 0o700); err != nil {
			return 0, err
		}
		entries, err := ReadJournal(j.Dir)
		if err != nil {
			return 0, err
		}
		j.seen = map[uuid.UUID]bool{}
		for _, e := range entries {
			j.seen[e.RequestID] = true
			if e.RequestedAt.After(j.last) {
				j.last = e.RequestedAt
			}
		}
	}
	// Margem de 1 h: um pedido confirmado com relógio um pouco atrás ainda é visto.
	rows, err := q.Query(ctx, `
		SELECT id, account_id, protocol_hmac, protocol_suffix, professional_ids, requested_at
		FROM deletion_request WHERE requested_at >= $1 ORDER BY requested_at, id`, j.last.Add(-time.Hour))
	if err != nil {
		return 0, err
	}
	var pending []JournalEntry
	for rows.Next() {
		var e JournalEntry
		var mac []byte
		if err := rows.Scan(&e.RequestID, &e.AccountID, &mac, &e.ProtocolSuffix, &e.ProfessionalIDs, &e.RequestedAt); err != nil {
			rows.Close()
			return 0, err
		}
		if !j.seen[e.RequestID] {
			e.V, e.ProtocolHMAC = 1, hex.EncodeToString(mac)
			pending = append(pending, e)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(pending) == 0 {
		return 0, err
	}
	f, err := os.OpenFile(filepath.Join(j.Dir, JournalFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	for _, e := range pending {
		line, _ := json.Marshal(e)
		if _, err := f.Write(append(line, '\n')); err != nil {
			return 0, err
		}
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	for _, e := range pending {
		j.seen[e.RequestID] = true
		if e.RequestedAt.After(j.last) {
			j.last = e.RequestedAt
		}
	}
	return len(pending), nil
}

// RunJournal exporta a cada `every` (worker).
func (s *Service) RunJournal(ctx context.Context, j *Journal, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := j.Export(ctx, s.Pool); err != nil && ctx.Err() == nil {
			s.Log.Error("diário de exclusões: gravação falhou; tentará de novo", "error", err.Error())
		} else if n > 0 {
			s.Log.Info("diário de exclusões atualizado", "pedidos", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ReapplyReport resume a reaplicação do diário depois de restaurar.
type ReapplyReport struct {
	InJournal     int
	AlreadyInDB   int
	Reapplied     int
	AccountAbsent int      // conta criada depois do backup: não existe no banco restaurado, nada a apagar
	NeedsManual   []string // ids de pedido que não puderam ser reaplicados (ex.: única pessoa dona)
}

// Reapply recria no banco restaurado os pedidos do diário que ele não conhece, com o mesmo id e
// protocolo, aplicando os mesmos efeitos do pedido original (sessões encerradas, fila cancelada,
// benefícios encerrados). Sem e-mail novo: o titular já recebeu o protocolo. Repetível.
func (s *Service) Reapply(ctx context.Context, entries []JournalEntry, reason string) (ReapplyReport, error) {
	rep := ReapplyReport{InJournal: len(entries)}
	for _, e := range entries {
		mac, err := hex.DecodeString(e.ProtocolHMAC)
		if err != nil || len(mac) != 32 || len(e.ProtocolSuffix) != 6 {
			return rep, fmt.Errorf("diário: pedido %s com protocolo inválido", e.RequestID)
		}
		outcome := ""
		err = database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM deletion_request WHERE id = $1 OR account_id = $2)`,
				e.RequestID, e.AccountID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				outcome = "already"
				return nil
			}
			now := s.Clock.Now()
			prev, err := s.Identity.BeginDeletion(ctx, tx, e.AccountID, now)
			if errors.Is(err, identidade.ErrAccountNotFound) {
				outcome = "absent"
				return nil
			}
			if err != nil {
				return err
			}
			if prev != identidade.StatusActive {
				outcome = "manual"
				return nil
			}
			if sole, err := estabelecimento.SoleOwnerOf(ctx, tx, e.AccountID); err != nil {
				return err
			} else if len(sole) > 0 {
				outcome = "manual"
				return errRollback
			}
			if _, err := estabelecimento.EndMembershipsForDeletion(ctx, tx, e.AccountID, now); err != nil {
				return err
			}
			if _, err := s.Queue.CancelForDeletion(ctx, tx, e.AccountID, now); err != nil {
				return err
			}
			if s.Benefits != nil {
				if err := s.Benefits.CloseForDeletion(ctx, tx, e.AccountID, now); err != nil {
					return err
				}
			}
			profIDs := e.ProfessionalIDs
			if profIDs == nil {
				profIDs = []uuid.UUID{}
			}
			x, err := scanRequest(tx.QueryRow(ctx, `
				INSERT INTO deletion_request (id, account_id, protocol_hmac, protocol_suffix, professional_ids, requested_at, checked_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING id, account_id, protocol_suffix, status, professional_ids, requested_at, completed_at, version`,
				e.RequestID, e.AccountID, mac, e.ProtocolSuffix, profIDs, e.RequestedAt, now))
			if err != nil {
				return err
			}
			blocks, _, err := s.assess(ctx, tx, &x)
			if err != nil {
				return err
			}
			if err := auditoria.Record(ctx, tx, auditoria.Event{OccurredAt: now, ActorKind: auditoria.ActorOperator, Action: "T-AC-03",
				TargetType: "account", TargetID: e.AccountID, FromState: prev, ToState: identidade.StatusDeletionProcessing,
				Reason: reason}); err != nil {
				return err
			}
			outcome = "reapplied"
			return s.notify(ctx, tx, x, blocks, establishmentsOf(blocks), now)
		})
		if err != nil && !errors.Is(err, errRollback) {
			return rep, fmt.Errorf("reaplicar pedido %s: %w", e.RequestID, err)
		}
		switch outcome {
		case "already":
			rep.AlreadyInDB++
		case "absent":
			rep.AccountAbsent++
		case "manual":
			rep.NeedsManual = append(rep.NeedsManual, e.RequestID.String())
		case "reapplied":
			rep.Reapplied++
		}
	}
	return rep, nil
}

// errRollback desfaz a transação sem ser erro de verdade.
var errRollback = errors.New("desfazer")
