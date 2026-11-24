import { useId } from "react";
import { Search, X } from "lucide-react";

/**
 * Campo de busca com rótulo acessível, botão de limpar e indicação discreta de carregamento.
 * O resultado é anunciado por quem usa o campo (contagem em região "polite").
 */
export function CampoBusca({
  label,
  placeholder,
  value,
  onChange,
  busy = false,
  className,
}: {
  label: string;
  placeholder: string;
  value: string;
  onChange: (v: string) => void;
  busy?: boolean;
  className?: string;
}) {
  const id = useId();
  return (
    <div className={className}>
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <div className="relative">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-muted"
        />
        <input
          id={id}
          type="search"
          inputMode="search"
          autoComplete="off"
          enterKeyHint="search"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && value) {
              e.preventDefault();
              onChange("");
            }
          }}
          placeholder={placeholder}
          aria-busy={busy || undefined}
          className="min-h-11 w-full rounded-lg border border-line-strong bg-surface py-2 pr-11 pl-9 text-base text-fg placeholder:text-fg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus [&::-webkit-search-cancel-button]:hidden"
        />
        {busy && (
          <span
            aria-hidden="true"
            className="absolute top-1/2 right-12 size-4 -translate-y-1/2 animate-spin rounded-full border-2 border-line border-t-accent"
          />
        )}
        {value && (
          <button
            type="button"
            onClick={() => onChange("")}
            aria-label={`Limpar ${label.toLowerCase()}`}
            className="absolute top-1/2 right-0.5 inline-flex size-11 -translate-y-1/2 items-center justify-center rounded-lg text-fg-muted hover:text-fg focus-visible:outline-2 focus-visible:outline-focus"
          >
            <X aria-hidden="true" className="size-4" />
          </button>
        )}
      </div>
    </div>
  );
}
