import { useCallback, useEffect, useState } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { Notice } from "../../ui/ui";
import { orderStatusText } from "../comanda/textos";

type HistoryEntry = components["schemas"]["HistoryEntry"];

const date = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

const visitText: Record<string, string> = {
  waiting: "aguardando",
  called: "chamado",
  in_service: "em atendimento",
  completed: "concluído",
  cancelled: "cancelado",
  no_show: "ausente",
  interrupted: "interrompido",
};

/**
 * Histórico privado da conta nesta loja (só da própria conta; visitas feitas como visitante não
 * aparecem). Paginado: "Carregar mais".
 */
export function Historico({ shopId }: { shopId: string }) {
  const [entries, setEntries] = useState<HistoryEntry[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fetchPage = useCallback(
    (cursor: string | null) =>
      call(() =>
        api.GET("/v1/me/history", {
          params: { query: { establishment_id: shopId, limit: 10, ...(cursor ? { cursor } : {}) } },
        }),
      ),
    [shopId],
  );
  useEffect(() => {
    let active = true;
    void fetchPage(null).then((out) => {
      if (!active) return;
      if (out.ok) {
        setEntries(out.data.data);
        setNext(out.data.page.next_cursor ?? null);
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchPage]);

  async function more() {
    if (!next) return;
    const out = await fetchPage(next);
    if (out.ok) {
      setEntries((cur) => [...(cur ?? []), ...out.data.data]);
      setNext(out.data.page.next_cursor ?? null);
    }
  }

  return (
    <section aria-labelledby="titulo-historico" className="card">
      <h3 id="titulo-historico">Meu histórico nesta loja</h3>
      {error && <Notice kind="erro">{error}</Notice>}
      {!entries && !error && <p role="status">Carregando histórico…</p>}
      {entries?.length === 0 && <p>Nenhuma visita com esta conta nesta loja.</p>}
      <ul className="cards">
        {(entries ?? []).map((h) => (
          <li key={h.order_id}>
            <strong>{date.format(new Date(h.visited_at))}</strong> · {h.professional_name} ·{" "}
            {visitText[h.participation_status] ?? h.participation_status}
            <ul className="order__items">
              {h.items.map((it, i) => (
                <li key={i}>
                  <span>{it.name}</span>
                  <span className="order__price">
                    {formatCents(it.unit_price.amount_cents)}
                    {h.order_status !== "open" && h.order_status !== "pending_review" && !it.charged
                      ? " (não cobrado)"
                      : ""}
                  </span>
                </li>
              ))}
            </ul>
            <p className="order__total">
              <span>{orderStatusText[h.order_status]}</span>
              <strong>{formatCents(h.total.amount_cents)}</strong>
            </p>
            {h.points_credited > 0 && (
              <p className="muted">+{h.points_credited} pontos ganhos nesta visita</p>
            )}
          </li>
        ))}
      </ul>
      {next && (
        <button type="button" className="button button--secondary" onClick={() => void more()}>
          Carregar mais
        </button>
      )}
    </section>
  );
}
