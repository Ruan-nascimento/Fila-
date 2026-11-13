import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";

export type Order = components["schemas"]["Order"];
export type OrderItem = Order["items"][number];
export type ChangeRequest = components["schemas"]["ChangeRequest"];

export const orderStatusText: Record<Order["status"], string> = {
  open: "Aberta",
  pending_review: "Interrompida — aguardando revisão do dono",
  awaiting_payment: "Aguardando pagamento",
  awaiting_confirmation: "Aguardando conferência",
  settled: "Paga",
  closed_no_charge: "Encerrada sem cobrança",
};

export const itemStatusText: Record<OrderItem["status"], string> = {
  accepted: "aceito",
  performed: "realizado",
  not_performed: "não realizado",
  removed: "retirado",
};

export const reportedText: Record<string, string> = {
  fully_performed: "feito por inteiro",
  partially_performed: "feito pela metade",
  not_started: "não iniciado",
};

export const closureText: Record<string, string> = {
  cancelled_by_customer: "você saiu da fila",
  cancelled_by_owner: "cancelada pela loja",
  cancelled_by_account_deletion: "conta excluída",
  no_show: "ausência",
  interruption_zero_total: "interrupção sem serviço completo",
  adjustment_zero_total: "correção do dono",
};

/** Itens não retirados (os retirados ficam só no histórico). */
export function liveItems(order: Order): OrderItem[] {
  return order.items.filter((i) => i.status !== "removed");
}

/** Texto de uma solicitação: o que incluir e o que retirar. */
export function describeRequest(order: Order, cr: ChangeRequest): string {
  const parts: string[] = [];
  if (cr.add.length > 0)
    parts.push(
      `incluir ${cr.add.map((a) => `${a.name} (${formatCents(a.unit_price.amount_cents)})`).join(", ")}`,
    );
  if (cr.remove_item_ids.length > 0) {
    const names = order.items.filter((i) => cr.remove_item_ids.includes(i.id)).map((i) => i.name);
    parts.push(`retirar ${names.join(", ")}`);
  }
  return parts.join(" e ");
}
