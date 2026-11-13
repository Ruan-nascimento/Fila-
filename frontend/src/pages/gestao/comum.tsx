import { useState, type FormEvent, type ReactNode } from "react";
import { api } from "../../api/client";
import { call, problemMessage, type Problem } from "../../api/problem";
import { Field, Notice, Submit } from "../../ui/ui";

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
  return <Notice kind="erro">{problemMessage(problem, offline)}</Notice>;
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
    <form onSubmit={onSubmit} noValidate className="card" aria-labelledby="titulo-reauth">
      <h3 id="titulo-reauth">Confirme sua senha</h3>
      <p className="muted">Por segurança, esta ação exige sua senha de novo (vale por 10 minutos).</p>
      {error && <Notice kind="erro">{error}</Notice>}
      <Field
        label="Sua senha"
        type="password"
        autoComplete="current-password"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      <Submit busy={busy}>Confirmar e continuar</Submit>
    </form>
  );
}
