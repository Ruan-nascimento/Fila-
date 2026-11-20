import { useEffect } from "react";
import type { components } from "../../api/schema.gen";
import { paletteVars } from "./paletas";

export type Appearance = components["schemas"]["Appearance"];

/** Chave da preferência neste aparelho (conveniência: o servidor guarda a da conta). */
export const APPEARANCE_KEY = "fila-aparencia";

/** Aplica a preferência: claro/escuro explícitos ou, em "system", segue o sistema operacional. */
export function applyAppearance(a: Appearance) {
  const root = document.documentElement;
  if (a === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", a);
  try {
    if (a === "system") localStorage.removeItem(APPEARANCE_KEY);
    else localStorage.setItem(APPEARANCE_KEY, a);
  } catch {
    // armazenamento bloqueado (janela privada): vale só nesta página
  }
}

/** Preferência guardada neste aparelho ("system" quando não há). */
export function storedAppearance(): Appearance {
  try {
    const v = localStorage.getItem(APPEARANCE_KEY);
    return v === "light" || v === "dark" ? v : "system";
  } catch {
    return "system";
  }
}

const BRAND_EVENT = "fila:identidade-da-loja";

/** Avisa as telas abertas desta aba que a identidade de uma loja mudou (depois de salvar). */
export function notifyBrandChanged(estId: string) {
  window.dispatchEvent(new CustomEvent(BRAND_EVENT, { detail: estId }));
}

/** Escuta mudanças de identidade (recarrega a paleta/logo da loja em uso). */
export function useBrandChanged(estId: string, onChange: () => void) {
  useEffect(() => {
    const h = (e: Event) => {
      if ((e as CustomEvent<string>).detail === estId) onChange();
    };
    window.addEventListener(BRAND_EVENT, h);
    return () => window.removeEventListener(BRAND_EVENT, h);
  }, [estId, onChange]);
}

/**
 * Aplica a paleta da loja à página inteira (inclusive modais e avisos, que ficam no mesmo
 * documento). Ao sair do contexto da loja ou trocar de loja, os tokens são limpos: nada vaza para
 * outra loja nem para as telas globais (login global e "Minha conta" usam a identidade padrão).
 */
export function useStoreBrand(palette: string | null | undefined) {
  useEffect(() => {
    if (!palette) return;
    const root = document.documentElement;
    const vars = paletteVars(palette);
    for (const [k, v] of Object.entries(vars)) root.style.setProperty(k, v);
    root.setAttribute("data-brand", palette);
    return () => {
      for (const k of Object.keys(vars)) root.style.removeProperty(k);
      root.removeAttribute("data-brand");
    };
  }, [palette]);
}
