import { useState, type FormEvent } from "react";
import { Undo2 } from "lucide-react";
import { api } from "../../api/client";
import { formatCents } from "../../money";
import { AmountRow, Button, Field, Submit, TextArea } from "../../ui/ui";
import { useInlinePanel } from "../../ui/useInlinePanel";
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
    <div className="grid gap-3" aria-label="Estornos" role="group">
      {pay.refunds.length > 0 && (
        <div className="divide-y divide-line" role="list" aria-label="Estornos da comanda">
          {pay.refunds.map((rf) => (
            <div role="listitem" key={rf.id}>
              <AmountRow
                label="Estorno"
                sub={[
                  refundStatusText[rf.status],
                  `pedido em ${when.format(new Date(rf.requested_at))}`,
                  `motivo: ${rf.reason}`,
                  rf.evidence === "owner_documented" ? "confirmado pelo dono com evidência" : "",
                  rf.evidence === "provider" ? "confirmado pelo banco" : "",
                ]
                  .filter(Boolean)
                  .join(" · ")}
                value={formatCents(rf.amount.amount_cents)}
              />
            </div>
          ))}
        </div>
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
  const panel = useInlinePanel();
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  const label = `${methodText[receipt.method]} de ${formatCents(receipt.amount.amount_cents)}${receipt.classification === "excess" ? " (valor a mais)" : ""}`;

  async function submit() {
    setTried(true);
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
      panel.hide();
      onSaved(out.data.data);
    } else if (out.problem?.status === 409) await onConflict();
  }

  if (!panel.open)
    return (
      <Button
        id={panel.triggerId}
        variant="secondary"
        icon={Undo2}
        className="justify-self-start"
        onClick={() => panel.show("aberto")}
      >
        Estornar {label}
      </Button>
    );
  return (
    <form
      id={panel.panelId}
      onKeyDown={panel.onKeyDown}
      noValidate
      aria-label={`Estornar ${label}`}
      className="grid gap-4 rounded-xl border border-line bg-surface-2 p-4"
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        void submit();
      }}
    >
      <p className="m-0 font-semibold" data-autofocus tabIndex={-1}>
        Pedir estorno de {label}
      </p>
      <p className="m-0 text-sm text-fg-muted">
        O estorno é sempre do valor integral ({formatCents(receipt.amount.amount_cents)}).{" "}
        {receipt.method === "pix"
          ? "O banco faz a devolução; aqui só aparece como estornado depois da confirmação."
          : "Devolva ao cliente e depois confirme aqui com a evidência (recibo, comprovante)."}{" "}
        Pede a sua senha de novo.
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} onRetry={() => void submit()} />
      <TextArea
        label="Motivo do estorno"
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={reason}
        error={tried && reason.trim().length < 3 ? "Escreva o motivo (pelo menos 3 caracteres)." : undefined}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>Pedir estorno</Submit>
        <Button variant="secondary" onClick={panel.hide}>
          Voltar
        </Button>
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
    <form
      noValidate
      aria-label="Resolver estorno em revisão"
      onSubmit={(e) => e.preventDefault()}
      className="grid gap-4 rounded-xl border border-warning/40 bg-warning-soft p-4"
    >
      <div className="grid gap-1">
        <p className="m-0 font-semibold">Estorno de {formatCents(refund.amount.amount_cents)} em revisão</p>
        <p className="m-0 text-sm">
          Confirme só com evidência (comprovante do banco, recibo assinado); se não houve devolução, registre
          que não ocorreu. A confirmação fica registrada com o texto abaixo.
        </p>
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} onRetry={() => void resolve(last)} />
      <TextArea
        label="Evidência (o que comprova)"
        hint="Descreva o comprovante: banco, data, valor, quem assinou."
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={note}
        onChange={(e) => setNote(e.target.value)}
      />
      <Field
        label="Referência externa (opcional)"
        hint="Ex.: identificador do estorno no banco."
        maxLength={80}
        value={reference}
        onChange={(e) => setReference(e.target.value)}
      />
      {!ready && <p className="m-0 text-sm text-fg-muted">Escreva a evidência para habilitar as opções.</p>}
      <div className="flex flex-wrap gap-2">
        <Button disabled={!ready || cmd.busy !== null} onClick={() => void resolve("confirm")}>
          Confirmar estorno
        </Button>
        <Button
          variant="secondary"
          disabled={!ready || cmd.busy !== null}
          onClick={() => void resolve("mark-failed")}
        >
          Estorno não ocorreu
        </Button>
      </div>
    </form>
  );
}
