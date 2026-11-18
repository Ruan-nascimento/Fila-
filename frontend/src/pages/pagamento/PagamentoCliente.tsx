import { useEffect, useRef, useState } from "react";
import { Banknote, CreditCard, QrCode, RefreshCw } from "lucide-react";
import { api } from "../../api/client";
import { formatCents } from "../../money";
import { Button, Choice, ChoiceGroup, Notice } from "../../ui/ui";
import type { Order } from "../comanda/textos";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { PixAberto, Recebimentos } from "./pix";
import { activeCharge, methodText, refundStatusText, type PaymentMethod } from "./textos";

const methodIcon = { pix: QrCode, cash: Banknote, card: CreditCard } as const;

/**
 * Pagamento visto pelo cliente: escolher o meio, gerar o Pix (valor calculado pelo servidor) e
 * acompanhar. Não existe botão "já paguei": só o banco confirma o Pix. Um erro nunca apaga a
 * comanda; a tela relê o estado atual. Etapas: escolha do meio → cobrança criada → verificação
 * → confirmado (aí as ações de pagamento somem).
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
  // Se o Pix aparece (resposta repetida ou atualização em tempo real depois de a rede cair), o
  // erro anterior de envio já não vale: some da tela.
  // A ordem pode ser qualquer uma (o Pix chega pelo tempo real antes ou depois da falha), por isso
  // há duas checagens: quando o Pix aparece e quando a tentativa falha.
  const activeId = activeCharge(order.payment.charges)?.id ?? null;
  const activeRef = useRef<string | null>(activeId);
  const clearError = cmd.clear;
  useEffect(() => {
    activeRef.current = activeId;
    if (activeId) clearError();
  }, [activeId, clearError]);
  const [confirmSwitch, setConfirmSwitch] = useState<PaymentMethod | null>(null);
  // Escolha enviada e ainda sem resposta: aparece marcada na hora (o servidor confirma ou desfaz).
  const [choosing, setChoosing] = useState<PaymentMethod | null>(null);
  // Nova tentativa depois de o provedor recusar = nova intenção (nova chave); repetição por
  // queda de rede usa a mesma chave e devolve a mesma tentativa.
  const [attempt, setAttempt] = useState(0);
  const pay = order.payment;

  if (order.status === "awaiting_confirmation")
    return (
      <div className="grid gap-2">
        <Notice kind="ok" title="Pagamento recebido pelo banco.">
          <p>Falta só a conferência da equipe. Você não precisa fazer mais nada.</p>
        </Notice>
        <Recebimentos receipts={pay.receipts} />
      </div>
    );
  if (order.status === "settled")
    return (
      <div className="grid gap-2">
        <Notice kind="ok" title="Pago. Obrigado!" />
        <Recebimentos receipts={pay.receipts} />
        {pay.refunds.map((rf) => (
          <p key={rf.id} className="m-0 text-sm">
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
    setChoosing(method);
    const out = await cmd.run(`meio:${order.id}:${order.version}:${method}:${ack}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/payment-method", {
        params: { path, header: { "Idempotency-Key": key } },
        body: { expected_version: order.version, method, acknowledge_active_pix: ack },
      }),
    );
    if (out.ok) {
      setConfirmSwitch(null);
      await onReload();
      setChoosing(null);
      return;
    }
    setChoosing(null);
    if (out.problem?.code === "pix_charge_active") {
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
    // Falhou, mas o Pix já chegou pelo tempo real (a resposta se perdeu): o erro não vale mais.
    if (!out.ok && activeRef.current) cmd.clear();
    if (!out.ok && out.problem?.code === "provider_unavailable") setAttempt((n) => n + 1);
    if (out.ok || out.problem?.status === 409 || out.problem?.code === "provider_unavailable")
      await onReload();
  }

  const method = pay.method_choice;
  return (
    <section className="grid gap-4 border-t border-line pt-4" aria-labelledby="titulo-pagamento">
      <h4
        id="titulo-pagamento"
        className="m-0 flex items-baseline justify-between gap-3 text-base font-semibold"
      >
        <span>Pagamento</span>
        <span className="text-xl tabular-nums">{formatCents(due)}</span>
      </h4>
      {pay.verifying && (
        <Notice kind="info" title="Verificando pagamento com o banco…">
          <p>Não gere outro Pix nem pague de novo. Esta tela atualiza sozinha.</p>
        </Notice>
      )}
      <ChoiceGroup legend="Como você vai pagar?" disabled={cmd.busy !== null || pay.verifying}>
        {(["pix", "cash", "card"] as const).map((m) => {
          const Icon = methodIcon[m];
          return (
            <Choice
              key={m}
              type="radio"
              name={`meio-${order.id}`}
              checked={(choosing ?? method) === m}
              onChange={() => void choose(m, false)}
              label={
                <span className="inline-flex items-center gap-2">
                  <Icon aria-hidden="true" className="size-5 text-fg-muted" />
                  {methodText[m]}
                </span>
              }
            />
          );
        })}
      </ChoiceGroup>
      {confirmSwitch && active && (
        <div className="grid gap-3 rounded-xl border border-warning/40 bg-warning-soft p-4" role="alert">
          <p className="m-0">
            Você já tem um Pix de {formatCents(active.amount.amount_cents)} em aberto. Ele continua válido até
            expirar e ainda pode ser pago. Se pagar o Pix e também pagar na loja, o valor a mais fica
            registrado para devolução.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void choose(confirmSwitch, true)}>
              Entendi, trocar para {methodText[confirmSwitch].toLowerCase()}
            </Button>
            <Button variant="secondary" onClick={() => setConfirmSwitch(null)}>
              Manter o Pix
            </Button>
          </div>
        </div>
      )}
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {method === "pix" && active?.status === "pending" && !active.cancel_requested_at && (
        <PixAberto charge={active} />
      )}
      {method === "pix" && !active && (
        <div className="grid gap-2">
          {last?.status === "expired" && (
            <p className="m-0 text-sm text-fg-muted">O Pix anterior expirou sem pagamento. Gere um novo.</p>
          )}
          {last?.status === "failed" && (
            <p className="m-0 text-sm text-fg-muted">O Pix anterior não foi gerado. Tente de novo.</p>
          )}
          <Button
            block
            icon={last?.status === "expired" || last?.status === "failed" ? RefreshCw : QrCode}
            disabled={cmd.busy !== null}
            onClick={() => void gerarPix()}
          >
            {cmd.busy ? "Gerando Pix…" : `Gerar Pix de ${formatCents(due)}`}
          </Button>
        </div>
      )}
      {(method === "cash" || method === "card") && (
        <p className="m-0 rounded-lg bg-surface-2 px-3 py-2">
          Pague <strong className="tabular-nums">{formatCents(due)}</strong> na loja. A equipe registra o
          recebimento.
        </p>
      )}
      {!method && <p className="m-0 text-sm text-fg-muted">Escolha o meio para continuar.</p>}
    </section>
  );
}
