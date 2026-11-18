import { useState, type FormEvent, type ReactNode } from "react";
import { api } from "../../api/client";
import { call, problemMessage, type Problem } from "../../api/problem";
import { SessaoExpirada } from "../../ui/estados";
import { Notice, PasswordField, Submit } from "../../ui/ui";

export type StaffContext = {
  estId: string;
  estName: string;
  isOwner: boolean;
  professionalId: string | null;
};

/**
 * Mostra o erro de um comando. Se o servidor pedir confirmação de senha (x-reauth), pede a senha
 * e repete a ação (com a mesma chave de idempotência).
 */
export function CommandError({
  problem,
  offline,
  onRetry,
}: {
  problem: Problem | null;
  offline: boolean;
  onRetry?: (() => void) | undefined;
}): ReactNode {
  if (!problem && !offline) return null;
  if (problem?.code === "reauthentication_required" && onRetry) return <Reauth onDone={onRetry} />;
  if (problem?.code === "unauthenticated") return <SessaoExpirada />;
  return (
    <Notice kind="erro">
      <p>{problemMessage(problem, offline)}</p>
    </Notice>
  );
}

function Reauth({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    // Este formulário pode aparecer dentro do formulário da ação; o envio não pode subir até ele
    // (senão a ação seria enviada duas vezes: aqui, por onDone, e pelo formulário de fora).
    e.stopPropagation();
    setBusy(true);
    const out = await call(() => api.POST("/v1/auth/reauthenticate", { body: { password } }));
    setBusy(false);
    if (out.ok) {
      setPassword("");
      onDone();
    } else {
      setError(problemMessage(out.problem, out.offline));
    }
  }
  return (
    <form
      onSubmit={onSubmit}
      noValidate
      className="grid gap-3 rounded-xl border border-primary/40 bg-select p-4"
      aria-labelledby="titulo-reauth"
    >
      <h3 id="titulo-reauth" className="m-0 text-base font-semibold">
        Confirme sua senha
      </h3>
      <p className="m-0 text-sm text-fg-muted">
        Por segurança, esta ação exige sua senha de novo (vale por 10 minutos).
      </p>
      {error && (
        <Notice kind="erro">
          <p>{error}</p>
        </Notice>
      )}
      <PasswordField
        label="Sua senha"
        autoComplete="current-password"
        required
        autoFocus
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      <div>
        <Submit busy={busy}>Confirmar e continuar</Submit>
      </div>
    </form>
  );
}
