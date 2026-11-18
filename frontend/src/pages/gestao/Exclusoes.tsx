import { useCallback, useEffect, useState } from "react";
import { FileClock } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useStreamMessages } from "../../stream/context";
import { Badge, EmptyState, LoadError, Loading, PageHeader } from "../../ui/ui";
import { blockerText, daysSince, metaText, type DeletionRequest } from "../exclusao/textos";
import type { StaffContext } from "./comum";

const date = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short" });

/**
 * Exclusões de conta que dependem desta loja (R-EXC-03): a dona vê o que falta resolver (sem nome
 * nem e-mail da pessoa; protocolo só pelo final) e destaca o que está perto da meta de 30 dias.
 */
export function Exclusoes({ ctx }: { ctx: StaffContext }) {
  const [list, setList] = useState<DeletionRequest[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fetchList = useCallback(
    () =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/deletion-requests", {
          params: { path: { establishmentId: ctx.estId } },
        }),
      ),
    [ctx.estId],
  );
  const reload = useCallback(async () => {
    const out = await fetchList();
    if (out.ok) {
      setList(out.data.data);
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
  }, [fetchList]);
  useEffect(() => {
    let active = true;
    void fetchList().then((out) => {
      if (!active) return;
      if (out.ok) setList(out.data.data);
      else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchList]);
  // Atualiza quando uma exclusão muda (bloqueio resolvido, concluída) ou ao reconectar.
  useStreamMessages((m) => {
    if (m.type === "deletion_request.updated" || m.type === "stream.snapshot") void reload();
  });

  return (
    <section aria-labelledby="titulo-exclusoes" className="grid gap-4">
      <PageHeader
        id="titulo-exclusoes"
        title="Exclusões de conta"
        description={`Pedidos que ainda dependem desta loja. Resolva as pendências em Filas e Comandas. ${metaText}`}
      />
      {error && <LoadError message={error} onRetry={() => void reload()} />}
      {!list && !error && <Loading>Carregando exclusões…</Loading>}
      {list?.length === 0 && <EmptyState icon={FileClock} title="Nenhuma exclusão aguardando esta loja" />}
      {list && list.length > 0 && (
        <ul className="m-0 grid list-none gap-3 p-0">
          {list.map((d) => {
            const days = daysSince(d.requested_at);
            const near = days >= 25;
            return (
              <li key={d.protocol} className="grid gap-2 rounded-xl border border-line bg-surface p-4">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div className="grid gap-0.5">
                    <p className="m-0 text-sm text-fg-muted">Protocolo</p>
                    <p className="m-0 font-mono font-semibold">{d.protocol}</p>
                  </div>
                  {near && <Badge tone="danger">Perto da meta operacional de 30 dias</Badge>}
                </div>
                <p className="m-0 text-sm tabular-nums">
                  Pedida em {date.format(new Date(d.requested_at))} ({days} {days === 1 ? "dia" : "dias"})
                </p>
                {near && (
                  <p className="m-0 text-sm text-fg-muted">Meta interna da operação, não é prazo legal.</p>
                )}
                <ul className="m-0 grid gap-1 pl-5 text-sm">
                  {d.blockers.map((b) => (
                    <li key={b}>{blockerText[b]}</li>
                  ))}
                </ul>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
