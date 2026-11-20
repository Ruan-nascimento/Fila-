import { useEffect, useState } from "react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import type { components } from "../../api/schema.gen";

export type PublicEstablishment = components["schemas"]["PublicEstablishment"];

/** Formato do endereço público da loja (contrato: Slug). */
export const SLUG = /^[a-z0-9](?:[a-z0-9-]{1,48}[a-z0-9])$/;

/** Loja de onde a pessoa veio: `?loja=slug` ou um `voltar` interno para /loja/slug. */
export function slugFromContext(loja: string | null, voltar: string | null): string | null {
  if (loja && SLUG.test(loja)) return loja;
  const m = voltar?.match(/^\/loja\/([a-z0-9-]+)(?:\/|$)/);
  return m?.[1] && SLUG.test(m[1]) ? m[1] : null;
}

/**
 * Lê o que a pessoa digitou (link do QR, endereço ou só o código da loja) e devolve o slug.
 * Nunca adivinha: texto que não forma um endereço válido devolve null.
 */
export function slugFromInput(raw: string): string | null {
  const t = raw.trim();
  if (!t) return null;
  const m = t.match(/\/loja\/([a-z0-9-]+)/i);
  const candidate = (m?.[1] ?? t).toLowerCase();
  return SLUG.test(candidate) ? candidate : null;
}

/** Destinos tipados do QR e dos links (ADR-0028). Nenhum segredo nem dado pessoal. */
export type AccessTarget = "loja" | "visitante" | "conta";

/** Monta a URL de acesso a partir da origem pública canônica e do slug (URL/URLSearchParams). */
export function accessUrl(origin: string, slug: string, target: AccessTarget): string {
  if (!SLUG.test(slug)) throw new Error("slug inválido");
  if (target === "conta") {
    const u = new URL("/entrar", origin);
    u.searchParams.set("loja", slug);
    u.searchParams.set("voltar", `/loja/${slug}`);
    return u.toString();
  }
  const u = new URL(`/loja/${slug}`, origin);
  if (target === "visitante") u.searchParams.set("entrada", "visitante");
  return u.toString();
}

/** Dados públicos da loja do contexto (para o login/cadastro mostrar nome, logo e cor). */
export function usePublicShop(slug: string | null) {
  const [got, setGot] = useState<{ slug: string; shop: PublicEstablishment | null; missing: boolean } | null>(
    null,
  );
  useEffect(() => {
    if (!slug) return;
    let active = true;
    void call(() => api.GET("/v1/public/slugs/{slug}", { params: { path: { slug } } })).then((out) => {
      if (!active) return;
      if (out.ok) setGot({ slug, shop: out.data.data, missing: false });
      else setGot({ slug, shop: null, missing: out.problem?.status === 404 });
    });
    return () => {
      active = false;
    };
  }, [slug]);
  // Resultado de outra loja (troca de contexto) não vale para esta.
  const cur = got && got.slug === slug ? got : null;
  return { shop: cur?.shop ?? null, missing: cur?.missing ?? false };
}

/** +5511999998888 → (11) 99999-8888; outros países ficam como vieram. */
export function formatPhone(e164: string): string {
  const m = e164.match(/^\+55(\d{2})(\d{4,5})(\d{4})$/);
  return m ? `(${m[1]}) ${m[2]}-${m[3]}` : e164;
}
