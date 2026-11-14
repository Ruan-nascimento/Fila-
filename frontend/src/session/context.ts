import { createContext, useContext } from "react";
import type { components } from "../api/schema.gen";

export type SessionView = components["schemas"]["SessionView"];

export type SessionState =
  { status: "loading" } | { status: "ready"; session: SessionView } | { status: "offline" };

export type SessionContextValue = {
  state: SessionState;
  /** Relê GET /v1/session (identidades deste navegador e novo token CSRF). */
  refresh: () => Promise<void>;
  /** A sessão de conta terminou durante o uso (401 numa ação); vale até entrar de novo. */
  expired: boolean;
  markExpired: () => void;
};

export const SessionContext = createContext<SessionContextValue | null>(null);

export function useSession(): SessionContextValue {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error("useSession fora de SessionProvider");
  return ctx;
}
