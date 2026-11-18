import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { CircleCheck, TriangleAlert } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import {
  Badge,
  EmptyState,
  LoadError,
  Loading,
  PageHeader,
  Section,
  Segmented,
  Submit,
  TextArea,
} from "../../ui/ui";
import { cx } from "../../ui/cx";
import { issueText, type FinancialIssue } from "../pagamento/textos";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Pendencies = components["schemas"]["Pendencies"];
type Filter = "open" | "resolved" | "";

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/** O que cada contador significa e onde se resolve. */
const counters: { key: keyof Pendencies; label: string; to?: string }[] = [
  {
    key: "interruptions_to_review",
    label: "Interrupções para revisar",
    to: "comandas?mostrar=pending_review",
  },
  {
    key: "orders_awaiting_payment",
    label: "Comandas aguardando pagamento",
    to: "comandas?mostrar=awaiting_payment",
  },
  {
    key: "orders_awaiting_confirmation",
    label: "Pix a conferir",
    to: "comandas?mostrar=awaiting_confirmation",
  },
  { key: "charges_unknown", label: "Pix verificando com o banco" },
  { key: "open_financial_issues", label: "Pendências financeiras abertas" },
  { key: "refunds_under_review", label: "Estornos em revisão" },
  { key: "deletion_requests_processing", label: "Exclusões de conta em andamento", to: "exclusoes" },
  { key: "unavailable_signals", label: "Profissionais sinalizando indisponibilidade", to: "filas" },
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

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchAll>>) => {
    const { p, l } = r;
    if (p.ok) setSummary(p.data.data);
    if (l.ok) setIssues(l.data.data);
    const failed = !p.ok ? p : !l.ok ? l : null;
    setError(failed && !failed.ok ? problemMessage(failed.problem, failed.offline) : null);
  }, []);
  const reload = useCallback(async () => apply(await fetchAll()), [fetchAll, apply]);

  useEffect(() => {
    let active = true;
    void fetchAll().then((r) => {
      if (active) apply(r);
    });
    return () => {
      active = false;
    };
  }, [fetchAll, apply]);

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

  const base = `/gestao/${ctx.estId}`;
  return (
    <div className="grid gap-6">
      <PageHeader title="Pendências" description="O que pede ação do dono, por ordem de assunto." />
      {error && <LoadError message={error} onRetry={() => void reload()} />}
      {!summary && !error && <Loading>Carregando pendências…</Loading>}
      {summary && (
        <ul className="m-0 grid list-none grid-cols-2 gap-2 p-0 md:grid-cols-4" aria-label="Resumo">
          {counters.map(({ key, label, to }) => {
            const n = summary[key];
            const body = (
              <>
                <strong className={cx("text-2xl tabular-nums", n > 0 ? "text-fg" : "text-fg-muted")}>
                  {n}
                </strong>
                <span className="text-sm text-fg-muted">{label}</span>
              </>
            );
            return (
              <li key={key}>
                {to && n > 0 ? (
                  <Link
                    to={`${base}/${to}`}
                    className="grid h-full min-h-11 content-start gap-1 rounded-xl border border-warning/40 bg-warning-soft p-3 text-fg no-underline hover:border-warning"
                  >
                    {body}
                  </Link>
                ) : (
                  <div
                    className={cx(
                      "grid h-full content-start gap-1 rounded-xl border p-3",
                      n > 0 ? "border-warning/40 bg-warning-soft" : "border-line bg-surface",
                    )}
                  >
                    {body}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <Section
        title="Pendências financeiras"
        description="Resolver registra como foi tratada. Os recebimentos continuam registrados; estorno é pedido na comanda."
      >
        <div className="max-w-md">
          <Segmented
            legend="Mostrar"
            name="filtro-pendencias"
            value={filter}
            onChange={setFilter}
            options={[
              { value: "open", label: "Abertas" },
              { value: "resolved", label: "Resolvidas" },
              { value: "", label: "Todas" },
            ]}
          />
        </div>
        {issues?.length === 0 && (
          <EmptyState icon={CircleCheck} title="Nenhuma pendência aqui">
            <p>{filter === "open" ? "Nada financeiro esperando ação agora." : "Nada nesta lista."}</p>
          </EmptyState>
        )}
        <ul className="m-0 grid list-none gap-3 p-0">
          {(issues ?? []).map((i) => (
            <li key={i.id} className="grid gap-3 rounded-xl border border-line bg-surface p-4">
              <div className="flex items-start gap-3">
                <TriangleAlert
                  aria-hidden="true"
                  className={cx(
                    "mt-0.5 size-5 shrink-0",
                    i.status === "resolved" ? "text-fg-muted" : "text-warning",
                  )}
                />
                <div className="grid min-w-0 flex-1 gap-1">
                  <div className="flex flex-wrap items-start justify-between gap-2">
                    <p className="m-0 font-semibold">{issueText[i.kind]}</p>
                    {i.amount && (
                      <span className="font-semibold tabular-nums">{formatCents(i.amount.amount_cents)}</span>
                    )}
                  </div>
                  <p className="m-0 text-sm text-fg-muted">
                    Aberta em {when.format(new Date(i.created_at))}
                    {i.amount && <> · valor {formatCents(i.amount.amount_cents)}</>}
                    {i.order_id ? (
                      <>
                        {" · "}
                        <Link to={`${base}/comandas/${i.order_id}`}>comanda {i.order_id.slice(-6)}</Link>
                      </>
                    ) : (
                      <> · sem comanda</>
                    )}
                    {i.provider_reference && <> · referência na AbacatePay {i.provider_reference}</>}
                  </p>
                </div>
                <Badge tone={i.status === "resolved" ? "success" : "warning"}>
                  {i.status === "resolved" ? "Resolvida" : "Aberta"}
                </Badge>
              </div>
              {i.status === "resolved" ? (
                <p className="m-0 rounded-lg bg-surface-2 px-3 py-2 text-sm">
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
      </Section>
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
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
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
    <form
      onSubmit={onSubmit}
      noValidate
      aria-label="Resolver pendência"
      className="grid gap-3 border-t border-line pt-3"
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <TextArea
        label="Como foi tratada"
        hint="Ex.: estorno confirmado, devolvido em dinheiro com recibo. Os recebimentos continuam registrados."
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={note}
        error={
          tried && note.trim().length < 3 ? "Descreva como foi tratada (pelo menos 3 caracteres)." : undefined
        }
        onChange={(e) => setNote(e.target.value)}
      />
      <div>
        <Submit busy={cmd.busy !== null}>Marcar como resolvida</Submit>
      </div>
    </form>
  );
}
