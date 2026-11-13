import { useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { formatCents, parseCents } from "../../money";
import { Field, Notice, Submit } from "../../ui/ui";
import type { Order } from "../comanda/textos";
import { useCommand } from "../gestao/comando";
import { CommandError, type StaffContext } from "../gestao/comum";
import { Recebimentos } from "./pix";
import { activeCharge, chargeStatusText, methodText } from "./textos";

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

/**
 * Pagamento visto pela equipe (profissional do atendimento ou dono): situação, conferência do Pix
 * recebido e recebimento presencial (dinheiro com troco ou maquininha). O valor é o devido
 * calculado pelo servidor; a equipe só confirma.
 */
export function ReceberPagamento({
  ctx,
  order,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  order: Order;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const pay = order.payment;
  const cmd = useCommand();
  const path = { establishmentId: ctx.estId, orderId: order.id };
  const active = activeCharge(pay.charges);
  const toConfer = pay.receipts.filter((r) => r.status === "registered");

  async function confer(receiptId: string) {
    const out = await cmd.run(`conferir:${receiptId}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/receipts/{receiptId}/confer", {
        params: { path: { ...path, receiptId }, header: { "Idempotency-Key": key } },
        body: {},
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }

  async function switchFromPix(method: "cash" | "card") {
    const out = await cmd.run(`trocar:${order.id}:${order.version}:${method}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/payment-method", {
        params: { path, header: { "Idempotency-Key": key } },
        body: { expected_version: order.version, method, acknowledge_active_pix: true },
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }

  return (
    <section className="pay" aria-label="Pagamento">
      <p>
        Devido <strong>{formatCents(pay.amounts.due.amount_cents)}</strong> · recebido{" "}
        {formatCents(pay.amounts.received.amount_cents)} · em aberto{" "}
        <strong>{formatCents(pay.amounts.outstanding.amount_cents)}</strong>
        {pay.method_choice && <> · meio escolhido: {methodText[pay.method_choice]}</>}
      </p>
      {pay.charges.length > 0 && (
        <ul className="order__items" aria-label="Cobranças Pix">
          {pay.charges.map((c) => (
            <li key={c.id}>
              <span>
                Pix · {chargeStatusText[c.status]}
                {c.status === "pending" && c.expires_at && <> até {time.format(new Date(c.expires_at))}</>}
                {c.cancel_requested_at && " · cancelamento pedido (o Pix segue válido até expirar)"}
              </span>
              <span className="order__price">{formatCents(c.amount.amount_cents)}</span>
            </li>
          ))}
        </ul>
      )}
      <Recebimentos receipts={pay.receipts} />
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {toConfer.map((r) => (
        <button
          key={r.id}
          type="button"
          className="button"
          disabled={cmd.busy !== null}
          onClick={() => void confer(r.id)}
        >
          Conferir {r.classification === "excess" ? "valor a mais" : "recebimento"} Pix de{" "}
          {formatCents(r.amount.amount_cents)}
        </button>
      ))}
      {order.status === "awaiting_payment" && pay.verifying && (
        <p role="status">
          Verificando um Pix com o banco. Aguarde a verificação antes de receber por outro meio, para não
          cobrar duas vezes.
        </p>
      )}
      {order.status === "awaiting_payment" && active?.status === "pending" && !active.cancel_requested_at && (
        <div className="notice notice--info">
          <p>
            Há um Pix de {formatCents(active.amount.amount_cents)} em aberto. Para receber na loja, avise o
            cliente: o Pix continua válido até expirar e, se for pago também, o valor a mais fica para
            devolução.
          </p>
          <div className="actions">
            <button
              type="button"
              className="button button--secondary"
              disabled={cmd.busy !== null}
              onClick={() => void switchFromPix("cash")}
            >
              Trocar para dinheiro
            </button>
            <button
              type="button"
              className="button button--secondary"
              disabled={cmd.busy !== null}
              onClick={() => void switchFromPix("card")}
            >
              Trocar para cartão
            </button>
          </div>
        </div>
      )}
      {order.status === "awaiting_payment" &&
        !pay.verifying &&
        (!active || (active.status === "pending" && active.cancel_requested_at)) && (
          <ReceberNaLoja ctx={ctx} order={order} onSaved={onSaved} onConflict={onConflict} />
        )}
    </section>
  );
}

function ReceberNaLoja({
  ctx,
  order,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  order: Order;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const due = order.payment.amounts.due.amount_cents;
  const [method, setMethod] = useState<"cash" | "card">(
    order.payment.method_choice === "card" ? "card" : "cash",
  );
  const [tendered, setTendered] = useState("");
  const [reference, setReference] = useState("");
  const cmd = useCommand();
  const tenderedCents = tendered.trim() === "" ? due : parseCents(tendered);
  const tenderedError =
    method === "cash" && tenderedCents === null
      ? "Use um valor como 50 ou 50,00."
      : method === "cash" && tenderedCents !== null && tenderedCents < due
        ? `O valor entregue precisa ser pelo menos ${formatCents(due)}.`
        : undefined;
  const referenceError =
    method === "card" && reference !== "" && !/^[A-Za-z0-9-]{1,40}$/.test(reference)
      ? "Só letras, números e hífen (nunca o número do cartão)."
      : undefined;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (tenderedError || referenceError || tenderedCents === null) return;
    const body =
      method === "cash"
        ? { expected_version: order.version, method, amount_cents: due, cash_tendered_cents: tenderedCents }
        : {
            expected_version: order.version,
            method,
            amount_cents: due,
            ...(reference ? { card_reference: reference } : {}),
          };
    const out = await cmd.run(`receber:${order.id}:${order.version}:${method}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/receipts", {
        params: {
          path: { establishmentId: ctx.estId, orderId: order.id },
          header: { "Idempotency-Key": key },
        },
        body,
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }

  return (
    <form onSubmit={onSubmit} noValidate aria-label="Receber na loja">
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <fieldset className="choices">
        <legend>Receber na loja</legend>
        {(["cash", "card"] as const).map((m) => (
          <label key={m} className="choice">
            <input
              type="radio"
              name={`receber-${order.id}`}
              checked={method === m}
              onChange={() => setMethod(m)}
            />
            {methodText[m]}
          </label>
        ))}
      </fieldset>
      {method === "cash" && (
        <>
          <Field
            label="Valor entregue pelo cliente (R$)"
            inputMode="decimal"
            placeholder={formatCents(due)}
            value={tendered}
            onChange={(e) => setTendered(e.target.value)}
            error={tenderedError}
          />
          {tenderedCents !== null && !tenderedError && (
            <p role="status">
              Troco: <strong>{formatCents(tenderedCents - due)}</strong>
            </p>
          )}
        </>
      )}
      {method === "card" && (
        <Field
          label="Referência da maquininha (opcional)"
          hint="Ex.: NSU ou código da transação. Nunca o número do cartão."
          maxLength={40}
          value={reference}
          onChange={(e) => setReference(e.target.value)}
          error={referenceError}
        />
      )}
      <Submit busy={cmd.busy !== null}>Registrar {formatCents(due)} recebido e conferido</Submit>
      {cmd.problem?.code === "pix_charge_active" && (
        <Notice kind="info">Há um Pix em aberto: troque o meio antes de receber na loja.</Notice>
      )}
    </form>
  );
}
