import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import { Field, Notice, Submit } from "../../ui/ui";
import { ItensDaComanda } from "../comanda/comanda";
import { describeRequest, liveItems, reportedText, type Order } from "../comanda/textos";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Entry = components["schemas"]["StaffQueue"]["entries"][number];
type Service = components["schemas"]["Service"];
type Mode = "" | "editar" | "concluir" | "interromper" | "recusar";

/**
 * Comanda de quem está na fila, vista pela equipe do atendimento: decidir o pedido do cliente,
 * marcar serviço realizado, alterar serviços (com motivo), concluir ou interromper. Cada comando
 * envia a versão esperada; se outra pessoa mudou antes, a tela relê os dados (409).
 */
export function Atendimento({
  ctx,
  entry,
  onDone,
}: {
  ctx: StaffContext;
  entry: Entry;
  onDone: () => Promise<void>;
}) {
  const [order, setOrder] = useState<Order | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<Mode>("");
  const cmd = useCommand();

  const fetchOrder = useCallback(
    () =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/orders/{orderId}", {
          params: { path: { establishmentId: ctx.estId, orderId: entry.order_id } },
        }),
      ),
    [ctx.estId, entry.order_id],
  );
  const reload = useCallback(async () => {
    const out = await fetchOrder();
    if (out.ok) setOrder(out.data.data);
    else setError(problemMessage(out.problem, out.offline));
  }, [fetchOrder]);
  useEffect(() => {
    let active = true;
    void fetchOrder().then((out) => {
      if (!active) return;
      if (out.ok) setOrder(out.data.data);
      else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchOrder]);

  // Comanda atualizada por outra pessoa (cliente pediu alteração, outra aba concluiu…).
  useStreamMessages((m) => {
    if (m.type !== "order.updated" && m.type !== "payment.updated") return;
    const next = m.data as Order;
    setOrder((cur) => (cur && cur.id === next.id && next.version > cur.version ? next : cur));
  });

  if (error) return <Notice kind="erro">{error}</Notice>;
  if (!order) return <p role="status">Carregando comanda…</p>;
  const o = order;
  const inService = entry.status === "in_service";
  const pending = o.pending_change_request;
  const path = { establishmentId: ctx.estId, orderId: o.id };

  // Outra pessoa mudou a comanda ou a fila antes: relê as duas e mostra o estado atual.
  async function onConflict() {
    await reload();
    await onDone();
  }

  async function decidir(approve: boolean, reason = "") {
    if (!pending) return;
    const p = { ...path, changeRequestId: pending.id };
    const out = approve
      ? await cmd.run(`aprovar:${pending.id}:${o.version}`, (key) =>
          api.POST(
            "/v1/establishments/{establishmentId}/orders/{orderId}/change-requests/{changeRequestId}/approve",
            {
              params: { path: p, header: { "Idempotency-Key": key } },
              body: { expected_version: o.version },
            },
          ),
        )
      : await cmd.run(`recusar:${pending.id}:${o.version}`, (key) =>
          api.POST(
            "/v1/establishments/{establishmentId}/orders/{orderId}/change-requests/{changeRequestId}/reject",
            {
              params: { path: p, header: { "Idempotency-Key": key } },
              body: { expected_version: o.version, reason },
            },
          ),
        );
    if (out.ok) {
      setMode("");
      setOrder(out.data.data);
      await onDone(); // o aviso de pedido pendente some da fila
    } else if (out.problem?.status === 409) await onConflict();
  }

  async function marcar(itemId: string) {
    const out = await cmd.run(`realizado:${itemId}:${o.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/items/{itemId}/mark-performed", {
        params: { path: { ...path, itemId }, header: { "Idempotency-Key": key } },
        body: { expected_version: o.version },
      }),
    );
    if (out.ok) setOrder(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }

  return (
    <div className="card" aria-label={`Comanda de ${entry.nick}`}>
      <ItensDaComanda order={o} />
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {pending && (
        <div role="group" aria-label="Pedido do cliente">
          <p>
            <strong>Pedido do cliente:</strong> {describeRequest(o, pending)}.
          </p>
          {mode !== "recusar" && (
            <div className="actions">
              <button
                type="button"
                className="button"
                disabled={cmd.busy !== null}
                onClick={() => void decidir(true)}
              >
                Aprovar pedido
              </button>
              <button type="button" className="button button--secondary" onClick={() => setMode("recusar")}>
                Recusar pedido
              </button>
            </div>
          )}
          {mode === "recusar" && (
            <Recusar
              busy={cmd.busy !== null}
              onSubmit={(r) => void decidir(false, r)}
              onBack={() => setMode("")}
            />
          )}
        </div>
      )}
      {o.status === "open" && inService && (
        <ul className="order__items" aria-label="Marcar serviços realizados">
          {liveItems(o)
            .filter((i) => i.status === "accepted")
            .map((i) => (
              <li key={i.id}>
                <span>{i.name}</span>
                <button
                  type="button"
                  className="button button--secondary"
                  disabled={cmd.busy !== null}
                  onClick={() => void marcar(i.id)}
                >
                  Marcar {i.name} como realizado
                </button>
              </li>
            ))}
        </ul>
      )}
      {o.status === "open" && !mode && (
        <div className="actions">
          {!pending && (
            <button type="button" className="button button--secondary" onClick={() => setMode("editar")}>
              Alterar serviços
            </button>
          )}
          {inService && (
            <>
              <button
                type="button"
                className="button"
                disabled={Boolean(pending)}
                onClick={() => setMode("concluir")}
              >
                Concluir atendimento
              </button>
              <button
                type="button"
                className="button button--secondary"
                onClick={() => setMode("interromper")}
              >
                Interromper
              </button>
            </>
          )}
        </div>
      )}
      {pending && inService && <p className="muted">Decida o pedido do cliente antes de concluir.</p>}
      {mode === "editar" && (
        <Editar
          ctx={ctx}
          order={o}
          onBack={() => setMode("")}
          onSaved={(n) => {
            setMode("");
            setOrder(n);
            void onDone();
          }}
          onConflict={onConflict}
        />
      )}
      {(mode === "concluir" || mode === "interromper") && (
        <Encerrar
          ctx={ctx}
          entry={entry}
          order={o}
          interrupt={mode === "interromper"}
          onBack={() => setMode("")}
          onFinished={() => {
            setMode("");
            void onDone();
          }}
          onConflict={onConflict}
        />
      )}
    </div>
  );
}

function Recusar({
  busy,
  onSubmit,
  onBack,
}: {
  busy: boolean;
  onSubmit: (reason: string) => void;
  onBack: () => void;
}) {
  const [reason, setReason] = useState("");
  return (
    <form
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        if (reason.trim().length >= 3) onSubmit(reason);
      }}
    >
      <Field
        label="Motivo da recusa"
        required
        minLength={3}
        maxLength={500}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="actions">
        <Submit busy={busy}>Confirmar recusa</Submit>
        <button type="button" className="button button--secondary" onClick={onBack}>
          Voltar
        </button>
      </div>
    </form>
  );
}

/** Alterar serviços pela equipe (T-O-03): motivo obrigatório; item realizado não sai. */
function Editar({
  ctx,
  order,
  onBack,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  order: Order;
  onBack: () => void;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const [services, setServices] = useState<Service[] | null>(null);
  const current = liveItems(order);
  const [chosen, setChosen] = useState(() => current.map((i) => i.service_id));
  const [reason, setReason] = useState("");
  const cmd = useCommand();
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/services", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (active && out.ok) setServices(out.data.data.filter((s) => s.status === "active"));
    });
    return () => {
      active = false;
    };
  }, [ctx.estId]);
  const performed = new Set(current.filter((i) => i.status === "performed").map((i) => i.service_id));

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (reason.trim().length < 3 || chosen.length === 0) return;
    const out = await cmd.run(`editar:${order.id}:${order.version}:${[...chosen].sort().join(",")}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/orders/{orderId}/items", {
        params: {
          path: { establishmentId: ctx.estId, orderId: order.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: order.version, service_ids: chosen, reason },
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }
  return (
    <form onSubmit={onSubmit} noValidate aria-label="Alterar serviços da comanda">
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {!services && <p role="status">Carregando serviços…</p>}
      <fieldset className="choices">
        <legend>Serviços</legend>
        {(services ?? []).map((s) => (
          <label key={s.id} className="choice">
            <input
              type="checkbox"
              disabled={performed.has(s.id)}
              checked={chosen.includes(s.id)}
              onChange={(e) =>
                setChosen((c) => (e.target.checked ? [...c, s.id] : c.filter((x) => x !== s.id)))
              }
            />
            {s.name} —{" "}
            {formatCents(
              current.find((i) => i.service_id === s.id)?.unit_price.amount_cents ?? s.price.amount_cents,
            )}
            {performed.has(s.id) && " (já realizado)"}
          </label>
        ))}
      </fieldset>
      <Field
        label="Motivo"
        required
        minLength={3}
        maxLength={500}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        hint="Ex.: cliente pediu na cadeira."
      />
      <div className="actions">
        <Submit busy={cmd.busy !== null}>Salvar serviços</Submit>
        <button type="button" className="button button--secondary" onClick={onBack}>
          Voltar
        </button>
      </div>
    </form>
  );
}

/**
 * Concluir (cada serviço: realizado ou não, com motivo) ou interromper (motivo e quanto de cada
 * serviço foi feito). Nada é pré-marcado: a equipe escolhe o resultado de cada item.
 */
function Encerrar({
  ctx,
  entry,
  order,
  interrupt,
  onBack,
  onFinished,
  onConflict,
}: {
  ctx: StaffContext;
  entry: Entry;
  order: Order;
  interrupt: boolean;
  onBack: () => void;
  onFinished: () => void;
  onConflict: () => Promise<void>;
}) {
  const items = liveItems(order);
  const [outcome, setOutcome] = useState<Record<string, string>>({});
  const [why, setWhy] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const cmd = useCommand();
  const options = interrupt
    ? (["fully_performed", "partially_performed", "not_started"] as const)
    : (["performed", "not_performed"] as const);
  const label = (v: string) =>
    v === "performed" ? "realizado" : v === "not_performed" ? "não realizado" : reportedText[v];
  const complete = items.every((i) => outcome[i.id]);
  const reasonsOk = interrupt
    ? reason.trim().length >= 3
    : items.every((i) => outcome[i.id] !== "not_performed" || (why[i.id] ?? "").trim().length >= 3);
  const someDone = interrupt || items.some((i) => outcome[i.id] === "performed");

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!complete || !reasonsOk) return;
    const list = items.map((i) => {
      const value = outcome[i.id] ?? "";
      return value === "not_performed"
        ? { item_id: i.id, outcome: value, reason: why[i.id] ?? "" }
        : { item_id: i.id, outcome: value };
    });
    const base = { establishmentId: ctx.estId, participationId: entry.participation_id };
    const out = interrupt
      ? await cmd.run(`interromper:${entry.participation_id}:${entry.version}:${order.version}`, (key) =>
          api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/interrupt", {
            params: { path: base, header: { "Idempotency-Key": key } },
            body: {
              expected_version: entry.version,
              order_expected_version: order.version,
              reason,
              items: list as {
                item_id: string;
                outcome: "fully_performed" | "partially_performed" | "not_started";
              }[],
            },
          }),
        )
      : await cmd.run(`concluir:${entry.participation_id}:${entry.version}:${order.version}`, (key) =>
          api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/complete", {
            params: { path: base, header: { "Idempotency-Key": key } },
            body: {
              expected_version: entry.version,
              order_expected_version: order.version,
              items: list as { item_id: string; outcome: "performed" | "not_performed"; reason?: string }[],
            },
          }),
        );
    if (out.ok) onFinished();
    else if (out.problem?.status === 409) await onConflict();
  }

  return (
    <form
      onSubmit={onSubmit}
      noValidate
      aria-label={interrupt ? "Interromper atendimento" : "Concluir atendimento"}
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {items.map((i) => (
        <fieldset key={i.id} className="choices">
          <legend>
            {i.name} — {formatCents(i.unit_price.amount_cents)}
          </legend>
          {options.map((v) => (
            <label key={v} className="choice">
              <input
                type="radio"
                name={`resultado-${i.id}`}
                checked={outcome[i.id] === v}
                onChange={() => setOutcome((m) => ({ ...m, [i.id]: v }))}
              />
              {label(v)}
            </label>
          ))}
          {outcome[i.id] === "not_performed" && (
            <Field
              label={`Por que ${i.name} não foi feito?`}
              required
              minLength={3}
              maxLength={500}
              value={why[i.id] ?? ""}
              onChange={(e) => setWhy((m) => ({ ...m, [i.id]: e.target.value }))}
            />
          )}
        </fieldset>
      ))}
      {interrupt && (
        <>
          <Field
            label="Motivo da interrupção"
            required
            minLength={3}
            maxLength={500}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <p className="muted">
            O dono revisa depois: só serviços feitos por inteiro são cobrados. Interrupção não gera pontos.
          </p>
        </>
      )}
      {!interrupt && complete && !someDone && (
        <Notice kind="info">Nenhum serviço realizado: use “Interromper” em vez de concluir.</Notice>
      )}
      <div className="actions">
        <Submit busy={cmd.busy !== null}>
          {interrupt ? "Confirmar interrupção" : "Confirmar conclusão"}
        </Submit>
        <button type="button" className="button button--secondary" onClick={onBack}>
          Voltar
        </button>
      </div>
      {(!complete || !reasonsOk) && (
        <p className="muted">
          Escolha o resultado de cada serviço{interrupt ? " e o motivo" : ""} para confirmar.
        </p>
      )}
    </form>
  );
}
