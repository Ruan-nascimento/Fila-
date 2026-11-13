import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { useStreamMessages, Versions } from "../../stream/context";
import { Field, Notice } from "../../ui/ui";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";
import { Entradas } from "./Entradas";

type StaffQueue = components["schemas"]["StaffQueue"];

const statusText: Record<StaffQueue["status"], { icon: string; text: string }> = {
  open: { icon: "●", text: "Aberta" },
  paused: { icon: "Ⅱ", text: "Pausada" },
  closed: { icon: "■", text: "Fechada" },
};

/** Filas: o dono abre, pausa e fecha; o profissional vê a própria e sinaliza indisponibilidade. */
export function Filas({ ctx }: { ctx: StaffContext }) {
  const [queues, setQueues] = useState<StaffQueue[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const versions = useRef(new Versions());
  const cmd = useCommand();
  // Atualizações pelo fluxo SSE: snapshot e queue.updated (só versão mais nova vale).
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      const list = (m.data as { staff_queues?: StaffQueue[] }).staff_queues;
      if (list) {
        versions.current.reset();
        setQueues(list);
      }
    } else if (m.type === "queue.updated" && versions.current.newer(m.resource.id, m.resource.version)) {
      const q = m.data as StaffQueue;
      setQueues((list) => {
        const cur = list ?? [];
        return cur.some((x) => x.queue_id === q.queue_id)
          ? cur.map((x) => (x.queue_id === q.queue_id ? q : x))
          : [...cur, q];
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

  async function change(q: StaffQueue, op: "open" | "pause" | "close") {
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
    if (out.ok || out.problem?.status === 409) await load();
  }

  if (loadError) return <Notice kind="erro">{loadError}</Notice>;
  if (!queues) return <p role="status">Carregando filas…</p>;
  if (queues.length === 0)
    return <p>Nenhuma fila ainda. {ctx.isOwner ? "Cadastre um profissional para criar a fila dele." : ""}</p>;
  return (
    <div>
      <p className="muted">
        A lista se atualiza sozinha.{" "}
        <button type="button" className="link-button" onClick={() => void load()}>
          Atualizar agora
        </button>
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <ul className="cards">
        {queues.map((q) => (
          <li key={q.queue_id} className="card">
            <h3>{q.professional.display_name}</h3>
            <p className={`badge badge--${q.status}`}>
              <span aria-hidden="true">{statusText[q.status].icon} </span>
              {statusText[q.status].text}
              {q.professional.status === "inactive" && " · profissional desativado"}
            </p>
            {q.unavailable_signal.active && (
              <p role="note">
                <strong>Profissional indisponível</strong>
                {q.unavailable_signal.note ? `: ${q.unavailable_signal.note}` : ""}
              </p>
            )}
            {ctx.isOwner && (
              <div className="actions">
                {q.status !== "open" && (
                  <button
                    type="button"
                    className="button"
                    disabled={cmd.busy !== null}
                    onClick={() => void change(q, "open")}
                  >
                    Abrir
                  </button>
                )}
                {q.status === "open" && (
                  <button
                    type="button"
                    className="button button--secondary"
                    disabled={cmd.busy !== null}
                    onClick={() => void change(q, "pause")}
                  >
                    Pausar
                  </button>
                )}
                {q.status !== "closed" && (
                  <button
                    type="button"
                    className="button button--secondary"
                    disabled={cmd.busy !== null}
                    onClick={() => void change(q, "close")}
                  >
                    Fechar
                  </button>
                )}
              </div>
            )}
            <Entradas ctx={ctx} queue={q} queues={queues} now={now} onDone={load} />
            {ctx.professionalId === q.professional.id && <Sinal ctx={ctx} queue={q} onDone={load} />}
          </li>
        ))}
      </ul>
    </div>
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
  const [note, setNote] = useState("");
  const active = queue.unavailable_signal.active;
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
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
      await onDone();
    }
  }
  return (
    <form onSubmit={onSubmit} noValidate>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {!active && (
        <Field
          label="Aviso ao dono (opcional)"
          maxLength={200}
          value={note}
          onChange={(e) => setNote(e.target.value)}
          hint="Ex.: almoço até 14h. Isso só avisa o dono; não muda a fila."
        />
      )}
      <button type="submit" className="button button--secondary" disabled={cmd.busy !== null}>
        {active ? "Estou disponível de novo" : "Avisar que estou indisponível"}
      </button>
    </form>
  );
}
