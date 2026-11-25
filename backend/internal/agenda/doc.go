// Package agenda implementa a agenda da Fila 2.0 (ADR-0038): jornadas, exceções, reservas
// gratuitas, disponibilidade e as regras de cadeira que a fila respeita no modo "os dois".
//
// Tabelas próprias: agenda_schedule, agenda_shift, agenda_exception, appointment,
// appointment_item, agenda_occupancy e appointment_event. A fila, a comanda, a loja e o catálogo
// são usados só pelas funções exportadas dos respectivos módulos.
//
// Ordem de travas (todos os caminhos): identidade (advisory "fila-entrada|loja|identidade") →
// fila(s) dos profissionais, por id → participação → reserva → ocupação. A restrição de exclusão
// GiST em agenda_occupancy é a última barreira contra sobreposição.
package agenda
