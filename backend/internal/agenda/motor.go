package agenda

import (
	"slices"
	"time"
)

// Motor de disponibilidade (ADR-0038). Funções puras: recebem tudo o que foi lido do banco em
// consultas por conjunto e calculam em memória. Intervalos são SEMIABERTOS [Start, End): se um
// compromisso termina às 10h30 (margem incluída), o próximo pode começar às 10h30.

// Interval é um intervalo [Start, End) em instantes absolutos.
type Interval struct {
	Start, End time.Time
}

func (a Interval) overlaps(b Interval) bool { return a.Start.Before(b.End) && b.Start.Before(a.End) }

// Modalidade de um turno no modo "os dois".
const (
	ModalityQueue    = "queue"
	ModalitySchedule = "schedule"
	ModalityBoth     = "both"
)

// ShiftDef é um turno semanal em minutos locais [StartMinute, EndMinute) (0..1440).
type ShiftDef struct {
	Weekday     int    `json:"weekday"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
	Modality    string `json:"modality"`
}

// Shift é um turno de um dia concreto, em instantes absolutos.
type Shift struct {
	Interval
	Modality string
}

// Day é uma data local (sem fuso).
type Day struct {
	Year  int
	Month time.Month
	Day   int
}

// DayOf devolve a data local de t no fuso loc.
func DayOf(t time.Time, loc *time.Location) Day {
	l := t.In(loc)
	return Day{l.Year(), l.Month(), l.Day()}
}

// ParseDay lê "AAAA-MM-DD".
func ParseDay(s string) (Day, bool) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Day{}, false
	}
	return Day{t.Year(), t.Month(), t.Day()}, true
}

func (d Day) String() string { return d.Date().Format(time.DateOnly) }

// Date devolve a data como time.Time em UTC à meia-noite (para colunas date).
func (d Day) Date() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

// Weekday da data (0 = domingo), independente do fuso de quem consulta.
func (d Day) Weekday() int { return int(d.Date().Weekday()) }

// AddDays soma dias de calendário.
func (d Day) AddDays(n int) Day {
	t := d.Date().AddDate(0, 0, n)
	return Day{t.Year(), t.Month(), t.Day()}
}

// Before compara datas.
func (d Day) Before(o Day) bool { return d.Date().Before(o.Date()) }

// WallTime converte "minuto local do dia" em instante absoluto, com a política de transição de
// fuso (ADR-0038):
//   - hora INEXISTENTE (o relógio pula, ex.: 02:00→03:00): ok = false (o horário não é oferecido);
//   - hora AMBÍGUA (o relógio volta): vale a PRIMEIRA ocorrência (o instante mais cedo).
//
// minute = 1440 é a meia-noite do dia seguinte (fim de turno).
func WallTime(d Day, minute int, loc *time.Location) (time.Time, bool) {
	t := time.Date(d.Year, d.Month, d.Day, 0, minute, 0, 0, loc)
	if minute < 1440 {
		l := t.In(loc)
		if l.Day() != d.Day || l.Hour()*60+l.Minute() != minute {
			return t, false
		}
	}
	// Ambígua: uma hora antes mostra o mesmo relógio → fica com a ocorrência anterior.
	if e := t.Add(-time.Hour); e.In(loc).Hour() == t.In(loc).Hour() && e.In(loc).Minute() == t.In(loc).Minute() &&
		e.In(loc).Day() == t.In(loc).Day() {
		t = e
	}
	return t, true
}

// ShiftsOn monta os turnos do dia a partir das definições semanais (só as do dia da semana).
// Bordas inexistentes (transição de fuso) são empurradas para o primeiro instante válido.
func ShiftsOn(d Day, defs []ShiftDef, loc *time.Location) []Shift {
	var out []Shift
	for _, s := range defs {
		if s.Weekday != d.Weekday() {
			continue
		}
		start := edge(d, s.StartMinute, loc)
		end := edge(d, s.EndMinute, loc)
		if start.Before(end) {
			out = append(out, Shift{Interval{start, end}, s.Modality})
		}
	}
	slices.SortFunc(out, func(a, b Shift) int { return a.Start.Compare(b.Start) })
	return out
}

func edge(d Day, minute int, loc *time.Location) time.Time {
	t, ok := WallTime(d, minute, loc)
	for !ok && minute < 1440 { // borda num horário inexistente: próximo minuto válido
		minute++
		t, ok = WallTime(d, minute, loc)
	}
	return t
}

// Intersect devolve os turnos do profissional limitados à abertura da loja (loja × profissional).
// A modalidade é a do profissional, salvo quando a loja restringe o trecho.
func Intersect(prof, store []Shift) []Shift {
	var out []Shift
	for _, p := range prof {
		for _, s := range store {
			st, en := maxTime(p.Start, s.Start), minTime(p.End, s.End)
			if !st.Before(en) {
				continue
			}
			mod := p.Modality
			if s.Modality != ModalityBoth && p.Modality == ModalityBoth {
				mod = s.Modality
			} else if s.Modality != ModalityBoth && s.Modality != p.Modality {
				continue // loja só fila e profissional só agenda (ou o contrário): nada em comum
			}
			out = append(out, Shift{Interval{st, en}, mod})
		}
	}
	slices.SortFunc(out, func(a, b Shift) int { return a.Start.Compare(b.Start) })
	return out
}

// eligible filtra e funde (trechos contíguos viram um só) os turnos aceitos pela modalidade.
func eligible(shifts []Shift, ok func(string) bool) []Interval {
	var out []Interval
	for _, s := range shifts {
		if !ok(s.Modality) {
			continue
		}
		if n := len(out); n > 0 && !out[n-1].End.Before(s.Start) {
			out[n-1].End = maxTime(out[n-1].End, s.End)
			continue
		}
		out = append(out, s.Interval)
	}
	return out
}

// ForSchedule / ForQueue: modalidades que aceitam reserva / fila conforme a forma de atendimento.
func ForSchedule(mode string) func(string) bool {
	return func(m string) bool { return mode == "schedule" || m == ModalitySchedule || m == ModalityBoth }
}

func ForQueue(mode string) func(string) bool {
	return func(m string) bool { return mode != "hybrid" || m == ModalityQueue || m == ModalityBoth }
}

// subtract remove os ocupados dos trechos livres.
func subtract(free []Interval, busy []Interval) []Interval {
	out := free
	for _, b := range busy {
		var next []Interval
		for _, f := range out {
			if !f.overlaps(b) {
				next = append(next, f)
				continue
			}
			if f.Start.Before(b.Start) {
				next = append(next, Interval{f.Start, b.Start})
			}
			if b.End.Before(f.End) {
				next = append(next, Interval{b.End, f.End})
			}
		}
		out = next
	}
	return out
}

// QueueInput descreve a cadeira e a fila do profissional para a projeção de hoje.
type QueueInput struct {
	// ChairBusyUntil: fim previsto de quem está chamado ou em atendimento (zero = cadeira livre).
	ChairBusyUntil time.Time
	// Waiting: minutos de cada visita aguardando (serviços × quantidade + margem), em ordem FIFO.
	Waiting []int
}

// Projection é o encaixe FIFO das visitas da fila nos trechos de fila do dia.
type Projection struct {
	Placed   []Interval // um por visita aguardando que cabe no expediente, na ordem
	Unplaced int        // visitas que não cabem até o fim dos trechos de fila
	// Free são os trechos de fila ainda livres depois do encaixe (para a próxima entrada).
	Free []Interval
}

// ProjectQueue encaixa a fila (FIFO) nos trechos de fila livres a partir de from. Uma visita
// nunca é dividida: espera o próximo trecho livre em que caiba inteira (ex.: antes de um horário
// marcado). Se uma não cabe, as de trás também não (a ordem de chegada se mantém).
func ProjectQueue(queueTime []Interval, busy []Interval, from time.Time, in QueueInput) Projection {
	cursor := from
	if in.ChairBusyUntil.After(cursor) {
		cursor = in.ChairBusyUntil
	}
	free := subtract(queueTime, busy)
	var p Projection
	for i, mins := range in.Waiting {
		iv, ok := place(free, cursor, time.Duration(mins)*time.Minute)
		if !ok {
			p.Unplaced = len(in.Waiting) - i
			break
		}
		p.Placed = append(p.Placed, iv)
		cursor = iv.End
	}
	p.Free = subtract(free, append(slices.Clone(p.Placed), Interval{time.Time{}, cursor}))
	return p
}

// place acha o primeiro início ≥ cursor em que dur cabe inteiro num trecho livre.
func place(free []Interval, cursor time.Time, dur time.Duration) (Interval, bool) {
	for _, f := range free {
		start := maxTime(f.Start, cursor)
		if end := start.Add(dur); !end.After(f.End) {
			return Interval{start, end}, true
		}
	}
	return Interval{}, false
}

// NextQueueStart diz onde caberia uma nova visita de dur minutos depois da fila atual (estimativa).
func (p Projection) NextQueueStart(from time.Time, dur int) (Interval, bool) {
	cursor := from
	if n := len(p.Placed); n > 0 {
		cursor = p.Placed[n-1].End
	}
	if p.Unplaced > 0 {
		return Interval{}, false
	}
	return place(p.Free, cursor, time.Duration(dur)*time.Minute)
}

// SlotInput reúne o necessário para listar os inícios oferecidos de um profissional num dia.
type SlotInput struct {
	Day      Day
	Loc      *time.Location
	Mode     string
	Shifts   []Shift    // loja × profissional, do dia
	Busy     []Interval // compromissos, bloqueios, folgas e, hoje, fila projetada e cadeira em uso
	Step     int        // minutos entre inícios sugeridos (ancorados no início do trecho)
	Total    int        // duração dos serviços + margem, em minutos
	Earliest time.Time  // agora + antecedência mínima
}

// Slots devolve os inícios oferecidos: o intervalo inteiro [início, início + Total) cabe num mesmo
// trecho livre de agenda. A grade é contada no relógio da loja a partir do início do trecho;
// horários inexistentes na transição de fuso não são oferecidos.
func Slots(in SlotInput) []time.Time {
	if in.Step <= 0 || in.Total <= 0 {
		return nil
	}
	free := subtract(eligible(in.Shifts, ForSchedule(in.Mode)), in.Busy)
	total := time.Duration(in.Total) * time.Minute
	var out []time.Time
	for _, seg := range eligible(in.Shifts, ForSchedule(in.Mode)) {
		l := seg.Start.In(in.Loc)
		base := l.Hour()*60 + l.Minute()
		for k := 0; ; k++ {
			wall := base + k*in.Step
			if wall >= 1440 {
				break
			}
			t, ok := WallTime(in.Day, wall, in.Loc)
			if !ok {
				continue
			}
			if !t.Before(seg.End) {
				break
			}
			if t.Before(in.Earliest) {
				continue
			}
			iv := Interval{t, t.Add(total)}
			if inside(free, iv) {
				out = append(out, t)
			}
		}
	}
	return out
}

func inside(free []Interval, iv Interval) bool {
	for _, f := range free {
		if !iv.Start.Before(f.Start) && !iv.End.After(f.End) {
			return true
		}
	}
	return false
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
