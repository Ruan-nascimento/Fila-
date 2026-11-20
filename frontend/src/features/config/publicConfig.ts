import { useEffect, useState } from "react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import type { components } from "../../api/schema.gen";

export type PublicConfig = components["schemas"]["PublicConfig"];

let cache: Promise<PublicConfig | null> | null = null;

/** Lê (uma vez por página) o que está ligado no servidor: Google, fotos e a origem pública. */
export function loadPublicConfig(): Promise<PublicConfig | null> {
  cache ??= call(() => api.GET("/v1/public/config")).then((out) => {
    if (out.ok) return out.data.data;
    cache = null; // falha de rede: tenta de novo depois
    return null;
  });
  return cache;
}

/** Só para testes: esquece a configuração guardada. */
export function resetPublicConfig() {
  cache = null;
}

/** null enquanto carrega ou se o servidor não respondeu (Google e fotos ficam escondidos). */
export function usePublicConfig(): PublicConfig | null {
  const [cfg, setCfg] = useState<PublicConfig | null>(null);
  useEffect(() => {
    let active = true;
    void loadPublicConfig().then((c) => {
      if (active) setCfg(c);
    });
    return () => {
      active = false;
    };
  }, []);
  return cfg;
}
