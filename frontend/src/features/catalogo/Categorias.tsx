import { cx } from "../../ui/cx";

export type CategoriaOpcao = { id: string; name: string; count: number };

/**
 * Filtro por categoria em chips com rolagem LOCAL (nunca rola a página na horizontal). "Todas"
 * e "Sem categoria" ficam sempre acessíveis. Trocar de categoria não mexe no que já foi escolhido.
 */
export function Categorias({
  label,
  options,
  value,
  onChange,
  uncategorized,
}: {
  label: string;
  options: CategoriaOpcao[];
  value: string;
  onChange: (id: string) => void;
  uncategorized: number;
}) {
  if (options.length === 0) return null;
  const all = [{ id: "", name: "Todas", count: -1 }, ...options];
  if (uncategorized > 0) all.push({ id: "-", name: "Sem categoria", count: uncategorized });
  return (
    <div
      role="group"
      aria-label={label}
      className="-mx-1 flex gap-2 overflow-x-auto px-1 pb-1 [scrollbar-width:thin]"
    >
      {all.map((c) => (
        <button
          key={c.id || "todas"}
          type="button"
          aria-pressed={value === c.id}
          onClick={() => onChange(c.id)}
          className={cx(
            "inline-flex min-h-11 shrink-0 items-center gap-1.5 rounded-full border px-4 text-sm font-medium whitespace-nowrap focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-focus",
            value === c.id
              ? "border-select-line bg-select text-accent"
              : "border-line bg-surface text-fg hover:bg-surface-2",
          )}
        >
          {c.name}
          {c.count >= 0 && <span className="text-xs opacity-80 tabular-nums">{c.count}</span>}
        </button>
      ))}
    </div>
  );
}
