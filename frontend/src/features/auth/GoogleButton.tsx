import { useState } from "react";
import { api } from "../../api/client";
import { call, problemMessage, type Problem } from "../../api/problem";

/** Logotipo "G" oficial (quatro cores), sem alteração de cor, como pedem as diretrizes do Google. */
function GoogleG() {
  return (
    <svg aria-hidden="true" viewBox="0 0 48 48" className="size-5 shrink-0">
      <path
        fill="#EA4335"
        d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"
      />
      <path
        fill="#4285F4"
        d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"
      />
      <path
        fill="#FBBC05"
        d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"
      />
      <path
        fill="#34A853"
        d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"
      />
    </svg>
  );
}

export type GoogleIntent =
  { kind: "login"; establishmentSlug?: string | undefined; returnTo?: string | undefined } | { kind: "link" };

/**
 * Botão "Continuar com o Google" (só na jornada de cliente: nunca no acesso da equipe, do dono ou
 * no convite). Visual fixo das diretrizes do Google (claro e escuro), sem a cor da loja. Não
 * recebe segredo nenhum: pede ao servidor a URL de autorização e navega para ela.
 */
export function GoogleButton({
  intent,
  label = "Continuar com o Google",
  onProblem,
}: {
  intent: GoogleIntent;
  label?: string;
  /** Erros que a tela trata (ex.: reautenticação para conectar). Os demais aparecem aqui. */
  onProblem?: (p: Problem | null) => boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function start() {
    setBusy(true);
    setError(null);
    const body =
      intent.kind === "link"
        ? { intent: "link" as const }
        : {
            intent: "login" as const,
            ...(intent.establishmentSlug ? { establishment_slug: intent.establishmentSlug } : {}),
            ...(intent.returnTo ? { return_to: intent.returnTo } : {}),
          };
    const out = await call(() => api.POST("/v1/auth/google/start", { body }));
    if (out.ok) {
      window.location.assign(out.data.data.authorization_url);
      return; // a página vai para o Google; o botão continua ocupado
    }
    setBusy(false);
    if (onProblem?.(out.problem)) return;
    setError(problemMessage(out.problem, out.offline));
  }

  return (
    <div className="grid gap-2">
      <button
        type="button"
        onClick={() => void start()}
        disabled={busy}
        aria-busy={busy}
        className="inline-flex min-h-11 w-full items-center justify-center gap-3 rounded-full border border-[#747775] bg-white px-4 text-sm font-medium text-[#1f1f1f] hover:bg-[#f2f2f2] disabled:opacity-60 dark:border-[#8e918f] dark:bg-[#131314] dark:text-[#e3e3e3] dark:hover:bg-[#1f1f20]"
      >
        <GoogleG />
        {busy ? "Abrindo o Google…" : label}
      </button>
      {error && (
        <p role="alert" className="m-0 text-sm text-danger">
          {error}
        </p>
      )}
    </div>
  );
}
