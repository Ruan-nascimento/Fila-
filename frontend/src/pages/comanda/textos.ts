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

/** "3 × Corte" (quantidade 1 fica só o nome). */
export function itemLabel(i: { name: string; quantity?: number }): string {
  return (i.quantity ?? 1) > 1 ? `${i.quantity} × ${i.name}` : i.name;
}

/** Unidades realizadas (serviço) ou entregues (produto): "2 de 3 realizados". */
export function unitsText(i: OrderItem): string {
  const verb = i.kind === "product" ? "entregue" : "realizado";
  if (i.quantity === 1) return i.performed_quantity === 1 ? verb : `não ${verb}`;
  return `${i.performed_quantity} de ${i.quantity} ${verb}s`;
}

/** Texto de uma solicitação: o que incluir, retirar e mudar de quantidade. */
export function describeRequest(order: Order, cr: ChangeRequest): string {
  const parts: string[] = [];
  if (cr.add.length > 0)
    parts.push(
      `incluir ${cr.add
        .map((a) => `${itemLabel(a)} (${formatCents(a.unit_price.amount_cents * (a.quantity ?? 1))})`)
        .join(", ")}`,
    );
  if (cr.remove_item_ids.length > 0) {
    const names = order.items.filter((i) => cr.remove_item_ids.includes(i.id)).map((i) => i.name);
    parts.push(`retirar ${names.join(", ")}`);
  }
  for (const q of cr.set_quantities ?? []) {
    const it = order.items.find((i) => i.id === q.item_id);
    if (it) parts.push(`${it.name}: de ${it.quantity} para ${q.quantity}`);
  }
  return parts.join(" e ");
}
