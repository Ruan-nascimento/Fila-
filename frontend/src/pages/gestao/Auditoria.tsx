import { useCallback, useEffect, useState } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { Notice } from "../../ui/ui";
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
 * Registro de auditoria da loja (só o dono). Mostra quem fez (por tipo de ator, nunca nome ou
 * e-mail), o quê (código da transição, ver docs/matriz-transicoes.md), quando e o motivo digitado.
 */
export function Auditoria({ ctx }: { ctx: StaffContext }) {
  const [list, setList] = useState<AuditEvent[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

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
      setList((l) => [...(l ?? []), ...out.data.data]);
      setNext(out.data.page.next_cursor ?? null);
    } else setError(problemMessage(out.problem, out.offline));
  }

  return (
    <section aria-labelledby="titulo-auditoria">
      <h3 id="titulo-auditoria">Auditoria</h3>
      <p className="muted">
        Ações importantes da loja, mais recentes primeiro. Pessoas aparecem só pelo tipo (cliente,
        profissional…).
      </p>
      {error && <Notice kind="erro">{error}</Notice>}
      {!list && !error && <p role="status">Carregando auditoria…</p>}
      {list?.length === 0 && <p>Nenhum registro ainda.</p>}
      <ul className="cards">
        {(list ?? []).map((e) => (
          <li key={e.id} className="card">
            <p>
              <strong>{e.action}</strong> · {targetText[e.target.type] ?? e.target.type} ·{" "}
              {actorText[e.actor.kind]}
            </p>
            <p className="muted">
              {when.format(new Date(e.occurred_at))}
              {(e.from_state || e.to_state) && (
                <>
                  {" "}
                  · {e.from_state ?? "—"} → {e.to_state ?? "—"}
                </>
              )}
            </p>
            {e.reason && <p>Motivo: {e.reason}</p>}
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
