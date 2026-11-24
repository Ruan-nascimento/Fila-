import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Navigate, NavLink, useParams } from "react-router";
import {
  CirclePause,
  CirclePlay,
  CircleStop,
  Coffee,
  DoorClosed,
  DoorOpen,
  Gauge,
  RefreshCw,
  UsersRound,
} from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { useStale, useStreamMessages, Versions } from "../../stream/context";
import { Badge, Button, EmptyState, Field, LoadError, Loading, PageHeader, Submit } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { isId, useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";
import { Entradas } from "./Entradas";

type StaffQueue = components["schemas"]["StaffQueue"];

const queueStatus: Record<StaffQueue["status"], { text: string; tone: "success" | "warning" | "neutral" }> = {
  open: { text: "Operação aberta", tone: "success" },
  paused: { text: "Operação pausada", tone: "warning" },
  closed: { text: "Operação encerrada", tone: "neutral" },
};

/** Situação da ENTRADA (separada da operação, ADR-0035), sempre em texto. */
const entryText: Record<string, { text: string; tone: "success" | "warning" | "neutral" }> = {
  accepting: { text: "Entrada aberta", tone: "success" },
  full: { text: "Fila cheia", tone: "warning" },
  closed_manual: { text: "Entrada fechada", tone: "warning" },
  operation_paused: { text: "Sem novas entradas (pausada)", tone: "neutral" },
  operation_closed: { text: "Sem novas entradas", tone: "neutral" },
};

function loadLine(q: StaffQueue): string {
  const l = q.load;
  if (!l || l.pending_participations === 0) return "Ninguém aguardando ou chamado";
  const parts = [
    `${l.pending_participations} ${l.pending_participations === 1 ? "participação pendente" : "participações pendentes"}`,
    `${l.pending_service_units} ${l.pending_service_units === 1 ? "unidade de serviço" : "unidades de serviço"}`,
  ];
  if (l.estimated_minutes > 0) parts.push(`~${l.estimated_minutes} min acumulados (estimativa)`);
  return parts.join(" · ");
}

/**
 * Filas da loja. O profissional cai direto na própria fila; o dono escolhe a fila (lista ao lado
 * no computador, abas no celular). A fila escolhida fica no endereço: recarregar e Voltar mantêm.
 */
export function Filas({ ctx }: { ctx: StaffContext }) {
  const { queueId } = useParams();
  const [queues, setQueues] = useState<StaffQueue[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const versions = useRef(new Versions());
  const stale = useStale();
  // Atualizações pelo fluxo SSE: snapshot e queue.updated (só versão mais nova vale).
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      const list = (m.data as { staff_queues?: StaffQueue[] }).staff_queues;
      if (list) {
        versions.current.reset();
        setQueues(list);
        setLoadError(null);
      }
    } else if (m.type === "queue.updated" && versions.current.newer(m.resource.id, m.resource.version)) {
      const q = m.data as StaffQueue;
      setQueues((list) => {
        if (list === null) return list;
        return list.some((x) => x.queue_id === q.queue_id)
          ? list.map((x) => (x.queue_id === q.queue_id ? q : x))
          : [...list, q];
      });
    }
  });
  // Relógio da tela para habilitar "Marcar ausência" após a tolerância (o servidor confere de novo).
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 15_000);
    return () => clearInterval(id);
  }, []);

  const load = useCallback(async () => {
    const out = await call(() =>
      api.GET("/v1/establishments/{establishmentId}/queues", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    );
    if (out.ok) {
      versions.current.reset();
      setQueues(out.data.data);
      setLoadError(null);
    } else setLoadError(problemMessage(out.problem, out.offline));
  }, [ctx.estId]);

  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/queues", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) setQueues((cur) => cur ?? out.data.data);
      else setLoadError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [ctx.estId]);

  if (queueId !== undefined && !isId(queueId)) return <Navigate to={`/gestao/${ctx.estId}/filas`} replace />;
  if (loadError && !queues) return <LoadError message={loadError} onRetry={() => void load()} />;
  if (!queues) return <Loading>Carregando filas…</Loading>;
  if (queues.length === 0)
    return (
      <>
        <PageHeader title="Filas" />
        <EmptyState icon={UsersRound} title="Nenhuma fila ainda">
          {ctx.isOwner ? (
            <p>Cadastre um profissional em Profissionais: a fila dele é criada fechada.</p>
          ) : null}
        </EmptyState>
      </>
    );

  const own = queues.find((q) => q.professional.id === ctx.professionalId);
  const selected = queues.find((q) => q.queue_id === queueId) ?? own ?? queues[0];
  if (!selected) return null;
  const multiple = queues.length > 1;
  const base = `/gestao/${ctx.estId}/filas`;

  return (
    <div className={cx("grid gap-5", multiple && "lg:grid-cols-[15rem_minmax(0,1fr)] lg:items-start")}>
      {multiple && (
        <nav aria-label="Filas da loja" className="min-w-0 lg:sticky lg:top-8">
          <h2 className="m-0 mb-2 hidden text-sm font-semibold text-fg-muted lg:block">Filas da equipe</h2>
          <ul className="-mx-4 m-0 flex list-none gap-2 overflow-x-auto px-4 pb-1 sm:-mx-6 sm:px-6 lg:mx-0 lg:grid lg:overflow-visible lg:px-0">
            {queues.map((q) => {
              const current = q.entries.find((e) => e.status === "called" || e.status === "in_service");
              const waiting = q.entries.filter((e) => e.status === "waiting").length;
              return (
                // No computador a coluna tem largura fixa: o texto quebra em vez de invadir a fila ao lado.
                <li key={q.queue_id} className="shrink-0 lg:min-w-0">
                  <NavLink
                    to={`${base}/${q.queue_id}`}
                    aria-current={q.queue_id === selected.queue_id ? "page" : undefined}
                    className={cx(
                      "grid min-h-11 gap-0.5 rounded-xl border px-3 py-2 text-fg no-underline",
                      q.queue_id === selected.queue_id
                        ? "border-primary bg-select"
                        : "border-line bg-surface hover:bg-surface-2",
                    )}
                  >
                    <span className="font-semibold">{q.professional.display_name}</span>
                    <span className="text-xs whitespace-nowrap text-fg-muted lg:whitespace-normal">
                      {queueStatus[q.status].text}
                      {q.admission && q.status === "open" && q.admission.state !== "accepting"
                        ? ` · ${entryText[q.admission.state]?.text.toLowerCase()}`
                        : ""}
                      {current ? (current.status === "called" ? " · chamando" : " · atendendo") : ""} ·{" "}
                      {waiting} aguardando
                    </span>
                  </NavLink>
                </li>
              );
            })}
          </ul>
        </nav>
      )}
      <FilaDetalhe
        key={selected.queue_id}
        ctx={ctx}
        queue={selected}
        queues={queues}
        now={now}
        stale={stale}
        onReload={load}
      />
    </div>
  );
}

function FilaDetalhe({
  ctx,
  queue: q,
  queues,
  now,
  stale,
  onReload,
}: {
  ctx: StaffContext;
  queue: StaffQueue;
  queues: StaffQueue[];
  now: Date;
  stale: boolean;
  onReload: () => Promise<void>;
}) {
  const cmd = useCommand();
  const st = queueStatus[q.status];
  const adm = q.admission;
  const entry = adm ? entryText[adm.state] : undefined;
  const canControlEntry =
    adm !== undefined &&
    (ctx.isOwner || (adm.professionals_can_control && ctx.professionalId === q.professional.id));
  const [entryNote, setEntryNote] = useState<string | null>(null);

  async function admission(op: "close" | "reopen") {
    const path =
      op === "close"
        ? ("/v1/establishments/{establishmentId}/queues/{queueId}/close-admission" as const)
        : ("/v1/establishments/{establishmentId}/queues/{queueId}/reopen-admission" as const);
    const out = await cmd.run(`entrada:${op}:${q.queue_id}:${q.version}`, (key) =>
      api.POST(path, {
        params: {
          path: { establishmentId: ctx.estId, queueId: q.queue_id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: q.version },
      }),
    );
    if (out.ok) {
      const state = out.data.data.admission?.state;
      setEntryNote(
        op === "close"
          ? "Entrada fechada. Quem já está na fila continua e será atendido normalmente."
          : state === "full"
            ? "Entrada reaberta. Novas participações serão aceitas quando houver vaga."
            : "Entrada reaberta.",
      );
    }
    if (out.ok || out.problem?.status === 409) await onReload();
  }

  async function change(op: "open" | "pause" | "close") {
    const path = {
      open: "/v1/establishments/{establishmentId}/queues/{queueId}/open",
      pause: "/v1/establishments/{establishmentId}/queues/{queueId}/pause",
      close: "/v1/establishments/{establishmentId}/queues/{queueId}/close",
    } as const;
    const out = await cmd.run(`${op}:${q.queue_id}:${q.version}`, (key) =>
      api.POST(path[op], {
        params: {
          path: { establishmentId: ctx.estId, queueId: q.queue_id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: q.version },
      }),
    );
    // Sucesso ou conflito: relê para mostrar o estado atual do servidor.
    if (out.ok || out.problem?.status === 409) await onReload();
  }

  return (
    <section aria-labelledby="titulo-fila" className="grid min-w-0 gap-4">
      <header className="grid gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <h1 id="titulo-fila" className="m-0 text-2xl font-bold tracking-tight">
              Fila de {q.professional.display_name}
            </h1>
            <Badge tone={st.tone} dot>
              {st.text}
            </Badge>
            {entry && q.status === "open" && (
              <Badge tone={entry.tone} dot>
                {entry.text}
              </Badge>
            )}
            {q.professional.status === "inactive" && <Badge>Profissional desativado</Badge>}
            {stale && <Badge tone="warning">Última informação conhecida</Badge>}
          </div>
          <Button variant="ghost" icon={RefreshCw} onClick={() => void onReload()}>
            Atualizar
          </Button>
        </div>
        {q.unavailable_signal.active && (
          <p role="note" className="m-0 flex items-start gap-2 rounded-lg bg-warning-soft px-3 py-2 text-sm">
            <Coffee aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-warning" />
            <span>
              <strong>Profissional indisponível</strong>
              {q.unavailable_signal.note ? `: ${q.unavailable_signal.note}` : ""}
            </span>
          </p>
        )}
        <p className="m-0 text-sm text-fg-muted" aria-live="polite">
          {loadLine(q)}
          {adm?.capacity != null && ` · limite de ${adm.capacity} participações pendentes`}
        </p>
        {(ctx.isOwner || (canControlEntry && q.status === "open")) && (
          <div className="grid gap-2">
            <div className="flex flex-wrap gap-2">
              {/* Entrada: só faz sentido com a operação aberta. */}
              {canControlEntry && q.status === "open" && adm?.status === "open" && (
                <Button
                  variant="secondary"
                  icon={DoorClosed}
                  disabled={cmd.busy !== null}
                  onClick={() => void admission("close")}
                >
                  Fechar entrada
                </Button>
              )}
              {canControlEntry && q.status === "open" && adm?.status === "closed" && (
                <Button icon={DoorOpen} disabled={cmd.busy !== null} onClick={() => void admission("reopen")}>
                  Reabrir entrada
                </Button>
              )}
              {ctx.isOwner && q.status === "closed" && (
                <Button icon={CirclePlay} disabled={cmd.busy !== null} onClick={() => void change("open")}>
                  Abrir operação
                </Button>
              )}
              {ctx.isOwner && q.status === "paused" && (
                <Button icon={CirclePlay} disabled={cmd.busy !== null} onClick={() => void change("open")}>
                  Retomar operação
                </Button>
              )}
              {ctx.isOwner && q.status === "open" && (
                <Button
                  variant="secondary"
                  icon={CirclePause}
                  disabled={cmd.busy !== null}
                  onClick={() => void change("pause")}
                >
                  Pausar operação
                </Button>
              )}
              {ctx.isOwner && q.status !== "closed" && (
                <Button
                  variant="ghost"
                  icon={CircleStop}
                  disabled={cmd.busy !== null}
                  onClick={() => void change("close")}
                >
                  Encerrar operação
                </Button>
              )}
            </div>
            {entryNote && (
              <p role="status" className="m-0 text-sm">
                {entryNote}
              </p>
            )}
            <p className="m-0 text-xs text-fg-muted">
              Fechar a entrada só impede novas participações: a equipe continua chamando, atendendo e
              recebendo. Pausar a operação bloqueia novas entradas e chamadas; quem já foi chamado ainda pode
              ser atendido. Para encerrar a operação, ninguém pode estar aguardando ou chamado.
            </p>
          </div>
        )}
        {ctx.isOwner && adm && <Capacidade ctx={ctx} queue={q} onDone={onReload} />}
        <CommandError problem={cmd.problem} offline={cmd.offline} />
      </header>
      <Entradas ctx={ctx} queue={q} queues={queues} now={now} onDone={onReload} />
      {ctx.professionalId === q.professional.id && <Sinal ctx={ctx} queue={q} onDone={onReload} />}
    </section>
  );
}

/** Limite de participações pendentes desta fila (só o dono): 1–200 ou o padrão da loja. */
function Capacidade({
  ctx,
  queue: q,
  onDone,
}: {
  ctx: StaffContext;
  queue: StaffQueue;
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  const panel = useInlinePanel();
  const override = q.admission?.capacity_override ?? null;
  const [value, setValue] = useState(override === null ? "" : String(override));
  const [error, setError] = useState<string | null>(null);
  async function save(capacity: number | null) {
    const out = await cmd.run(`capacidade:${q.queue_id}:${q.version}:${capacity ?? "padrao"}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/queues/{queueId}/capacity", {
        params: {
          path: { establishmentId: ctx.estId, queueId: q.queue_id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: q.version, capacity },
      }),
    );
    if (out.ok) panel.hide();
    if (out.ok || out.problem?.status === 409) await onDone();
  }
  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!/^\d{1,3}$/.test(value) || Number(value) < 1 || Number(value) > 200) {
      setError("Use um número de 1 a 200.");
      return;
    }
    setError(null);
    void save(Number(value));
  }
  if (!panel.open)
    return (
      <Button
        id={panel.triggerId}
        variant="ghost"
        icon={Gauge}
        className="justify-self-start"
        onClick={() => panel.show("aberto")}
      >
        Limite desta fila:{" "}
        {override === null
          ? q.admission?.capacity == null
            ? "sem limite (padrão da loja)"
            : `padrão da loja (${q.admission.capacity})`
          : override}
      </Button>
    );
  return (
    <form
      id={panel.panelId}
      onKeyDown={panel.onKeyDown}
      onSubmit={onSubmit}
      noValidate
      aria-label="Limite da fila"
      className="grid max-w-xl gap-3 rounded-xl border border-line bg-surface p-4"
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Field
        label="Limite de participações pendentes (1 a 200)"
        inputMode="numeric"
        value={value}
        error={error ?? undefined}
        hint="Aguardando e chamado contam; em atendimento não. Um pedido com 3 cortes ocupa 1 vaga. Reduzir não tira ninguém."
        onChange={(e) => setValue(e.target.value)}
      />
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>Salvar limite</Submit>
        <Button variant="secondary" disabled={cmd.busy !== null} onClick={() => void save(null)}>
          Usar o padrão da loja
        </Button>
        <Button variant="ghost" onClick={panel.hide}>
          Cancelar
        </Button>
      </div>
    </form>
  );
}

function Sinal({
  ctx,
  queue,
  onDone,
}: {
  ctx: StaffContext;
  queue: StaffQueue;
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  const panel = useInlinePanel();
  const [note, setNote] = useState("");
  const active = queue.unavailable_signal.active;
  async function send() {
    const body = active
      ? { unavailable: false }
      : note.trim()
        ? { unavailable: true, note }
        : { unavailable: true };
    const out = await cmd.run(`sinal:${queue.queue_id}:${String(!active)}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/queues/{queueId}/availability-signal", {
        params: {
          path: { establishmentId: ctx.estId, queueId: queue.queue_id },
          header: { "Idempotency-Key": key },
        },
        body,
      }),
    );
    if (out.ok) {
      setNote("");
      panel.hide();
      await onDone();
    }
  }
  function onSubmit(e: FormEvent) {
    e.preventDefault();
    void send();
  }
  return (
    <div className="grid gap-2 border-t border-line pt-4">
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {active ? (
        <Button
          variant="secondary"
          className="justify-self-start"
          disabled={cmd.busy !== null}
          onClick={() => void send()}
        >
          Estou disponível de novo
        </Button>
      ) : panel.open ? (
        <form
          id={panel.panelId}
          onKeyDown={panel.onKeyDown}
          onSubmit={onSubmit}
          noValidate
          aria-label="Avisar indisponibilidade"
          className="grid max-w-xl gap-3 rounded-xl border border-line bg-surface p-4"
        >
          <Field
            label="Aviso ao dono (opcional)"
            maxLength={200}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            hint="Ex.: almoço até 14h. Isso só avisa o dono; não muda a fila."
          />
          <div className="flex flex-wrap gap-2">
            <Submit busy={cmd.busy !== null}>Avisar que estou indisponível</Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Cancelar
            </Button>
          </div>
        </form>
      ) : (
        <Button
          id={panel.triggerId}
          variant="ghost"
          icon={Coffee}
          className="justify-self-start"
          onClick={() => panel.show("aberto")}
        >
          Avisar que estou indisponível
        </Button>
      )}
    </div>
  );
}
