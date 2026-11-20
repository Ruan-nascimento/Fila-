import { Check } from "lucide-react";
import { PALETTES, type PaletteId } from "../theme/paletas";
import { cx } from "../../ui/cx";

/**
 * Escolha da paleta da loja entre as 16 do catálogo. Grupo de rádios nativos: setas do teclado
 * mudam a opção, o leitor de tela lê o nome ("Verde-petróleo, selecionado"). A seleção aparece por
 * borda grossa e ícone de marcado, não só pela cor.
 */
export function ThemePalettePicker({
  value,
  onChange,
  disabled,
}: {
  value: PaletteId;
  onChange: (p: PaletteId) => void;
  disabled?: boolean;
}) {
  return (
    <fieldset className="m-0 min-w-0 border-0 p-0" disabled={disabled}>
      <legend className="mb-2 p-0 text-sm font-semibold">Cor principal</legend>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {PALETTES.map((p) => {
          const checked = p.id === value;
          return (
            <label
              key={p.id}
              className={cx(
                "flex min-h-11 cursor-pointer items-center gap-2 rounded-lg border bg-surface px-2 py-1.5 text-sm",
                "has-focus-visible:outline-3 has-focus-visible:outline-offset-2 has-focus-visible:outline-focus",
                checked ? "border-2 border-fg font-semibold" : "border-line",
              )}
            >
              <input
                type="radio"
                name="paleta"
                value={p.id}
                checked={checked}
                onChange={() => onChange(p.id)}
                className="sr-only"
              />
              <span
                aria-hidden="true"
                className="inline-flex size-6 shrink-0 items-center justify-center rounded-full border border-line"
                style={{ background: p.light.primary }}
              >
                {checked && <Check className="size-4 text-white" />}
              </span>
              <span className="min-w-0 truncate">{p.name}</span>
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}
