import { useId, useState, type ReactNode } from "react";
import { Table2, ChartColumn } from "lucide-react";

/**
 * Moldura de cada gráfico: título, explicação curta da fórmula, aviso de filtro inaplicável e o
 * botão "Ver tabela", que mostra EXATAMENTE os mesmos dados (mesma resposta, mesmos filtros).
 */
export function Cartao({
  titulo,
  definicao,
  aviso,
  tabela,
  children,
  acoes,
}: {
  titulo: string;
  definicao?: ReactNode;
  aviso?: ReactNode;
  tabela?: ReactNode;
  children: ReactNode;
  acoes?: ReactNode;
}) {
  const [verTabela, setVerTabela] = useState(false);
  const id = useId();
  return (
    <section aria-labelledby={id} className="grid min-w-0 content-start gap-3 rounded-xl border border-line bg-surface p-4">
      <header className="flex flex-wrap items-start justify-between gap-2">
        <div className="grid min-w-0 gap-0.5">
          <h3 id={id} className="m-0 text-base font-semibold">{titulo}</h3>
          {definicao && <p className="m-0 text-xs text-fg-muted">{definicao}</p>}
        </div>
        <div className="flex items-center gap-1">
          {acoes}
          {tabela && (
            <button
              type="button"
              aria-pressed={verTabela}
              onClick={() => setVerTabela((v) => !v)}
              className="inline-flex min-h-9 items-center gap-1.5 rounded-lg px-2 text-sm font-medium text-accent hover:bg-select"
            >
              {verTabela ? <ChartColumn aria-hidden="true" className="size-4" /> : <Table2 aria-hidden="true" className="size-4" />}
              {verTabela ? "Ver gráfico" : "Ver tabela"}
            </button>
          )}
        </div>
      </header>
      {aviso}
      {verTabela && tabela ? <div className="overflow-x-auto">{tabela}</div> : children}
    </section>
  );
}

export function Vazio({ children = "Sem dados para este período" }: { children?: ReactNode }) {
  return (
    <p role="status" className="m-0 grid min-h-32 place-items-center rounded-lg bg-surface-2 p-4 text-center text-sm text-fg-muted">
      {children}
    </p>
  );
}

export function Tabela({ cabecalho, linhas }: { cabecalho: string[]; linhas: (string | number)[][] }) {
  return (
    <table className="w-full border-collapse text-sm tabular-nums">
      <thead>
        <tr>
          {cabecalho.map((c, i) => (
            <th key={c} scope="col" className={`border-b border-line px-2 py-1.5 font-semibold ${i ? "text-right" : "text-left"}`}>
              {c}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {linhas.map((l, i) => (
          <tr key={i} className="border-b border-line/60">
            {l.map((c, j) =>
              j === 0 ? (
                <th key={j} scope="row" className="px-2 py-1.5 text-left font-normal">{c}</th>
              ) : (
                <td key={j} className="px-2 py-1.5 text-right">{c}</td>
              ),
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
