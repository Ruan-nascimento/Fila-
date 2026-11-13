import { useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { formatCents } from "../../money";
import { Field, Submit } from "../../ui/ui";
import type { Order } from "../comanda/textos";
import { useCommand } from "../gestao/comando";
import { CommandError, type StaffContext } from "../gestao/comum";
import { liveRefund, methodText, refundStatusText, type Receipt, type Refund } from "./textos";

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/**
 * Estornos da comanda. Todos da equipe veem a situação; só o dono pede estorno (integral, com
 * motivo e senha confirmada) e resolve os que estão em revisão, com evidência. "Estornado" só
 * aparece quando o banco confirmou (Pix) ou o dono documentou (dinheiro/cartão).
 */
export function Estornos({
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
  const refundable = pay.receipts.filter(
    (r) => !liveRefund(pay.refunds, r.id) && (r.classification === "excess" || r.status === "conferred"),
  );
  if (pay.refunds.length === 0 && (!ctx.isOwner || refundable.length === 0)) return null;
  return (
    <div className="pay" aria-label="Estornos">
      {pay.refunds.length > 0 && (
        <ul className="order__items" aria-label="Estornos da comanda">
          {pay.refunds.map((rf) => (
            <li key={rf.id}>
              <span>
                {refundStatusText[rf.status]} · pedido em {when.format(new Date(rf.requested_at))} · motivo:{" "}
                {rf.reason}
                {rf.evidence === "owner_documented" && " · confirmado pelo dono com evidência"}
                {rf.evidence === "provider" && " · confirmado pelo banco"}
              </span>
              <span className="order__price">{formatCents(rf.amount.amount_cents)}</span>
            </li>
          ))}
        </ul>
      )}
      {ctx.isOwner &&
        pay.refunds
          .filter((rf) => rf.status === "under_review")
          .map((rf) => (
            <Revisao
              key={rf.id}
              ctx={ctx}
              order={order}
              refund={rf}
              onSaved={onSaved}
              onConflict={onConflict}
            />
          ))}
      {ctx.isOwner &&
        refundable.map((r) => (
          <PedirEstorno
            key={r.id}
            ctx={ctx}
            order={order}
            receipt={r}
            onSaved={onSaved}
            onConflict={onConflict}
          />
        ))}
    </div>
  );
}

function PedirEstorno({
  ctx,
  order,
  receipt,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  order: Order;
  receipt: Receipt;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const cmd = useCommand();
  const label = `${methodText[receipt.method]} de ${formatCents(receipt.amount.amount_cents)}${receipt.classification === "excess" ? " (valor a mais)" : ""}`;

  async function submit() {
    if (reason.trim().length < 3) return;
    const out = await cmd.run(`estornar:${receipt.id}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/receipts/{receiptId}/refunds", {
        params: {
          path: { establishmentId: ctx.estId, orderId: order.id, receiptId: receipt.id },
          header: { "Idempotency-Key": key },
        },
        body: { reason },
      }),
    );
    if (out.ok) {
      setOpen(false);
      onSaved(out.data.data);
    } else if (out.problem?.status === 409) await onConflict();
  }

  if (!open)
    return (
      <button type="button" className="button button--secondary" onClick={() => setOpen(true)}>
        Estornar {label}
      </button>
    );
  return (
    <form
      noValidate
      aria-label={`Estornar ${label}`}
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        void submit();
      }}
    >
      <p>
        O estorno é sempre do valor integral ({formatCents(receipt.amount.amount_cents)}).{" "}
        {receipt.method === "pix"
          ? "O banco faz a devolução; aqui só aparece como estornado depois da confirmação."
          : "Devolva ao cliente e depois confirme aqui com a evidência (recibo, comprovante)."}
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} onRetry={() => void submit()} />
      <Field
        label="Motivo do estorno"
        required
        minLength={3}
        maxLength={500}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="actions">
        <Submit busy={cmd.busy !== null}>Pedir estorno</Submit>
        <button type="button" className="button button--secondary" onClick={() => setOpen(false)}>
          Voltar
        </button>
      </div>
    </form>
  );
}

function Revisao({
  ctx,
  order,
  refund,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  order: Order;
  refund: Refund;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const [note, setNote] = useState("");
  const [reference, setReference] = useState("");
  const cmd = useCommand();
  const ready = note.trim().length >= 3;

  const [last, setLast] = useState<"confirm" | "mark-failed">("confirm");

  async function resolve(kind: "confirm" | "mark-failed") {
    if (!ready) return;
    setLast(kind);
    const body = { evidence_note: note, ...(reference ? { external_reference: reference } : {}) };
    const path = { establishmentId: ctx.estId, orderId: order.id, refundId: refund.id };
    // Mesma intenção = mesma chave (a confirmação de senha repete com a mesma chave).
    const out =
      kind === "confirm"
        ? await cmd.run(`confirmar-estorno:${refund.id}`, (key) =>
            api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/refunds/{refundId}/confirm", {
              params: { path, header: { "Idempotency-Key": key } },
              body,
            }),
          )
        : await cmd.run(`estorno-falhou:${refund.id}`, (key) =>
            api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/refunds/{refundId}/mark-failed", {
              params: { path, header: { "Idempotency-Key": key } },
              body,
            }),
          );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }

  return (
    <form noValidate aria-label="Resolver estorno em revisão" onSubmit={(e) => e.preventDefault()}>
      <p>
        <strong>Estorno de {formatCents(refund.amount.amount_cents)} em revisão.</strong> Confirme só com
        evidência (comprovante do banco, recibo assinado); se não houve devolução, registre que não ocorreu.
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} onRetry={() => void resolve(last)} />
      <Field
        label="Evidência (o que comprova)"
        required
        minLength={3}
        maxLength={500}
        value={note}
        onChange={(e) => setNote(e.target.value)}
      />
      <Field
        label="Referência externa (opcional)"
        maxLength={80}
        value={reference}
        onChange={(e) => setReference(e.target.value)}
      />
      <div className="actions">
        <button
          type="button"
          className="button"
          disabled={!ready || cmd.busy !== null}
          onClick={() => void resolve("confirm")}
        >
          Confirmar estorno
        </button>
        <button
          type="button"
          className="button button--secondary"
          disabled={!ready || cmd.busy !== null}
          onClick={() => void resolve("mark-failed")}
        >
          Estorno não ocorreu
        </button>
      </div>
    </form>
  );
}
