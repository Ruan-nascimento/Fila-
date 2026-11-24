import { useEffect, useState, type FormEvent } from "react";
import { Pencil } from "lucide-react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { Button, Loading, Notice, Submit } from "../../ui/ui";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { describeRequest, liveItems, type Order } from "../comanda/textos";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { useStoreData } from "./dadosContexto";
import { SeletorItens } from "./SeletorItens";
import { totais, type Escolha } from "./pedidoTotais";

type Visit = components["schemas"]["Visit"];
type PublicService = components["schemas"]["PublicService"];

/** Serviços públicos que o profissional da visita faz (preço mostrado = catálogo atual). */
function useServicesFor(shopId: string, professionalId: string, enabled: boolean) {
  const [services, setServices] = useState<PublicService[] | null>(null);
  useEffect(() => {
    if (!enabled) return;
    let active = true;
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/services", {
        params: { path: { establishmentId: shopId } },
      }),
    ).then((out) => {
      if (active && out.ok)
        setServices(out.data.data.filter((s) => s.professional_ids.includes(professionalId)));
    });
    return () => {
      active = false;
    };
  }, [shopId, professionalId, enabled]);
  return services;
}

/** Quantidades atuais da comanda, por item do catálogo. */
function currentChoice(o: Order) {
  const s: Escolha = {};
  const p: Escolha = {};
  for (const i of liveItems(o)) {
    if (i.kind === "product" && i.product_id) p[i.product_id] = i.quantity;
    else if (i.service_id) s[i.service_id] = i.quantity;
  }
  return { s, p };
}

/**
 * Mudar os itens da visita, com quantidade. Enquanto aguarda, o cliente troca direto (T-O-02,
 * quantidade absoluta). Depois da chamada, faz uma solicitação que o profissional aprova ou
 * recusa (T-CR-01); o total aceito não muda até a decisão. O navegador envia só itens e
 * quantidades: preço e total vêm do servidor. Linhas que já estão na comanda mostram o preço
 * aceito (snapshot); unidades já realizadas não podem sair.
 */
export function Pedido({
  shopId,
  visit,
  onOrder,
  onConflict,
}: {
  shopId: string;
  visit: Visit;
  onOrder: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  const p = visit.participation;
  const o = visit.order;
  const store = useStoreData();
  const panel = useInlinePanel();
  const open = panel.open !== null;
  const direct = p.status === "waiting";
  const services = useServicesFor(shopId, p.professional_id, open);
  const cmd = useCommand();
  const current = liveItems(o);
  const [choice, setChoice] = useState(() => currentChoice(o));

  if (o.status !== "open" || !["waiting", "called", "in_service"].includes(p.status)) return null;
  const pending = o.pending_change_request;
  const decided = [...(o.change_requests ?? [])].reverse().find((c) => c.status !== "pending");
  const products = store.ordering.products_enabled ? store.products : [];
  const lineOf = (kind: "service" | "product", id: string) =>
    current.find((i) => (kind === "product" ? i.product_id === id : i.service_id === id));
  const priceOf = (kind: "service" | "product", id: string) => lineOf(kind, id)?.unit_price.amount_cents;
  const minOf = (kind: "service" | "product", id: string) => lineOf(kind, id)?.performed_quantity ?? 0;
  // Serviços já na comanda continuam visíveis mesmo se saíram do catálogo público.
  const shownServices: PublicService[] | null = services
    ? [
        ...services,
        ...current
          .filter((i) => i.kind !== "product" && i.service_id && !services.some((s) => s.id === i.service_id))
          .map((i) => ({
            id: i.service_id as string,
            name: i.name,
            price: i.unit_price,
            duration_minutes: i.duration_minutes,
            professional_ids: [p.professional_id],
            category_id: null,
            display_order: 0,
          })),
      ]
    : null;
  const before = currentChoice(o);
  const t = shownServices ? totais(shownServices, products, choice.s, choice.p, priceOf) : null;
  const changed = JSON.stringify(before) !== JSON.stringify(choice);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!t || t.serviceUnits === 0 || !changed) return;
    const items = [
      ...Object.entries(choice.s)
        .filter(([, n]) => n > 0)
        .map(([id, n]) => ({ service_id: id, quantity: n })),
      ...Object.entries(choice.p)
        .filter(([, n]) => n > 0)
        .map(([id, n]) => ({ product_id: id, quantity: n })),
    ];
    const sig = items
      .map((i) => `${"service_id" in i ? i.service_id : i.product_id}x${i.quantity}`)
      .sort()
      .join(",");
    const out = direct
      ? await cmd.run(`itens:${o.id}:${o.version}:${sig}`, (key) =>
          api.PUT("/v1/establishments/{establishmentId}/orders/{orderId}/items", {
            params: { path: { establishmentId: shopId, orderId: o.id }, header: { "Idempotency-Key": key } },
            body: { expected_version: o.version, items },
          }),
        )
      : await cmd.run(`pedido:${o.id}:${o.version}:${sig}`, (key) => {
          // Solicitação: o que é novo, o que sai e o que muda de quantidade.
          const add = items.filter((i) =>
            "service_id" in i ? before.s[i.service_id] === undefined : before.p[i.product_id] === undefined,
          );
          const remove = current
            .filter((i) =>
              i.kind === "product" ? !choice.p[i.product_id ?? ""] : !choice.s[i.service_id ?? ""],
            )
            .map((i) => i.id);
          const setQ = current
            .map((i) => {
              const n = i.kind === "product" ? choice.p[i.product_id ?? ""] : choice.s[i.service_id ?? ""];
              return n && n !== i.quantity ? { item_id: i.id, quantity: n } : null;
            })
            .filter((x): x is { item_id: string; quantity: number } => x !== null);
          return api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/change-requests", {
            params: { path: { establishmentId: shopId, orderId: o.id }, header: { "Idempotency-Key": key } },
            body: {
              expected_version: o.version,
              add_items: add,
              remove_item_ids: remove,
              set_quantities: setQ,
            },
          });
        });
    if (out.ok) {
      panel.hide();
      onOrder(out.data.data);
    } else if (out.problem?.status === 409) await onConflict();
  }

  const set = (kind: "s" | "p") => (id: string, n: number) =>
    setChoice((c) => {
      const next = { ...c[kind] };
      if (n > 0) next[id] = n;
      else delete next[id];
      return { ...c, [kind]: next };
    });
  const label = direct ? "Alterar pedido" : "Pedir alteração";

  return (
    <div className="grid gap-3">
      {pending && (
        <Notice kind="info" title="Pedido de alteração enviado">
          <p>
            {describeRequest(o, pending)}. Aguardando o profissional. O total continua{" "}
            {formatCents(o.total.amount_cents)} até a decisão.
          </p>
        </Notice>
      )}
      {!pending && decided?.status === "rejected" && (
        <Notice kind="info">
          <p>Seu último pedido de alteração foi recusado: {decided.decision_reason}</p>
        </Notice>
      )}
      {!pending && decided?.status === "approved" && (
        <Notice kind="ok">
          <p>Seu último pedido de alteração foi aprovado.</p>
        </Notice>
      )}
      {!pending && !open && (
        <Button
          id={panel.triggerId}
          variant="secondary"
          icon={Pencil}
          onClick={() => {
            setChoice(currentChoice(o));
            panel.show("aberto");
          }}
        >
          {direct ? "Alterar pedido" : "Pedir alteração ao profissional"}
        </Button>
      )}
      {open && (
        <form
          id={panel.panelId}
          tabIndex={-1}
          onKeyDown={panel.onKeyDown}
          onSubmit={onSubmit}
          noValidate
          className="grid min-w-0 gap-4 rounded-xl border border-line bg-surface-2 p-4"
          aria-label={label}
        >
          <p className="m-0 font-semibold" data-autofocus tabIndex={-1}>
            {label}
          </p>
          {!direct && (
            <p className="m-0 text-sm text-fg-muted">
              O profissional aprova ou recusa. Até lá, o total da comanda não muda.
            </p>
          )}
          <CommandError problem={cmd.problem} offline={cmd.offline} />
          {!shownServices && <Loading>Carregando serviços…</Loading>}
          {shownServices && (
            <SeletorItens
              title="Serviços"
              services={shownServices}
              products={products}
              categories={store.categories}
              services_chosen={choice.s}
              products_chosen={choice.p}
              onService={set("s")}
              onProduct={set("p")}
              max={store.ordering.max_service_quantity}
              min={minOf}
              priceOf={priceOf}
            />
          )}
          {t && (
            <p className="m-0 text-sm" aria-live="polite">
              {t.serviceUnits === 0 ? (
                <span className="text-danger">
                  Mantenha pelo menos um serviço. Para sair da fila, use “Desistir”.
                </span>
              ) : (
                <>
                  Novo total previsto: <strong className="tabular-nums">{formatCents(t.cents)}</strong> · ~
                  {t.minutes} min (confirmado pelo servidor).
                </>
              )}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <Submit busy={cmd.busy !== null}>{direct ? "Salvar pedido" : "Enviar pedido"}</Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Voltar
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}
