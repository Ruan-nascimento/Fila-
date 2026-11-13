import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { useStreamMessages, Versions } from "../../stream/context";

type PublicQueue = components["schemas"]["PublicQueue"];

/**
 * Filas públicas da loja: lidas uma vez e depois mantidas pelo fluxo SSE (snapshot e
 * queue.public.updated), sem consulta periódica.
 */
export function usePublicQueues(shopId: string): PublicQueue[] | null {
  const [queues, setQueues] = useState<PublicQueue[] | null>(null);
  const versions = useRef(new Versions());
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/queues", {
        params: { path: { establishmentId: shopId } },
      }),
    ).then((out) => {
      if (active && out.ok) setQueues((cur) => cur ?? out.data.data);
    });
    return () => {
      active = false;
    };
  }, [shopId]);
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      const list = (m.data as { public_queues?: PublicQueue[] }).public_queues;
      if (list) {
        versions.current.reset();
        setQueues(list);
      }
    } else if (
      m.type === "queue.public.updated" &&
      versions.current.newer(m.resource.id, m.resource.version)
    ) {
      const q = m.data as PublicQueue;
      setQueues((list) => {
        const cur = list ?? [];
        return cur.some((x) => x.queue_id === q.queue_id)
          ? cur.map((x) => (x.queue_id === q.queue_id ? q : x))
          : [...cur, q];
      });
    }
  });
  return queues;
}
