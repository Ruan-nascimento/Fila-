import { useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { Notice, Submit } from "../../ui/ui";
import { describeRequest, liveItems, type Order } from "../comanda/textos";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";

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

/**
 * Mudar os serviços da visita. Enquanto aguarda, o cliente troca direto (T-O-02). Depois da
 * chamada, faz uma solicitação que o profissional aprova ou recusa (T-CR-01); o total aceito não
 * muda até a decisão. O navegador envia só os serviços: preço e total vêm do servidor.
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
  const [open, setOpen] = useState(false);
  const direct = p.status === "waiting";
  const services = useServicesFor(shopId, p.professional_id, open);
  const cmd = useCommand();
  const current = liveItems(o);
  const [chosen, setChosen] = useState<string[]>(() => current.map((i) => i.service_id));
  const [add, setAdd] = useState<string[]>([]);
  const [remove, setRemove] = useState<string[]>([]);

  if (o.status !== "open" || !["waiting", "called", "in_service"].includes(p.status)) return null;
  const pending = o.pending_change_request;
  const decided = [...(o.change_requests ?? [])].reverse().find((c) => c.status !== "pending");

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const out = direct
      ? await cmd.run(`itens:${o.id}:${o.version}:${[...chosen].sort().join(",")}`, (key) =>
          api.PUT("/v1/establishments/{establishmentId}/orders/{orderId}/items", {
            params: { path: { establishmentId: shopId, orderId: o.id }, header: { "Idempotency-Key": key } },
            body: { expected_version: o.version, service_ids: chosen },
          }),
        )
      : await cmd.run(`pedido:${o.id}:${o.version}:${add.join(",")}:${remove.join(",")}`, (key) =>
          api.POST("/v1/establishments/{establishmentId}/orders/{orderId}/change-requests", {
            params: { path: { establishmentId: shopId, orderId: o.id }, header: { "Idempotency-Key": key } },
            body: { expected_version: o.version, add_service_ids: add, remove_item_ids: remove },
          }),
        );
    if (out.ok) {
      setOpen(false);
      setAdd([]);
      setRemove([]);
      onOrder(out.data.data);
    } else if (out.problem?.status === 409) await onConflict();
  }

  const toggle = (list: string[], id: string, on: boolean) =>
    on ? [...list, id] : list.filter((x) => x !== id);
  const inOrder = new Set(current.map((i) => i.service_id));
  const preview = (services ?? [])
    .filter((s) => chosen.includes(s.id))
    .reduce((sum, s) => {
      const accepted = current.find((i) => i.service_id === s.id);
      return sum + (accepted ? accepted.unit_price.amount_cents : s.price.amount_cents);
    }, 0);

  return (
    <div>
      {pending && (
        <Notice kind="info">
          Pedido enviado: {describeRequest(o, pending)}. Aguardando o profissional. O total continua{" "}
          {formatCents(o.total.amount_cents)} até a decisão.
        </Notice>
      )}
      {!pending && decided?.status === "rejected" && (
        <Notice kind="info">Seu último pedido de alteração foi recusado: {decided.decision_reason}</Notice>
      )}
      {!pending && decided?.status === "approved" && (
        <Notice kind="ok">Seu último pedido de alteração foi aprovado.</Notice>
      )}
      {!pending && !open && (
        <button
          type="button"
          className="button button--secondary"
          onClick={() => {
            setChosen(current.map((i) => i.service_id));
            setOpen(true);
          }}
        >
          {direct ? "Alterar serviços" : "Pedir alteração ao profissional"}
        </button>
      )}
      {open && (
        <form
          onSubmit={onSubmit}
          noValidate
          className="card"
          aria-label={direct ? "Alterar serviços" : "Pedir alteração"}
        >
          <CommandError problem={cmd.problem} offline={cmd.offline} />
          {!services && <p role="status">Carregando serviços…</p>}
          {services && direct && (
            <fieldset className="choices">
              <legend>Serviços</legend>
              {services.map((s) => (
                <label key={s.id} className="choice">
                  <input
                    type="checkbox"
                    checked={chosen.includes(s.id)}
                    onChange={(e) => setChosen((c) => toggle(c, s.id, e.target.checked))}
                  />
                  {s.name} —{" "}
                  {formatCents(
                    current.find((i) => i.service_id === s.id)?.unit_price.amount_cents ??
                      s.price.amount_cents,
                  )}
                </label>
              ))}
              {chosen.length > 0 && (
                <p>Novo total previsto: {formatCents(preview)} (confirmado pelo servidor).</p>
              )}
            </fieldset>
          )}
          {services && !direct && (
            <>
              <fieldset className="choices">
                <legend>Incluir</legend>
                {services
                  .filter((s) => !inOrder.has(s.id))
                  .map((s) => (
                    <label key={s.id} className="choice">
                      <input
                        type="checkbox"
                        checked={add.includes(s.id)}
                        onChange={(e) => setAdd((c) => toggle(c, s.id, e.target.checked))}
                      />
                      {s.name} — {formatCents(s.price.amount_cents)}
                    </label>
                  ))}
              </fieldset>
              <fieldset className="choices">
                <legend>Retirar</legend>
                {current
                  .filter((i) => i.status === "accepted")
                  .map((i) => (
                    <label key={i.id} className="choice">
                      <input
                        type="checkbox"
                        checked={remove.includes(i.id)}
                        onChange={(e) => setRemove((c) => toggle(c, i.id, e.target.checked))}
                      />
                      {i.name}
                    </label>
                  ))}
                {current.some((i) => i.status === "performed") && (
                  <p className="muted">Serviços já realizados não podem ser retirados.</p>
                )}
              </fieldset>
            </>
          )}
          <div className="actions">
            <Submit busy={cmd.busy !== null}>{direct ? "Salvar serviços" : "Enviar pedido"}</Submit>
            <button type="button" className="button button--secondary" onClick={() => setOpen(false)}>
              Voltar
            </button>
          </div>
        </form>
      )}
    </div>
  );
}
