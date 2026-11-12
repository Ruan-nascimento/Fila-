import { useId, type InputHTMLAttributes, type ReactNode } from "react";

type FieldProps = {
  label: string;
  error?: string | undefined;
  hint?: string;
} & InputHTMLAttributes<HTMLInputElement>;

/** Campo com rótulo visível, dica e erro junto ao campo (R-WEB-02). */
export function Field({ label, error, hint, ...input }: FieldProps) {
  const id = useId();
  const hintId = hint ? `${id}-dica` : undefined;
  const errorId = error ? `${id}-erro` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined;
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {hint && (
        <p className="field__hint" id={hintId}>
          {hint}
        </p>
      )}
      <input id={id} aria-invalid={error ? true : undefined} aria-describedby={describedBy} {...input} />
      {error && (
        <p className="field__error" id={errorId}>
          <span aria-hidden="true">✕ </span>
          {error}
        </p>
      )}
    </div>
  );
}

/** Aviso com ícone e texto (estado nunca só por cor). Erros são anunciados ao leitor de tela. */
export function Notice({ kind, children }: { kind: "ok" | "erro" | "info"; children: ReactNode }) {
  const icon = kind === "ok" ? "✓" : kind === "erro" ? "!" : "i";
  return (
    <div className={`notice notice--${kind}`} role={kind === "erro" ? "alert" : "status"}>
      <span aria-hidden="true" className="notice__icon">
        {icon}
      </span>
      <div>{children}</div>
    </div>
  );
}

/** Botão principal; fica desabilitado e com texto de andamento enquanto envia. */
export function Submit({ busy, children }: { busy: boolean; children: ReactNode }) {
  return (
    <button type="submit" className="button" disabled={busy} aria-busy={busy}>
      {busy ? "Enviando…" : children}
    </button>
  );
}
