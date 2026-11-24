import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Check, CircleCheck, Package, Pause, Pencil, X } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import { Button, Choice, LoadError, Loading, Notice, Submit, TextArea } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { ItensDaComanda } from "../comanda/comanda";
import { describeRequest, liveItems, reportedText, unitsText, type Order } from "../comanda/textos";
import { Quantidade } from "../../features/catalogo/Quantidade";
import { SeletorItens } from "../fila/SeletorItens";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Entry = components["schemas"]["StaffQueue"]["entries"][number];
type Service = components["schemas"]["Service"];
type Product = components["schemas"]["Product"];
type Category = components["schemas"]["CatalogCategory"];
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

  /** Registra unidades feitas/entregues em valor ABSOLUTO (repetir não soma): T-O-13/T-O-14. */
  async function marcar(itemId: string, units?: number) {
    const out = await cmd.run(`realizado:${itemId}:${o.version}:${units ?? "todas"}`, (key) =>
      units === undefined
        ? api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/items/{itemId}/mark-performed", {
            params: { path: { ...path, itemId }, header: { "Idempotency-Key": key } },
            body: { expected_version: o.version },
          })
        : api.PUT("/v1/establishments/{establishmentId}/orders/{orderId}/items/{itemId}/performed-quantity", {
            params: { path: { ...path, itemId }, header: { "Idempotency-Key": key } },
            body: { expected_version: o.version, performed_quantity: units },
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
        <div className="grid gap-2" role="group" aria-label="Registrar o que foi feito">
          <p className="m-0 text-sm font-semibold">Registrar o que foi feito</p>
          <ul className="m-0 grid list-none gap-2 p-0">
            {toMark.map((i) => {
              const product = i.kind === "product";
              const verb = product ? "entregue" : "realizado";
              return (
                <li key={i.id} className="flex flex-wrap items-center justify-between gap-2">
                  <span className="min-w-0">
                    <strong>{i.name}</strong>
                    {i.quantity > 1 && <span className="text-fg-muted"> · {unitsText(i)}</span>}
                  </span>
                  <span className="flex flex-wrap gap-2">
                    {i.quantity > 1 && (
                      <Button
                        variant="secondary"
                        disabled={cmd.busy !== null}
                        aria-label={`Registrar mais 1 ${i.name} ${verb} (${i.performed_quantity + 1} de ${i.quantity})`}
                        onClick={() => void marcar(i.id, i.performed_quantity + 1)}
                      >
                        +1 {verb}
                      </Button>
                    )}
                    <Button
                      variant="secondary"
                      icon={product ? Package : CircleCheck}
                      disabled={cmd.busy !== null}
                      aria-label={
                        i.quantity > 1
                          ? `Marcar todas as ${i.quantity} unidades de ${i.name} como ${verb}s`
                          : `Marcar ${i.name} como ${verb}`
                      }
                      onClick={() => void marcar(i.id)}
                    >
                      {product
                        ? i.quantity > 1
                          ? "Entregar todos"
                          : "Entregar"
                        : i.quantity > 1
                          ? "Todos realizados"
                          : "Realizado"}
                    </Button>
                  </span>
                </li>
              );
            })}
          </ul>
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
                Alterar itens
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

/**
 * Alterar itens pela equipe (T-O-03), com quantidade: motivo obrigatório; unidades já realizadas
 * não saem; linha existente mantém o preço aceito; linha nova entra pelo preço atual.
 */
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
  const [catalog, setCatalog] = useState<{
    services: Service[];
    products: Product[];
    categories: Category[];
  } | null>(null);
  const current = liveItems(order);
  const [s, setS] = useState<Record<string, number>>(() =>
    Object.fromEntries(current.filter((i) => i.service_id).map((i) => [i.service_id as string, i.quantity])),
  );
  const [p, setP] = useState<Record<string, number>>(() =>
    Object.fromEntries(current.filter((i) => i.product_id).map((i) => [i.product_id as string, i.quantity])),
  );
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  useEffect(() => {
    let active = true;
    const path = { params: { path: { establishmentId: ctx.estId } } };
    void Promise.all([
      call(() => api.GET("/v1/establishments/{establishmentId}/services", path)),
      call(() => api.GET("/v1/establishments/{establishmentId}/products", path)),
      call(() => api.GET("/v1/establishments/{establishmentId}/catalog-categories", path)),
    ]).then(([sv, pr, ca]) => {
      if (!active || !sv.ok) return;
      setCatalog({
        services: sv.data.data.filter(
          (x) => x.status === "active" || current.some((i) => i.service_id === x.id),
        ),
        products: pr.ok
          ? pr.data.data.filter((x) => x.status === "active" || current.some((i) => i.product_id === x.id))
          : [],
        categories: ca.ok ? ca.data.data.filter((c) => c.status === "active") : [],
      });
    });
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- lê o catálogo uma vez ao abrir
  }, [ctx.estId]);
  const lineOf = (kind: "service" | "product", id: string) =>
    current.find((i) => (kind === "product" ? i.product_id === id : i.service_id === id));
  const services = (catalog?.services ?? []).map((x) => ({
    id: x.id,
    name: x.name,
    price: x.price,
    duration_minutes: x.duration_minutes,
    professional_ids: x.professional_ids,
    category_id: x.category_id ?? null,
    display_order: x.display_order ?? 0,
  }));
  const products = (catalog?.products ?? []).map((x) => ({
    id: x.id,
    name: x.name,
    description: x.description,
    price: x.price,
    image: x.image,
    category_id: x.category_id,
    display_order: x.display_order,
  }));
  const serviceUnits = Object.values(s).reduce((a, b) => a + b, 0);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
    if (reason.trim().length < 3 || serviceUnits === 0) return;
    const items = [
      ...Object.entries(s).map(([id, n]) => ({ service_id: id, quantity: n })),
      ...Object.entries(p).map(([id, n]) => ({ product_id: id, quantity: n })),
    ];
    const sig = items
      .map((i) => `${"service_id" in i ? i.service_id : i.product_id}x${i.quantity}`)
      .sort()
      .join(",");
    const out = await cmd.run(`editar:${order.id}:${order.version}:${sig}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/orders/{orderId}/items", {
        params: {
          path: { establishmentId: ctx.estId, orderId: order.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: order.version, items, reason },
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }
  const set = (setter: typeof setS) => (id: string, n: number) =>
    setter((c) => {
      const next = { ...c };
      if (n > 0) next[id] = n;
      else delete next[id];
      return next;
    });
  return (
    <form
      id={panelId}
      onKeyDown={onKeyDown}
      onSubmit={onSubmit}
      noValidate
      aria-label="Alterar itens da comanda"
      className={cx(panelForm, "min-w-0")}
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {!catalog && <Loading>Carregando catálogo…</Loading>}
      {catalog && (
        <SeletorItens
          title="Serviços"
          services={services}
          products={products}
          categories={catalog.categories}
          services_chosen={s}
          products_chosen={p}
          onService={set(setS)}
          onProduct={set(setP)}
          max={50}
          min={(kind, id) => lineOf(kind, id)?.performed_quantity ?? 0}
          priceOf={(kind, id) => lineOf(kind, id)?.unit_price.amount_cents}
        />
      )}
      <p className="m-0 text-sm text-fg-muted">
        Preço registrado na comanda é mantido; item novo entra pelo preço atual. O limite de unidades da loja
        é conferido pelo servidor.
      </p>
      {tried && serviceUnits === 0 && (
        <p role="alert" className="m-0 text-sm font-semibold text-danger">
          Mantenha pelo menos um serviço.
        </p>
      )}
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
        <Submit busy={cmd.busy !== null}>Salvar itens</Submit>
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
  const [units, setUnits] = useState<Record<string, number>>({});
  const [why, setWhy] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  const options = interrupt
    ? (["fully_performed", "partially_performed", "not_started"] as const)
    : (["performed", "not_performed"] as const);
  const label = (v: string) =>
    v === "performed" ? "realizado" : v === "not_performed" ? "não realizado" : (reportedText[v] ?? v);
  /** Unidades informadas: conclusão "realizado" = todas por padrão; interrupção = as já registradas. */
  const unitsOf = (i: (typeof items)[number]) =>
    units[i.id] ??
    (interrupt ? (outcome[i.id] === "fully_performed" ? i.quantity : i.performed_quantity) : i.quantity);
  const needsWhy = (i: (typeof items)[number]) =>
    !interrupt &&
    (outcome[i.id] === "not_performed" || (outcome[i.id] === "performed" && unitsOf(i) < i.quantity));
  const complete = items.every((i) => outcome[i.id]);
  const reasonsOk = interrupt
    ? reason.trim().length >= 3
    : items.every((i) => !needsWhy(i) || (why[i.id] ?? "").trim().length >= 3);
  const someDone = interrupt || items.some((i) => outcome[i.id] === "performed" && i.kind !== "product");

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
    if (!complete || !reasonsOk) return;
    const list = items.map((i) => {
      const value = outcome[i.id] ?? "";
      const item: { item_id: string; outcome: string; quantity?: number; reason?: string } = {
        item_id: i.id,
        outcome: value,
      };
      // Unidades só quando a linha tem mais de uma (quantidade 1 segue a regra anterior).
      if (i.quantity > 1 && value !== "not_performed") item.quantity = unitsOf(i);
      if (needsWhy(i)) item.reason = why[i.id] ?? "";
      return item;
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
                quantity?: number;
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
              items: list as {
                item_id: string;
                outcome: "performed" | "not_performed";
                quantity?: number;
                reason?: string;
              }[],
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
            ? "Use quando o atendimento parou no meio. Informe quantas unidades de cada item foram feitas por inteiro. O dono revisa depois: só unidades completas são cobradas e a interrupção não gera pontos."
            : "Informe o resultado de cada item e quantas unidades foram feitas (ou entregues). A comanda vai para pagamento com o que foi realizado."}
        </p>
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {items.map((i) => (
        <fieldset key={i.id} className="m-0 grid gap-2 border-0 p-0">
          <legend className="mb-2 flex w-full flex-wrap justify-between gap-3 p-0 font-semibold">
            <span>
              {i.quantity > 1 ? `${i.quantity} × ` : ""}
              {i.name} — {formatCents(i.unit_price.amount_cents)}
              {i.kind === "product" && <span className="font-normal text-fg-muted"> (produto)</span>}
            </span>
            {i.performed_quantity > 0 && (
              <span className="text-sm font-normal text-fg-muted">{unitsText(i)}</span>
            )}
          </legend>
          <div className={cx("grid gap-2", options.length === 2 ? "grid-cols-2" : "sm:grid-cols-3")}>
            {options.map((v) => (
              <Choice
                key={v}
                type="radio"
                name={`resultado-${i.id}`}
                checked={outcome[i.id] === v}
                onChange={() => {
                  setOutcome((m) => ({ ...m, [i.id]: v }));
                  // Trocar o resultado volta as unidades ao padrão dele (evita combinação inválida).
                  setUnits((m) => {
                    const next = { ...m };
                    delete next[i.id];
                    return next;
                  });
                }}
                label={label(v)}
              />
            ))}
          </div>
          {i.quantity > 1 &&
            outcome[i.id] &&
            outcome[i.id] !== "not_performed" &&
            outcome[i.id] !== "fully_performed" && (
              <div className="flex flex-wrap items-center gap-3">
                <span className="text-sm">
                  {i.kind === "product" ? "Unidades entregues" : "Unidades feitas por inteiro"}
                </span>
                <Quantidade
                  label={`Unidades de ${i.name}`}
                  value={unitsOf(i)}
                  min={interrupt ? i.performed_quantity : Math.max(1, i.performed_quantity)}
                  max={interrupt ? i.quantity - 1 : i.quantity}
                  onChange={(n) => setUnits((m) => ({ ...m, [i.id]: n }))}
                />
                <span className="text-sm text-fg-muted">de {i.quantity}</span>
              </div>
            )}
          {needsWhy(i) && (
            <TextArea
              label={
                outcome[i.id] === "not_performed"
                  ? `Por que ${i.name} não foi feito?`
                  : `Por que só ${unitsOf(i)} de ${i.quantity} ${i.name}?`
              }
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
