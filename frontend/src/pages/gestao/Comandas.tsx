import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { Field, Notice, Submit } from "../../ui/ui";
import { ItensDaComanda } from "../comanda/comanda";
import { liveItems, reportedText, type Order } from "../comanda/textos";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Service = components["schemas"]["Service"];
type Filter = "pending_review" | "awaiting_payment" | "";

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/**
 * Comandas da loja (dono) ou dos próprios atendimentos (profissional). O dono revisa
 * interrupções e corrige valores fechados; o pagamento em si chega nas próximas etapas.
 */
export function Comandas({ ctx }: { ctx: StaffContext }) {
  const [filter, setFilter] = useState<Filter>(ctx.isOwner ? "pending_review" : "");
  const [orders, setOrders] = useState<Order[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const fetchPage = useCallback(
    (cursor: string | null) =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/orders", {
          params: {
            path: { establishmentId: ctx.estId },
            query: { ...(filter ? { status: [filter] } : {}), limit: 20, ...(cursor ? { cursor } : {}) },
          },
        }),
      ),
    [ctx.estId, filter],
  );
  const reload = useCallback(async () => {
    const out = await fetchPage(null);
    if (out.ok) {
      setOrders(out.data.data);
      setNext(out.data.page.next_cursor ?? null);
    } else setError(problemMessage(out.problem, out.offline));
  }, [fetchPage]);
  useEffect(() => {
    let active = true;
    void fetchPage(null).then((out) => {
      if (!active) return;
      if (out.ok) {
        setOrders(out.data.data);
        setNext(out.data.page.next_cursor ?? null);
        setError(null);
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
      setOrders((list) => [...(list ?? []), ...out.data.data]);
      setNext(out.data.page.next_cursor ?? null);
    }
  }

  const replace = (o: Order) => setOrders((list) => (list ?? []).map((x) => (x.id === o.id ? o : x)));

  return (
    <div>
      <label htmlFor="filtro-comandas">Mostrar</label>{" "}
      <select
        id="filtro-comandas"
        className="select"
        value={filter}
        onChange={(e) => setFilter(e.target.value as Filter)}
      >
        <option value="pending_review">Interrompidas para revisar</option>
        <option value="awaiting_payment">Aguardando pagamento</option>
        <option value="">Todas</option>
      </select>
      {error && <Notice kind="erro">{error}</Notice>}
      {!orders && !error && <p role="status">Carregando comandas…</p>}
      {orders?.length === 0 && <p>Nenhuma comanda aqui.</p>}
      <ul className="cards">
        {(orders ?? []).map((o) => (
          <li key={o.id} className="card">
            <h3>Comanda de {when.format(new Date(o.created_at))}</h3>
            <Cliente ctx={ctx} order={o} />
            <ItensDaComanda order={o} showRemoved />
            {ctx.isOwner && o.status === "pending_review" && (
              <Revisar ctx={ctx} order={o} onSaved={replace} onConflict={reload} />
            )}
            {ctx.isOwner && o.status === "awaiting_payment" && (
              <Corrigir ctx={ctx} order={o} onSaved={replace} onConflict={reload} />
            )}
          </li>
        ))}
      </ul>
      {next && (
        <button type="button" className="button button--secondary" onClick={() => void more()}>
          Carregar mais
        </button>
      )}
    </div>
  );
}

/** Senha, nick e motivo da interrupção (dados que a equipe pode ver). */
function Cliente({ ctx, order }: { ctx: StaffContext; order: Order }) {
  const [info, setInfo] = useState<string>("");
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/participations/{participationId}", {
        params: { path: { establishmentId: ctx.estId, participationId: order.participation_id } },
      }),
    ).then((out) => {
      if (!active || !out.ok) return;
      const p = out.data.data;
      setInfo(`${p.ticket_code} · ${p.nick}${p.end_reason ? ` · motivo informado: ${p.end_reason}` : ""}`);
    });
    return () => {
      active = false;
    };
  }, [ctx.estId, order.participation_id]);
  return info ? <p className="muted">{info}</p> : null;
}

/**
 * Revisão da interrupção (T-O-07/08): o dono decide o resultado de cada serviço, partindo do que o
 * profissional informou. Só "feito por inteiro" é cobrado; soma zero encerra sem cobrança.
 */
function Revisar({
  ctx,
  order,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  order: Order;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const items = liveItems(order);
  const [outcome, setOutcome] = useState<Record<string, string>>(() =>
    Object.fromEntries(items.map((i) => [i.id, i.reported_outcome ?? ""])),
  );
  const [note, setNote] = useState("");
  const cmd = useCommand();
  const charged = items
    .filter((i) => outcome[i.id] === "fully_performed")
    .reduce((s, i) => s + i.unit_price.amount_cents, 0);
  const ready = note.trim().length >= 3 && items.every((i) => outcome[i.id]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    const out = await cmd.run(`revisar:${order.id}:${order.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/close-interruption", {
        params: {
          path: { establishmentId: ctx.estId, orderId: order.id },
          header: { "Idempotency-Key": key },
        },
        body: {
          expected_version: order.version,
          review_note: note,
          items: items.map((i) => ({
            item_id: i.id,
            outcome: outcome[i.id] as "fully_performed" | "partially_performed" | "not_started",
          })),
        },
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }
  return (
    <form onSubmit={onSubmit} noValidate aria-label="Revisar interrupção">
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {items.map((i) => (
        <fieldset key={i.id} className="choices">
          <legend>
            {i.name} — {formatCents(i.unit_price.amount_cents)}
          </legend>
          {(["fully_performed", "partially_performed", "not_started"] as const).map((v) => (
            <label key={v} className="choice">
              <input
                type="radio"
                name={`revisao-${i.id}`}
                checked={outcome[i.id] === v}
                onChange={() => setOutcome((m) => ({ ...m, [i.id]: v }))}
              />
              {reportedText[v]}
              {v === "fully_performed" ? " (cobrado)" : " (não cobrado)"}
            </label>
          ))}
        </fieldset>
      ))}
      <Field
        label="Nota da revisão"
        required
        minLength={3}
        maxLength={500}
        value={note}
        onChange={(e) => setNote(e.target.value)}
      />
      <p>
        Valor depois da revisão: <strong>{formatCents(charged)}</strong>
        {charged === 0 && " — a comanda será encerrada sem cobrança."}
      </p>
      <Submit busy={cmd.busy !== null}>Fechar revisão</Submit>
    </form>
  );
}

/**
 * Correção depois de fechado (T-O-11): só por item — retirar a cobrança de um serviço ou incluir
 * um serviço feito e não registrado (preço atual do catálogo). Pede a senha de novo.
 */
function Corrigir({
  ctx,
  order,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  order: Order;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [services, setServices] = useState<Service[]>([]);
  const [uncharge, setUncharge] = useState<string[]>([]);
  const [add, setAdd] = useState("");
  const [reason, setReason] = useState("");
  const cmd = useCommand();
  useEffect(() => {
    if (!open) return;
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/services", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (active && out.ok) setServices(out.data.data);
    });
    return () => {
      active = false;
    };
  }, [open, ctx.estId]);
  const inOrder = new Set(liveItems(order).map((i) => i.service_id));
  const ready = reason.trim().length >= 3 && (uncharge.length > 0 || add !== "");

  async function submit() {
    if (!ready) return;
    const body = {
      expected_version: order.version,
      reason,
      ...(uncharge.length > 0 ? { uncharge_item_ids: uncharge } : {}),
      ...(add ? { add_performed_service_ids: [add] } : {}),
    };
    const out = await cmd.run(`corrigir:${order.id}:${order.version}:${uncharge.join(",")}:${add}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/adjustments", {
        params: {
          path: { establishmentId: ctx.estId, orderId: order.id },
          header: { "Idempotency-Key": key },
        },
        body,
      }),
    );
    if (out.ok) {
      setOpen(false);
      setUncharge([]);
      setAdd("");
      setReason("");
      onSaved(out.data.data);
    } else if (out.problem?.status === 409) await onConflict();
  }
  if (!open)
    return (
      <button type="button" className="button button--secondary" onClick={() => setOpen(true)}>
        Corrigir valor
      </button>
    );
  return (
    <form
      noValidate
      aria-label="Corrigir valor"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} onRetry={() => void submit()} />
      <fieldset className="choices">
        <legend>Retirar a cobrança de</legend>
        {liveItems(order)
          .filter((i) => i.charged)
          .map((i) => (
            <label key={i.id} className="choice">
              <input
                type="checkbox"
                checked={uncharge.includes(i.id)}
                onChange={(e) =>
                  setUncharge((c) => (e.target.checked ? [...c, i.id] : c.filter((x) => x !== i.id)))
                }
              />
              {i.name} — {formatCents(i.unit_price.amount_cents)}
            </label>
          ))}
      </fieldset>
      <label htmlFor={`incluir-${order.id}`}>Incluir serviço feito e não registrado</label>
      <select
        id={`incluir-${order.id}`}
        className="select"
        value={add}
        onChange={(e) => setAdd(e.target.value)}
      >
        <option value="">Nenhum</option>
        {services
          .filter((s) => !inOrder.has(s.id))
          .map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} — {formatCents(s.price.amount_cents)}
            </option>
          ))}
      </select>
      <Field
        label="Motivo da correção"
        required
        minLength={3}
        maxLength={500}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="actions">
        <Submit busy={cmd.busy !== null}>Salvar correção</Submit>
        <button type="button" className="button button--secondary" onClick={() => setOpen(false)}>
          Voltar
        </button>
      </div>
    </form>
  );
}
