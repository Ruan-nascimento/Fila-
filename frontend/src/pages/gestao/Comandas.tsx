import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link, Navigate, useParams, useSearchParams } from "react-router";
import { ChevronRight, ReceiptText } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import {
  AmountRow,
  Button,
  Card,
  Choice,
  ChoiceGroup,
  EmptyState,
  LoadError,
  Loading,
  PageHeader,
  SelectField,
  Submit,
  TextArea,
} from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { ItensDaComanda, OrderBadge } from "../comanda/comanda";
import { liveItems, reportedText, type Order } from "../comanda/textos";
import { ReceberPagamento } from "../pagamento/ReceberPagamento";
import { CampoBusca } from "../../features/catalogo/CampoBusca";
import { fold, useDebounced } from "../../features/catalogo/busca";
import { isId, useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Service = components["schemas"]["Service"];
type Filter = "pending_review" | "awaiting_payment" | "awaiting_confirmation" | "";
type Who = { ticket: string; nick: string; reason: string | null };

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

const filterText: Record<Filter, string> = {
  pending_review: "Interrompidas para revisar",
  awaiting_payment: "A receber",
  awaiting_confirmation: "A conferir",
  "": "Todas",
};

/**
 * Comandas da loja (dono) ou dos próprios atendimentos (profissional): lista e detalhe. No
 * computador ficam lado a lado; no celular a lista abre o detalhe e "Comandas" volta. A comanda
 * aberta fica no endereço (recarregar, link direto e Voltar funcionam).
 */
export function Comandas({ ctx }: { ctx: StaffContext }) {
  const { orderId } = useParams();
  // O filtro fica no endereço (?mostrar=): recarregar, Voltar e os atalhos de Pendências mantêm.
  const [params, setParams] = useSearchParams();
  const asked = params.get("mostrar");
  const filter: Filter =
    asked !== null && Object.hasOwn(filterText, asked)
      ? (asked as Filter)
      : ctx.isOwner
        ? "pending_review"
        : "";
  const setFilter = (f: Filter) =>
    setParams(
      (cur) => {
        const n = new URLSearchParams(cur);
        n.set("mostrar", f);
        return n;
      },
      { replace: true },
    );
  const [orders, setOrders] = useState<Order[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [busca, setBusca] = useState("");
  // Busca no SERVIDOR (lista paginada, ADR-0036): ~250 ms depois de parar de digitar.
  const term = useDebounced(fold(busca));
  const [searching, setSearching] = useState(false);
  const who = useWho(ctx.estId);

  const fetchPage = useCallback(
    (cursor: string | null, signal?: AbortSignal) =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/orders", {
          params: {
            path: { establishmentId: ctx.estId },
            query: {
              ...(filter ? { status: [filter] } : {}),
              ...(term ? { q: term } : {}),
              limit: 20,
              ...(cursor ? { cursor } : {}),
            },
          },
          ...(signal ? { signal } : {}),
        }),
      ),
    [ctx.estId, filter, term],
  );
  const reload = useCallback(async () => {
    const out = await fetchPage(null);
    if (out.ok) {
      setOrders(out.data.data);
      setNext(out.data.page.next_cursor ?? null);
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
  }, [fetchPage]);
  useEffect(() => {
    // Busca nova cancela a anterior: uma resposta velha nunca substitui a mais recente.
    const ctl = new AbortController();
    void fetchPage(null, ctl.signal).then((out) => {
      if (ctl.signal.aborted) return;
      setSearching(false);
      if (out.ok) {
        setOrders(out.data.data);
        setNext(out.data.page.next_cursor ?? null);
        setError(null);
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => ctl.abort();
  }, [fetchPage]);

  async function more() {
    if (!next) return;
    setLoadingMore(true);
    const out = await fetchPage(next);
    setLoadingMore(false);
    if (out.ok) {
      setOrders((list) => [...(list ?? []), ...out.data.data]);
      setNext(out.data.page.next_cursor ?? null);
    } else setError(problemMessage(out.problem, out.offline));
  }

  const replace = (o: Order) => setOrders((list) => (list ?? []).map((x) => (x.id === o.id ? o : x)));

  // Fluxo SSE: comanda da lista mudou → troca se for mais nova; comanda nova que entra no filtro
  // (ex.: interrupção para revisar) ou reconexão com snapshot → relê a lista.
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      void reload();
      return;
    }
    if (m.type !== "order.updated") return;
    const o = m.data as Order;
    const known = orders?.find((x) => x.id === o.id);
    if (known) {
      // Compara com o estado ATUAL: eventos fora de ordem não trazem de volta uma versão velha.
      setOrders((list) => (list ?? []).map((x) => (x.id === o.id && o.version > x.version ? o : x)));
    } else if (!filter || o.status === filter) {
      void reload();
    }
  });

  const ensureWho = who.ensure;
  useEffect(() => {
    if (orders) ensureWho(orders.map((o) => o.participation_id));
  }, [orders, ensureWho]);

  const base = `/gestao/${ctx.estId}/comandas`;
  const selected = orders?.find((o) => o.id === orderId);
  if (orderId !== undefined && !isId(orderId)) return <Navigate to={base} replace />;

  return (
    <div className="grid gap-4">
      <div className={cx(orderId && "max-lg:hidden")}>
        <PageHeader title="Comandas" />
      </div>
      <div className="grid gap-5 lg:grid-cols-[minmax(18rem,22rem)_minmax(0,1fr)] lg:items-start">
        {/* Lista: no celular some quando uma comanda está aberta */}
        <section
          aria-labelledby="titulo-lista-comandas"
          className={cx("grid gap-3", orderId && "max-lg:hidden")}
        >
          <h2 id="titulo-lista-comandas" className="sr-only">
            Lista de comandas
          </h2>
          <SelectField label="Mostrar" value={filter} onChange={(e) => setFilter(e.target.value as Filter)}>
            {(Object.keys(filterText) as Filter[]).map((f) => (
              <option key={f} value={f}>
                {filterText[f]}
              </option>
            ))}
          </SelectField>
          <CampoBusca
            label="Buscar comanda"
            placeholder="Buscar por senha, nome ou item"
            value={busca}
            busy={searching || fold(busca) !== term}
            onChange={(v) => {
              setBusca(v);
              setSearching(true);
            }}
          />
          {error && <LoadError message={error} onRetry={() => void reload()} />}
          {!orders && !error && <Loading>Carregando comandas…</Loading>}
          {orders?.length === 0 && (
            <EmptyState
              icon={ReceiptText}
              title={term ? "Nenhuma comanda encontrada" : "Nenhuma comanda aqui"}
            >
              <p>
                {term
                  ? `Nada para “${busca.trim()}”${filter ? ` em “${filterText[filter]}”` : ""}. A busca considera todas as páginas.`
                  : filter
                    ? `Nada em “${filterText[filter]}” agora.`
                    : "Ainda não há comandas."}
              </p>
            </EmptyState>
          )}
          {orders && orders.length > 0 && (
            <ul className="m-0 grid list-none gap-2 p-0">
              {orders.map((o) => (
                <li key={o.id}>
                  <Link
                    to={`${base}/${o.id}${params.toString() ? `?${params.toString()}` : ""}`}
                    aria-current={o.id === orderId ? "page" : undefined}
                    className={cx(
                      "flex min-h-11 items-start gap-3 rounded-xl border px-4 py-3 text-fg no-underline",
                      o.id === orderId
                        ? "border-primary bg-select"
                        : "border-line bg-surface hover:bg-surface-2",
                    )}
                  >
                    <span className="grid min-w-0 flex-1 gap-1">
                      <span className="flex flex-wrap items-baseline justify-between gap-x-3">
                        <span className="font-semibold">
                          {who.get(o.participation_id)?.ticket ?? "Comanda"}
                          {who.get(o.participation_id) && ` · ${who.get(o.participation_id)?.nick}`}
                        </span>
                        <span className="font-semibold tabular-nums">
                          {formatCents(o.total.amount_cents)}
                        </span>
                      </span>
                      <span className="text-sm text-fg-muted tabular-nums">
                        {when.format(new Date(o.created_at))}
                      </span>
                      <span className="truncate text-sm text-fg-muted">
                        {liveItems(o)
                          .map((i) => i.name)
                          .join(" + ")}
                      </span>
                      <OrderBadge order={o} />
                    </span>
                    <ChevronRight aria-hidden="true" className="mt-1 size-5 shrink-0 text-fg-muted" />
                  </Link>
                </li>
              ))}
            </ul>
          )}
          {next && (
            <Button variant="secondary" disabled={loadingMore} onClick={() => void more()}>
              {loadingMore ? "Carregando…" : "Carregar mais"}
            </Button>
          )}
        </section>

        {/* Detalhe */}
        <section aria-label="Detalhe da comanda" className={cx(!orderId && "max-lg:hidden")}>
          {!orderId ? (
            <Card className="hidden lg:block">
              <EmptyState icon={ReceiptText} title="Escolha uma comanda">
                <p>O detalhe, o recebimento e as correções aparecem aqui.</p>
              </EmptyState>
            </Card>
          ) : (
            <Detalhe
              key={orderId}
              ctx={ctx}
              orderId={orderId}
              known={selected}
              who={who}
              back={`${base}${params.toString() ? `?${params.toString()}` : ""}`}
              onSaved={replace}
              onConflict={reload}
            />
          )}
        </section>
      </div>
    </div>
  );
}

/** Senha, apelido e motivo informado de cada comanda (lidos uma vez por participação). */
function useWho(estId: string) {
  const [map, setMap] = useState<Map<string, Who>>(() => new Map());
  const asked = useRef(new Set<string>());
  const ensure = useCallback(
    (ids: string[]) => {
      for (const participationId of ids) {
        if (asked.current.has(participationId)) continue;
        asked.current.add(participationId);
        void call(() =>
          api.GET("/v1/establishments/{establishmentId}/participations/{participationId}", {
            params: { path: { establishmentId: estId, participationId } },
          }),
        ).then((out) => {
          if (!out.ok) {
            asked.current.delete(participationId);
            return;
          }
          const p = out.data.data;
          setMap((m) =>
            new Map(m).set(participationId, {
              ticket: p.ticket_code,
              nick: p.nick,
              reason: p.end_reason ?? null,
            }),
          );
        });
      }
    },
    [estId],
  );
  return { get: (id: string) => map.get(id), ensure };
}

function Detalhe({
  ctx,
  orderId,
  known,
  who,
  back,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  orderId: string;
  known: Order | undefined;
  who: ReturnType<typeof useWho>;
  back: string;
  onSaved: (o: Order) => void;
  onConflict: () => Promise<void>;
}) {
  // Link direto para uma comanda fora do filtro atual: lê só ela.
  const [own, setOwn] = useState<Order | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (known) return;
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/orders/{orderId}", {
        params: { path: { establishmentId: ctx.estId, orderId } },
      }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) setOwn(out.data.data);
      else
        setError(
          out.problem?.status === 404 ? "Comanda não encontrada." : problemMessage(out.problem, out.offline),
        );
    });
    return () => {
      active = false;
    };
  }, [known, ctx.estId, orderId]);
  // Eventos da comanda aberta fora da lista.
  useStreamMessages((m) => {
    if (known || (m.type !== "order.updated" && m.type !== "payment.updated")) return;
    const o = m.data as Order;
    setOwn((cur) => (cur && cur.id === o.id && o.version > cur.version ? o : cur));
  });

  const o = known ?? own;
  const participationId = o?.participation_id;
  const ensureWho = who.ensure;
  useEffect(() => {
    if (participationId) ensureWho([participationId]);
  }, [participationId, ensureWho]);
  const backLink = (
    <Link
      to={back}
      className="-ml-2 inline-flex min-h-11 w-fit items-center gap-1 rounded-lg px-2 text-sm font-semibold text-fg-muted no-underline hover:text-fg lg:hidden"
    >
      <span aria-hidden="true">←</span> Comandas
    </Link>
  );
  if (error)
    return (
      <div className="grid gap-2">
        {backLink}
        <LoadError message={error} />
      </div>
    );
  if (!o) return <Loading>Carregando comanda…</Loading>;
  const w = who.get(o.participation_id);
  const save = (n: Order) => {
    if (known) onSaved(n);
    else setOwn(n);
  };
  return (
    <Card className="grid gap-5 p-4 sm:p-6">
      <div className="grid gap-1">
        {backLink}
        <div className="flex flex-wrap items-start justify-between gap-2">
          <h2 className="m-0 text-xl font-bold">Comanda {w ? `${w.ticket} · ${w.nick}` : ""}</h2>
          <OrderBadge order={o} />
        </div>
        <p className="m-0 text-sm text-fg-muted tabular-nums">{when.format(new Date(o.created_at))}</p>
        {w?.reason && <p className="m-0 text-sm text-fg-muted">motivo informado: {w.reason}</p>}
      </div>
      <ItensDaComanda order={o} showRemoved />
      {ctx.isOwner && o.status === "pending_review" && (
        <Revisar ctx={ctx} order={o} onSaved={save} onConflict={onConflict} />
      )}
      {(o.status === "awaiting_payment" ||
        o.status === "awaiting_confirmation" ||
        o.payment.receipts.length > 0) && (
        <ReceberPagamento ctx={ctx} order={o} onSaved={save} onConflict={onConflict} />
      )}
      {ctx.isOwner && o.status === "awaiting_payment" && (
        <Corrigir ctx={ctx} order={o} onSaved={save} onConflict={onConflict} />
      )}
    </Card>
  );
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
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  const charged = items
    .filter((i) => outcome[i.id] === "fully_performed")
    .reduce((s, i) => s + i.unit_price.amount_cents, 0);
  const ready = note.trim().length >= 3 && items.every((i) => outcome[i.id]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
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
    <form
      onSubmit={onSubmit}
      noValidate
      aria-label="Revisar interrupção"
      className="grid gap-4 border-t border-line pt-5"
    >
      <div className="grid gap-1">
        <h3 className="m-0 text-base font-semibold">Revisar interrupção</h3>
        <p className="m-0 text-sm text-fg-muted">
          Parte do que o profissional informou. Só serviço feito por inteiro é cobrado; interrupção não gera
          pontos.
        </p>
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {items.map((i) => (
        <ChoiceGroup key={i.id} legend={`${i.name} — ${formatCents(i.unit_price.amount_cents)}`}>
          <div className="grid gap-2 sm:grid-cols-3">
            {(["fully_performed", "partially_performed", "not_started"] as const).map((v) => (
              <Choice
                key={v}
                type="radio"
                name={`revisao-${i.id}`}
                checked={outcome[i.id] === v}
                onChange={() => setOutcome((m) => ({ ...m, [i.id]: v }))}
                label={reportedText[v]}
                description={v === "fully_performed" ? "cobrado" : "não cobrado"}
              />
            ))}
          </div>
        </ChoiceGroup>
      ))}
      <TextArea
        label="Nota da revisão"
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={note}
        error={
          tried && note.trim().length < 3 ? "Escreva a nota da revisão (pelo menos 3 caracteres)." : undefined
        }
        onChange={(e) => setNote(e.target.value)}
      />
      <div className="rounded-lg bg-surface-2 px-3">
        <AmountRow
          strong
          label="Valor depois da revisão:"
          value={formatCents(charged)}
          sub={charged === 0 ? "A comanda será encerrada sem cobrança." : undefined}
        />
      </div>
      <div>
        <Submit busy={cmd.busy !== null}>Fechar revisão</Submit>
      </div>
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
  const panel = useInlinePanel();
  const open = panel.open !== null;
  const [services, setServices] = useState<Service[]>([]);
  const [uncharge, setUncharge] = useState<string[]>([]);
  const [add, setAdd] = useState("");
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
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
    setTried(true);
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
      panel.hide();
      setUncharge([]);
      setAdd("");
      setReason("");
      setTried(false);
      onSaved(out.data.data);
    } else if (out.problem?.status === 409) await onConflict();
  }
  if (!open)
    return (
      <div className="border-t border-line pt-4">
        <Button id={panel.triggerId} variant="secondary" onClick={() => panel.show("aberto")}>
          Corrigir valor
        </Button>
      </div>
    );
  return (
    <form
      id={panel.panelId}
      onKeyDown={panel.onKeyDown}
      noValidate
      aria-label="Corrigir valor"
      className="grid gap-4 rounded-xl border border-line bg-surface-2 p-4"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <p className="m-0 font-semibold" data-autofocus tabIndex={-1}>
        Corrigir valor da comanda ({formatCents(order.total.amount_cents)})
      </p>
      <p className="m-0 text-sm text-fg-muted">
        Pede a sua senha de novo. A correção fica registrada com o motivo.
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} onRetry={() => void submit()} />
      <ChoiceGroup legend="Retirar a cobrança de">
        {liveItems(order)
          .filter((i) => i.charged)
          .map((i) => (
            <Choice
              key={i.id}
              type="checkbox"
              checked={uncharge.includes(i.id)}
              onChange={(e) =>
                setUncharge((c) => (e.target.checked ? [...c, i.id] : c.filter((x) => x !== i.id)))
              }
              label={i.name}
              trailing={formatCents(i.unit_price.amount_cents)}
            />
          ))}
      </ChoiceGroup>
      <SelectField
        label="Incluir serviço feito e não registrado"
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
      </SelectField>
      <TextArea
        label="Motivo da correção"
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      {tried && !ready && (
        <p role="alert" className="m-0 text-sm font-semibold text-danger">
          Escolha o que corrigir e escreva o motivo.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>Salvar correção</Submit>
        <Button variant="secondary" onClick={panel.hide}>
          Voltar
        </Button>
      </div>
    </form>
  );
}
