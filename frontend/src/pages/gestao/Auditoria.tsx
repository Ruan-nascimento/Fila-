import { useCallback, useEffect, useState } from "react";
import { ChevronDown, ScrollText } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { Button, EmptyState, LoadError, Loading, PageHeader } from "../../ui/ui";
import { actionText } from "./auditoriaTextos";
import type { StaffContext } from "./comum";

type AuditEvent = components["schemas"]["AuditEvent"];

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "medium" });

const actorText: Record<AuditEvent["actor"]["kind"], string> = {
  visitor: "Visitante",
  customer: "Cliente",
  professional: "Profissional",
  owner: "Dono",
  provider: "Provedor de pagamento",
  system: "Sistema",
  operator: "Operador",
};

const targetText: Record<string, string> = {
  participation: "visita na fila",
  order: "comanda",
  queue: "fila",
  professional: "profissional",
  service: "serviço",
  pix_charge: "cobrança Pix",
  receipt: "recebimento",
  refund: "estorno",
  financial_issue: "pendência financeira",
  reward: "prêmio",
  reward_reservation: "reserva de prêmio",
  loyalty_program: "programa de pontos",
  invite: "convite",
  establishment: "loja",
  account: "conta",
  change_request: "solicitação de alteração",
};

/**
 * Registro de auditoria da loja (só o dono), mais recente primeiro. Mostra quem fez (por tipo de
 * ator, nunca nome ou e-mail), o quê (frase e código da transição, ver docs/matriz-transicoes.md),
 * quando e o motivo digitado. No computador vira tabela; no celular, linhas empilhadas.
 */
export function Auditoria({ ctx }: { ctx: StaffContext }) {
  const [list, setList] = useState<AuditEvent[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [attempt, setAttempt] = useState(0);

  const fetchPage = useCallback(
    (cursor: string | null) =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/audit-events", {
          params: {
            path: { establishmentId: ctx.estId },
            query: { limit: 50, ...(cursor ? { cursor } : {}) },
          },
        }),
      ),
    [ctx.estId],
  );

  useEffect(() => {
    let active = true;
    void fetchPage(null).then((out) => {
      if (!active) return;
      if (out.ok) {
        setList(out.data.data);
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
      setList((l) => [...(l ?? []), ...out.data.data]);
      setNext(out.data.page.next_cursor ?? null);
    } else setError(problemMessage(out.problem, out.offline));
  }

  return (
    <section aria-labelledby="titulo-auditoria" className="grid gap-4">
      <PageHeader
        id="titulo-auditoria"
        title="Auditoria"
        description="Ações importantes da loja, mais recentes primeiro. Pessoas aparecem só pelo tipo (cliente, profissional…)."
      />
      {error && <LoadError message={error} onRetry={() => setAttempt((n) => n + 1)} />}
      {!list && !error && <Loading>Carregando auditoria…</Loading>}
      {list?.length === 0 && <EmptyState icon={ScrollText} title="Nenhum registro ainda" />}
      {list && list.length > 0 && (
        <div className="overflow-hidden rounded-xl border border-line bg-surface">
          <div
            aria-hidden="true"
            className="hidden grid-cols-[9rem_minmax(0,1fr)_11rem] gap-4 border-b border-line bg-surface-2 px-4 py-2 text-sm font-semibold md:grid"
          >
            <span>Ator</span>
            <span>Evento</span>
            <span>Data e hora</span>
          </div>
          <ul className="m-0 list-none divide-y divide-line p-0">
            {list.map((e) => (
              <li
                key={e.id}
                className="grid gap-1 px-4 py-3 md:grid-cols-[9rem_minmax(0,1fr)_11rem] md:gap-4"
              >
                <span className="text-sm font-semibold md:font-medium">{actorText[e.actor.kind]}</span>
                <span className="grid gap-0.5">
                  <span>
                    {actionText[e.action] ?? e.action}{" "}
                    <span className="text-xs text-fg-muted">
                      ({targetText[e.target.type] ?? e.target.type})
                    </span>
                  </span>
                  <span className="text-xs text-fg-muted tabular-nums">
                    {e.action}
                    {(e.from_state || e.to_state) && ` · ${e.from_state ?? "—"} → ${e.to_state ?? "—"}`}
                  </span>
                  {e.reason && <span className="text-sm">Motivo: {e.reason}</span>}
                </span>
                <span className="text-sm text-fg-muted tabular-nums">
                  {when.format(new Date(e.occurred_at))}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {next && (
        <Button
          variant="secondary"
          icon={ChevronDown}
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
