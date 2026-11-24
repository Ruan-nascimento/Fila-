import { formatCents } from "../../money";
import { AmountRow, Badge } from "../../ui/ui";
import { cx } from "../../ui/cx";
import {
  closureText,
  itemLabel,
  itemStatusText,
  liveItems,
  orderStatusText,
  reportedText,
  unitsText,
  type Order,
} from "./textos";

const orderTone: Record<Order["status"], "neutral" | "primary" | "success" | "warning" | "danger"> = {
  open: "primary",
  pending_review: "warning",
  awaiting_payment: "warning",
  awaiting_confirmation: "primary",
  settled: "success",
  closed_no_charge: "neutral",
};

/** Situação da comanda como etiqueta (texto sempre presente). */
export function OrderBadge({ order }: { order: Order }) {
  return <Badge tone={orderTone[order.status]}>{orderStatusText[order.status]}</Badge>;
}

/**
 * Comanda item a item: o valor cobrado é a soma dos itens marcados como cobrados, pelo preço
 * aceito na inclusão (preço registrado, mesmo que o catálogo tenha mudado). Enquanto aberta,
 * mostra o total previsto.
 */
export function ItensDaComanda({
  order,
  showRemoved = false,
  title = true,
}: {
  order: Order;
  showRemoved?: boolean;
  title?: boolean;
}) {
  const frozen = Boolean(order.total_frozen_at);
  const items = showRemoved ? order.items : liveItems(order);
  return (
    <div className="grid gap-1">
      {title && (
        <p className="m-0 text-sm text-fg-muted">
          {`Comanda: ${orderStatusText[order.status]}`}
          {order.closure_reason && ` (${closureText[order.closure_reason] ?? order.closure_reason})`}
        </p>
      )}
      <div className="divide-y divide-line">
        {items.map((i) => {
          const qty = i.quantity ?? 1;
          const charged = i.charged_quantity ?? (i.charged ? 1 : 0);
          const details = [
            i.kind === "product" ? "produto" : "",
            qty > 1 || charged > 1 ? `${formatCents(i.unit_price.amount_cents)} cada` : "",
            // "aceito" é o estado comum: só o que muda (realizado, não realizado, retirado) aparece.
            qty > 1 && i.status !== "removed" && (i.performed_quantity ?? 0) > 0 && !frozen
              ? unitsText(i)
              : i.status === "accepted"
                ? ""
                : i.kind === "product" && i.status === "performed"
                  ? "entregue"
                  : itemStatusText[i.status],
            frozen && qty > 1 && i.status !== "removed" ? `${charged} de ${qty} cobrado(s)` : "",
            i.reported_outcome
              ? `informado: ${reportedText[i.reported_outcome]}${qty > 1 && i.reported_quantity != null ? ` (${i.reported_quantity} de ${qty} completos)` : ""}`
              : "",
            i.outcome_reason ? `motivo: ${i.outcome_reason}` : "",
          ]
            .filter(Boolean)
            .join(" · ");
          return (
            <AmountRow
              key={i.id}
              muted={i.status === "removed"}
              className={cx(i.status === "removed" && "line-through decoration-1")}
              label={itemLabel({ name: i.name, quantity: qty })}
              sub={details}
              value={
                frozen
                  ? i.charged
                    ? formatCents(i.subtotal?.amount_cents ?? i.unit_price.amount_cents * charged)
                    : `${formatCents(0)} (não cobrado)`
                  : formatCents(i.subtotal?.amount_cents ?? i.unit_price.amount_cents * qty)
              }
            />
          );
        })}
        {order.totals && (order.totals.service_units > 1 || order.totals.product_units > 0) && (
          <p className="m-0 py-2 text-sm text-fg-muted">
            {order.totals.service_units}{" "}
            {order.totals.service_units === 1 ? "unidade de serviço" : "unidades de serviço"}
            {order.totals.product_units > 0 &&
              ` · ${order.totals.product_units} ${order.totals.product_units === 1 ? "produto" : "produtos"}`}
            {order.totals.duration_minutes > 0 && ` · ~${order.totals.duration_minutes} min`}
            {" · uma posição na fila"}
          </p>
        )}
        <AmountRow
          strong
          label={frozen ? "Total cobrado" : "Total previsto"}
          value={formatCents(order.total.amount_cents)}
        />
      </div>
      {order.review_note && <p className="m-0 text-sm text-fg-muted">Revisão do dono: {order.review_note}</p>}
      {(order.adjustments ?? []).map((a) => (
        <p key={a.id} className="m-0 text-sm text-fg-muted">
          Correção: {a.reason} — de {formatCents(a.previous_total.amount_cents)} para{" "}
          {formatCents(a.new_total.amount_cents)}
        </p>
      ))}
    </div>
  );
}
