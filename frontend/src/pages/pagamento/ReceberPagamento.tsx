import { useState, type FormEvent } from "react";
import { Banknote, CreditCard, Info, RefreshCw, ShieldCheck } from "lucide-react";
import { api } from "../../api/client";
import { formatCents, parseCents } from "../../money";
import { AmountRow, Badge, Button, Choice, ChoiceGroup, Field, Notice, Submit } from "../../ui/ui";
import type { Order } from "../comanda/textos";
import { useCommand } from "../gestao/comando";
import { CommandError, type StaffContext } from "../gestao/comum";
import { Recebimentos } from "./pix";
import { Estornos } from "./Estornos";
import { activeCharge, chargeStatusText, issueText, liveRefund, methodText } from "./textos";

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
  // Recebimento com estorno em andamento/confirmado não é conferido (seria receita devolvida).
  const toConfer = pay.receipts.filter((r) => r.status === "registered" && !liveRefund(pay.refunds, r.id));

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
    <section className="grid gap-4 border-t border-line pt-5" aria-label="Pagamento">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="m-0 text-base font-semibold">Pagamento</h3>
        {pay.method_choice && <Badge tone="primary">Meio escolhido: {methodText[pay.method_choice]}</Badge>}
      </div>
      <div className="divide-y divide-line rounded-lg bg-surface-2 px-3">
        <AmountRow label="Devido" value={formatCents(pay.amounts.due.amount_cents)} />
        <AmountRow label="Recebido" value={formatCents(pay.amounts.received.amount_cents)} />
        <AmountRow strong label="Em aberto" value={formatCents(pay.amounts.outstanding.amount_cents)} />
      </div>
      {pay.charges.length > 0 && (
        <div className="divide-y divide-line" role="list" aria-label="Cobranças Pix">
          {pay.charges.map((c) => (
            <div role="listitem" key={c.id}>
              <AmountRow
                label="Pix"
                sub={[
                  chargeStatusText[c.status],
                  c.status === "pending" && c.expires_at ? `até ${time.format(new Date(c.expires_at))}` : "",
                  c.cancel_requested_at ? "cancelamento pedido (o Pix segue válido até expirar)" : "",
                ]
                  .filter(Boolean)
                  .join(" · ")}
                value={formatCents(c.amount.amount_cents)}
              />
            </div>
          ))}
        </div>
      )}
      {pay.open_issue_kinds.length > 0 && (
        <div
          className="grid gap-1 rounded-xl border border-warning/40 bg-warning-soft p-4 text-sm"
          role="note"
          aria-label="Pendências desta comanda"
        >
          <p className="m-0 font-semibold">Pendência financeira (o dono trata em Pendências):</p>
          <ul className="m-0 grid gap-1 pl-5">
            {pay.open_issue_kinds.map((k) => (
              <li key={k}>{issueText[k]}</li>
            ))}
          </ul>
        </div>
      )}
      <Recebimentos receipts={pay.receipts} />
      <Estornos ctx={ctx} order={order} onSaved={onSaved} onConflict={onConflict} />
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {toConfer.map((r) => (
        <Button key={r.id} icon={ShieldCheck} disabled={cmd.busy !== null} onClick={() => void confer(r.id)}>
          Conferir {r.classification === "excess" ? "valor a mais" : "recebimento"} Pix de{" "}
          {formatCents(r.amount.amount_cents)}
        </Button>
      ))}
      {order.status === "awaiting_payment" && pay.verifying && (
        <div className="grid gap-2">
          <Notice kind="info" title="Verificando com o banco">
            <p>Aguardando a confirmação do pagamento via Pix.</p>
          </Notice>
          <p className="m-0 flex items-start gap-2 rounded-lg bg-surface-2 px-3 py-2 text-sm" role="status">
            <Info aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-fg-muted" />
            Verificando um Pix com o banco. Outras formas de pagamento ficam indisponíveis até a verificação
            terminar, para não cobrar duas vezes.
          </p>
        </div>
      )}
      {order.status === "awaiting_payment" && active?.status === "pending" && !active.cancel_requested_at && (
        <div className="grid gap-3 rounded-xl border border-line bg-surface-2 p-4">
          <p className="m-0 text-sm">
            Há um Pix de {formatCents(active.amount.amount_cents)} em aberto. Para receber na loja, avise o
            cliente: o Pix continua válido até expirar e, se for pago também, o valor a mais fica para
            devolução.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="secondary"
              icon={Banknote}
              disabled={cmd.busy !== null}
              onClick={() => void switchFromPix("cash")}
            >
              Trocar para dinheiro
            </Button>
            <Button
              variant="secondary"
              icon={CreditCard}
              disabled={cmd.busy !== null}
              onClick={() => void switchFromPix("card")}
            >
              Trocar para cartão
            </Button>
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
    <form
      onSubmit={onSubmit}
      noValidate
      aria-label="Receber na loja"
      className="grid gap-4 rounded-xl border border-line p-4"
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <ChoiceGroup legend="Receber na loja">
        <div className="grid grid-cols-2 gap-2">
          {(["cash", "card"] as const).map((m) => {
            const Icon = m === "cash" ? Banknote : CreditCard;
            return (
              <Choice
                key={m}
                type="radio"
                name={`receber-${order.id}`}
                checked={method === m}
                onChange={() => setMethod(m)}
                label={
                  <span className="inline-flex items-center gap-2">
                    <Icon aria-hidden="true" className="size-5 text-fg-muted" />
                    {methodText[m]}
                  </span>
                }
              />
            );
          })}
        </div>
      </ChoiceGroup>
      {method === "cash" && (
        <>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5">
              <span className="text-sm font-semibold">Valor devido</span>
              <span className="flex min-h-11 items-center rounded-lg bg-surface-2 px-3 tabular-nums">
                {formatCents(due)}
              </span>
            </div>
            <Field
              label="Valor entregue pelo cliente (R$)"
              inputMode="decimal"
              placeholder={formatCents(due)}
              value={tendered}
              onChange={(e) => setTendered(e.target.value)}
              error={tenderedError}
            />
          </div>
          {tenderedCents !== null && !tenderedError && (
            <p
              role="status"
              className="m-0 flex items-center justify-between rounded-lg bg-select px-4 py-3 text-lg font-semibold text-accent"
            >
              Troco:
              <strong className="text-xl tabular-nums">{formatCents(tenderedCents - due)}</strong>
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
      <Submit busy={cmd.busy !== null} block>
        Registrar {formatCents(due)} recebido e conferido
      </Submit>
      {cmd.problem?.code === "pix_charge_active" && (
        <Notice kind="info">
          <p>
            <RefreshCw aria-hidden="true" className="mr-1 inline size-4" />
            Há um Pix em aberto: troque o meio antes de receber na loja.
          </p>
        </Notice>
      )}
    </form>
  );
}
