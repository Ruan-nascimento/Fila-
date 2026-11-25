package agenda

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// Leitura para os indicadores do dono (módulo analitico, ADR-0039/0027). Fica aqui porque jornada,
// exceções e o recorte dos turnos são da agenda: o painel não duplica a regra.

// WorkIntervals devolve, por profissional, os trechos de trabalho de cada dia em [from, to]:
// turnos vigentes NAQUELA data (as versões de jornada são imutáveis, então o relatório de ontem
// usa a jornada de ontem), limitados à abertura da loja, menos folgas, feriados e bloqueios.
// Três consultas no total, qualquer que seja o tamanho do período.
func WorkIntervals(ctx context.Context, q database.Querier, estID uuid.UUID, profIDs []uuid.UUID, from, to Day,
	loc *time.Location) (map[uuid.UUID][]Interval, error) {
	rows, err := q.Query(ctx, `
		SELECT id, professional_id, effective_from, inherits_store FROM agenda_schedule
		WHERE establishment_id = $1 AND effective_from <= $2 ORDER BY effective_from, created_at, id`, estID, to.Date())
	if err != nil {
		return nil, err
	}
	versions, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (weekly, error) {
		var w weekly
		err := r.Scan(&w.ID, &w.Professional, &w.EffectiveFrom, &w.Inherits)
		return w, err
	})
	if err != nil {
		return nil, err
	}
	if versions, err = withShifts(ctx, q, versions); err != nil {
		return nil, err
	}
	exc, err := exceptionsBetween(ctx, q, estID, from, to)
	if err != nil {
		return nil, err
	}
	bySubject := map[uuid.UUID][]weekly{}
	for _, w := range versions {
		key := uuid.Nil
		if w.Professional != nil {
			key = *w.Professional
		}
		bySubject[key] = append(bySubject[key], w)
	}
	for k := range bySubject {
		sort.SliceStable(bySubject[k], func(i, j int) bool { return bySubject[k][i].EffectiveFrom.Before(bySubject[k][j].EffectiveFrom) })
	}
	vigente := func(key uuid.UUID, d Day) (weekly, bool) {
		var cur weekly
		found := false
		for _, w := range bySubject[key] {
			if w.EffectiveFrom.After(d.Date()) {
				break
			}
			cur, found = w, true
		}
		return cur, found
	}
	out := map[uuid.UUID][]Interval{}
	for d := from; !to.Before(d); d = d.AddDays(1) {
		sched := map[uuid.UUID]weekly{}
		if w, ok := vigente(uuid.Nil, d); ok {
			sched[uuid.Nil] = w
		}
		for _, p := range profIDs {
			if w, ok := vigente(p, d); ok {
				sched[p] = w
			}
		}
		for _, p := range profIDs {
			var free []Interval
			for _, s := range professionalShifts(sched, p, d, loc) {
				free = append(free, s.Interval)
			}
			out[p] = append(out[p], subtract(free, closuresFor(exc, p, loc))...)
		}
	}
	return out, nil
}

// Clip soma a interseção de [a, b) com os trechos (em segundos).
func Clip(iv Interval, work []Interval) time.Duration {
	var total time.Duration
	for _, w := range work {
		s, e := maxTime(iv.Start, w.Start), minTime(iv.End, w.End)
		if e.After(s) {
			total += e.Sub(s)
		}
	}
	return total
}
