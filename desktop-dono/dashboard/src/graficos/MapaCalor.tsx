import { useState } from "react";

export type Celula = { dow: number; hour: number; entries: number };
const DIAS = ["Seg", "Ter", "Qua", "Qui", "Sex", "Sáb", "Dom"];

/**
 * Horários de pico: entradas por dia da semana × hora local. Cor sequencial de um só matiz (mais
 * escuro = mais entradas) e o número na célula ao focar. Cada célula é um botão (teclado/toque).
 */
export function MapaCalor({ celulas, aoSelecionar }: { celulas: Celula[]; aoSelecionar?: (c: Celula) => void }) {
  const [foco, setFoco] = useState<Celula | null>(null);
  const horas = celulas.length ? range(Math.min(...celulas.map((c) => c.hour)), Math.max(...celulas.map((c) => c.hour))) : [];
  const max = Math.max(1, ...celulas.map((c) => c.entries));
  const de = (d: number, h: number) => celulas.find((c) => c.dow === d && c.hour === h)?.entries ?? 0;
  return (
    <div className="grid gap-2">
      <div className="overflow-x-auto">
        <table className="border-separate border-spacing-0.5 text-xs" aria-label="Entradas por dia da semana e hora">
          <thead>
            <tr>
              <th scope="col" className="w-10" />
              {horas.map((h) => (
                <th key={h} scope="col" className="px-0.5 font-normal text-fg-muted">{h}h</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {DIAS.map((nome, i) => (
              <tr key={nome}>
                <th scope="row" className="pr-2 text-right font-normal text-fg-muted">{nome}</th>
                {horas.map((h) => {
                  const n = de(i + 1, h);
                  const c = { dow: i + 1, hour: h, entries: n };
                  return (
                    <td key={h} className="p-0">
                      <button
                        type="button"
                        aria-label={`${nome} ${h}h: ${n} entradas`}
                        onFocus={() => setFoco(c)}
                        onMouseEnter={() => setFoco(c)}
                        onClick={() => aoSelecionar?.(c)}
                        className="block size-7 rounded text-[10px] tabular-nums focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                        style={{
                          background: n ? `color-mix(in oklch, var(--calor) ${15 + (n / max) * 85}%, var(--surface))` : "var(--surface-2)",
                          color: n / max > 0.55 ? "white" : "var(--fg)",
                        }}
                      >
                        {foco?.dow === c.dow && foco.hour === h ? n : ""}
                      </button>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="m-0 flex items-center gap-2 text-xs text-fg-muted">
        Menos
        <span aria-hidden="true" className="h-2 w-24 rounded" style={{ background: "linear-gradient(90deg, color-mix(in oklch, var(--calor) 15%, var(--surface)), var(--calor))" }} />
        Mais (máx. {max} entradas numa célula)
        {foco && <span className="ml-auto text-fg">{DIAS[foco.dow - 1]} {foco.hour}h: {foco.entries} entradas</span>}
      </p>
    </div>
  );
}

function range(a: number, b: number): number[] {
  const out: number[] = [];
  for (let i = a; i <= b; i++) out.push(i);
  return out;
}

export { DIAS };
