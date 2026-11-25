import { useCallback, useEffect, useState } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";

type MyAppointment = components["schemas"]["MyAppointment"];

/** Reservas da pessoa (todas as lojas ou só uma). Atualiza pelo fluxo da página, sem consultar em ciclo. */
export function useMinhasReservas(scope: "upcoming" | "past", shopId?: string) {
  const [list, setList] = useState<MyAppointment[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  useEffect(() => {
    let active = true;
    void call(() => api.GET("/v1/my-appointments", { params: { query: { scope } } })).then((out) => {
      if (!active) return;
      if (out.ok) {
        setList(out.data.data.filter((x) => !shopId || x.establishment.id === shopId));
        setError(null);
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [scope, shopId, attempt]);
  return { list, error, reload };
}
