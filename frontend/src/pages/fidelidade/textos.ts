import type { components } from "../../api/schema.gen";

export type LoyaltySummary = components["schemas"]["LoyaltySummary"];
export type LedgerEntry = components["schemas"]["LedgerEntry"];
export type Reward = components["schemas"]["Reward"];
export type Reservation = components["schemas"]["RewardReservation"];
export type LoyaltyProgram = components["schemas"]["LoyaltyProgram"];

/** O que cada lançamento do extrato significa (o saldo é a soma deles). */
export const ledgerText: Record<LedgerEntry["kind"], string> = {
  credit: "Pontos ganhos no atendimento",
  credit_reversal: "Pontos estornados (pagamento devolvido)",
  hold: "Pontos reservados para um prêmio",
  hold_release: "Reserva encerrada: pontos liberados",
  consume: "Prêmio entregue",
  consume_reversal: "Entrega corrigida pela loja: pontos devolvidos",
  account_closure: "Conta excluída: saldo encerrado",
};

export const reservationStatusText: Record<Reservation["status"], string> = {
  active: "reservado",
  cancelled: "cancelado",
  expired: "vencido",
  delivered: "entregue",
  delivery_reverted: "entrega corrigida",
};

/** "+10" / "−5" para mostrar variações de pontos. */
export function signed(n: number): string {
  return n > 0 ? `+${n}` : n < 0 ? `−${Math.abs(n)}` : "0";
}

/** Disponibilidade de um prêmio para exibir. */
export function stockText(r: Reward): string {
  if (r.stock.kind === "unlimited") return "disponível";
  const n = r.stock.units_available ?? 0;
  return n === 0 ? "esgotado" : n === 1 ? "1 unidade" : `${n} unidades`;
}
