// Token do link de e-mail lido do fragmento (#token=…). Guardado só em memória.
let cached: { path: string; token: string | null } | null = null;

/**
 * Lê o token do fragmento do link do e-mail (#token=…) e o apaga da barra de endereço.
 * O fragmento nunca vai ao servidor, a logs nem ao Referer (ADR-0017).
 * O resultado fica em memória por caminho, então chamadas repetidas (ex.: renderização dupla
 * do StrictMode em desenvolvimento) devolvem o mesmo token.
 */
export function takeFragmentToken(): string | null {
  const loc = globalThis.location;
  if (!loc) return null;
  const hash = loc.hash ?? "";
  if (!hash && cached?.path === loc.pathname) return cached.token;
  const raw = new URLSearchParams(hash.replace(/^#/, "")).get("token");
  if (hash) {
    globalThis.history.replaceState(globalThis.history.state, "", loc.pathname + loc.search);
  }
  const token = raw && /^[A-Za-z0-9_-]{32,128}$/.test(raw) ? raw : null;
  cached = { path: loc.pathname, token };
  return token;
}

/** Só para testes. */
export function resetFragmentCache() {
  cached = null;
}
