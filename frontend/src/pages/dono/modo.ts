import type { SessionView } from "../../session/context";

/**
 * Modo dono: a janela entrou por /dono (o programa Fila Gestão sempre abre por ali). Enquanto a
 * janela existir, a gestão só oferece lojas em que a conta é dona. É só apresentação: quem
 * autoriza cada pedido continua sendo o servidor, pelo vínculo e pela sessão de equipe.
 */
const CHAVE = "fila.modo";

export function ativarModoDono(): void {
  try {
    sessionStorage.setItem(CHAVE, "dono");
  } catch {
    // Armazenamento bloqueado: /dono continua funcionando, só não lembra o modo nas outras telas.
  }
}

export function modoDono(): boolean {
  try {
    return sessionStorage.getItem(CHAVE) === "dono";
  } catch {
    return false;
  }
}

/** Lojas em que a conta tem vínculo de dono (o servidor só lista vínculos ativos). */
export function lojasDoDono(view: SessionView): { id: string; name: string }[] {
  const lojas = new Map<string, string>();
  for (const m of view.memberships)
    if (m.role === "owner") lojas.set(m.establishment_id, m.establishment_name);
  return [...lojas].map(([id, name]) => ({ id, name }));
}

/**
 * A janela é a do programa Fila Gestão (ele injeta `window.__GESTAO_DESKTOP__`). Só orienta a
 * apresentação (mostrar o atalho "Dashboard"): os dados analíticos exigem, no servidor, a prova de
 * posse da chave de uma instalação autorizada, que esta página nunca tem.
 */
export function noProgramaDesktop(): boolean {
  return modoDono() && (globalThis as { __GESTAO_DESKTOP__?: boolean }).__GESTAO_DESKTOP__ === true;
}
