import type { components } from "../../api/schema.gen";

export type ServiceMode = components["schemas"]["ServiceMode"];
export type Appointment = components["schemas"]["Appointment"];
export type WeeklyShift = components["schemas"]["WeeklyShift"];
export type Modality = components["schemas"]["ShiftModality"];

/** Dias da semana (0 = domingo), como a loja vê. */
export const diasDaSemana = ["Domingo", "Segunda", "Terça", "Quarta", "Quinta", "Sexta", "Sábado"];
export const diasCurtos = ["dom", "seg", "ter", "qua", "qui", "sex", "sáb"];

/** 540 → "09:00"; 1440 → "24:00". */
export function minutosParaHora(m: number): string {
  const h = Math.floor(m / 60);
  return `${String(h).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
}

/** "09:00" → 540; "24:00" → 1440; inválido → null. */
export function horaParaMinutos(s: string): number | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec(s.trim());
  if (!m) return null;
  const h = Number(m[1]);
  const min = Number(m[2]);
  if (min > 59 || h > 24 || (h === 24 && min > 0)) return null;
  return h * 60 + min;
}

/** Data local (AAAA-MM-DD) de um instante num fuso IANA (o da loja, não o do aparelho). */
export function dataNoFuso(instante: Date, fuso: string): string {
  const p = new Intl.DateTimeFormat("en-CA", {
    timeZone: fuso,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  })
    .formatToParts(instante)
    .reduce<Record<string, string>>((acc, x) => ({ ...acc, [x.type]: x.value }), {});
  return `${p.year}-${p.month}-${p.day}`;
}

/** "HH:MM" de um instante no fuso da loja. */
export function horaNoFuso(instante: string | Date, fuso: string): string {
  return new Intl.DateTimeFormat("pt-BR", {
    timeZone: fuso,
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(typeof instante === "string" ? new Date(instante) : instante);
}

/** Soma dias a uma data AAAA-MM-DD (calendário, sem fuso). */
export function somarDias(data: string, n: number): string {
  const [y, m, d] = data.split("-").map(Number);
  const t = new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, (d ?? 1) + n));
  return t.toISOString().slice(0, 10);
}

/** Dia da semana (0 = domingo) de uma data AAAA-MM-DD. */
export function diaDaSemana(data: string): number {
  const [y, m, d] = data.split("-").map(Number);
  return new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1)).getUTCDay();
}

/** "qui, 12/11" a partir de AAAA-MM-DD. */
export function rotuloDia(data: string, hoje?: string): string {
  if (hoje && data === hoje) return "Hoje";
  if (hoje && data === somarDias(hoje, 1)) return "Amanhã";
  const [, m, d] = data.split("-");
  return `${diasCurtos[diaDaSemana(data)]}, ${d}/${m}`;
}

/** "quinta-feira, 12 de novembro" (leitura completa, para revisão e leitores de tela). */
export function dataPorExtenso(data: string): string {
  const [y, m, d] = data.split("-").map(Number);
  return new Intl.DateTimeFormat("pt-BR", {
    timeZone: "UTC",
    weekday: "long",
    day: "numeric",
    month: "long",
  }).format(new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1)));
}

export const modoTexto: Record<ServiceMode, string> = {
  queue: "Por ordem de chegada",
  schedule: "Com horário marcado",
  hybrid: "Os dois",
};

export const modalidadeTexto: Record<Modality, string> = {
  both: "Fila e horário marcado",
  queue: "Só fila",
  schedule: "Só horário marcado",
};

/** Situação da reserva em texto (nunca só cor). */
export function situacaoReserva(a: Appointment): {
  texto: string;
  tom: "neutral" | "primary" | "success" | "warning" | "danger";
} {
  switch (a.status) {
    case "held":
      return { texto: "Aguardando pagamento", tom: "warning" };
    case "expired":
      return { texto: "Não paga no prazo", tom: "neutral" };
    case "confirmed":
      if (a.store_late) return { texto: "Horário passou · a loja não chamou", tom: "warning" };
      if (a.arrived_at) return { texto: "Confirmada · cliente chegou", tom: "success" };
      if (a.arrival_signal_at) return { texto: "Confirmada · cliente avisou que chegou", tom: "primary" };
      return { texto: "Confirmada", tom: "primary" };
    case "called":
      return { texto: a.arrived_at ? "Chamado · presente" : "Chamado · aguardando chegar", tom: "warning" };
    case "in_service":
      return { texto: "Em atendimento", tom: "success" };
    case "completed":
      return { texto: "Atendido", tom: "neutral" };
    case "interrupted":
      return { texto: "Interrompido", tom: "neutral" };
    case "no_show":
      return { texto: "Não compareceu", tom: "danger" };
    case "cancelled":
      return {
        texto: a.cancelled_by === "store" ? "Cancelada pela barbearia" : "Cancelada pelo cliente",
        tom: "neutral",
      };
  }
}

/** "3× Corte, 1× Pomada". */
export function itensTexto(a: Appointment): string {
  return a.items.map((i) => (i.quantity > 1 ? `${i.quantity}× ${i.name}` : i.name)).join(", ");
}

type BookingPayment = components["schemas"]["BookingPayment"];

/** Situação da devolução do valor pago (solicitada não é devolvida). */
export function devolucaoTexto(b: BookingPayment): string | null {
  const r = b.refund;
  if (!r) return null;
  const valor = (r.amount.amount_cents / 100).toLocaleString("pt-BR", { style: "currency", currency: "BRL" });
  switch (r.status) {
    case "requested":
    case "processing":
    case "unknown":
      return `Devolução de ${valor} solicitada. Pelo Pix pode levar alguns dias.`;
    case "under_review":
      return `Devolução de ${valor} em análise pela barbearia.`;
    case "confirmed":
      return `Devolução de ${valor} confirmada.`;
    case "failed":
      return `A devolução de ${valor} não foi concluída. Fale com a barbearia.`;
  }
}
