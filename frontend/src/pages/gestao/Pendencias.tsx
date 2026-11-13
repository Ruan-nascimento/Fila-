import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import { Field, Notice, Submit } from "../../ui/ui";
import { issueText, type FinancialIssue } from "../pagamento/textos";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Pendencies = components["schemas"]["Pendencies"];
type Filter = "open" | "resolved" | "";

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

const counters: [keyof Pendencies, string][] = [
  ["orders_awaiting_payment", "Comandas aguardando pagamento"],
  ["orders_awaiting_confirmation", "Pix a conferir"],
  ["interruptions_to_review", "Interrupções para revisar"],
  ["charges_unknown", "Pix verificando com o banco"],
  ["open_financial_issues", "Pendências financeiras abertas"],
  ["refunds_under_review", "Estornos em revisão"],
  ["deletion_requests_processing", "Exclusões de conta em andamento"],
  ["unavailable_signals", "Profissionais sinalizando indisponibilidade"],
];

/**
 * Painel do dono: contadores do que pede ação e as pendências financeiras (duplicidade, Pix
 * tardio, valor divergente, Pix sem resposta, estorno em revisão). Resolver registra uma nota;
 * nenhum recebimento é alterado ou apagado.
 */
export function Pendencias({ ctx }: { ctx: StaffContext }) {
  const [summary, setSummary] = useState<Pendencies | null>(null);
  const [filter, setFilter] = useState<Filter>("open");
  const [issues, setIssues] = useState<FinancialIssue[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const fetchAll = useCallback(async () => {
    const path = { establishmentId: ctx.estId };
    const [p, l] = await Promise.all([
      call(() => api.GET("/v1/establishments/{establishmentId}/pendencies", { params: { path } })),
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/financial-issues", {
          params: { path, query: { limit: 50, ...(filter ? { status: filter } : {}) } },
        }),
      ),
    ]);
    return { p, l };
  }, [ctx.estId, filter]);

  const reload = useCallback(async () => {
    const { p, l } = await fetchAll();
    if (p.ok) setSummary(p.data.data);
    if (l.ok) setIssues(l.data.data);
    const failed = !p.ok ? p : !l.ok ? l : null;
    setError(failed && !failed.ok ? problemMessage(failed.problem, failed.offline) : null);
  }, [fetchAll]);

  useEffect(() => {
    let active = true;
    void fetchAll().then(({ p, l }) => {
      if (!active) return;
      if (p.ok) setSummary(p.data.data);
      if (l.ok) setIssues(l.data.data);
      const failed = !p.ok ? p : !l.ok ? l : null;
      setError(failed && !failed.ok ? problemMessage(failed.problem, failed.offline) : null);
    });
    return () => {
      active = false;
    };
  }, [fetchAll]);

  // SSE: contadores chegam prontos; pendência aberta/resolvida troca na lista (ou relê se é nova).
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      void reload();
    } else if (m.type === "pendencies.updated") {
      setSummary(m.data as Pendencies);
    } else if (m.type === "financial_issue.updated") {
      const issue = m.data as FinancialIssue;
      setIssues((list) => {
        if (!list) return list;
        const known = list.some((i) => i.id === issue.id);
        const fits = !filter || issue.status === filter;
        if (known)
          return fits
            ? list.map((i) => (i.id === issue.id ? issue : i))
            : list.filter((i) => i.id !== issue.id);
        return fits ? [issue, ...list] : list;
      });
    }
  });

  return (
    <div>
      {error && <Notice kind="erro">{error}</Notice>}
      {!summary && !error && <p role="status">Carregando pendências…</p>}
      {summary && (
        <ul className="cards" aria-label="Resumo">
          {counters.map(([key, label]) => (
            <li key={key} className="card">
              <strong>{summary[key]}</strong> {label}
            </li>
          ))}
        </ul>
      )}
      <h3>Pendências financeiras</h3>
      <label htmlFor="filtro-pendencias">Mostrar</label>{" "}
      <select
        id="filtro-pendencias"
        className="select"
        value={filter}
        onChange={(e) => setFilter(e.target.value as Filter)}
      >
        <option value="open">Abertas</option>
        <option value="resolved">Resolvidas</option>
        <option value="">Todas</option>
      </select>
      {issues?.length === 0 && <p>Nenhuma pendência aqui.</p>}
      <ul className="cards">
        {(issues ?? []).map((i) => (
          <li key={i.id} className="card">
            <p>
              <strong>{issueText[i.kind]}</strong>
            </p>
            <p className="muted">
              Aberta em {when.format(new Date(i.created_at))}
              {i.amount && <> · valor {formatCents(i.amount.amount_cents)}</>} · comanda{" "}
              {i.order_id.slice(-6)}
            </p>
            {i.status === "resolved" ? (
              <p>
                Resolvida{i.resolved_at && <> em {when.format(new Date(i.resolved_at))}</>}:{" "}
                {i.resolution_note}
              </p>
            ) : (
              <Resolver
                ctx={ctx}
                issue={i}
                onDone={(done) => setIssues((l) => (l ?? []).map((x) => (x.id === done.id ? done : x)))}
              />
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Resolver({
  ctx,
  issue,
  onDone,
}: {
  ctx: StaffContext;
  issue: FinancialIssue;
  onDone: (i: FinancialIssue) => void;
}) {
  const [note, setNote] = useState("");
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (note.trim().length < 3) return;
    const out = await cmd.run(`resolver:${issue.id}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/financial-issues/{issueId}/resolve", {
        params: {
          path: { establishmentId: ctx.estId, issueId: issue.id },
          header: { "Idempotency-Key": key },
        },
        body: { resolution_note: note },
      }),
    );
    if (out.ok) onDone(out.data.data);
  }
  return (
    <form onSubmit={onSubmit} noValidate aria-label="Resolver pendência">
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Field
        label="Como foi tratada"
        hint="Ex.: estorno confirmado, devolvido em dinheiro com recibo. Os recebimentos continuam registrados."
        required
        minLength={3}
        maxLength={500}
        value={note}
        onChange={(e) => setNote(e.target.value)}
      />
      <Submit busy={cmd.busy !== null}>Marcar como resolvida</Submit>
    </form>
  );
}
