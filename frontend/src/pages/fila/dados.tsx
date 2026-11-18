import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { Ctx, type StoreData } from "./dadosContexto";
import { usePublicQueues } from "./filasPublicas";
import { reconcile, type PublicService, type Selection } from "./selecao";

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

export function StoreDataProvider({ shopId, children }: { shopId: string; children: ReactNode }) {
  const q = usePublicQueues(shopId);
  const s = usePublicServices(shopId);
  const [selection, setSelectionRaw] = useState<Selection>({ queueId: "", serviceIds: [] });
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
      selection,
      setSelection,
      selectionNote,
      clearSelectionNote,
    ],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
