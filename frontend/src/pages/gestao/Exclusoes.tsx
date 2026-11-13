import { useCallback, useEffect, useState } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useStreamMessages } from "../../stream/context";
import { Notice } from "../../ui/ui";
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
    if (out.ok) setList(out.data.data);
    else setError(problemMessage(out.problem, out.offline));
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

  if (error) return <Notice kind="erro">{error}</Notice>;
  if (!list) return <p role="status">Carregando exclusões…</p>;
  return (
    <div>
      <p className="muted">
        Pessoas que pediram para excluir a conta e ainda dependem desta loja. Resolva as pendências pelas
        telas de filas e comandas. {metaText}
      </p>
      {list.length === 0 && <p>Nenhuma exclusão aguardando esta loja.</p>}
      <ul className="cards">
        {list.map((d) => {
          const days = daysSince(d.requested_at);
          return (
            <li key={d.protocol} className="card">
              <h3>
                Protocolo <span className="code">{d.protocol}</span>
              </h3>
              <p>
                Pedida em {date.format(new Date(d.requested_at))} ({days} {days === 1 ? "dia" : "dias"})
              </p>
              {days >= 25 && (
                <Notice kind="erro">
                  Perto da meta operacional de 30 dias (meta interna, não é prazo legal).
                </Notice>
              )}
              <ul>
                {d.blockers.map((b) => (
                  <li key={b}>{blockerText[b]}</li>
                ))}
              </ul>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
