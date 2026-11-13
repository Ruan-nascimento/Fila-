import qrcode from "qrcode-generator";
import { useMemo, useState } from "react";
import { formatCents } from "../../money";
import { methodText, type PixCharge, type Receipt } from "./textos";

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
      className="pix__qr"
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
  const [copied, setCopied] = useState<"" | "ok" | "erro">("");
  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied("ok");
    } catch {
      setCopied("erro");
    }
  }
  return (
    <div className="pix__code">
      <label htmlFor="pix-copia-e-cola">Pix copia e cola</label>
      <textarea
        id="pix-copia-e-cola"
        readOnly
        rows={3}
        value={code}
        onFocus={(e) => e.currentTarget.select()}
      />
      <button type="button" className="button" onClick={() => void copy()}>
        Copiar código
      </button>
      <p role="status" aria-live="polite" className="muted">
        {copied === "ok" && "Código copiado. Cole no app do seu banco."}
        {copied === "erro" && "Não foi possível copiar. Selecione o texto e copie manualmente."}
      </p>
    </div>
  );
}

/** Cobrança Pix em aberto: QR, copia-e-cola e validade. */
export function PixAberto({ charge }: { charge: PixCharge }) {
  if (!charge.br_code) return null;
  return (
    <div className="pix">
      <p>
        Pague <strong>{formatCents(charge.amount.amount_cents)}</strong> pelo app do seu banco
        {charge.expires_at && <> até {time.format(new Date(charge.expires_at))}</>}.
      </p>
      <QrPix code={charge.br_code} amountCents={charge.amount.amount_cents} />
      <CopiaECola code={charge.br_code} />
      <p className="muted">
        Esta tela atualiza sozinha quando o banco confirmar. Comprovante ou print não confirmam o pagamento.
      </p>
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
    <ul className="order__items" aria-label="Recebimentos">
      {receipts.map((r) => (
        <li key={r.id}>
          <span>
            {methodText[r.method]} · {receiptStatusText[r.status]}
            {r.classification === "excess" && " · valor a mais (devolução pendente)"}
            {r.change_given && r.change_given.amount_cents > 0 && (
              <>
                {" "}
                · entregue {formatCents(r.cash_tendered?.amount_cents ?? 0)}, troco{" "}
                {formatCents(r.change_given.amount_cents)}
              </>
            )}
            {r.card_reference && <> · ref. {r.card_reference}</>}
            {(r.refunded?.amount_cents ?? 0) > 0 && (
              <> · estornado {formatCents(r.refunded?.amount_cents ?? 0)}</>
            )}
          </span>
          <span className="order__price">{formatCents(r.amount.amount_cents)}</span>
        </li>
      ))}
    </ul>
  );
}
