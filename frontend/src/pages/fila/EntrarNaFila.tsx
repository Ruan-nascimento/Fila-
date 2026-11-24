import { useState, type FormEvent } from "react";
import { Ban, Clock, PauseCircle, Scissors, SquareX, UsersRound, X } from "lucide-react";
import { api } from "../../api/client";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { Choice, ChoiceGroup, EmptyState, LoadError, Loading, Notice, Submit, Avatar } from "../../ui/ui";
import { SeletorItens } from "./SeletorItens";
import { totais, unidades } from "./pedidoTotais";
import { cx } from "../../ui/cx";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { useStoreData } from "./dadosContexto";
import { accepting, emptySelection, qtyOf, queueLine, servicesFor } from "./selecao";

type Visit = components["schemas"]["Visit"];

const queueIcon = { open: Clock, paused: PauseCircle, closed: SquareX } as const;

/**
 * Catálogo e entrada na fila numa lista só: o visitante vê os profissionais e a situação de cada
 * fila; ao escolher um, os serviços dele viram opções com quantidade (busca e categorias quando a
 * lista cresce), os produtos ficam como complemento opcional e o resumo mostra quantidades,
 * subtotais e duração junto de "Entrar na fila". O navegador envia só a fila, os itens e as
 * quantidades: preço e total vêm do servidor (R-CMD-02). Um pedido = uma posição na fila.
 */
export function EntrarNaFila({
  identified,
  onNeedIdentity,
  onEntered,
}: {
  identified: boolean;
  onNeedIdentity?: () => void;
  onEntered?: (v: Visit) => void;
}) {
  const data = useStoreData();
  const { queues, services, selection, setSelection } = data;
  const cmd = useCommand();
  const [missing, setMissing] = useState(false);

  const queue = queues?.find((q) => q.queue_id === selection.queueId);
  const offered = services ? servicesFor(services, queue) : [];
  const chosenServices: Record<string, number> = {};
  for (const id of selection.serviceIds) chosenServices[id] = qtyOf(selection, id);
  const chosenProducts = selection.products ?? {};
  const products = data.ordering.products_enabled ? data.products : [];
  const t = totais(offered, products, chosenServices, chosenProducts);
  const anyOpen = (queues ?? []).some((q) => q.status === "open");
  const anyAccepting = (queues ?? []).some(accepting);

  const setService = (id: string, n: number) => {
    setMissing(false);
    const ids =
      n > 0
        ? selection.serviceIds.includes(id)
          ? selection.serviceIds
          : [...selection.serviceIds, id]
        : selection.serviceIds.filter((x) => x !== id);
    const quantities = { ...(selection.quantities ?? {}) };
    if (n > 0) quantities[id] = n;
    else delete quantities[id];
    setSelection({ ...selection, serviceIds: ids, quantities });
  };
  const setProduct = (id: string, n: number) => {
    const next = { ...chosenProducts };
    if (n > 0) next[id] = n;
    else delete next[id];
    setSelection({ ...selection, products: next });
  };

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!selection.queueId || t.serviceUnits === 0) {
      setMissing(true);
      return;
    }
    setMissing(false);
    if (!identified) {
      onNeedIdentity?.();
      return;
    }
    const items = [
      ...selection.serviceIds.map((id) => ({ service_id: id, quantity: qtyOf(selection, id) })),
      ...Object.entries(chosenProducts)
        .filter(([id, n]) => n > 0 && (products ?? []).some((p) => p.id === id))
        .map(([id, n]) => ({ product_id: id, quantity: n })),
    ];
    const body = { queue_id: selection.queueId, items };
    const sig = items
      .map((i) => `${"service_id" in i ? i.service_id : i.product_id}x${i.quantity}`)
      .sort()
      .join(",");
    const out = await cmd.run(`entrar:${body.queue_id}:${sig}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/participations", {
        params: { path: { establishmentId: data.shopId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      setSelection(emptySelection);
      onEntered?.(out.data.data);
    } else if (out.problem?.status === 409) {
      data.retryQueues();
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-entrar-fila" className="grid gap-6">
      <h2 id="titulo-entrar-fila" className="sr-only">
        Entrar na fila
      </h2>
      {data.selectionNote && (
        <div className="relative">
          <Notice kind="aviso">
            <p className="pr-8">{data.selectionNote}</p>
          </Notice>
          <button
            type="button"
            onClick={data.clearSelectionNote}
            aria-label="Fechar aviso"
            className="absolute top-1 right-1 inline-flex size-11 items-center justify-center rounded-lg text-fg-muted hover:text-fg"
          >
            <X aria-hidden="true" className="size-4" />
          </button>
        </div>
      )}

      {/* Profissionais e situação das filas */}
      <div className="grid gap-2">
        {queues === null ? (
          data.queuesError ? (
            <>
              <h3 className="m-0 text-sm font-semibold">Profissionais</h3>
              <LoadError
                message={`Não foi possível ver as filas agora. ${data.queuesError}`}
                onRetry={data.retryQueues}
              />
            </>
          ) : (
            <>
              <h3 className="m-0 text-sm font-semibold">Profissionais</h3>
              <Loading>Carregando filas…</Loading>
            </>
          )
        ) : queues.length === 0 ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Profissionais</h3>
            <EmptyState icon={UsersRound} title="Nenhuma fila aberta no momento">
              <p>A barbearia ainda não iniciou o atendimento por fila.</p>
            </EmptyState>
          </>
        ) : (
          <ChoiceGroup
            legend="Profissional"
            hint={
              !anyOpen
                ? "Nenhuma fila aberta no momento. Volte mais tarde ou fale com a loja."
                : !anyAccepting
                  ? "As filas não estão recebendo novas entradas agora. Quem já está será atendido normalmente."
                  : undefined
            }
          >
            {queues.map((q) => {
              const Icon = q.status === "open" && !accepting(q) ? Ban : queueIcon[q.status];
              return (
                <Choice
                  key={q.queue_id}
                  type="radio"
                  name="profissional"
                  value={q.queue_id}
                  disabled={!accepting(q)}
                  checked={selection.queueId === q.queue_id}
                  onChange={() => {
                    setMissing(false);
                    setSelection({ ...selection, queueId: q.queue_id });
                  }}
                  label={
                    <span className="inline-flex items-center gap-2">
                      {/* Foto profissional publicada; sem ela, iniciais (nunca bloqueia a escolha). */}
                      <Avatar
                        name={q.professional.display_name}
                        image={q.professional.photo}
                        size={32}
                        className="size-8 text-xs"
                      />
                      {q.professional.display_name}
                    </span>
                  }
                  description={
                    <span className="inline-flex items-center gap-1.5">
                      <Icon aria-hidden="true" className="size-4 shrink-0" />
                      {queueLine(q)}
                    </span>
                  }
                />
              );
            })}
          </ChoiceGroup>
        )}
      </div>

      {/* Serviços: catálogo antes da escolha; opções depois dela */}
      <div className="grid gap-2">
        {services === null ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Serviços</h3>
            {data.servicesError ? (
              <LoadError message={data.servicesError} onRetry={data.retryServices} />
            ) : (
              <Loading>Carregando serviços…</Loading>
            )}
          </>
        ) : services.length === 0 ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Serviços</h3>
            <p className="m-0 text-fg-muted">Nenhum serviço disponível no momento.</p>
          </>
        ) : !queue ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Serviços</h3>
            {anyOpen && (
              <p className="m-0 text-sm text-fg-muted">
                Escolha um profissional para selecionar os serviços.
              </p>
            )}
            <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
              {services.map((s) => {
                const names = s.professional_ids
                  .map((id) => queues?.find((q) => q.professional.id === id)?.professional.display_name)
                  .filter(Boolean);
                return (
                  <li key={s.id} className="flex items-start gap-3 px-4 py-3">
                    <Scissors aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-fg-muted" />
                    <div className="min-w-0 flex-1">
                      <p className="m-0 font-medium">{s.name}</p>
                      <p className="m-0 text-sm text-fg-muted">
                        cerca de {s.duration_minutes} min
                        {queues && <> · com {names.join(", ") || "nenhum profissional no momento"}</>}
                      </p>
                    </div>
                    <span className="font-semibold tabular-nums">{formatCents(s.price.amount_cents)}</span>
                  </li>
                );
              })}
            </ul>
          </>
        ) : (
          <>
            {offered.length === 0 ? (
              <p className="m-0 text-fg-muted">Este profissional não tem serviços disponíveis.</p>
            ) : (
              <SeletorItens
                title={`Serviços com ${queue.professional.display_name}`}
                services={offered}
                products={products}
                categories={data.categories}
                services_chosen={chosenServices}
                products_chosen={chosenProducts}
                onService={setService}
                onProduct={setProduct}
                max={data.ordering.max_service_quantity}
              />
            )}
            {services.length > offered.length && (
              <p className="m-0 text-sm text-fg-muted">
                {services.length - offered.length === 1
                  ? "1 serviço da loja não é feito por este profissional."
                  : `${services.length - offered.length} serviços da loja não são feitos por este profissional.`}
              </p>
            )}
          </>
        )}
      </div>

      {anyOpen && (
        <div
          className={cx(
            "grid gap-3 rounded-xl border border-line bg-surface p-4",
            // No celular, o resumo acompanha a rolagem quando já há algo escolhido.
            t.lines.length > 0 && "max-md:sticky max-md:bottom-2 max-md:z-20 max-md:shadow-lg",
          )}
        >
          <CommandError problem={cmd.problem} offline={cmd.offline} />
          {missing && (
            <p role="alert" className="m-0 text-sm font-semibold text-danger">
              Escolha um profissional e pelo menos um serviço.
            </p>
          )}
          {t.lines.length > 0 && (
            <details className="group max-md:text-sm">
              <summary className="cursor-pointer list-none text-sm font-semibold text-accent [&::-webkit-details-marker]:hidden">
                Resumo do pedido ({t.lines.length} {t.lines.length === 1 ? "item" : "itens"})
              </summary>
              <ul className="m-0 mt-2 grid list-none gap-1 p-0">
                {t.lines.map((l) => (
                  <li key={l.key} className="flex justify-between gap-3 tabular-nums">
                    <span className="min-w-0 break-words">
                      {l.qty} × {l.name}
                    </span>
                    <span>{formatCents(l.subtotal)}</span>
                  </li>
                ))}
              </ul>
            </details>
          )}
          <div className="flex flex-wrap items-end justify-between gap-3">
            <div className="grid" aria-live="polite">
              <span className="text-sm text-fg-muted">
                {t.lines.length === 0
                  ? "Nenhum serviço escolhido"
                  : `Total estimado · ${unidades(t.serviceUnits, t.productUnits)} · ~${t.minutes} min`}
              </span>
              <span className="text-xl font-bold tabular-nums">{formatCents(t.cents)}</span>
            </div>
            <Submit busy={cmd.busy !== null}>Entrar na fila</Submit>
          </div>
          {t.serviceUnits > 0 && (
            <p className="m-0 text-sm">
              Este pedido ocupa uma posição na fila. Os serviços serão realizados pelo profissional escolhido,
              em sequência.
            </p>
          )}
          <p className="m-0 text-xs text-fg-muted">
            O valor é confirmado pela loja ao entrar. A espera é uma estimativa e pode mudar.
          </p>
        </div>
      )}
    </form>
  );
}
