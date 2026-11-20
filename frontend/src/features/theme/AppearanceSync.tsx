import { useEffect } from "react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import { useSession } from "../../session/context";
import { applyAppearance } from "./tema";

/**
 * Ao entrar numa conta, aplica a aparência guardada nela (claro, escuro ou do sistema). Sem conta,
 * vale a escolha deste aparelho (localStorage, aplicada antes do primeiro desenho por /tema.js).
 */
export function AppearanceSync() {
  const { state } = useSession();
  const accountId = state.status === "ready" ? state.session.account?.id : undefined;
  useEffect(() => {
    if (!accountId) return;
    let active = true;
    void call(() => api.GET("/v1/me")).then((out) => {
      if (active && out.ok) applyAppearance(out.data.data.appearance);
    });
    return () => {
      active = false;
    };
  }, [accountId]);
  return null;
}
