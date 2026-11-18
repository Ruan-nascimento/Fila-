import {
  useEffect,
  useId,
  useState,
  type ButtonHTMLAttributes,
  type ComponentType,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { Link, type LinkProps } from "react-router";
import { buttonClass, cx, type ButtonVariant } from "./cx";
import {
  Check,
  CircleAlert,
  CircleCheck,
  Copy,
  Eye,
  EyeOff,
  Info,
  LoaderCircle,
  RefreshCw,
  TriangleAlert,
  type LucideProps,
} from "lucide-react";

export type IconType = ComponentType<LucideProps>;
export type { ButtonVariant } from "./cx";

/* ---------------------------------------------------------------- Botões */

type ButtonProps = {
  variant?: ButtonVariant;
  block?: boolean;
  icon?: IconType | undefined;
  ref?: React.Ref<HTMLButtonElement>;
} & ButtonHTMLAttributes<HTMLButtonElement>;

export function Button({
  variant = "primary",
  block,
  icon: Icon,
  className,
  children,
  type = "button",
  ...rest
}: ButtonProps) {
  return (
    <button type={type} className={cx(buttonClass(variant, { block: block ?? false }), className)} {...rest}>
      {Icon && <Icon aria-hidden="true" className="size-5 shrink-0" />}
      {children}
    </button>
  );
}

/** Botão de envio; fica desabilitado e com texto de andamento enquanto envia (evita toque duplo). */
export function Submit({
  busy,
  children,
  variant = "primary",
  block,
  icon,
  busyText = "Enviando…",
}: {
  busy: boolean;
  children: ReactNode;
  variant?: ButtonVariant;
  block?: boolean;
  icon?: IconType | undefined;
  busyText?: string;
}) {
  return (
    <Button
      type="submit"
      variant={variant}
      block={block ?? false}
      icon={busy ? Spinner : icon}
      disabled={busy}
      aria-busy={busy}
    >
      {busy ? busyText : children}
    </Button>
  );
}

export function ButtonLink({
  variant = "primary",
  block,
  icon: Icon,
  className,
  children,
  ...rest
}: { variant?: ButtonVariant; block?: boolean; icon?: IconType | undefined } & LinkProps) {
  return (
    <Link className={cx(buttonClass(variant, { block: block ?? false }), className)} {...rest}>
      {Icon && <Icon aria-hidden="true" className="size-5 shrink-0" />}
      {children}
    </Link>
  );
}

function Spinner(props: LucideProps) {
  return <LoaderCircle {...props} className={cx(props.className, "animate-spin")} />;
}

/* ---------------------------------------------------------------- Campos */

const control =
  "block w-full min-h-11 rounded-lg border border-line-strong/70 bg-surface px-3 py-2 text-base text-fg " +
  "placeholder:text-fg-muted/80 aria-[invalid=true]:border-danger aria-[invalid=true]:border-2 " +
  "disabled:bg-surface-2 disabled:text-fg-muted read-only:bg-surface-2";

function useDescribedBy(hint?: string, error?: string) {
  const id = useId();
  const hintId = hint ? `${id}-dica` : undefined;
  const errorId = error ? `${id}-erro` : undefined;
  return { id, hintId, errorId, describedBy: [hintId, errorId].filter(Boolean).join(" ") || undefined };
}

function FieldShell({
  id,
  label,
  hint,
  hintId,
  error,
  errorId,
  children,
  className,
}: {
  id: string;
  label: ReactNode;
  hint?: string | undefined;
  hintId?: string | undefined;
  error?: string | undefined;
  errorId?: string | undefined;
  children: ReactNode;
  className?: string | undefined;
}) {
  return (
    <div className={cx("grid gap-1.5", className)}>
      <label htmlFor={id} className="text-sm font-semibold text-fg">
        {label}
      </label>
      {children}
      {hint && (
        <p id={hintId} className="m-0 text-sm text-fg-muted">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="m-0 flex items-start gap-1.5 text-sm font-semibold text-danger">
          <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          {error}
        </p>
      )}
    </div>
  );
}

type FieldProps = {
  label: string;
  error?: string | undefined;
  hint?: string;
  /** Texto fixo antes do valor (ex.: "R$", "fila.com/loja/"). */
  prefix?: string;
  wrapperClassName?: string;
  ref?: React.Ref<HTMLInputElement>;
} & InputHTMLAttributes<HTMLInputElement>;

/** Campo com rótulo visível, dica e erro junto ao campo (R-WEB-02). */
export function Field({ label, error, hint, prefix, wrapperClassName, className, ...input }: FieldProps) {
  const { id, hintId, errorId, describedBy } = useDescribedBy(hint, error);
  const el = (
    <input
      id={id}
      aria-invalid={error ? true : undefined}
      aria-describedby={describedBy}
      className={cx(control, prefix && "rounded-l-none", className)}
      {...input}
    />
  );
  return (
    <FieldShell
      id={id}
      label={label}
      hint={hint}
      hintId={hintId}
      error={error}
      errorId={errorId}
      className={wrapperClassName}
    >
      {prefix ? (
        <div className="flex min-w-0">
          <span className="inline-flex max-w-[55%] shrink-0 items-center truncate rounded-l-lg border border-r-0 border-line-strong/70 bg-surface-2 px-3 text-sm text-fg-muted">
            {prefix}
          </span>
          <div className="min-w-0 flex-1">{el}</div>
        </div>
      ) : (
        el
      )}
    </FieldShell>
  );
}

/** Senha com botão de mostrar/ocultar (o botão não envia o formulário). */
export function PasswordField(props: Omit<FieldProps, "type" | "prefix">) {
  const [visible, setVisible] = useState(false);
  const { label, error, hint, wrapperClassName, className, ...input } = props;
  const { id, hintId, errorId, describedBy } = useDescribedBy(hint, error);
  return (
    <FieldShell
      id={id}
      label={label}
      hint={hint}
      hintId={hintId}
      error={error}
      errorId={errorId}
      className={wrapperClassName}
    >
      <div className="relative">
        <input
          id={id}
          type={visible ? "text" : "password"}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={cx(control, "pr-12", className)}
          {...input}
        />
        <button
          type="button"
          className="absolute inset-y-0 right-0 inline-flex w-11 items-center justify-center rounded-r-lg text-fg-muted hover:text-fg"
          aria-label={visible ? "Ocultar senha" : "Mostrar senha"}
          aria-pressed={visible}
          onClick={() => setVisible((v) => !v)}
        >
          {visible ? (
            <EyeOff aria-hidden="true" className="size-5" />
          ) : (
            <Eye aria-hidden="true" className="size-5" />
          )}
        </button>
      </div>
    </FieldShell>
  );
}

type TextAreaProps = {
  label: string;
  error?: string | undefined;
  hint?: string;
  wrapperClassName?: string;
} & TextareaHTMLAttributes<HTMLTextAreaElement>;

export function TextArea({ label, error, hint, wrapperClassName, className, ...rest }: TextAreaProps) {
  const { id, hintId, errorId, describedBy } = useDescribedBy(hint, error);
  const max = rest.maxLength;
  const length = typeof rest.value === "string" ? rest.value.length : 0;
  return (
    <FieldShell
      id={id}
      label={label}
      hint={hint}
      hintId={hintId}
      error={error}
      errorId={errorId}
      className={wrapperClassName}
    >
      <textarea
        id={id}
        rows={3}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={cx(control, "min-h-24 resize-y", className)}
        {...rest}
      />
      {max && max <= 500 && (
        <span className="justify-self-end text-xs tabular-nums text-fg-muted" aria-hidden="true">
          {length}/{max}
        </span>
      )}
    </FieldShell>
  );
}

type SelectProps = {
  label: string;
  error?: string | undefined;
  hint?: string;
  wrapperClassName?: string;
} & SelectHTMLAttributes<HTMLSelectElement>;

export function SelectField({
  label,
  error,
  hint,
  wrapperClassName,
  className,
  children,
  ...rest
}: SelectProps) {
  const { id, hintId, errorId, describedBy } = useDescribedBy(hint, error);
  return (
    <FieldShell
      id={id}
      label={label}
      hint={hint}
      hintId={hintId}
      error={error}
      errorId={errorId}
      className={wrapperClassName}
    >
      <select
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={cx(control, "pr-8", className)}
        {...rest}
      >
        {children}
      </select>
    </FieldShell>
  );
}

/* ---------------------------------------------------------------- Escolhas */

type ChoiceProps = {
  type: "radio" | "checkbox";
  label: ReactNode;
  description?: ReactNode;
  trailing?: ReactNode;
  /** "card": linha com borda (seleção principal); "plain": linha simples dentro de um grupo. */
  look?: "card" | "plain";
} & Omit<InputHTMLAttributes<HTMLInputElement>, "type">;

/**
 * Opção de rádio/caixa com o rótulo inteiro clicável (alvo ≥ 44 px). Selecionado = fundo roxo
 * suave + borda + a própria marca do controle (nunca só a cor).
 */
export function Choice({
  type,
  label,
  description,
  trailing,
  look = "card",
  className,
  ...input
}: ChoiceProps) {
  return (
    <label
      className={cx(
        "flex min-h-11 cursor-pointer items-start gap-3 text-fg",
        look === "card" &&
          "rounded-lg border border-line bg-surface px-3 py-2.5 has-checked:border-primary has-checked:bg-select",
        look === "plain" && "py-2",
        "has-disabled:cursor-not-allowed has-disabled:opacity-60 has-focus-visible:outline-3 has-focus-visible:outline-offset-2 has-focus-visible:outline-focus",
        className,
      )}
    >
      <input
        type={type}
        className="mt-0.5 size-5 shrink-0 accent-primary focus-visible:outline-none"
        {...input}
      />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="font-medium">{label}</span>
        {description && <span className="text-sm text-fg-muted">{description}</span>}
      </span>
      {trailing && <span className="shrink-0 font-semibold tabular-nums">{trailing}</span>}
    </label>
  );
}

export function ChoiceGroup({
  legend,
  hint,
  children,
  className,
  disabled,
}: {
  legend: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
  className?: string;
  disabled?: boolean;
}) {
  return (
    <fieldset className={cx("m-0 grid min-w-0 gap-2 border-0 p-0", className)} disabled={disabled}>
      <legend className="mb-2 p-0 text-sm font-semibold text-fg">{legend}</legend>
      {hint && <p className="-mt-1 mb-1 text-sm text-fg-muted">{hint}</p>}
      {children}
    </fieldset>
  );
}

/** Controle segmentado (rádios com aparência de abas), ex.: Cliente | Equipe da barbearia. */
export function Segmented<T extends string>({
  legend,
  name,
  value,
  options,
  onChange,
}: {
  legend: string;
  name: string;
  value: T;
  options: { value: T; label: string; icon?: IconType }[];
  onChange: (v: T) => void;
}) {
  return (
    <fieldset className="m-0 min-w-0 border-0 p-0">
      <legend className="mb-2 p-0 text-sm font-semibold">{legend}</legend>
      <div className="grid grid-flow-col auto-cols-fr gap-1 rounded-xl border border-line bg-surface-2 p-1">
        {options.map((o) => (
          <label
            key={o.value}
            className={cx(
              "relative flex min-h-11 cursor-pointer flex-col items-center justify-center gap-0.5 rounded-lg px-2 py-1.5 text-center text-sm font-semibold",
              "text-fg-muted has-checked:bg-primary has-checked:text-on-primary",
              "has-focus-visible:outline-3 has-focus-visible:outline-offset-2 has-focus-visible:outline-focus",
            )}
          >
            <input
              type="radio"
              name={name}
              value={o.value}
              checked={value === o.value}
              onChange={() => onChange(o.value)}
              className="absolute inset-0 m-0 size-full cursor-pointer opacity-0"
            />
            {o.icon && <o.icon aria-hidden="true" className="size-5" />}
            {o.label}
          </label>
        ))}
      </div>
    </fieldset>
  );
}

/* ---------------------------------------------------------------- Avisos e estados */

type NoticeKind = "ok" | "erro" | "info" | "aviso";

const noticeStyle: Record<NoticeKind, { box: string; icon: IconType }> = {
  ok: { box: "border-success/40 bg-success-soft", icon: CircleCheck },
  erro: { box: "border-danger/40 bg-danger-soft", icon: CircleAlert },
  info: { box: "border-line bg-surface-2", icon: Info },
  aviso: { box: "border-warning/40 bg-warning-soft", icon: TriangleAlert },
};
const noticeIconColor: Record<NoticeKind, string> = {
  ok: "text-success",
  erro: "text-danger",
  info: "text-accent",
  aviso: "text-warning",
};

/** Aviso com ícone e texto (estado nunca só por cor). Erros são anunciados ao leitor de tela. */
export function Notice({
  kind,
  title,
  children,
  className,
  live = true,
}: {
  kind: NoticeKind;
  title?: ReactNode;
  children?: ReactNode;
  className?: string;
  /** false: não é região viva (ex.: aviso fixo que não deve ser repetido pelo leitor). */
  live?: boolean;
}) {
  const s = noticeStyle[kind];
  const Icon = s.icon;
  return (
    <div
      className={cx("flex gap-3 rounded-xl border px-4 py-3 text-fg", s.box, className)}
      role={live ? (kind === "erro" ? "alert" : "status") : undefined}
    >
      <Icon aria-hidden="true" className={cx("mt-0.5 size-5 shrink-0", noticeIconColor[kind])} />
      <div className="grid min-w-0 flex-1 gap-1 [&_p]:m-0">
        {title && <p className="font-semibold">{title}</p>}
        {children}
      </div>
    </div>
  );
}

export function Loading({ children = "Carregando…" }: { children?: ReactNode }) {
  return (
    <p role="status" className="m-0 flex items-center gap-2 py-6 text-fg-muted">
      <LoaderCircle aria-hidden="true" className="size-5 animate-spin" />
      {children}
    </p>
  );
}

/** Erro de leitura com ação de recuperação. */
export function LoadError({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <Notice kind="erro">
      <p>{message}</p>
      {onRetry && (
        <p>
          <Button variant="secondary" icon={RefreshCw} onClick={onRetry} className="mt-1">
            Tentar de novo
          </Button>
        </p>
      )}
    </Notice>
  );
}

export function EmptyState({
  icon: Icon,
  title,
  children,
  action,
  className,
}: {
  icon?: IconType;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("grid justify-items-center gap-2 px-4 py-8 text-center", className)}>
      {Icon && (
        <span className="mb-1 inline-flex size-12 items-center justify-center rounded-2xl bg-surface-2 text-fg-muted">
          <Icon aria-hidden="true" className="size-6" />
        </span>
      )}
      <p className="m-0 font-semibold text-fg">{title}</p>
      {children && <div className="m-0 max-w-prose text-sm text-fg-muted [&_p]:m-0">{children}</div>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}

/* ---------------------------------------------------------------- Estrutura */

export function Card({
  children,
  className,
  as: Tag = "div",
  ...rest
}: {
  children: ReactNode;
  className?: string | undefined;
  as?: "div" | "section" | "li" | "article";
} & React.HTMLAttributes<HTMLElement>) {
  return (
    <Tag className={cx("rounded-xl border border-line bg-surface", className)} {...rest}>
      {children}
    </Tag>
  );
}

/** Título de página (h1) com descrição curta, voltar e ações. */
export function PageHeader({
  title,
  description,
  back,
  actions,
  id,
}: {
  title: ReactNode;
  description?: ReactNode;
  back?: { to: string; label: string };
  actions?: ReactNode;
  id?: string;
}) {
  return (
    <header className="mb-5 grid gap-1">
      {back && (
        <Link
          to={back.to}
          className="-ml-2 mb-1 inline-flex min-h-11 w-fit items-center gap-1 rounded-lg px-2 text-sm font-semibold text-fg-muted no-underline hover:text-fg"
        >
          <span aria-hidden="true">←</span> {back.label}
        </Link>
      )}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h1 id={id} className="m-0 text-2xl font-bold tracking-tight sm:text-[1.75rem]">
          {title}
        </h1>
        {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
      </div>
      {description && <p className="m-0 max-w-prose text-fg-muted">{description}</p>}
    </header>
  );
}

/** Seção com título (h2 por padrão) e ação opcional à direita. */
export function Section({
  title,
  description,
  action,
  children,
  level = 2,
  className,
  id: givenId,
}: {
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  level?: 2 | 3;
  className?: string;
  id?: string;
}) {
  const autoId = useId();
  const id = givenId ?? `${autoId}-titulo`;
  const H = level === 2 ? "h2" : "h3";
  return (
    <section aria-labelledby={id} className={cx("grid gap-3", className)}>
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div className="grid gap-0.5">
          <H id={id} className={cx("m-0 font-semibold", level === 2 ? "text-lg" : "text-base")}>
            {title}
          </H>
          {description && <p className="m-0 text-sm text-fg-muted">{description}</p>}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

type Tone = "neutral" | "primary" | "success" | "warning" | "danger";
const toneClass: Record<Tone, string> = {
  neutral: "bg-surface-2 text-fg-muted border-line",
  primary: "bg-select text-accent border-select-line/60",
  success: "bg-success-soft text-success border-success/30",
  warning: "bg-warning-soft text-warning border-warning/30",
  danger: "bg-danger-soft text-danger border-danger/30",
};

/** Etiqueta de estado: sempre com texto (o ícone ou ponto só reforça). */
export function Badge({
  tone = "neutral",
  icon: Icon,
  dot,
  children,
  className,
}: {
  tone?: Tone;
  icon?: IconType;
  dot?: boolean;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cx(
        "inline-flex w-fit max-w-full items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-semibold",
        toneClass[tone],
        className,
      )}
    >
      {Icon && <Icon aria-hidden="true" className="size-3.5" />}
      {dot && <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />}
      {children}
    </span>
  );
}

/** Linha "rótulo … valor" com números tabulares (itens de comanda, totais). */
export function AmountRow({
  label,
  value,
  sub,
  strong,
  muted,
  className,
}: {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  strong?: boolean;
  muted?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cx(
        "flex items-start justify-between gap-3 py-2.5",
        strong && "text-lg font-bold",
        muted && "text-fg-muted",
        className,
      )}
    >
      <span className="min-w-0">
        <span className={cx("block", !strong && "font-medium")}>{label}</span>
        {sub && <span className="block text-sm font-normal text-fg-muted">{sub}</span>}
      </span>
      <span className="shrink-0 text-right tabular-nums whitespace-nowrap">{value}</span>
    </div>
  );
}

export function Avatar({ name, className }: { name: string; className?: string }) {
  const initials =
    name
      .trim()
      .split(/\s+/)
      .slice(0, 2)
      .map((p) => p[0]?.toUpperCase() ?? "")
      .join("") || "?";
  return (
    <span
      aria-hidden="true"
      className={cx(
        "inline-flex size-10 shrink-0 items-center justify-center rounded-full bg-select font-semibold text-accent",
        className,
      )}
    >
      {initials}
    </span>
  );
}

/** Botão de copiar com retorno anunciado ("Copiado") ao leitor de tela. */
export function CopyButton({
  text,
  label = "Copiar",
  copiedText = "Copiado.",
  variant = "secondary",
  block,
}: {
  text: string;
  label?: string;
  copiedText?: string;
  variant?: ButtonVariant;
  block?: boolean;
}) {
  const [state, setState] = useState<"" | "ok" | "erro">("");
  useEffect(() => {
    if (state !== "ok") return;
    const t = setTimeout(() => setState(""), 4000);
    return () => clearTimeout(t);
  }, [state]);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setState("ok");
    } catch {
      setState("erro");
    }
  }
  return (
    <div className={cx("grid gap-1", block ? "w-full" : "w-fit")}>
      <Button
        variant={variant}
        block={block ?? false}
        icon={state === "ok" ? Check : Copy}
        onClick={() => void copy()}
      >
        {label}
      </Button>
      <span role="status" aria-live="polite" className="text-sm text-fg-muted">
        {state === "ok" && copiedText}
        {state === "erro" && "Não foi possível copiar. Selecione o texto e copie manualmente."}
      </span>
    </div>
  );
}

/* ---------------------------------------------------------------- Foco */

/**
 * Confirmação no lugar para ação com consequência: diz o que vai acontecer e nomeia o botão com
 * a operação real. Usar junto com useInlinePanel (ref/onKeyDown).
 */
export function ConfirmPanel({
  title,
  children,
  confirmLabel,
  cancelLabel = "Voltar",
  variant = "primary",
  busy,
  onConfirm,
  onCancel,
  panelId,
  onKeyDown,
}: {
  title: string;
  children?: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  variant?: ButtonVariant;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  panelId?: string;
  onKeyDown?: (e: React.KeyboardEvent) => void;
}) {
  const id = useId();
  return (
    <div
      id={panelId}
      role="group"
      aria-labelledby={id}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      className={cx(
        "grid gap-3 rounded-xl border p-4",
        variant === "danger" ? "border-danger/40 bg-danger-soft" : "border-line bg-surface-2",
      )}
    >
      <p id={id} className="m-0 font-semibold" data-autofocus tabIndex={-1}>
        {title}
      </p>
      {children && <div className="grid gap-2 text-sm text-fg-muted [&_p]:m-0">{children}</div>}
      <div className="flex flex-wrap gap-2">
        <Button variant={variant} disabled={busy} aria-busy={busy} onClick={onConfirm}>
          {busy ? "Enviando…" : confirmLabel}
        </Button>
        <Button variant="secondary" onClick={onCancel}>
          {cancelLabel}
        </Button>
      </div>
    </div>
  );
}
