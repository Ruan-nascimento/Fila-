import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Check, CircleCheck, Pause, Pencil, X } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import { Button, Choice, ChoiceGroup, LoadError, Loading, Notice, Submit, TextArea } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { ItensDaComanda } from "../comanda/comanda";
import { describeRequest, liveItems, reportedText, type Order } from "../comanda/textos";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Entry = components["schemas"]["StaffQueue"]["entries"][number];
type Service = components["schemas"]["Service"];
type Mode = "editar" | "concluir" | "interromper" | "recusar";

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
  const panel = useInlinePanel<Mode>();
  const mode = panel.open;
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
    if (out.ok) {
      setOrder(out.data.data);
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
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

  if (error && !order) return <LoadError message={error} onRetry={() => void reload()} />;
  if (!order) return <Loading>Carregando comanda…</Loading>;
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
      panel.hide();
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

  const toMark = liveItems(o).filter((i) => i.status === "accepted");
  return (
    <div className="grid gap-4" aria-label={`Comanda de ${entry.nick}`} role="group">
      <ItensDaComanda order={o} />
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {pending && (
        <div
          role="group"
          aria-label="Pedido do cliente"
          className="grid gap-3 rounded-xl border border-warning/40 bg-warning-soft p-4"
        >
          <p className="m-0 flex items-start gap-2">
            <Pencil aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-warning" />
            <span>
              <strong>Pedido do cliente:</strong> {describeRequest(o, pending)}.
            </span>
          </p>
          {mode !== "recusar" ? (
            <div className="flex flex-wrap gap-2">
              <Button icon={Check} disabled={cmd.busy !== null} onClick={() => void decidir(true)}>
                Aprovar pedido
              </Button>
              <Button id={panel.triggerId} variant="secondary" icon={X} onClick={() => panel.show("recusar")}>
                Recusar pedido
              </Button>
            </div>
          ) : (
            <Recusar
              panelId={panel.panelId}
              onKeyDown={panel.onKeyDown}
              busy={cmd.busy !== null}
              onSubmit={(r) => void decidir(false, r)}
              onBack={panel.hide}
            />
          )}
        </div>
      )}
      {o.status === "open" && inService && toMark.length > 0 && (
        <div className="grid gap-2" role="group" aria-label="Marcar serviços realizados">
          <p className="m-0 text-sm font-semibold">Marcar como realizado</p>
          <div className="flex flex-wrap gap-2">
            {toMark.map((i) => (
              <Button
                key={i.id}
                variant="secondary"
                icon={CircleCheck}
                disabled={cmd.busy !== null}
                aria-label={`Marcar ${i.name} como realizado`}
                onClick={() => void marcar(i.id)}
              >
                {i.name}
              </Button>
            ))}
          </div>
        </div>
      )}
      {o.status === "open" && !mode && (
        <div className="grid gap-2">
          <div className="flex flex-wrap gap-2">
            {inService && (
              <Button icon={CircleCheck} disabled={Boolean(pending)} onClick={() => panel.show("concluir")}>
                Concluir atendimento
              </Button>
            )}
            {!pending && (
              <Button variant="secondary" icon={Pencil} onClick={() => panel.show("editar")}>
                Alterar serviços
              </Button>
            )}
            {inService && (
              <Button variant="secondary" icon={Pause} onClick={() => panel.show("interromper")}>
                Interromper
              </Button>
            )}
          </div>
          {pending && inService && (
            <p className="m-0 text-sm text-fg-muted">Decida o pedido do cliente antes de concluir.</p>
          )}
        </div>
      )}
      {mode === "editar" && (
        <Editar
          ctx={ctx}
          order={o}
          panelId={panel.panelId}
          onKeyDown={panel.onKeyDown}
          onBack={panel.hide}
          onSaved={(n) => {
            panel.hide();
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
          panelId={panel.panelId}
          onKeyDown={panel.onKeyDown}
          onBack={panel.hide}
          onFinished={() => {
            panel.hide();
            void onDone();
          }}
          onConflict={onConflict}
        />
      )}
    </div>
  );
}

type PanelProps = {
  panelId: string;
  onKeyDown: (e: React.KeyboardEvent) => void;
};

const panelForm = "grid gap-4 rounded-xl border border-line bg-surface-2 p-4";

function Recusar({
  busy,
  onSubmit,
  onBack,
  panelId,
  onKeyDown,
}: {
  busy: boolean;
  onSubmit: (reason: string) => void;
  onBack: () => void;
} & PanelProps) {
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
  return (
    <form
      id={panelId}
      onKeyDown={onKeyDown}
      noValidate
      aria-label="Recusar pedido"
      className="grid gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if (reason.trim().length >= 3) onSubmit(reason);
      }}
    >
      <TextArea
        label="Motivo da recusa"
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={reason}
        error={tried && reason.trim().length < 3 ? "Escreva o motivo (pelo menos 3 caracteres)." : undefined}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="flex flex-wrap gap-2">
        <Submit busy={busy}>Confirmar recusa</Submit>
        <Button variant="secondary" onClick={onBack}>
          Voltar
        </Button>
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
  panelId,
  onKeyDown,
}: {
  ctx: StaffContext;
  order: Order;
  onBack: () => void;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
} & PanelProps) {
  const [services, setServices] = useState<Service[] | null>(null);
  const current = liveItems(order);
  const [chosen, setChosen] = useState(() => current.map((i) => i.service_id));
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
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
    setTried(true);
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
    <form
      id={panelId}
      onKeyDown={onKeyDown}
      onSubmit={onSubmit}
      noValidate
      aria-label="Alterar serviços da comanda"
      className={panelForm}
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {!services && <Loading>Carregando serviços…</Loading>}
      <ChoiceGroup
        legend="Serviços"
        hint="Preço registrado na comanda é mantido; serviço novo entra pelo preço atual."
      >
        {(services ?? []).map((s) => (
          <Choice
            key={s.id}
            type="checkbox"
            disabled={performed.has(s.id)}
            checked={chosen.includes(s.id)}
            onChange={(e) =>
              setChosen((c) => (e.target.checked ? [...c, s.id] : c.filter((x) => x !== s.id)))
            }
            label={s.name}
            description={performed.has(s.id) ? "já realizado" : undefined}
            trailing={formatCents(
              current.find((i) => i.service_id === s.id)?.unit_price.amount_cents ?? s.price.amount_cents,
            )}
          />
        ))}
      </ChoiceGroup>
      <TextArea
        label="Motivo"
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={reason}
        error={tried && reason.trim().length < 3 ? "Escreva o motivo (pelo menos 3 caracteres)." : undefined}
        onChange={(e) => setReason(e.target.value)}
        hint="Ex.: cliente pediu na cadeira."
      />
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>Salvar serviços</Submit>
        <Button variant="secondary" onClick={onBack}>
          Voltar
        </Button>
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
  panelId,
  onKeyDown,
}: {
  ctx: StaffContext;
  entry: Entry;
  order: Order;
  interrupt: boolean;
  onBack: () => void;
  onFinished: () => void;
  onConflict: () => Promise<void>;
} & PanelProps) {
  const items = liveItems(order);
  const [outcome, setOutcome] = useState<Record<string, string>>({});
  const [why, setWhy] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  const options = interrupt
    ? (["fully_performed", "partially_performed", "not_started"] as const)
    : (["performed", "not_performed"] as const);
  const label = (v: string) =>
    v === "performed" ? "realizado" : v === "not_performed" ? "não realizado" : (reportedText[v] ?? v);
  const complete = items.every((i) => outcome[i.id]);
  const reasonsOk = interrupt
    ? reason.trim().length >= 3
    : items.every((i) => outcome[i.id] !== "not_performed" || (why[i.id] ?? "").trim().length >= 3);
  const someDone = interrupt || items.some((i) => outcome[i.id] === "performed");

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
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
      id={panelId}
      onKeyDown={onKeyDown}
      onSubmit={onSubmit}
      noValidate
      aria-label={interrupt ? "Interromper atendimento" : "Concluir atendimento"}
      className={panelForm}
    >
      <div className="grid gap-1">
        <p className="m-0 font-semibold" data-autofocus tabIndex={-1}>
          {interrupt ? "Interromper atendimento" : "Concluir atendimento"}
        </p>
        <p className="m-0 text-sm text-fg-muted">
          {interrupt
            ? "Use quando o atendimento parou no meio. O dono revisa depois: só serviços feitos por inteiro são cobrados e a interrupção não gera pontos."
            : "Informe o resultado de cada serviço. A comanda vai para pagamento com o que foi realizado."}
        </p>
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {items.map((i) => (
        <fieldset key={i.id} className="m-0 grid gap-2 border-0 p-0">
          <legend className="mb-2 flex w-full justify-between gap-3 p-0 font-semibold">
            <span>
              {i.name} — {formatCents(i.unit_price.amount_cents)}
            </span>
          </legend>
          <div className={cx("grid gap-2", options.length === 2 ? "grid-cols-2" : "sm:grid-cols-3")}>
            {options.map((v) => (
              <Choice
                key={v}
                type="radio"
                name={`resultado-${i.id}`}
                checked={outcome[i.id] === v}
                onChange={() => setOutcome((m) => ({ ...m, [i.id]: v }))}
                label={label(v)}
              />
            ))}
          </div>
          {outcome[i.id] === "not_performed" && (
            <TextArea
              label={`Por que ${i.name} não foi feito?`}
              required
              minLength={3}
              maxLength={500}
              rows={2}
              value={why[i.id] ?? ""}
              onChange={(e) => setWhy((m) => ({ ...m, [i.id]: e.target.value }))}
            />
          )}
        </fieldset>
      ))}
      {interrupt && (
        <TextArea
          label="Motivo da interrupção"
          required
          minLength={3}
          maxLength={500}
          rows={2}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
      )}
      {!interrupt && complete && !someDone && (
        <Notice kind="info">
          <p>Nenhum serviço realizado: use “Interromper” em vez de concluir.</p>
        </Notice>
      )}
      {tried && (!complete || !reasonsOk) && (
        <p role="alert" className="m-0 text-sm font-semibold text-danger">
          Escolha o resultado de cada serviço{interrupt ? " e o motivo" : " e explique o que não foi feito"}{" "}
          para confirmar.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>
          {interrupt ? "Confirmar interrupção" : "Confirmar conclusão"}
        </Submit>
        <Button variant="secondary" onClick={onBack}>
          Voltar
        </Button>
      </div>
    </form>
  );
}
