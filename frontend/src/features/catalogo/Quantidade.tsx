import { useState } from "react";
import { Minus, Plus } from "lucide-react";

/**
 * Quantidade inteira com diminuir, aumentar e edição direta (alvos de 44 px). `min` protege as
 * unidades que não podem sair (já realizadas); o servidor confere tudo de novo.
 */
export function Quantidade({
  label,
  value,
  onChange,
  min = 0,
  max,
}: {
  /** Ex.: "Quantidade de Corte" — lido por leitor de tela. */
  label: string;
  value: number;
  onChange: (n: number) => void;
  min?: number;
  max: number;
}) {
  const [draft, setDraft] = useState(String(value));
  // Valor novo vindo de fora (botões, servidor): o rascunho acompanha na mesma renderização.
  const [seen, setSeen] = useState(value);
  if (seen !== value) {
    setSeen(value);
    setDraft(String(value));
  }
  const commit = (raw: string) => {
    const n = Number.parseInt(raw, 10);
    if (Number.isNaN(n)) {
      setDraft(String(value));
      return;
    }
    const clamped = Math.min(max, Math.max(min, n));
    setDraft(String(clamped));
    if (clamped !== value) onChange(clamped);
  };
  const btn =
    "inline-flex size-11 items-center justify-center rounded-lg border border-line-strong bg-surface text-fg hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-focus";
  return (
    <div role="group" aria-label={label} className="inline-flex items-center gap-1">
      <button
        type="button"
        className={btn}
        onClick={() => onChange(value - 1)}
        disabled={value <= min}
        aria-label={`Diminuir ${label.toLowerCase()}`}
      >
        <Minus aria-hidden="true" className="size-4" />
      </button>
      <input
        type="text"
        inputMode="numeric"
        pattern="[0-9]*"
        aria-label={label}
        value={draft}
        onChange={(e) => setDraft(e.target.value.replace(/\D/g, "").slice(0, 2))}
        onBlur={(e) => commit(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            commit((e.target as HTMLInputElement).value);
          }
        }}
        className="h-11 w-12 rounded-lg border border-line-strong bg-surface text-center text-base font-semibold tabular-nums focus-visible:outline-2 focus-visible:outline-focus"
      />
      <button
        type="button"
        className={btn}
        onClick={() => onChange(value + 1)}
        disabled={value >= max}
        aria-label={`Aumentar ${label.toLowerCase()}`}
      >
        <Plus aria-hidden="true" className="size-4" />
      </button>
    </div>
  );
}
