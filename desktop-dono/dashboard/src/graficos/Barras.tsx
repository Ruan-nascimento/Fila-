import { formatar, type Unidade } from "../formato";

export type Linha = { chave: string; rotulo: string; valor: number; secundario?: string };

/**
 * Ranking/comparação: barras horizontais (comprimento a partir de zero), rótulo e valor em texto
 * (nunca só cor). Cada barra é um botão que abre o detalhamento daquele recorte.
 */
export function Barras({
  linhas,
  unidade,
  aoSelecionar,
  cor = "var(--serie-1)",
}: {
  linhas: Linha[];
  unidade: Unidade;
  aoSelecionar?: (l: Linha) => void;
  cor?: string;
}) {
  const max = Math.max(0, ...linhas.map((l) => l.valor));
  return (
    <ul className="m-0 grid list-none gap-1.5 p-0">
      {linhas.map((l) => (
        <li key={l.chave}>
          <button
            type="button"
            disabled={!aoSelecionar}
            onClick={() => aoSelecionar?.(l)}
            className="grid w-full grid-cols-[minmax(6rem,11rem)_1fr_auto] items-center gap-3 rounded-lg px-2 py-1.5 text-left text-sm hover:bg-surface-2 disabled:hover:bg-transparent"
            aria-label={`${l.rotulo}: ${formatar(l.valor, unidade)}${l.secundario ? `, ${l.secundario}` : ""}`}
          >
            <span className="truncate">{l.rotulo}</span>
            <span aria-hidden="true" className="h-3 rounded-r bg-surface-2">
              <span className="block h-3 rounded-r" style={{ width: `${max > 0 ? (l.valor / max) * 100 : 0}%`, background: cor }} />
            </span>
            <span className="text-right tabular-nums">
              {formatar(l.valor, unidade)}
              {l.secundario && <span className="block text-xs text-fg-muted">{l.secundario}</span>}
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}
