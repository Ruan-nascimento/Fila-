import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import {
  Bell,
  CircleCheck,
  CircleSlash,
  Clock,
  Hourglass,
  LogOut,
  RefreshCw,
  Scissors,
  TriangleAlert,
  UserRound,
} from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { espera } from "../../tempo";
import { ConviteNotificacoes } from "../../features/notificacoes/Notificacoes";
import { useStale, useStreamMessages } from "../../stream/context";
import { Badge, Button, Card, ConfirmPanel, LoadError, Loading, Notice } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { ItensDaComanda, OrderBadge } from "../comanda/comanda";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { PagamentoCliente } from "../pagamento/PagamentoCliente";
import { useStoreData } from "./dadosContexto";
import { EntrarNaFila } from "./EntrarNaFila";
import { Pedido } from "./Pedido";

type Visit = components["schemas"]["Visit"];
type Status = Visit["participation"]["status"];

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

/** Texto da situação para o leitor de tela (uma frase curta por mudança, sem repetir a fila). */
const statusText: Record<Status, string> = {
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
export function MinhaVisita({
  shopId,
  identified = true,
  onNeedIdentity,
}: {
  shopId: string;
  identified?: boolean;
  onNeedIdentity?: () => void;
}) {
  const [visit, setVisit] = useState<Visit | null | undefined>(identified ? undefined : null);
  const [error, setError] = useState<string | null>(null);
  const [entered, setEntered] = useState(false);

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
    if (out.ok) {
      setVisit(out.data.data ?? null);
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
  }, [fetchVisit]);

  useEffect(() => {
    if (!identified) return;
    let active = true;
    void fetchVisit().then((out) => {
      if (!active) return;
      if (out.ok) setVisit((cur) => (cur === undefined ? (out.data.data ?? null) : cur));
      else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchVisit, identified]);

  // Atualizações pelo fluxo SSE: snapshot (ao conectar/reconectar) e eventos da própria visita.
  // Cada evento traz o recurso completo e a versão; só vale se for mais novo que o da tela.
  // A posição e a espera dependem de QUEM ESTÁ NA FRENTE: quando a fila pública da pessoa muda
  // (alguém chamado, saiu, entrou), relê a própria visita. Uma leitura por vez; mudanças durante a
  // leitura pedem mais uma. A leitura só substitui a tela se não for mais velha que ela.
  const refreshing = useRef<"idle" | "busy" | "again">("idle");
  // Lido por função: o valor muda em outra chamada enquanto esta espera a resposta.
  const again = () => refreshing.current === "again";
  const refreshPosition = useCallback(async () => {
    if (refreshing.current !== "idle") {
      refreshing.current = "again";
      return;
    }
    refreshing.current = "busy";
    try {
      for (;;) {
        const out = await fetchVisit();
        if (out.ok) {
          const fresh = out.data.data ?? null;
          setVisit((cur) =>
            !cur || !fresh || cur.participation.id !== fresh.participation.id
              ? cur
              : fresh.participation.version >= cur.participation.version &&
                  fresh.order.version >= cur.order.version
                ? fresh
                : cur,
          );
        }
        if (!again()) break;
        refreshing.current = "busy";
      }
    } finally {
      refreshing.current = "idle";
    }
  }, [fetchVisit]);

  useStreamMessages((m) => {
    if (!identified) return;
    if (m.type === "stream.snapshot") {
      const d = m.data as { visit?: Visit | null; granted_channels: string[] };
      if (d.granted_channels.includes("visit")) setVisit(d.visit ?? null);
      return;
    }
    if (m.type === "queue.public.updated") {
      if (visit && visit.participation.status === "waiting" && m.resource.id === visit.participation.queue_id)
        void refreshPosition();
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

  if (error && visit === undefined) return <LoadError message={error} onRetry={() => void reload()} />;
  if (visit === undefined) return <Loading>Carregando sua visita…</Loading>;
  // Com participação ativa ou comanda ainda pendente, não há nova entrada (R-FILA-02/03).
  const ocupada =
    visit &&
    (["waiting", "called", "in_service"].includes(visit.participation.status) ||
      ["open", "pending_review", "awaiting_payment", "awaiting_confirmation"].includes(visit.order.status));
  return (
    <div className="grid gap-6">
      {entered && visit?.participation.status === "waiting" && (
        <Notice kind="ok" title="Você entrou na fila.">
          <p>Acompanhe por aqui: esta tela se atualiza sozinha.</p>
        </Notice>
      )}
      {visit && (
        <Acompanhar
          shopId={shopId}
          visit={visit}
          onChange={(v) => (v ? setVisit(v) : void reload())}
          onReload={reload}
        />
      )}
      {!ocupada && (
        <section aria-labelledby="titulo-nova-entrada" className="grid gap-3">
          <h2 id="titulo-nova-entrada" className="m-0 text-lg font-semibold">
            {visit ? "Entrar na fila de novo" : "Entrar na fila"}
          </h2>
          <EntrarNaFila
            identified={identified}
            {...(onNeedIdentity ? { onNeedIdentity } : {})}
            onEntered={(v) => {
              setEntered(true);
              setVisit(v);
            }}
          />
        </section>
      )}
    </div>
  );
}

/** Destaque da situação: posição, chamada, atendimento ou encerramento. */
function Destaque({
  visit,
  professional,
  stale,
}: {
  visit: Visit;
  professional: string | undefined;
  stale: boolean;
}) {
  const p = visit.participation;
  const staleTag = stale && (
    <Badge tone="warning" icon={TriangleAlert}>
      Última informação conhecida
    </Badge>
  );
  const ticket = (
    <span className="text-fg-muted">
      Senha <strong className="font-semibold text-fg">{p.ticket_code}</strong>
    </span>
  );

  if (p.status === "waiting") {
    return (
      <div className="grid gap-4">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone="primary" icon={Clock}>
            Aguardando atendimento
          </Badge>
          {staleTag}
        </div>
        <p className="m-0 flex flex-wrap items-baseline gap-x-3">
          <span className="text-6xl font-bold tracking-tight tabular-nums sm:text-7xl">
            {p.position ? `${p.position}º` : "—"}
          </span>
          <span className="text-lg text-fg-muted">na fila{professional ? ` de ${professional}` : ""}</span>
        </p>
        <dl className="m-0 grid grid-cols-2 gap-3">
          <div className="rounded-lg bg-surface-2 px-3 py-2">
            <dt className="text-xs text-fg-muted">Senha de atendimento</dt>
            <dd className="m-0 text-xl font-bold">{p.ticket_code}</dd>
          </div>
          <div className="rounded-lg bg-surface-2 px-3 py-2">
            <dt className="text-xs text-fg-muted">Espera (estimativa)</dt>
            <dd className="m-0 text-xl font-bold tabular-nums">
              {p.estimated_wait ? `~${espera(p.estimated_wait.minutes)}` : "—"}
            </dd>
          </div>
        </dl>
        <p className="m-0 text-sm text-fg-muted">
          A espera é uma estimativa e pode mudar. Quando chegar sua vez, esta tela avisa.
        </p>
      </div>
    );
  }
  if (p.status === "called") {
    return (
      <div className="grid justify-items-center gap-3 py-2 text-center">
        <span className="inline-flex size-16 items-center justify-center rounded-full bg-select text-accent">
          <Bell aria-hidden="true" className="size-8" />
        </span>
        <p className="m-0 text-3xl font-bold tracking-tight sm:text-4xl">Sua vez, {p.nick}</p>
        <Badge tone="primary" className="px-4 py-1 text-base">
          Senha {p.ticket_code}
        </Badge>
        {p.tolerance_ends_at && (
          <p className="m-0 flex items-center gap-2 text-lg">
            <Clock aria-hidden="true" className="size-5 text-fg-muted" />
            Apresente-se até{" "}
            <strong className="tabular-nums">{time.format(new Date(p.tolerance_ends_at))}</strong>
          </p>
        )}
        <p className="m-0 text-sm text-fg-muted">
          Vá até {professional ?? "o profissional"}. Depois desse horário, a loja pode marcar ausência.
        </p>
        {staleTag}
      </div>
    );
  }
  const info: Record<
    Exclude<Status, "waiting" | "called">,
    { icon: typeof Clock; title: string; text: ReactNode }
  > = {
    in_service: {
      icon: Scissors,
      title: "Em atendimento",
      text: (
        <>
          Com {professional ?? "o profissional"}
          {p.started_at && ` desde ${time.format(new Date(p.started_at))}`}.
        </>
      ),
    },
    completed: {
      icon: CircleCheck,
      title: "Atendimento concluído",
      text: "Confira a comanda e o pagamento abaixo.",
    },
    interrupted: {
      icon: TriangleAlert,
      title: "Atendimento interrompido",
      text:
        visit.order.status === "pending_review"
          ? "O dono da loja vai revisar o que foi feito. Só serviços feitos por inteiro são cobrados. Você não precisa fazer nada agora."
          : "A revisão terminou. Confira a comanda abaixo.",
    },
    cancelled: {
      icon: CircleSlash,
      title: "Você saiu da fila",
      text: p.end_reason ? `Motivo informado pela loja: ${p.end_reason}` : "Sua senha foi cancelada.",
    },
    no_show: {
      icon: CircleSlash,
      title: "Marcado como ausente",
      text: "O prazo para se apresentar terminou. Se ainda quiser atendimento, entre na fila de novo.",
    },
  };
  const s = info[p.status];
  return (
    <div className="flex items-start gap-3">
      <span
        className={cx(
          "inline-flex size-11 shrink-0 items-center justify-center rounded-full",
          p.status === "in_service" || p.status === "completed"
            ? "bg-select text-accent"
            : "bg-surface-2 text-fg-muted",
        )}
      >
        <s.icon aria-hidden="true" className="size-6" />
      </span>
      <div className="grid gap-1">
        <p className="m-0 text-xl font-bold">{s.title}</p>
        <p className="m-0 text-fg-muted">{s.text}</p>
        <p className="m-0 text-sm">{ticket}</p>
        {staleTag}
      </div>
    </div>
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
  const late = useCommand();
  const leave = useInlinePanel();
  const stale = useStale();
  const { queues } = useStoreData();
  const p = visit.participation;
  const o = visit.order;
  const professional =
    queues?.find((q) => q.queue_id === p.queue_id)?.professional.display_name ??
    queues?.find((q) => q.professional.id === p.professional_id)?.professional.display_name;
  const active = p.status === "waiting" || p.status === "called";

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
    if (out.ok) {
      leave.hide();
      onChange(out.data.data);
    } else if (out.problem?.status === 409) await onReload();
  }

  // Avisa a barbearia pelo app; a equipe é notificada e decide (o lugar na fila não muda sozinho).
  async function atrasar() {
    const out = await late.run(`atraso:${p.id}:${p.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/late-notice", {
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

  const showOrder = p.status !== "waiting" || o.items.length > 0;
  return (
    <section
      aria-labelledby="titulo-visita"
      className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] lg:items-start"
    >
      <h2 id="titulo-visita" className="sr-only">
        Sua senha: {p.ticket_code}
      </h2>
      {/* Uma frase curta por mudança de situação: o leitor de tela não repete a tela inteira. */}
      <p role="status" className="sr-only">
        {statusText[p.status]}
        {p.status === "waiting" && p.position ? ` — você é o ${p.position}º da fila.` : "."}
      </p>
      <div className="grid gap-3">
        <Card className={cx("p-5 sm:p-6", p.status === "called" && "border-primary ring-2 ring-primary/30")}>
          <Destaque visit={visit} professional={professional} stale={stale} />
        </Card>
        {active && (
          <ConviteNotificacoes texto="Quer ser avisado quando a fila andar e quando chegar a sua vez, mesmo usando outro app? Ative as notificações." />
        )}
        {/* Avisar atraso só quando a vez está perto (chamado ou até o 3º): antes disso não muda nada. */}
        {active &&
          (p.status === "called" || (p.position != null && p.position <= 3)) &&
          (p.late_notice_at ? (
            <p
              role="status"
              className="m-0 flex items-center gap-2 rounded-lg bg-surface-2 px-3 py-2 text-sm"
            >
              <Hourglass aria-hidden="true" className="size-4 text-accent" />
              Você avisou a barbearia que vai se atrasar às {time.format(new Date(p.late_notice_at))}. Seu
              lugar continua guardado até {professional ?? "o profissional"} decidir.
            </p>
          ) : (
            <div className="grid gap-1">
              <Button
                variant="secondary"
                icon={Hourglass}
                disabled={late.busy !== null}
                onClick={() => void atrasar()}
              >
                Vou me atrasar
              </Button>
              <p className="m-0 text-sm text-fg-muted">
                Avisa {professional ?? "o profissional"} na hora. Se for preciso, ele chama o próximo e você
                continua logo em seguida.
              </p>
              <CommandError problem={late.problem} offline={late.offline} />
            </div>
          ))}
      </div>

      <div className="grid gap-4">
        {showOrder && (
          <Card className="grid gap-3 p-4 sm:p-5">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="m-0 text-base font-semibold">
                {p.status === "waiting" || p.status === "called" ? "Serviços na fila" : "Comanda"}
              </h3>
              <OrderBadge order={o} />
            </div>
            <ItensDaComanda order={o} title={false} />
            <PagamentoCliente shopId={shopId} order={o} onReload={onReload} />
            <Pedido
              shopId={shopId}
              visit={visit}
              onOrder={(order) => onChange({ ...visit, order })}
              onConflict={onReload}
            />
          </Card>
        )}
        <CommandError problem={cmd.problem} offline={cmd.offline} />
        <div className="flex flex-wrap items-center gap-2">
          {active && !leave.open && (
            <Button
              id={leave.triggerId}
              variant="danger-ghost"
              icon={LogOut}
              onClick={() => leave.show("aberto")}
            >
              Sair da fila
            </Button>
          )}
          <Button variant="ghost" icon={RefreshCw} onClick={() => void onReload()}>
            Atualizar
          </Button>
        </div>
        {leave.open && (
          <ConfirmPanel
            panelId={leave.panelId}
            onKeyDown={leave.onKeyDown}
            variant="danger"
            title={`Sair da fila e cancelar a senha ${p.ticket_code}?`}
            confirmLabel="Sim, sair da fila"
            cancelLabel="Continuar na fila"
            busy={cmd.busy !== null}
            onConfirm={() => void desistir()}
            onCancel={leave.hide}
          >
            <p>
              Você perde a posição atual. Se quiser atendimento depois, entra de novo no fim da fila. Nada é
              cobrado.
            </p>
          </ConfirmPanel>
        )}
      </div>
      {p.status === "waiting" && (
        <p className="m-0 flex items-center gap-2 text-sm text-fg-muted lg:col-span-2">
          <UserRound aria-hidden="true" className="size-4" />
          Você pode sair e voltar a esta página: a fila guarda seu lugar.
        </p>
      )}
    </section>
  );
}
