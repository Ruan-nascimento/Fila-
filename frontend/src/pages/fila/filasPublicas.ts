import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { useStreamMessages, Versions } from "../../stream/context";

type PublicQueue = components["schemas"]["PublicQueue"];

export type PublicQueuesState = {
  /** null = ainda não se sabe (carregando ou falhou). [] = o servidor confirmou que não há filas. */
  queues: PublicQueue[] | null;
  error: string | null;
  retry: () => void;
};

/**
 * Filas públicas da loja: lidas uma vez e depois mantidas pelo fluxo SSE (snapshot e
 * queue.public.updated), sem consulta periódica. "Desconhecido" nunca vira lista vazia: quem usa
 * mostra carregando (ou o erro com nova tentativa) até ter o resultado próprio das filas.
 */
export function usePublicQueues(shopId: string): PublicQueuesState {
  const [queues, setQueues] = useState<PublicQueue[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const versions = useRef(new Versions());
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/queues", {
        params: { path: { establishmentId: shopId } },
      }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) {
        setQueues((cur) => cur ?? out.data.data);
        setError(null);
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [shopId, attempt]);
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      const list = (m.data as { public_queues?: PublicQueue[] }).public_queues;
      if (list) {
        versions.current.reset();
        setQueues(list);
        setError(null);
      }
    } else if (
      m.type === "queue.public.updated" &&
      versions.current.newer(m.resource.id, m.resource.version)
    ) {
      const q = m.data as PublicQueue | null;
      // Carga incompleta não entra na lista (a tela usa nome e situação de cada fila).
      if (!q?.queue_id || !q.professional) return;
      // Evento avulso antes da primeira leitura completa: guarda, mas a lista ainda não é "a lista".
      setQueues((list) => {
        if (list === null) return list;
        return list.some((x) => x.queue_id === q.queue_id)
          ? list.map((x) => (x.queue_id === q.queue_id ? q : x))
          : [...list, q];
      });
    }
  });
  const retry = useCallback(() => {
    setError(null);
    setAttempt((n) => n + 1);
  }, []);
  return { queues, error, retry };
}
