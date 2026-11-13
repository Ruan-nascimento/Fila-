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

export type Refund = components["schemas"]["Refund"];
export type FinancialIssue = components["schemas"]["FinancialIssue"];
export type IssueKind = components["schemas"]["FinancialIssueKind"];

export const refundStatusText: Record<Refund["status"], string> = {
  requested: "estorno pedido",
  processing: "estorno em processamento",
  unknown: "estorno: verificando com o banco",
  under_review: "estorno em revisão do dono",
  confirmed: "estornado",
  failed: "estorno não realizado",
};

/** O que cada pendência significa e o que fazer (painel do dono e aviso na comanda). */
export const issueText: Record<IssueKind, string> = {
  duplicate_receipt: "Pagamento em duplicidade: o valor a mais precisa ser devolvido.",
  late_receipt: "Pix pago depois de trocar o meio ou de expirar: devolva o valor a mais.",
  amount_mismatch: "Valor recebido diferente do cobrado: confira e trate a diferença.",
  charge_outcome_unresolved:
    "Pix com resultado desconhecido há muito tempo: confira no painel da AbacatePay.",
  refund_under_review: "Estorno em revisão: confirme com evidência ou registre que não ocorreu.",
  active_pix_on_method_switch: "Troca de meio com Pix em aberto: o Pix segue válido até expirar.",
  adjustment_excess: "Correção deixou valor recebido a mais: devolva o excesso.",
};

/** Estorno que ainda conta como ativo ou feito (não falhou). */
export function liveRefund(refunds: Refund[], receiptId: string): Refund | undefined {
  return refunds.find((r) => r.receipt_id === receiptId && r.status !== "failed");
}
