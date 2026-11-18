import { useCallback, useEffect, useState } from "react";
import { ChevronDown, History } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { AmountRow, Badge, Button, Card, EmptyState, LoadError, Loading } from "../../ui/ui";
import { orderStatusText } from "../comanda/textos";

type HistoryEntry = components["schemas"]["HistoryEntry"];

const date = new Intl.DateTimeFormat("pt-BR", {
  weekday: "short",
  day: "2-digit",
  month: "short",
  year: "numeric",
});
const hour = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

const visitText: Record<string, string> = {
  waiting: "Aguardando",
  called: "Chamado",
  in_service: "Em atendimento",
  completed: "Concluído",
  cancelled: "Cancelado",
  no_show: "Ausente",
  interrupted: "Interrompido",
};
const visitTone: Record<string, "neutral" | "primary" | "success" | "warning" | "danger"> = {
  completed: "success",
  cancelled: "neutral",
  no_show: "warning",
  interrupted: "warning",
};

/**
 * Histórico privado da conta nesta loja (só da própria conta; visitas feitas como visitante não
 * aparecem). Linhas escaneáveis; os itens abrem sob demanda. Paginado: "Carregar mais".
 */
export function Historico({ shopId }: { shopId: string }) {
  const [entries, setEntries] = useState<HistoryEntry[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [attempt, setAttempt] = useState(0);
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
        setError(null);
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchPage, attempt]);

  async function more() {
    if (!next) return;
    setLoadingMore(true);
    const out = await fetchPage(next);
    setLoadingMore(false);
    if (out.ok) {
      setEntries((cur) => [...(cur ?? []), ...out.data.data]);
      setNext(out.data.page.next_cursor ?? null);
    } else setError(problemMessage(out.problem, out.offline));
  }

  return (
    <section aria-labelledby="titulo-historico" className="grid max-w-3xl gap-4">
      <div className="grid gap-1">
        <h2 id="titulo-historico" className="m-0 text-lg font-semibold">
          Histórico nesta loja
        </h2>
        <p className="m-0 text-sm text-fg-muted">Suas visitas com esta conta, mais recentes primeiro.</p>
      </div>
      {error && <LoadError message={error} onRetry={() => setAttempt((n) => n + 1)} />}
      {!entries && !error && <Loading>Carregando histórico…</Loading>}
      {entries?.length === 0 && (
        <EmptyState icon={History} title="Nenhuma visita com esta conta nesta loja">
          <p>Visitas feitas só com apelido não aparecem aqui.</p>
        </EmptyState>
      )}
      {entries && entries.length > 0 && (
        <Card as="div" className="divide-y divide-line">
          <ul className="m-0 list-none divide-y divide-line p-0">
            {entries.map((h) => (
              <Linha key={h.order_id} h={h} />
            ))}
          </ul>
        </Card>
      )}
      {next && (
        <Button
          variant="secondary"
          className="justify-self-center"
          disabled={loadingMore}
          onClick={() => void more()}
        >
          {loadingMore ? "Carregando…" : "Carregar mais"}
        </Button>
      )}
    </section>
  );
}

function Linha({ h }: { h: HistoryEntry }) {
  const [open, setOpen] = useState(false);
  const when = new Date(h.visited_at);
  const services = h.items.map((i) => i.name).join(" + ");
  const panelId = `historico-${h.order_id}`;
  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((v) => !v)}
        className="flex min-h-11 w-full items-center gap-3 px-4 py-3 text-left hover:bg-surface-2"
      >
        <span className="grid min-w-0 flex-1 gap-1">
          <span className="text-sm text-fg-muted tabular-nums">
            {date.format(when)} · {hour.format(when)}
          </span>
          <span className="font-semibold">{services || "Sem serviços"}</span>
          <span className="flex flex-wrap items-center gap-2 text-sm text-fg-muted">
            <Badge tone={visitTone[h.participation_status] ?? "neutral"}>
              {visitText[h.participation_status] ?? h.participation_status}
            </Badge>
            com {h.professional_name}
          </span>
        </span>
        <span className="font-semibold tabular-nums">{formatCents(h.total.amount_cents)}</span>
        <ChevronDown
          aria-hidden="true"
          className={`size-5 shrink-0 text-fg-muted transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>
      {open && (
        <div id={panelId} className="grid gap-1 bg-surface-2/50 px-4 pb-3">
          <div className="divide-y divide-line">
            {h.items.map((it, i) => (
              <AmountRow
                key={i}
                label={it.name}
                value={
                  formatCents(it.unit_price.amount_cents) +
                  (h.order_status !== "open" && h.order_status !== "pending_review" && !it.charged
                    ? " (não cobrado)"
                    : "")
                }
              />
            ))}
            <AmountRow
              strong
              label={orderStatusText[h.order_status]}
              value={formatCents(h.total.amount_cents)}
            />
          </div>
          {h.points_credited > 0 && (
            <p className="m-0 text-sm text-fg-muted">+{h.points_credited} pontos ganhos nesta visita</p>
          )}
        </div>
      )}
    </li>
  );
}
