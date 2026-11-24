import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { Ctx, type StoreData } from "./dadosContexto";
import { usePublicQueues } from "./filasPublicas";
import {
  emptySelection,
  reconcile,
  type PublicCategory,
  type PublicProduct,
  type Ordering,
  type PublicService,
  type Selection,
} from "./selecao";

/** Sem `ordering` (API anterior): limite padrão de 10 e produtos só se houver algum ativo. */
const defaultOrdering: Ordering = {
  max_service_quantity: 10,
  products_enabled: true,
  visitors_enabled: true,
  payment_methods: [],
};

/** Produtos e categorias públicos (o catálogo não vem pelo SSE: lido ao abrir a tela). */
function usePublicExtras(shopId: string, attempt: number) {
  const [products, setProducts] = useState<PublicProduct[] | null>(null);
  const [categories, setCategories] = useState<PublicCategory[]>([]);
  useEffect(() => {
    let active = true;
    void Promise.all([
      call(() =>
        api.GET("/v1/public/establishments/{establishmentId}/products", {
          params: { path: { establishmentId: shopId } },
        }),
      ),
      call(() =>
        api.GET("/v1/public/establishments/{establishmentId}/catalog-categories", {
          params: { path: { establishmentId: shopId } },
        }),
      ),
    ]).then(([p, c]) => {
      if (!active) return;
      // Sem produtos (ou falha ao ler): o pedido segue só com serviços.
      setProducts(p.ok ? p.data.data : []);
      setCategories(c.ok ? c.data.data : []);
    });
    return () => {
      active = false;
    };
  }, [shopId, attempt]);
  return { products, categories };
}

function usePublicServices(shopId: string) {
  const [services, setServices] = useState<PublicService[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    // O catálogo de serviços não vem pelo fluxo SSE: é lido ao abrir a tela (eventos-sse.md §4).
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/services", {
        params: { path: { establishmentId: shopId } },
      }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) {
        setServices(out.data.data);
        setError(null);
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [shopId, attempt]);
  const retry = useCallback(() => {
    setError(null);
    setAttempt((n) => n + 1);
  }, []);
  return { services, error, retry };
}

export function StoreDataProvider({
  shopId,
  ordering,
  children,
}: {
  shopId: string;
  ordering?: Ordering | undefined;
  children: ReactNode;
}) {
  const q = usePublicQueues(shopId);
  const s = usePublicServices(shopId);
  const extras = usePublicExtras(shopId, 0);
  const [selection, setSelectionRaw] = useState<Selection>(emptySelection);
  const [selectionNote, setNote] = useState<string | null>(null);

  // Dados novos (tempo real) podem invalidar a escolha: corrige na mesma renderização e explica.
  const [seen, setSeen] = useState({ queues: q.queues, services: s.services });
  if (seen.queues !== q.queues || seen.services !== s.services) {
    setSeen({ queues: q.queues, services: s.services });
    const { next, note } = reconcile(selection, q.queues, s.services);
    if (next !== selection) {
      setSelectionRaw(next);
      if (note) setNote(note);
    }
  }

  const setSelection = useCallback(
    (sel: Selection) => {
      const { next, note } = reconcile(sel, q.queues, s.services);
      setSelectionRaw(next);
      setNote(note);
    },
    [q.queues, s.services],
  );
  const clearSelectionNote = useCallback(() => setNote(null), []);

  const value = useMemo<StoreData>(
    () => ({
      shopId,
      queues: q.queues,
      queuesError: q.error,
      retryQueues: q.retry,
      services: s.services,
      servicesError: s.error,
      retryServices: s.retry,
      products: extras.products,
      categories: extras.categories,
      ordering: ordering ?? defaultOrdering,
      selection,
      setSelection,
      selectionNote,
      clearSelectionNote,
    }),
    [
      shopId,
      q.queues,
      q.error,
      q.retry,
      s.services,
      s.error,
      s.retry,
      extras.products,
      extras.categories,
      ordering,
      selection,
      setSelection,
      selectionNote,
      clearSelectionNote,
    ],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
