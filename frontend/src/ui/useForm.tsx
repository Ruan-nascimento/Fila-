import { useState } from "react";
import { call, fieldErrors, problemMessage, type Problem } from "../api/problem";
import { SessaoExpirada } from "./estados";
import { Notice } from "./ui";

/** Estado comum de formulário: enviando, erro geral e erros por campo. */
export function useForm() {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [offline, setOffline] = useState(false);
  async function run<T>(fn: () => Promise<{ data?: T; error?: unknown; response: Response }>) {
    setBusy(true);
    setProblem(null);
    setOffline(false);
    const out = await call(fn);
    setBusy(false);
    if (!out.ok) {
      setProblem(out.problem);
      setOffline(out.offline);
    }
    return out;
  }
  const generalError =
    problem?.code === "unauthenticated" ? (
      <SessaoExpirada />
    ) : (problem || offline) && !problem?.errors?.length ? (
      <Notice kind="erro">{problemMessage(problem, offline)}</Notice>
    ) : null;
  return { busy, run, errors: fieldErrors(problem), generalError };
}
