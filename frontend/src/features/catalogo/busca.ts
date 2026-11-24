import { useEffect, useState } from "react";

/**
 * Normalização da busca (ADR-0036), igual à do servidor (platform/busca): minúsculas, sem acento,
 * espaços repetidos e das pontas removidos.
 */
export function fold(s: string): string {
  return s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().replace(/\s+/g, " ").trim();
}

/** Todos os termos da busca aparecem no texto (ordem livre: "barba corte" acha "Corte e barba"). */
export function matches(text: string, query: string): boolean {
  const q = fold(query);
  if (!q) return true;
  const t = fold(text);
  return q.split(" ").every((part) => t.includes(part));
}

/** Valor com atraso (~250 ms) para buscas remotas: digitar não dispara uma requisição por tecla. */
export function useDebounced<T>(value: T, ms = 250): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(id);
  }, [value, ms]);
  return v;
}

export type Ordenavel = { name: string; display_order?: number | undefined; id: string };

/** Ordem estável do catálogo: posição, nome (sem diferenciar maiúsculas e acentos), id. */
export function byDisplay<T extends Ordenavel>(a: T, b: T): number {
  return (
    (a.display_order ?? 0) - (b.display_order ?? 0) ||
    fold(a.name).localeCompare(fold(b.name), "pt-BR") ||
    a.id.localeCompare(b.id)
  );
}
