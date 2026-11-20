package analitico

import (
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	hs "fila/internal/platform/httpserver"
)

// Período, fuso e filtros de uma consulta analítica.
//
// O dono escolhe datas LOCAIS da loja (from e to, inclusivos). O intervalo consultado é
// [from 00:00, to+1 00:00) no fuso da loja, convertido para instantes antes de ir ao banco. Se o
// período ainda não terminou, o fim efetivo é "agora" (os buckets futuros não existem).
//
// Comparação "período anterior equivalente":
//   - mês civil inteiro (dia 1 ao último dia) → mês anterior;
//   - outro intervalo de N dias → os N dias imediatamente anteriores;
//   - nos dois casos, com o MESMO tempo transcorrido: hoje até 14h compara com ontem até 14h.

const (
	maxDays = 366
	dateFmt = "2006-01-02"
)

// Granularidades das séries.
const (
	GranHour  = "hour"
	GranDay   = "day"
	GranWeek  = "week"
	GranMonth = "month"
)

// Período resolvido.
type Period struct {
	From, To     string // datas locais pedidas (inclusivas)
	Start, End   time.Time
	EffectiveEnd time.Time // min(End, agora)
	Loc          *time.Location
}

// Elapsed é o tempo efetivamente decorrido do período (0 se começa no futuro).
func (p Period) Elapsed() time.Duration {
	if !p.EffectiveEnd.After(p.Start) {
		return 0
	}
	return p.EffectiveEnd.Sub(p.Start)
}

// Filters são os recortes aceitos. Cada métrica diz quais aplica (ver dicionário).
type Filters struct {
	ProfessionalID *uuid.UUID
	ServiceID      *uuid.UUID
	Method         string // pix | cash | card
}

// Query é a consulta já validada.
type Query struct {
	Period      Period
	Compare     *Period
	Granularity string
	Filters     Filters
}

func fieldErr(field, code, msg string) *hs.Error {
	return hs.ErrValidation(hs.FieldError{Field: field, Code: code, Message: msg})
}

// parseQuery valida período, comparação, granularidade e filtros (negação por padrão: parâmetro
// desconhecido é erro, para o dono nunca achar que filtrou o que não foi filtrado).
func parseQuery(v url.Values, loc *time.Location, now time.Time, allowed map[string]bool) (Query, error) {
	var q Query
	for k := range v {
		if !allowed[k] {
			return q, fieldErr("/"+k, hs.FieldUnknown, "parâmetro não aceito nesta consulta")
		}
	}
	from, err1 := time.ParseInLocation(dateFmt, v.Get("from"), loc)
	to, err2 := time.ParseInLocation(dateFmt, v.Get("to"), loc)
	if err1 != nil {
		return q, fieldErr("/from", hs.FieldInvalidFormat, "use AAAA-MM-DD")
	}
	if err2 != nil {
		return q, fieldErr("/to", hs.FieldInvalidFormat, "use AAAA-MM-DD")
	}
	if to.Before(from) {
		return q, fieldErr("/to", hs.FieldOutOfRange, "o fim vem antes do início")
	}
	days := int(to.Sub(from).Hours()/24+0.5) + 1
	if days > maxDays {
		return q, fieldErr("/to", hs.FieldOutOfRange, "no máximo 366 dias")
	}
	q.Period = makePeriod(from, to, loc, now)

	switch c := v.Get("compare"); c {
	case "":
	case "previous":
		prev := previousOf(q.Period, loc)
		q.Compare = &prev
	default:
		return q, fieldErr("/compare", hs.FieldNotAllowed, "use previous")
	}

	switch g := v.Get("granularity"); g {
	case "":
		q.Granularity = autoGranularity(days)
	case GranHour:
		if days > 7 {
			return q, fieldErr("/granularity", hs.FieldOutOfRange, "por hora, no máximo 7 dias")
		}
		q.Granularity = g
	case GranDay:
		if days > 92 {
			return q, fieldErr("/granularity", hs.FieldOutOfRange, "por dia, no máximo 92 dias")
		}
		q.Granularity = g
	case GranWeek, GranMonth:
		q.Granularity = g
	default:
		return q, fieldErr("/granularity", hs.FieldNotAllowed, "use hour, day, week ou month")
	}

	if s := v.Get("professional_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			return q, fieldErr("/professional_id", hs.FieldInvalidFormat, "")
		}
		q.Filters.ProfessionalID = &id
	}
	if s := v.Get("service_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			return q, fieldErr("/service_id", hs.FieldInvalidFormat, "")
		}
		q.Filters.ServiceID = &id
	}
	switch m := v.Get("method"); m {
	case "", "pix", "cash", "card":
		q.Filters.Method = m
	default:
		return q, fieldErr("/method", hs.FieldNotAllowed, "use pix, cash ou card")
	}
	return q, nil
}

func makePeriod(from, to time.Time, loc *time.Location, now time.Time) Period {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.Year(), to.Month(), to.Day()+1, 0, 0, 0, 0, loc)
	// Período em andamento: até o instante da leitura, inclusive (+1 µs, a precisão do banco).
	eff := end
	if inclusive := now.Add(time.Microsecond); inclusive.Before(end) {
		eff = inclusive
	}
	if eff.Before(start) {
		eff = start
	}
	return Period{From: start.Format(dateFmt), To: to.Format(dateFmt), Start: start, End: end, EffectiveEnd: eff, Loc: loc}
}

// previousOf calcula o período anterior equivalente, com o mesmo tempo transcorrido.
func previousOf(p Period, loc *time.Location) Period {
	start := p.Start
	lastDay := p.End.AddDate(0, 0, -1)
	var ps, pe time.Time
	wholeMonth := start.Day() == 1 && lastDay.Month() == start.Month() && lastDay.AddDate(0, 0, 1).Day() == 1
	if wholeMonth {
		ps = start.AddDate(0, -1, 0)
		pe = start
	} else {
		days := int(p.End.Sub(p.Start).Hours()/24 + 0.5)
		ps = start.AddDate(0, 0, -days)
		pe = start
	}
	eff := pe
	if p.EffectiveEnd.Before(p.End) {
		eff = ps.Add(p.Elapsed())
		if eff.After(pe) {
			eff = pe
		}
	}
	return Period{From: ps.Format(dateFmt), To: pe.AddDate(0, 0, -1).In(loc).Format(dateFmt), Start: ps, End: pe, EffectiveEnd: eff, Loc: loc}
}

func autoGranularity(days int) string {
	switch {
	case days <= 2:
		return GranHour
	case days <= 62:
		return GranDay
	default:
		return GranWeek
	}
}

// bucketStarts lista os inícios (hora local) dos buckets do período, iguais aos de
// date_trunc(granularidade, instante AT TIME ZONE fuso) no PostgreSQL (semana começa na segunda).
func bucketStarts(p Period, gran string) []time.Time {
	t := truncLocal(p.Start, gran)
	var out []time.Time
	for t.Before(p.End) {
		out = append(out, t)
		t = nextBucket(t, gran)
	}
	return out
}

func truncLocal(t time.Time, gran string) time.Time {
	t = t.In(t.Location())
	switch gran {
	case GranHour:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	case GranWeek:
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		wd := (int(d.Weekday()) + 6) % 7 // segunda = 0
		return d.AddDate(0, 0, -wd)
	case GranMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
}

func nextBucket(t time.Time, gran string) time.Time {
	switch gran {
	case GranHour:
		return t.Add(time.Hour)
	case GranWeek:
		return t.AddDate(0, 0, 7)
	case GranMonth:
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

// localKey identifica um bucket pelo relógio local (igual ao timestamp sem fuso do banco).
func localKey(t time.Time) string { return t.Format("2006-01-02T15") }

// allowedParams monta o conjunto de parâmetros aceitos.
func allowedParams(extra ...string) map[string]bool {
	m := map[string]bool{"from": true, "to": true, "compare": true, "professional_id": true, "service_id": true, "method": true}
	for _, e := range extra {
		m[strings.TrimSpace(e)] = true
	}
	return m
}
