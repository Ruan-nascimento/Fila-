import { useCallback, useRef, useState } from "react";
import { call, type Outcome, type Problem } from "../../api/problem";

/** Chave de idempotência nova (16–128 caracteres [A-Za-z0-9_-]). */
export function newIdempotencyKey(): string {
  return crypto.randomUUID().replaceAll("-", "");
}

type Fetcher<T> = (idempotencyKey: string) => Promise<{ data?: T; error?: unknown; response: Response }>;

/**
 * Executa comandos com Idempotency-Key por intenção (ADR-0009): a mesma intenção reenviada
 * (ex.: rede caiu, clique duplo) usa a mesma chave, então o servidor não aplica duas vezes.
 * A chave é descartada quando o resultado é definitivo (sucesso ou erro de regra).
 */
export function useCommand() {
  const keys = useRef(new Map<string, string>());
  const [busy, setBusy] = useState<string | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [offline, setOffline] = useState(false);

  const run = useCallback(async <T>(intent: string, fetcher: Fetcher<T>): Promise<Outcome<T>> => {
    const key = keys.current.get(intent) ?? newIdempotencyKey();
    keys.current.set(intent, key);
    setBusy(intent);
    setProblem(null);
    setOffline(false);
    const out = await call(() => fetcher(key));
    setBusy(null);
    const retryable =
      !out.ok &&
      (out.offline ||
        (out.problem?.status ?? 0) >= 500 ||
        out.problem?.code === "idempotency_in_progress" ||
        // Depois de confirmar a senha, a mesma intenção é repetida com a mesma chave.
        out.problem?.code === "reauthentication_required");
    if (!retryable) keys.current.delete(intent);
    if (!out.ok) {
      setProblem(out.problem);
      setOffline(out.offline);
    }
    return out;
  }, []);

  const clear = useCallback(() => {
    setProblem(null);
    setOffline(false);
  }, []);

  return { run, busy, problem, offline, clear };
}
