import { formatCents } from "../../money";
import { closureText, itemStatusText, liveItems, orderStatusText, reportedText, type Order } from "./textos";

/**
 * Comanda item a item: o valor cobrado é a soma dos itens marcados como cobrados, pelo preço
 * aceito na inclusão. Enquanto aberta, mostra o total previsto.
 */
export function ItensDaComanda({ order, showRemoved = false }: { order: Order; showRemoved?: boolean }) {
  const frozen = Boolean(order.total_frozen_at);
  const items = showRemoved ? order.items : liveItems(order);
  return (
    <div className="order">
      <p className="badge">
        Comanda: {orderStatusText[order.status]}
        {order.closure_reason && <> ({closureText[order.closure_reason] ?? order.closure_reason})</>}
      </p>
      <ul className="order__items">
        {items.map((i) => (
          <li key={i.id} className={i.status === "removed" ? "muted" : undefined}>
            <span>
              {i.name} · {itemStatusText[i.status]}
              {i.reported_outcome && <> · informado: {reportedText[i.reported_outcome]}</>}
              {i.outcome_reason && <> · motivo: {i.outcome_reason}</>}
            </span>
            <span className="order__price">
              {frozen
                ? i.charged
                  ? formatCents(i.unit_price.amount_cents)
                  : `${formatCents(0)} (não cobrado)`
                : formatCents(i.unit_price.amount_cents)}
            </span>
          </li>
        ))}
      </ul>
      <p className="order__total">
        <span>{frozen ? "Total cobrado" : "Total previsto"}</span>
        <strong>{formatCents(order.total.amount_cents)}</strong>
      </p>
      {order.review_note && <p className="muted">Revisão do dono: {order.review_note}</p>}
      {(order.adjustments ?? []).map((a) => (
        <p key={a.id} className="muted">
          Correção: {a.reason} — de {formatCents(a.previous_total.amount_cents)} para{" "}
          {formatCents(a.new_total.amount_cents)}
        </p>
      ))}
    </div>
  );
}
