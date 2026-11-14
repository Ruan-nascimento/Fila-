import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { api, setCsrfToken } from "../api/client";
import { call, type Outcome } from "../api/problem";
import { SessionContext, type SessionState, type SessionView } from "./context";

function fetchSession(): Promise<Outcome<{ data: SessionView }>> {
  return call(() => api.GET("/v1/session"));
}

/**
 * Guarda em memória quem está usando este navegador, segundo o servidor.
 * A tela nunca decide permissão: ela só esconde o que não faz sentido mostrar.
 */
export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ status: "loading" });
  const [expired, setExpired] = useState(false);

  const apply = useCallback((out: Outcome<{ data: SessionView }>) => {
    if (out.ok) {
      setCsrfToken(out.data.data.csrf_token ?? null);
      setState({ status: "ready", session: out.data.data });
      if (out.data.data.account) setExpired(false); // entrou de novo
    } else {
      setState({ status: "offline" });
    }
  }, []);

  const refresh = useCallback(async () => apply(await fetchSession()), [apply]);

  useEffect(() => {
    let active = true;
    const load = () => {
      void fetchSession().then((out) => {
        if (active) apply(out);
      });
    };
    load();
    // Ao voltar para a aba, sincroniza antes de permitir ações (R-WEB-03).
    const onVisible = () => {
      if (document.visibilityState === "visible") load();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      active = false;
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [apply]);

  const markExpired = useCallback(() => setExpired(true), []);
  const value = useMemo(
    () => ({ state, refresh, expired, markExpired }),
    [state, refresh, expired, markExpired],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
