import qrcode from "qrcode-generator";
import { useMemo } from "react";
import { Clock } from "lucide-react";
import { formatCents } from "../../money";
import { AmountRow, CopyButton } from "../../ui/ui";
import { methodText, type PixCharge, type Receipt, cardTypeText } from "./textos";

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

/**
 * QR Code do Pix desenhado no navegador a partir do copia-e-cola (br_code), em SVG: nenhuma
 * imagem vem do provedor e nada sai do navegador.
 */
export function QrPix({ code, amountCents }: { code: string; amountCents: number }) {
  const path = useMemo(() => {
    const qr = qrcode(0, "M");
    qr.addData(code);
    qr.make();
    const n = qr.getModuleCount();
    let d = "";
    for (let r = 0; r < n; r++)
      for (let c = 0; c < n; c++) if (qr.isDark(r, c)) d += `M${c + 4} ${r + 4}h1v1h-1z`;
    return { d, size: n + 8 };
  }, [code]);
  return (
    <svg
      className="h-auto w-full max-w-60 rounded-lg border border-line bg-white"
      viewBox={`0 0 ${path.size} ${path.size}`}
      role="img"
      aria-label={`QR Code do Pix de ${formatCents(amountCents)}`}
      shapeRendering="crispEdges"
    >
      <rect width={path.size} height={path.size} fill="#fff" />
      <path d={path.d} fill="#000" />
    </svg>
  );
}

/** Copia-e-cola com botão de copiar (anuncia o resultado para leitores de tela). */
export function CopiaECola({ code }: { code: string }) {
  return (
    <div className="grid w-full gap-2">
      <label htmlFor="pix-copia-e-cola" className="text-sm font-semibold">
        Pix copia e cola
      </label>
      <textarea
        id="pix-copia-e-cola"
        readOnly
        rows={3}
        value={code}
        onFocus={(e) => e.currentTarget.select()}
        className="w-full resize-none rounded-lg border border-line bg-surface-2 px-3 py-2 font-mono text-xs break-all text-fg"
      />
      <CopyButton
        text={code}
        label="Copiar código"
        copiedText="Código copiado. Cole no app do seu banco."
        block
      />
    </div>
  );
}

/** Cobrança Pix em aberto: QR, copia-e-cola, validade e situação. */
export function PixAberto({ charge }: { charge: Pick<PixCharge, "br_code" | "amount" | "expires_at"> }) {
  if (!charge.br_code) return null;
  return (
    <div className="grid justify-items-center gap-4">
      <p className="m-0 text-center">
        Pague <strong className="tabular-nums">{formatCents(charge.amount.amount_cents)}</strong> pelo app do
        seu banco
        {charge.expires_at && (
          <>
            {" "}
            até <strong className="tabular-nums">{time.format(new Date(charge.expires_at))}</strong>
          </>
        )}
        .
      </p>
      <QrPix code={charge.br_code} amountCents={charge.amount.amount_cents} />
      <CopiaECola code={charge.br_code} />
      <div className="flex w-full gap-3 rounded-xl bg-select p-3 text-sm">
        <Clock aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-accent" />
        <p className="m-0">
          <strong className="block text-accent">Aguardando confirmação do banco</strong>
          Esta tela atualiza sozinha quando o banco confirmar. Comprovante ou print não confirmam o pagamento.
        </p>
      </div>
    </div>
  );
}

const receiptStatusText: Record<Receipt["status"], string> = {
  registered: "recebido, aguardando conferência",
  conferred: "conferido",
};

/** Lista de recebimentos (recebimento e conferência são registros distintos). */
export function Recebimentos({ receipts }: { receipts: Receipt[] }) {
  if (receipts.length === 0) return null;
  return (
    <div className="divide-y divide-line" role="list" aria-label="Recebimentos">
      {receipts.map((r) => {
        const sub = [
          receiptStatusText[r.status],
          r.classification === "excess" ? "valor a mais (devolução pendente)" : "",
          r.change_given && r.change_given.amount_cents > 0
            ? `entregue ${formatCents(r.cash_tendered?.amount_cents ?? 0)}, troco ${formatCents(r.change_given.amount_cents)}`
            : "",
          r.method === "card" ? `cartão ${cardTypeText(r.card_type)}` : "",
          r.card_reference ? `ref. ${r.card_reference}` : "",
          (r.refunded?.amount_cents ?? 0) > 0
            ? `estornado ${formatCents(r.refunded?.amount_cents ?? 0)}`
            : "",
        ]
          .filter(Boolean)
          .join(" · ");
        return (
          <div role="listitem" key={r.id}>
            <AmountRow label={methodText[r.method]} sub={sub} value={formatCents(r.amount.amount_cents)} />
          </div>
        );
      })}
    </div>
  );
}
