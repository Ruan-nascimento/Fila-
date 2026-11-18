// Utilitários de classe compartilhados pelos componentes (arquivo sem componentes).

/** Junta classes ignorando valores falsos. */
export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(" ");
}

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "danger-ghost";

const variants: Record<ButtonVariant, string> = {
  primary: "bg-primary text-on-primary hover:bg-primary-hover",
  secondary: "border border-line-strong/40 bg-surface text-fg hover:bg-surface-2",
  ghost: "text-accent hover:bg-select",
  danger: "bg-danger-solid text-white hover:bg-danger-solid-hover",
  "danger-ghost": "text-danger hover:bg-danger-soft",
};

/** Classes de botão (também usadas por links com cara de botão). Alvo mínimo de 44×44 px. */
export function buttonClass(variant: ButtonVariant = "primary", opts: { block?: boolean } = {}): string {
  return cx(
    "inline-flex min-h-11 min-w-11 items-center justify-center gap-2 rounded-lg px-4 py-2 text-center",
    "text-[0.9375rem] font-semibold leading-tight no-underline transition-colors",
    "disabled:cursor-not-allowed disabled:opacity-50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50",
    variants[variant],
    opts.block && "w-full",
  );
}
