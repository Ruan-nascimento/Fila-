import { useState } from "react";
import { api } from "../../api/client";
import { formatCents } from "../../money";
import { Notice } from "../../ui/ui";
import type { Order } from "../comanda/textos";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { PixAberto, Recebimentos } from "./pix";
import { activeCharge, methodText, refundStatusText, type PaymentMethod } from "./textos";

/**
 * Pagamento visto pelo cliente: escolher o meio, gerar o Pix (valor calculado pelo servidor) e
 * acompanhar. Não existe botão "já paguei": só o banco confirma o Pix. Um erro nunca apaga a
 * comanda; a tela relê o estado atual.
 */
export function PagamentoCliente({
  shopId,
  order,
  onReload,
}: {
  shopId: string;
  order: Order;
  onReload: () => Promise<void>;
}) {
  const cmd = useCommand();
  const [confirmSwitch, setConfirmSwitch] = useState<PaymentMethod | null>(null);
  // Nova tentativa depois de o provedor recusar = nova intenção (nova chave); repetição por
  // queda de rede usa a mesma chave e devolve a mesma tentativa.
  const [attempt, setAttempt] = useState(0);
  const pay = order.payment;

  if (order.status === "awaiting_confirmation")
    return (
      <div role="status">
        <Notice kind="ok">Pagamento recebido pelo banco. Falta só a conferência da equipe.</Notice>
        <Recebimentos receipts={pay.receipts} />
      </div>
    );
  if (order.status === "settled")
    return (
      <div role="status">
        <Notice kind="ok">Pago. Obrigado!</Notice>
        <Recebimentos receipts={pay.receipts} />
        {pay.refunds.map((rf) => (
          <p key={rf.id}>
            {refundStatusText[rf.status]}: {formatCents(rf.amount.amount_cents)}
          </p>
        ))}
      </div>
    );
  if (order.status !== "awaiting_payment") return null;
  const due = pay.amounts.due.amount_cents;
  if (due === 0) return null;

  const active = activeCharge(pay.charges);
  const last = pay.charges[0];
  const path = { establishmentId: shopId, orderId: order.id };

  async function choose(method: PaymentMethod, ack: boolean) {
    const out = await cmd.run(`meio:${order.id}:${order.version}:${method}:${ack}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/payment-method", {
        params: { path, header: { "Idempotency-Key": key } },
        body: { expected_version: order.version, method, acknowledge_active_pix: ack },
      }),
    );
    if (out.ok) {
      setConfirmSwitch(null);
      await onReload();
    } else if (out.problem?.code === "pix_charge_active") {
      setConfirmSwitch(method);
      cmd.clear();
    } else if (out.problem?.status === 409) await onReload();
  }

  async function gerarPix() {
    // Mesma intenção = mesma chave: clique duplo ou rede instável não geram dois Pix.
    const out = await cmd.run(`pix:${order.id}:${attempt}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/pix-charges", {
        params: { path, header: { "Idempotency-Key": key } },
        body: { expected_version: order.version },
      }),
    );
    if (!out.ok && out.problem?.code === "provider_unavailable") setAttempt((n) => n + 1);
    if (out.ok || out.problem?.status === 409 || out.problem?.code === "provider_unavailable")
      await onReload();
  }

  const method = pay.method_choice;
  return (
    <section className="pay" aria-labelledby="titulo-pagamento">
      <h4 id="titulo-pagamento">
        Pagamento: <span className="order__price">{formatCents(due)}</span>
      </h4>
      {pay.verifying && (
        <p role="status">
          <strong>Verificando pagamento com o banco…</strong> Não gere outro Pix nem pague de novo. Esta tela
          atualiza sozinha.
        </p>
      )}
      <fieldset className="choices" disabled={cmd.busy !== null || pay.verifying}>
        <legend>Como você vai pagar?</legend>
        {(["pix", "cash", "card"] as const).map((m) => (
          <label key={m} className="choice">
            <input
              type="radio"
              name={`meio-${order.id}`}
              checked={method === m}
              onChange={() => void choose(m, false)}
            />
            {methodText[m]}
          </label>
        ))}
      </fieldset>
      {confirmSwitch && active && (
        <div className="notice notice--info" role="alert">
          <p>
            Você já tem um Pix de {formatCents(active.amount.amount_cents)} em aberto. Ele continua válido até
            expirar e ainda pode ser pago. Se pagar o Pix e também pagar na loja, o valor a mais fica
            registrado para devolução.
          </p>
          <div className="actions">
            <button type="button" className="button" onClick={() => void choose(confirmSwitch, true)}>
              Entendi, trocar para {methodText[confirmSwitch].toLowerCase()}
            </button>
            <button type="button" className="button button--secondary" onClick={() => setConfirmSwitch(null)}>
              Manter o Pix
            </button>
          </div>
        </div>
      )}
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {method === "pix" && active?.status === "pending" && !active.cancel_requested_at && (
        <PixAberto charge={active} />
      )}
      {method === "pix" && !active && (
        <>
          {last?.status === "expired" && <p>O Pix anterior expirou sem pagamento. Gere um novo.</p>}
          {last?.status === "failed" && <p>O Pix anterior não foi gerado. Tente de novo.</p>}
          <button
            type="button"
            className="button"
            disabled={cmd.busy !== null}
            onClick={() => void gerarPix()}
          >
            {cmd.busy ? "Gerando Pix…" : `Gerar Pix de ${formatCents(due)}`}
          </button>
        </>
      )}
      {(method === "cash" || method === "card") && (
        <p>Pague {formatCents(due)} na loja. A equipe registra o recebimento.</p>
      )}
      {!method && <p className="muted">Escolha o meio para continuar.</p>}
    </section>
  );
}
