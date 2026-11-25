package agenda

import (
	"slices"
	"testing"
	"time"
)

var sp = mustLoc("America/Sao_Paulo")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func at(d Day, hhmm string, loc *time.Location) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", d.String()+" "+hhmm, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func hm(ts []time.Time, loc *time.Location) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.In(loc).Format("15:04")
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("obtido %v, esperado %v", got, want)
	}
}

// Quarta-feira 25/11/2026.
var qua = Day{2026, time.November, 25}

func shifts(d Day, defs ...ShiftDef) []Shift { return ShiftsOn(d, defs, sp) }

// Três cortes de 25 min + 1 produto (zero) + limpeza de 5 = 80 min numa grade de 15, com pausa no
// meio da manhã: nenhum início invade a pausa, a margem ou o fechamento.
// C-AG-12
func TestSlotsDuracaoMultiplicadaPausaEFechamento(t *testing.T) {
	sh := shifts(qua, ShiftDef{3, 9 * 60, 10*60 + 30, ModalityBoth}, ShiftDef{3, 11 * 60, 13 * 60, ModalityBoth})
	total := 3*25 + 0 + 5
	got := Slots(SlotInput{Day: qua, Loc: sp, Mode: "schedule", Shifts: sh, Step: 15, Total: total,
		Earliest: at(qua, "00:00", sp)})
	eq(t, hm(got, sp), []string{"09:00", "11:00", "11:15", "11:30"})
}

// Intervalo semiaberto: o compromisso que termina às 10h30 (margem incluída) libera as 10h30.
// C-AG-12
func TestSlotsSemiaberto(t *testing.T) {
	sh := shifts(qua, ShiftDef{3, 9 * 60, 12 * 60, ModalityBoth})
	busy := []Interval{{at(qua, "09:00", sp), at(qua, "10:30", sp)}}
	got := Slots(SlotInput{Day: qua, Loc: sp, Mode: "schedule", Shifts: sh, Busy: busy, Step: 30, Total: 30,
		Earliest: at(qua, "00:00", sp)})
	eq(t, hm(got, sp), []string{"10:30", "11:00", "11:30"})
}

// Antecedência mínima: "agora 9h10 + 30 min" → primeiro início na grade a partir de 9h40.
// C-AG-12
func TestSlotsAntecedenciaENuncaNoPassado(t *testing.T) {
	sh := shifts(qua, ShiftDef{3, 9 * 60, 11 * 60, ModalityBoth})
	got := Slots(SlotInput{Day: qua, Loc: sp, Mode: "schedule", Shifts: sh, Step: 15, Total: 30,
		Earliest: at(qua, "09:40", sp)})
	eq(t, hm(got, sp), []string{"09:45", "10:00", "10:15", "10:30"})
}

// Modo "os dois": trecho só de fila não é oferecido para reserva; trechos contíguos se fundem.
// C-AG-12
func TestSlotsModalidades(t *testing.T) {
	sh := shifts(qua,
		ShiftDef{3, 9 * 60, 10 * 60, ModalityQueue},
		ShiftDef{3, 10 * 60, 11 * 60, ModalitySchedule},
		ShiftDef{3, 11 * 60, 12 * 60, ModalityBoth})
	got := Slots(SlotInput{Day: qua, Loc: sp, Mode: "hybrid", Shifts: sh, Step: 30, Total: 60,
		Earliest: at(qua, "00:00", sp)})
	eq(t, hm(got, sp), []string{"10:00", "10:30", "11:00"})
	// No modo "com horário marcado" a modalidade é ignorada.
	got = Slots(SlotInput{Day: qua, Loc: sp, Mode: "schedule", Shifts: sh, Step: 60, Total: 60,
		Earliest: at(qua, "00:00", sp)})
	eq(t, hm(got, sp), []string{"09:00", "10:00", "11:00"})
}

// Turnos do profissional ficam dentro da abertura da loja.
// C-AG-12
func TestIntersectLojaProfissional(t *testing.T) {
	loja := shifts(qua, ShiftDef{3, 9 * 60, 18 * 60, ModalityBoth})
	prof := shifts(qua, ShiftDef{3, 8 * 60, 12 * 60, ModalityBoth}, ShiftDef{3, 13 * 60, 20 * 60, ModalitySchedule})
	got := Intersect(prof, loja)
	if len(got) != 2 || got[0].Start != at(qua, "09:00", sp) || got[1].End != at(qua, "18:00", sp) ||
		got[1].Modality != ModalitySchedule {
		t.Fatalf("interseção errada: %+v", got)
	}
}

// Projeção FIFO: a cadeira em uso e a fila admitida ocupam os trechos de fila antes da agenda
// compartilhada; uma visita espera inteira pelo próximo trecho livre (não invade o horário marcado).
// C-AG-12
func TestProjecaoFIFOAntesDaAgenda(t *testing.T) {
	sh := shifts(qua, ShiftDef{3, 14 * 60, 18 * 60, ModalityBoth})
	queueTime := eligible(sh, ForQueue("hybrid"))
	reserva := Interval{at(qua, "15:00", sp), at(qua, "15:30", sp)}
	now := at(qua, "14:20", sp)
	p := ProjectQueue(queueTime, []Interval{reserva}, now, QueueInput{
		ChairBusyUntil: at(qua, "14:30", sp), Waiting: []int{30, 25}})
	if len(p.Placed) != 2 || p.Placed[0].Start != at(qua, "14:30", sp) || p.Placed[1].Start != at(qua, "15:30", sp) {
		t.Fatalf("projeção errada: %+v", p.Placed)
	}
	// Agenda de hoje não oferece o que a fila já ocupa.
	busy := append([]Interval{reserva, {now, at(qua, "14:30", sp)}}, p.Placed...)
	got := Slots(SlotInput{Day: qua, Loc: sp, Mode: "hybrid", Shifts: sh, Busy: busy, Step: 15, Total: 30, Earliest: now})
	eq(t, hm(got, sp), []string{"16:00", "16:15", "16:30", "16:45", "17:00", "17:15", "17:30"})
	// Próxima entrada na fila: depois das duas.
	if iv, ok := p.NextQueueStart(now, 40); !ok || iv.Start != at(qua, "15:55", sp) {
		t.Fatalf("próximo início da fila errado: %+v %v", iv, ok)
	}
}

// Sem capacidade até o fechamento: a nova entrada (e as de trás) ficam sem encaixe.
// C-AG-12
func TestProjecaoSemCapacidade(t *testing.T) {
	sh := shifts(qua, ShiftDef{3, 17 * 60, 18 * 60, ModalityQueue})
	now := at(qua, "17:10", sp)
	p := ProjectQueue(eligible(sh, ForQueue("hybrid")), nil, now, QueueInput{Waiting: []int{30, 30}})
	if len(p.Placed) != 1 || p.Unplaced != 1 {
		t.Fatalf("esperava 1 encaixe e 1 sem vaga: %+v", p)
	}
	if _, ok := p.NextQueueStart(now, 10); ok {
		t.Fatal("não deveria haver vaga para nova entrada")
	}
}

// Fuso: datas e dia da semana seguem a loja, mesmo que quem consulta esteja em outro fuso.
// C-AG-12
func TestDiaDaLojaEmOutroFuso(t *testing.T) {
	// 01:30 UTC de quinta = 22:30 de quarta em São Paulo.
	t0 := time.Date(2026, time.November, 26, 1, 30, 0, 0, time.UTC)
	if d := DayOf(t0, sp); d != qua || d.Weekday() != 3 {
		t.Fatalf("dia da loja errado: %v", d)
	}
}

// Transição de horário de verão (política documentada no ADR-0038): hora inexistente não é
// oferecida; hora ambígua vale uma vez (a primeira ocorrência).
// C-AG-12
func TestTransicaoDeFuso(t *testing.T) {
	ny := mustLoc("America/New_York")
	primavera := Day{2026, time.March, 8} // 02:00 → 03:00
	sh := ShiftsOn(primavera, []ShiftDef{{0, 60, 4 * 60, ModalityBoth}}, ny)
	got := Slots(SlotInput{Day: primavera, Loc: ny, Mode: "schedule", Shifts: sh, Step: 30, Total: 30,
		Earliest: time.Time{}})
	eq(t, hm(got, ny), []string{"01:00", "01:30", "03:00", "03:30"})
	if _, ok := WallTime(primavera, 2*60+30, ny); ok {
		t.Fatal("02:30 não existe em 08/03/2026 em Nova York")
	}
	outono := Day{2026, time.November, 1} // 02:00 → 01:00
	sh = ShiftsOn(outono, []ShiftDef{{0, 60, 3 * 60, ModalityBoth}}, ny)
	got = Slots(SlotInput{Day: outono, Loc: ny, Mode: "schedule", Shifts: sh, Step: 30, Total: 30, Earliest: time.Time{}})
	eq(t, hm(got, ny), []string{"01:00", "01:30", "02:00", "02:30"})
	first, _ := WallTime(outono, 90, ny)
	if _, off := first.Zone(); off != -4*3600 {
		t.Fatalf("01:30 ambígua deve ser a primeira ocorrência (EDT): %v", first)
	}
}

// Meia-noite: turno até 24:00 termina no início do dia seguinte; virada de mês.
// C-AG-12
func TestMeiaNoiteEViradaDeMes(t *testing.T) {
	d := Day{2026, time.November, 30}
	sh := ShiftsOn(d, []ShiftDef{{1, 22 * 60, 1440, ModalityBoth}}, sp)
	if len(sh) != 1 || sh[0].End != time.Date(2026, time.December, 1, 0, 0, 0, 0, sp) {
		t.Fatalf("turno até a meia-noite errado: %+v", sh)
	}
	if d.AddDays(1) != (Day{2026, time.December, 1}) {
		t.Fatal("virada de mês errada")
	}
}
