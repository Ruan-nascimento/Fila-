import { useState } from "react";
import type { Valor } from "../nativo";
import { formatar, textoValor, type Unidade } from "../formato";
import { CORES, escala } from "./escala";
import { useLargura } from "./useLargura";

export type Bucket = {
  start: string;
  end: string;
  label: string;
  collected: boolean;
  partial: boolean;
  values: Record<string, Valor>;
};
export type DefSerie = { key: string; label: string; unit: Unidade };

/**
 * Tendência no tempo: linhas (2 px, marcador ≥ 8 px) ou colunas, eixo a partir de zero, uma só
 * escala (séries de unidades diferentes nunca dividem o eixo: quem chama separa os gráficos).
 * Cada período é um alvo focável (teclado e toque): foco/ponteiro mostra o tooltip; Enter/clique
 * abre o detalhamento daquele recorte. A legenda liga e desliga séries (aria-pressed).
 */
export function SerieTemporal({
  buckets,
  series,
  forma = "linha",
  aoSelecionar,
  rotulo,
}: {
  buckets: Bucket[];
  series: DefSerie[];
  forma?: "linha" | "coluna";
  aoSelecionar?: (b: Bucket) => void;
  rotulo: string;
}) {
  const [ref, largura] = useLargura<HTMLDivElement>();
  const [ocultas, setOcultas] = useState<Set<string>>(new Set());
  const [ativo, setAtivo] = useState<number | null>(null);
  const visiveis = series.filter((s) => !ocultas.has(s.key));
  const unidade = series[0]?.unit ?? "count";
  const altura = 220;
  const m = { t: 12, r: 12, b: 28, l: 72 };
  const w = largura - m.l - m.r;
  const h = altura - m.t - m.b;
  let max = 0;
  for (const b of buckets)
    for (const s of visiveis) {
      const v = b.values[s.key];
      if (v?.status === "ok" && (v.value ?? 0) > max) max = v.value ?? 0;
    }
  const { topo, marcas } = escala(
    max,
    unidade === "count" || unidade === "points",
  );
  const passo = buckets.length ? w / buckets.length : w;
  const x = (i: number) => m.l + passo * i + passo / 2;
  const y = (v: number) => m.t + h - (v / topo) * h;
  const cada = Math.max(
    1,
    Math.ceil(buckets.length / Math.max(2, Math.floor(w / 64))),
  );
  const b = ativo !== null ? buckets[ativo] : undefined;

  return (
    <div className="grid gap-2">
      {series.length > 1 && (
        <div
          role="group"
          aria-label="Séries do gráfico"
          className="flex flex-wrap gap-2"
        >
          {series.map((s, i) => {
            const on = !ocultas.has(s.key);
            return (
              <button
                key={s.key}
                type="button"
                aria-pressed={on}
                onClick={() =>
                  setOcultas((o) => {
                    const n = new Set(o);
                    if (n.has(s.key)) n.delete(s.key);
                    else if (series.length - n.size > 1) n.add(s.key); // ao menos uma visível
                    return n;
                  })
                }
                className={`inline-flex min-h-9 items-center gap-2 rounded-full border px-3 text-sm ${on ? "border-line-strong/40 text-fg" : "border-line text-fg-muted line-through"}`}
              >
                <span
                  aria-hidden="true"
                  className="inline-block size-2.5 rounded-full"
                  style={{ background: CORES[i] }}
                />
                {s.label}
              </button>
            );
          })}
        </div>
      )}
      <div ref={ref} className="relative">
        <svg
          width={largura}
          height={altura}
          role="img"
          aria-label={rotulo}
          className="block max-w-full overflow-visible"
        >
          {marcas.map((v) => (
            <g key={v}>
              <line
                x1={m.l}
                x2={m.l + w}
                y1={y(v)}
                y2={y(v)}
                stroke="var(--line)"
              />
              <text
                x={m.l - 8}
                y={y(v)}
                dy="0.32em"
                textAnchor="end"
                className="fill-fg-muted text-[11px] tabular-nums"
              >
                {formatar(v, unidade)}
              </text>
            </g>
          ))}
          {buckets.map((bk, i) =>
            !bk.collected ? (
              <rect
                key={`nc${i}`}
                x={m.l + passo * i}
                y={m.t}
                width={passo}
                height={h}
                fill="var(--surface-2)"
              />
            ) : null,
          )}
          {forma === "coluna"
            ? buckets.map((bk, i) => {
                const bw = Math.max(2, (passo - 2) / visiveis.length - 2);
                return visiveis.map((s, j) => {
                  const v = bk.values[s.key];
                  if (v?.status !== "ok") return null;
                  const val = v.value ?? 0;
                  const top = y(val);
                  const cor = CORES[series.indexOf(s)];
                  return (
                    <path
                      key={`${i}-${s.key}`}
                      d={barra(
                        m.l + passo * i + 1 + j * (bw + 2),
                        top,
                        bw,
                        m.t + h - top,
                      )}
                      fill={cor}
                    />
                  );
                });
              })
            : visiveis.map((s) => {
                const cor = CORES[series.indexOf(s)];
                const pts = buckets.map((bk, i) => {
                  const v = bk.values[s.key];
                  return v?.status === "ok"
                    ? `${x(i)},${y(v.value ?? 0)}`
                    : null;
                });
                // Lacunas (sem coleta, sem amostra) quebram a linha: nada de zero inventado.
                const trechos: string[][] = [[]];
                for (const p of pts) {
                  if (p) trechos[trechos.length - 1]?.push(p);
                  else trechos.push([]);
                }
                return (
                  <g key={s.key}>
                    {trechos
                      .filter((t) => t.length > 1)
                      .map((t, k) => (
                        <polyline
                          key={k}
                          points={t.join(" ")}
                          fill="none"
                          stroke={cor}
                          strokeWidth={2}
                          strokeLinejoin="round"
                        />
                      ))}
                    {pts.map((p, i) =>
                      p && (buckets.length <= 40 || ativo === i) ? (
                        <circle
                          key={i}
                          cx={x(i)}
                          cy={Number(p.split(",")[1])}
                          r={4}
                          fill={cor}
                          stroke="var(--surface)"
                          strokeWidth={2}
                        />
                      ) : null,
                    )}
                  </g>
                );
              })}
          {buckets.map((bk, i) =>
            i % cada === 0 ? (
              <text
                key={`l${i}`}
                x={x(i)}
                y={altura - 8}
                textAnchor="middle"
                className="fill-fg-muted text-[11px]"
              >
                {bk.label}
              </text>
            ) : null,
          )}
          {ativo !== null && (
            <line
              x1={x(ativo)}
              x2={x(ativo)}
              y1={m.t}
              y2={m.t + h}
              stroke="var(--line-strong)"
              strokeDasharray="3 3"
            />
          )}
          {buckets.map((bk, i) => (
            <rect
              key={`alvo${i}`}
              x={m.l + passo * i}
              y={m.t}
              width={passo}
              height={h}
              fill="transparent"
              tabIndex={0}
              role="button"
              aria-label={`${bk.label}: ${visiveis.map((s) => `${s.label} ${textoValor(bk.values[s.key], s.unit)}`).join("; ")}. Enter abre os registros.`}
              onMouseEnter={() => setAtivo(i)}
              onFocus={() => setAtivo(i)}
              onMouseLeave={() => setAtivo(null)}
              onBlur={() => setAtivo(null)}
              onClick={() => aoSelecionar?.(bk)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  aoSelecionar?.(bk);
                }
              }}
              className="cursor-pointer outline-none focus-visible:stroke-[var(--focus)] focus-visible:stroke-2"
            />
          ))}
        </svg>
        {buckets.some((bk) => !bk.collected) && (
          <p className="m-0 flex items-center gap-2 text-xs text-fg-muted">
            <span
              aria-hidden="true"
              className="inline-block size-3 rounded-sm bg-surface-2"
            />{" "}
            Área cinza: antes do início da coleta desta loja (sem dados, não é
            zero).
          </p>
        )}
        {b && ativo !== null && (
          <div
            role="tooltip"
            className="pointer-events-none absolute top-0 z-10 grid min-w-44 gap-1 rounded-lg border border-line bg-surface px-3 py-2 text-sm shadow-lg"
            style={{
              left: Math.min(
                Math.max(0, x(ativo) + 12),
                Math.max(0, largura - 200),
              ),
            }}
          >
            <strong className="font-semibold">
              {b.label}
              {b.partial ? " (parcial)" : ""}
            </strong>
            {!b.collected && (
              <span className="text-fg-muted">Antes do início da coleta</span>
            )}
            {visiveis.map((s, i) => {
              const v = b.values[s.key];
              return (
                <span
                  key={s.key}
                  className="flex items-center justify-between gap-3"
                >
                  <span className="flex items-center gap-1.5">
                    <span
                      aria-hidden="true"
                      className="inline-block size-2 rounded-full"
                      style={{
                        background: CORES[series.indexOf(s)] ?? CORES[i],
                      }}
                    />
                    {s.label}
                  </span>
                  <span className="tabular-nums">
                    {textoValor(v, s.unit)}
                    {v?.sample !== undefined &&
                    v.status === "ok" &&
                    s.unit === "seconds"
                      ? ` · n=${v.sample}`
                      : ""}
                  </span>
                </span>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

/** Coluna com o topo arredondado (4 px) e a base reta, presa à linha de base. */
function barra(x: number, y: number, w: number, h: number): string {
  if (h <= 0) return "";
  const r = Math.min(4, w / 2, h);
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}
