import type { components } from "../../api/schema.gen";

export type PixCharge = components["schemas"]["PixCharge"];
export type Receipt = components["schemas"]["Receipt"];
export type PaymentMethod = components["schemas"]["PaymentMethod"];

export const methodText: Record<PaymentMethod, string> = {
  pix: "Pix",
  cash: "Dinheiro",
  card: "Cartão (maquininha)",
};

export const chargeStatusText: Record<PixCharge["status"], string> = {
  creating: "gerando",
  pending: "aguardando pagamento",
  unknown: "verificando com o banco",
  paid: "pago",
  expired: "expirado",
  cancelled: "cancelado",
  failed: "não gerado",
};

/** Tentativa Pix ainda ativa (no máximo uma por comanda). */
export function activeCharge(charges: PixCharge[]): PixCharge | undefined {
  return charges.find((c) => c.status === "creating" || c.status === "pending" || c.status === "unknown");
}
