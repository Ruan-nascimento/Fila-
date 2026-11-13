import { useState, type FormEvent } from "react";
import { api } from "../../api/client";
import type { components } from "../../api/schema.gen";
import { Field, Submit } from "../../ui/ui";
import { Atendimento } from "./Atendimento";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type StaffQueue = components["schemas"]["StaffQueue"];
type Entry = StaffQueue["entries"][number];

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });
const statusText: Record<string, string> = {
  waiting: "Aguardando",
  called: "Chamado",
  in_service: "Em atendimento",
};

/**
 * Pessoas de uma fila e as ações da equipe. A tela só mostra os botões que fazem sentido; o
 * servidor confere papel, estado, versão e o prazo de 10 minutos.
 */
export function Entradas({
  ctx,
  queue,
  queues,
  now,
  onDone,
}: {
  ctx: StaffContext;
  queue: StaffQueue;
  queues: StaffQueue[];
  now: Date;
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  const [shown, setShown] = useState<string | null>(null);
  const canOperate = ctx.isOwner || ctx.professionalId === queue.professional.id;
  const busy = queue.entries.some((e) => e.status === "called" || e.status === "in_service");
  const waiting = queue.entries.some((e) => e.status === "waiting");

  async function chamar() {
    const out = await cmd.run(
      `chamar:${queue.queue_id}:${queue.entries.map((e) => e.version).join(".")}`,
      (key) =>
        api.POST("/v1/establishments/{establishmentId}/queues/{queueId}/call-next", {
          params: {
            path: { establishmentId: ctx.estId, queueId: queue.queue_id },
            header: { "Idempotency-Key": key },
          },
        }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }

  async function acao(e: Entry, op: "start" | "no-show") {
    const path =
      op === "start"
        ? ("/v1/establishments/{establishmentId}/participations/{participationId}/start" as const)
        : ("/v1/establishments/{establishmentId}/participations/{participationId}/no-show" as const);
    const out = await cmd.run(`${op}:${e.participation_id}:${e.version}`, (key) =>
      api.POST(path, {
        params: {
          path: { establishmentId: ctx.estId, participationId: e.participation_id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: e.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }

  return (
    <div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {canOperate && queue.status === "open" && (
        <button
          type="button"
          className="button"
          disabled={cmd.busy !== null || busy || !waiting}
          onClick={() => void chamar()}
        >
          Chamar próximo
        </button>
      )}
      {canOperate && busy && (
        <p className="muted">
          A cadeira está ocupada: conclua ou resolva o atendimento atual antes de chamar.
        </p>
      )}
      {queue.entries.length === 0 ? (
        <p className="muted">Ninguém na fila.</p>
      ) : (
        <ol className="entries">
          {queue.entries.map((e) => {
            const toleranceOver = e.tolerance_ends_at ? now >= new Date(e.tolerance_ends_at) : false;
            return (
              <li key={e.participation_id}>
                <strong>{e.ticket_code}</strong> · {e.nick} · {statusText[e.status] ?? e.status}
                <span className="muted"> · {e.service_names.join(", ")}</span>
                {e.has_pending_change_request && <strong> · pedido de alteração aguardando</strong>}
                {e.status === "called" && e.tolerance_ends_at && (
                  <span className="muted">
                    {" "}
                    · tolerância até {time.format(new Date(e.tolerance_ends_at))}
                  </span>
                )}
                {canOperate && e.status === "called" && (
                  <div className="actions">
                    <button
                      type="button"
                      className="button button--secondary"
                      disabled={cmd.busy !== null}
                      onClick={() => void acao(e, "start")}
                    >
                      Iniciar atendimento
                    </button>
                    <button
                      type="button"
                      className="button button--secondary"
                      disabled={cmd.busy !== null || !toleranceOver}
                      title={toleranceOver ? undefined : "Disponível depois de 10 minutos da chamada"}
                      onClick={() => void acao(e, "no-show")}
                    >
                      Marcar ausência
                    </button>
                  </div>
                )}
                {canOperate &&
                  (e.status === "in_service" || shown === e.participation_id ? (
                    <Atendimento ctx={ctx} entry={e} onDone={onDone} />
                  ) : (
                    <button
                      type="button"
                      className="link-button"
                      onClick={() => setShown(e.participation_id)}
                    >
                      Ver comanda de {e.ticket_code}
                    </button>
                  ))}
                {ctx.isOwner && (e.status === "waiting" || e.status === "called") && (
                  <DonoAcoes ctx={ctx} entry={e} queue={queue} queues={queues} onDone={onDone} />
                )}
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}

/** Cancelar (com motivo) e transferir (com consentimento) — só o dono. */
function DonoAcoes({
  ctx,
  entry,
  queue,
  queues,
  onDone,
}: {
  ctx: StaffContext;
  entry: Entry;
  queue: StaffQueue;
  queues: StaffQueue[];
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  const [mode, setMode] = useState<"" | "cancelar" | "transferir">("");
  const [reason, setReason] = useState("");
  const [target, setTarget] = useState("");
  const [consent, setConsent] = useState(false);
  const targets = queues.filter(
    (q) => q.queue_id !== queue.queue_id && q.status === "open" && q.professional.status === "active",
  );
  const base = { establishmentId: ctx.estId, participationId: entry.participation_id };
  const ready = reason.trim().length >= 3 && (mode === "cancelar" || (target !== "" && consent));

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    // Sem motivo, ou transferência sem destino ou sem a concordância marcada, nada é enviado.
    if (!ready) return;
    const out =
      mode === "cancelar"
        ? await cmd.run(`cancelar:${entry.participation_id}:${entry.version}`, (key) =>
            api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/cancel", {
              params: { path: base, header: { "Idempotency-Key": key } },
              body: { expected_version: entry.version, reason },
            }),
          )
        : await cmd.run(`transferir:${entry.participation_id}:${entry.version}:${target}`, (key) =>
            api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/transfer", {
              params: { path: base, header: { "Idempotency-Key": key } },
              body: {
                expected_version: entry.version,
                target_queue_id: target,
                reason,
                customer_consent: { given: true, method: "in_person_verbal" },
              },
            }),
          );
    if (out.ok) {
      setMode("");
      setReason("");
      await onDone();
    } else if (out.problem?.status === 409) await onDone();
  }

  if (!mode) {
    return (
      <div className="actions">
        <button type="button" className="button button--secondary" onClick={() => setMode("cancelar")}>
          Cancelar participação
        </button>
        {entry.status === "waiting" && targets.length > 0 && (
          <button type="button" className="button button--secondary" onClick={() => setMode("transferir")}>
            Transferir
          </button>
        )}
      </div>
    );
  }
  return (
    <form
      onSubmit={onSubmit}
      noValidate
      className="card"
      aria-label={mode === "cancelar" ? "Cancelar participação" : "Transferir participação"}
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {mode === "transferir" && (
        <>
          <label htmlFor={`destino-${entry.participation_id}`}>Fila de destino</label>
          <select
            id={`destino-${entry.participation_id}`}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            className="select"
          >
            <option value="">Escolha…</option>
            {targets.map((q) => (
              <option key={q.queue_id} value={q.queue_id}>
                {q.professional.display_name}
              </option>
            ))}
          </select>
          <p className="muted">
            O cliente vai para o FIM da fila escolhida, com nova senha, e mantém os preços.
          </p>
          <label className="choice">
            <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} />O
            cliente concordou pessoalmente com a transferência
          </label>
        </>
      )}
      <Field
        label="Motivo"
        required
        minLength={3}
        maxLength={500}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="actions">
        <Submit busy={cmd.busy !== null}>
          {mode === "cancelar" ? "Confirmar cancelamento" : "Confirmar transferência"}
        </Submit>
        <button type="button" className="button button--secondary" onClick={() => setMode("")}>
          Voltar
        </button>
      </div>
      {!ready && (
        <p className="muted">
          Preencha o motivo{mode === "transferir" ? ", o destino e a concordância" : ""} para confirmar.
        </p>
      )}
    </form>
  );
}
