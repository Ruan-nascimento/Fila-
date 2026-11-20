package midia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// job é um envio reservado para processamento.
type job struct {
	ID       uuid.UUID
	Purpose  string
	Staging  *string
	Crop     *Crop
	Attempts int
}

// Run processa envios pendentes com no máximo `workers` imagens ao mesmo tempo (memória e CPU
// limitadas). Trabalho persistente: a reserva tem prazo (lease); se o worker cair no meio, outro
// retoma depois do prazo. Nada fica "ready" sem todas as variantes gravadas.
func (s *Service) Run(ctx context.Context, workers int, idle time.Duration) {
	if !s.Enabled() {
		return
	}
	var wg sync.WaitGroup
	for range max(1, workers) {
		wg.Go(func() {
			for {
				did, err := s.ProcessOnce(ctx)
				if err != nil && ctx.Err() == nil {
					s.Log.Warn("processamento de imagem falhou", "error", database.Describe(err))
				}
				if did && err == nil {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(idle):
				}
			}
		})
	}
	wg.Go(func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			if n, err := s.CleanOnce(ctx); err != nil && ctx.Err() == nil {
				s.Log.Warn("limpeza de imagens falhou", "error", database.Describe(err))
			} else if n > 0 {
				s.Log.Info("imagens antigas apagadas", "quantidade", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
	wg.Wait()
}

// reserve trava um envio pendente (SKIP LOCKED: vários workers não pegam o mesmo).
func (s *Service) reserve(ctx context.Context) (*job, error) {
	var j *job
	now := s.Clock.Now()
	err := database.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var x job
		var cx, cy, cs *float64
		err := tx.QueryRow(ctx, `SELECT id, purpose, staging_key, crop_x, crop_y, crop_size, attempts FROM media_asset
			WHERE state = 'processing' AND attempts < $2 AND (lease_until IS NULL OR lease_until < $1)
			ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now, maxAttempts).
			Scan(&x.ID, &x.Purpose, &x.Staging, &cx, &cy, &cs, &x.Attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if cx != nil && cy != nil && cs != nil {
			x.Crop = &Crop{X: *cx, Y: *cy, Size: *cs}
		}
		x.Attempts++
		if _, err := tx.Exec(ctx, `UPDATE media_asset SET attempts = $2, lease_until = $3, updated_at = $4 WHERE id = $1`,
			x.ID, x.Attempts, now.Add(leaseFor), now); err != nil {
			return err
		}
		j = &x
		return nil
	})
	return j, err
}

// ProcessOnce processa um envio pendente. Devolve false quando não havia nenhum.
func (s *Service) ProcessOnce(ctx context.Context) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	j, err := s.reserve(ctx)
	if err != nil || j == nil {
		return false, err
	}
	if j.Staging == nil {
		return true, s.fail(ctx, j.ID, "processing_error")
	}
	original, err := s.Store.Get(ctx, *j.Staging)
	if errors.Is(err, ErrObjectNotFound) {
		return true, s.fail(ctx, j.ID, "processing_error")
	}
	if err != nil {
		return true, s.retryOrFail(ctx, j, err)
	}
	variants, err := processWithTimeout(ctx, original, j.Purpose, j.Crop)
	switch {
	case errors.Is(err, errUnsupported):
		return true, s.finishFailed(ctx, j, "unsupported_format")
	case errors.Is(err, errDimensions):
		return true, s.finishFailed(ctx, j, "dimensions")
	case errors.Is(err, errInvalid):
		return true, s.finishFailed(ctx, j, "invalid_image")
	case err != nil:
		return true, s.retryOrFail(ctx, j, err)
	}
	rows := make([]variantRow, 0, len(variants))
	for _, v := range variants {
		key := variantKey(j.ID.String(), v.Size, v.Ext)
		if err := s.Store.Put(ctx, key, v.Data); err != nil {
			s.deleteKeys(ctx, rows)
			return true, s.retryOrFail(ctx, j, err)
		}
		rows = append(rows, variantRow{Size: v.Size, Width: v.Width, Height: v.Height, Bytes: len(v.Data), Key: key, Mime: v.Mime})
	}
	enc, _ := json.Marshal(rows)
	now := s.Clock.Now()
	tag, err := s.Pool.Exec(ctx, `UPDATE media_asset SET state = 'ready', variants = $2, ready_at = $3, updated_at = $3,
		staging_key = NULL, lease_until = NULL WHERE id = $1 AND state = 'processing'`, j.ID, enc, now)
	if err != nil {
		s.deleteKeys(ctx, rows)
		return true, err
	}
	if tag.RowsAffected() == 0 {
		s.deleteKeys(ctx, rows) // foi encerrado enquanto processava (ex.: exclusão de conta)
	}
	_ = s.Store.Delete(ctx, *j.Staging) // o original nunca é publicado
	return true, nil
}

// processWithTimeout limita o tempo de espera pelo processamento. A decodificação em si não é
// interrompível; o limite de concorrência do worker impede acúmulo.
func processWithTimeout(ctx context.Context, b []byte, purpose string, crop *Crop) ([]encoded, error) {
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()
	type result struct {
		v   []encoded
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- result{err: errInvalid} // decodificador em pânico com entrada maliciosa
			}
		}()
		v, err := process(b, purpose, crop)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("tempo de processamento esgotado")
	}
}

func (s *Service) retryOrFail(ctx context.Context, j *job, cause error) error {
	if j.Attempts >= maxAttempts {
		s.Log.Warn("imagem desistida depois das tentativas", "asset_id", j.ID.String(), "error", cause.Error())
		return s.finishFailed(ctx, j, "processing_error")
	}
	return cause // a reserva vence e outra tentativa acontece depois
}

func (s *Service) finishFailed(ctx context.Context, j *job, reason string) error {
	if err := s.fail(ctx, j.ID, reason); err != nil {
		return err
	}
	if j.Staging != nil {
		_ = s.Store.Delete(ctx, *j.Staging)
	}
	return nil
}

func (s *Service) fail(ctx context.Context, id uuid.UUID, reason string) error {
	now := s.Clock.Now()
	_, err := s.Pool.Exec(ctx, `UPDATE media_asset SET state = 'failed', failure_reason = $2, updated_at = $3, lease_until = NULL
		WHERE id = $1 AND state = 'processing'`, id, reason, now)
	return err
}

func (s *Service) deleteKeys(ctx context.Context, rows []variantRow) {
	for _, r := range rows {
		_ = s.Store.Delete(ctx, r.Key)
	}
}

// CleanOnce apaga os arquivos de envios não associados (24 h), de imagens removidas (retenção de
// 24 h) e de originais órfãos no staging; desiste de envios presos. Nunca toca imagem associada.
func (s *Service) CleanOnce(ctx context.Context) (int, error) {
	if !s.Enabled() {
		return 0, nil
	}
	now := s.Clock.Now()
	// Presos: esgotaram as tentativas e a última reserva venceu.
	if _, err := s.Pool.Exec(ctx, `UPDATE media_asset SET state = 'failed', failure_reason = 'processing_error', updated_at = $1
		WHERE state = 'processing' AND attempts >= $2 AND lease_until < $1`, now, maxAttempts); err != nil {
		return 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, staging_key, variants FROM media_asset
		WHERE (state IN ('ready', 'failed') AND updated_at < $1) OR (state = 'released' AND released_at < $2)
		ORDER BY updated_at LIMIT 200`, now.Add(-OrphanAfter), now.Add(-RetainReleased))
	if err != nil {
		return 0, err
	}
	type victim struct {
		id      uuid.UUID
		staging *string
		vs      []variantRow
	}
	var list []victim
	for rows.Next() {
		var v victim
		var raw []byte
		if err := rows.Scan(&v.id, &v.staging, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		_ = json.Unmarshal(raw, &v.vs)
		list = append(list, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, v := range list {
		// Primeiro marca (condicional: se foi associada nesse meio-tempo, não apaga nada).
		tag, err := s.Pool.Exec(ctx, `UPDATE media_asset SET state = 'purged', purged_at = $2, updated_at = $2, staging_key = NULL,
			variants = '[]' WHERE id = $1 AND state IN ('ready', 'failed', 'released')`, v.id, now)
		if err != nil {
			return n, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if v.staging != nil {
			_ = s.Store.Delete(ctx, *v.staging)
		}
		s.deleteKeys(ctx, v.vs)
		n++
	}
	// Originais órfãos (o COMMIT do envio não aconteceu): sem registro vivo que os cite.
	orphans, err := s.Store.StagingOlderThan(ctx, now.Add(-OrphanAfter))
	if err != nil {
		return n, err
	}
	for _, key := range orphans {
		var used bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_asset WHERE staging_key = $1)`, key).Scan(&used); err != nil {
			return n, err
		}
		if !used {
			_ = s.Store.Delete(ctx, key)
			n++
		}
	}
	return n, nil
}
