import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { Notice, Submit } from "../../ui/ui";
import { ItensDaComanda } from "../comanda/comanda";
import { useStreamMessages } from "../../stream/context";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { usePublicQueues } from "./filasPublicas";
import { PagamentoCliente } from "../pagamento/PagamentoCliente";
import { Pedido } from "./Pedido";

type Visit = components["schemas"]["Visit"];
type PublicService = components["schemas"]["PublicService"];

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

const statusText: Record<Visit["participation"]["status"], string> = {
  waiting: "Aguardando",
  called: "Chamado! Dirija-se ao profissional",
  in_service: "Em atendimento",
  completed: "Atendimento concluído",
  cancelled: "Você saiu da fila",
  no_show: "Marcado como ausente",
  interrupted: "Atendimento interrompido",
};

/**
 * Visita do cliente nesta loja: entrar na fila (escolhendo profissional e serviços) ou acompanhar
 * a senha, a posição e a estimativa. O preço mostrado vem do servidor; o pedido envia só a fila
 * e os serviços (R-CMD-02).
 */
export function MinhaVisita({ shopId }: { shopId: string }) {
  const [visit, setVisit] = useState<Visit | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);

  const fetchVisit = useCallback(
    () =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/my-visit", {
          params: { path: { establishmentId: shopId } },
        }),
      ),
    [shopId],
  );
  const reload = useCallback(async () => {
    const out = await fetchVisit();
    if (out.ok) setVisit(out.data.data ?? null);
    else setError(problemMessage(out.problem, out.offline));
  }, [fetchVisit]);

  useEffect(() => {
    let active = true;
    void fetchVisit().then((out) => {
      if (!active) return;
      if (out.ok) setVisit((cur) => (cur === undefined ? (out.data.data ?? null) : cur));
      else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchVisit]);

  // Atualizações pelo fluxo SSE: snapshot (ao conectar/reconectar) e eventos da própria visita.
  // Cada evento traz o recurso completo e a versão; só vale se for mais novo que o da tela.
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      const d = m.data as { visit?: Visit | null; granted_channels: string[] };
      if (d.granted_channels.includes("visit")) setVisit(d.visit ?? null);
      return;
    }
    if (m.audience !== "visit") return;
    // Sempre a partir do estado ATUAL (setVisit com função): dois eventos que chegam juntos
    // (participação e comanda) não podem sobrescrever um ao outro com uma cópia velha.
    if (m.type === "participation.updated") {
      const p = m.data as Visit["participation"];
      if (!visit || visit.participation.id !== p.id) {
        void reload(); // visita nova (outra aba): relê a visita inteira
        return;
      }
      setVisit((cur) =>
        cur && cur.participation.id === p.id && p.version > cur.participation.version
          ? { ...cur, participation: p }
          : cur,
      );
    } else if (m.type === "order.updated" || m.type === "payment.updated") {
      const o = m.data as Visit["order"];
      setVisit((cur) =>
        cur && cur.order.id === o.id && o.version > cur.order.version ? { ...cur, order: o } : cur,
      );
    }
  });

  if (error) return <Notice kind="erro">{error}</Notice>;
  if (visit === undefined) return <p role="status">Carregando sua visita…</p>;
  // Com participação ativa ou comanda ainda pendente, não há nova entrada (R-FILA-02/03).
  const ocupada =
    visit &&
    (["waiting", "called", "in_service"].includes(visit.participation.status) ||
      ["open", "pending_review", "awaiting_payment"].includes(visit.order.status));
  return (
    <>
      {visit && (
        <Acompanhar
          shopId={shopId}
          visit={visit}
          onChange={(v) => (v ? setVisit(v) : void reload())}
          onReload={reload}
        />
      )}
      {!ocupada && <Entrar shopId={shopId} onEntered={setVisit} />}
    </>
  );
}

function Acompanhar({
  shopId,
  visit,
  onChange,
  onReload,
}: {
  shopId: string;
  visit: Visit;
  onChange: (v: Visit | null) => void;
  onReload: () => Promise<void>;
}) {
  const cmd = useCommand();
  const p = visit.participation;
  const o = visit.order;
  async function desistir() {
    const out = await cmd.run(`desistir:${p.id}:${p.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/cancel", {
        params: {
          path: { establishmentId: shopId, participationId: p.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: p.version },
      }),
    );
    if (out.ok) onChange(out.data.data);
    else if (out.problem?.status === 409) await onReload();
  }
  return (
    <section className="card" aria-labelledby="titulo-visita">
      <h3 id="titulo-visita">
        Sua senha: <span className="code">{p.ticket_code}</span>
      </h3>
      <p role="status">
        <strong>{statusText[p.status]}</strong>
        {p.status === "waiting" && p.position && <> — você é o {p.position}º da fila</>}
      </p>
      {p.status === "waiting" && p.estimated_wait && (
        <p>Espera estimada: cerca de {p.estimated_wait.minutes} min (estimativa, pode mudar).</p>
      )}
      {p.status === "called" && p.tolerance_ends_at && (
        <p>
          Você tem até {time.format(new Date(p.tolerance_ends_at))} para chegar. Depois disso o profissional
          pode marcar ausência.
        </p>
      )}
      <ItensDaComanda order={o} />
      <PagamentoCliente shopId={shopId} order={o} onReload={onReload} />
      {o.status === "pending_review" && (
        <p>O dono da loja vai revisar o que foi feito. Só serviços feitos por inteiro são cobrados.</p>
      )}
      <Pedido
        shopId={shopId}
        visit={visit}
        onOrder={(order) => onChange({ ...visit, order })}
        onConflict={onReload}
      />
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <div className="actions">
        <button type="button" className="button button--secondary" onClick={() => void onReload()}>
          Atualizar
        </button>
        {(p.status === "waiting" || p.status === "called") && (
          <button
            type="button"
            className="button button--secondary"
            disabled={cmd.busy !== null}
            onClick={() => void desistir()}
          >
            Sair da fila
          </button>
        )}
      </div>
    </section>
  );
}

function Entrar({ shopId, onEntered }: { shopId: string; onEntered: (v: Visit) => void }) {
  const [services, setServices] = useState<PublicService[]>([]);
  const queues = (usePublicQueues(shopId) ?? []).filter((x) => x.status === "open");
  const [queueId, setQueueId] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const cmd = useCommand();

  useEffect(() => {
    let active = true;
    const path = { establishmentId: shopId };
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/services", { params: { path } }),
    ).then((s) => {
      if (active && s.ok) setServices(s.data.data);
    });
    return () => {
      active = false;
    };
  }, [shopId]);

  const queue = queues.find((q) => q.queue_id === queueId);
  const available = queue ? services.filter((s) => s.professional_ids.includes(queue.professional.id)) : [];
  const total = available
    .filter((s) => chosen.includes(s.id))
    .reduce((sum, s) => sum + s.price.amount_cents, 0);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!queueId || chosen.length === 0) return;
    const body = { queue_id: queueId, service_ids: chosen };
    const out = await cmd.run(`entrar:${queueId}:${[...chosen].sort().join(",")}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/participations", {
        params: { path: { establishmentId: shopId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) onEntered(out.data.data);
  }

  if (queues.length === 0) return <p>Nenhuma fila aberta no momento.</p>;
  return (
    <form onSubmit={onSubmit} noValidate className="card" aria-labelledby="titulo-entrar-fila">
      <h3 id="titulo-entrar-fila">Entrar na fila</h3>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <fieldset className="choices">
        <legend>Profissional</legend>
        {queues.map((q) => (
          <label key={q.queue_id} className="choice">
            <input
              type="radio"
              name="profissional"
              value={q.queue_id}
              checked={queueId === q.queue_id}
              onChange={() => {
                setQueueId(q.queue_id);
                setChosen([]);
              }}
            />
            {q.professional.display_name} — {q.waiting_count} aguardando (~{q.estimated_wait.minutes} min,
            estimativa)
          </label>
        ))}
      </fieldset>
      {queue && (
        <fieldset className="choices">
          <legend>Serviços</legend>
          {available.length === 0 && <p>Este profissional não tem serviços disponíveis.</p>}
          {available.map((s) => (
            <label key={s.id} className="choice">
              <input
                type="checkbox"
                checked={chosen.includes(s.id)}
                onChange={(e) =>
                  setChosen((c) => (e.target.checked ? [...c, s.id] : c.filter((x) => x !== s.id)))
                }
              />
              {s.name} — {formatCents(s.price.amount_cents)} · {s.duration_minutes} min
            </label>
          ))}
        </fieldset>
      )}
      {chosen.length > 0 && <p>Total previsto: {formatCents(total)} (confirmado pelo servidor ao entrar).</p>}
      <Submit busy={cmd.busy !== null}>Entrar na fila</Submit>
    </form>
  );
}
