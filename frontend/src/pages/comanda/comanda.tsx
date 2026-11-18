import { formatCents } from "../../money";
import { AmountRow, Badge } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { closureText, itemStatusText, liveItems, orderStatusText, reportedText, type Order } from "./textos";

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
          const details = [
            // "aceito" é o estado comum: só o que muda (realizado, não realizado, retirado) aparece.
            i.status === "accepted" ? "" : itemStatusText[i.status],
            i.reported_outcome ? `informado: ${reportedText[i.reported_outcome]}` : "",
            i.outcome_reason ? `motivo: ${i.outcome_reason}` : "",
          ]
            .filter(Boolean)
            .join(" · ");
          return (
            <AmountRow
              key={i.id}
              muted={i.status === "removed"}
              className={cx(i.status === "removed" && "line-through decoration-1")}
              label={i.name}
              sub={details}
              value={
                frozen
                  ? i.charged
                    ? formatCents(i.unit_price.amount_cents)
                    : `${formatCents(0)} (não cobrado)`
                  : formatCents(i.unit_price.amount_cents)
              }
            />
          );
        })}
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
