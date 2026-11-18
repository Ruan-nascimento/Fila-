import { useCallback, useEffect, useState } from "react";
import { CalendarClock, Gift, Star } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useStreamMessages } from "../../stream/context";
import { Badge, Button, Card, LoadError, Loading, Notice, Section } from "../../ui/ui";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import {
  ledgerText,
  signed,
  stockText,
  type LedgerEntry,
  type LoyaltySummary,
  type Reservation,
  type Reward,
} from "./textos";

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/**
 * Pontos da conta nesta loja: saldo explicado pelo extrato, prêmio reservado (24 h) e catálogo.
 * Prêmio é cortesia separada: não dá desconto na comanda nem prioridade na fila. Os números vêm
 * sempre do servidor; a tela só mostra.
 */
export function Pontos({ shopId }: { shopId: string }) {
  const [summary, setSummary] = useState<LoyaltySummary | null>(null);
  const [rewards, setRewards] = useState<Reward[]>([]);
  const [ledger, setLedger] = useState<LedgerEntry[]>([]);
  const [error, setError] = useState<string | null>(null);

  const fetchAll = useCallback(async () => {
    const path = { establishmentId: shopId };
    const [s, r, l] = await Promise.all([
      call(() => api.GET("/v1/me/loyalty/{establishmentId}", { params: { path } })),
      call(() => api.GET("/v1/establishments/{establishmentId}/reward-catalog", { params: { path } })),
      call(() =>
        api.GET("/v1/me/loyalty/{establishmentId}/ledger", { params: { path, query: { limit: 20 } } }),
      ),
    ]);
    return { s, r, l };
  }, [shopId]);

  const apply = useCallback((out: Awaited<ReturnType<typeof fetchAll>>) => {
    const { s, r, l } = out;
    if (s.ok) setSummary(s.data.data);
    if (r.ok) setRewards(r.data.data);
    if (l.ok) setLedger(l.data.data);
    setError(!s.ok ? problemMessage(s.problem, s.offline) : null);
  }, []);

  const reload = useCallback(async () => apply(await fetchAll()), [apply, fetchAll]);

  useEffect(() => {
    let active = true;
    void fetchAll().then((out) => {
      if (active) apply(out);
    });
    return () => {
      active = false;
    };
  }, [fetchAll, apply]);

  // SSE: saldo chega pronto; mudanças na reserva ou no extrato → relê.
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot") {
      const d = m.data as { loyalty?: LoyaltySummary | null };
      if (d.loyalty) setSummary(d.loyalty);
    } else if (
      m.audience === "account" &&
      (m.type === "loyalty.updated" || m.type === "reward_reservation.updated")
    ) {
      void reload();
    }
  });

  if (error) return <LoadError message={error} onRetry={() => void reload()} />;
  if (!summary) return <Loading>Carregando seus pontos…</Loading>;
  return (
    <section className="grid max-w-4xl gap-6" aria-labelledby="titulo-pontos">
      <div className="grid gap-1">
        <h2 id="titulo-pontos" className="m-0 text-lg font-semibold">
          Seus pontos nesta loja
        </h2>
        <p className="m-0 text-sm text-fg-muted">Saldo, reserva e prêmios desta barbearia.</p>
      </div>
      {!summary.program_enabled && (
        <Notice kind="info">
          <p>
            O programa de pontos está desligado no momento. Seu saldo continua guardado e uma reserva já feita
            continua valendo.
          </p>
        </Notice>
      )}
      <ul className="m-0 grid list-none grid-cols-2 gap-3 p-0 sm:grid-cols-3" aria-label="Saldo">
        <li className="col-span-2 flex items-center gap-3 rounded-xl border border-select-line/60 bg-select p-4 sm:col-span-1">
          <Star aria-hidden="true" className="size-7 shrink-0 text-accent" />
          <span className="grid">
            <strong className="text-3xl tabular-nums text-accent">{summary.available}</strong>
            <span className="text-sm">disponíveis</span>
          </span>
        </li>
        <li className="flex items-center gap-3 rounded-xl border border-line bg-surface p-4">
          <CalendarClock aria-hidden="true" className="size-6 shrink-0 text-fg-muted" />
          <span className="grid">
            <strong className="text-2xl tabular-nums">{summary.held}</strong>
            <span className="text-sm text-fg-muted">reservados</span>
          </span>
        </li>
        <li className="flex items-center gap-3 rounded-xl border border-line bg-surface p-4">
          <span className="grid">
            <strong className="text-2xl tabular-nums">{summary.balance}</strong>
            <span className="text-sm text-fg-muted">no saldo</span>
          </span>
        </li>
      </ul>
      {summary.balance < 0 && (
        <p className="m-0 text-sm text-fg-muted">
          Seu saldo ficou negativo porque um pagamento com pontos já usados foi devolvido. Não há cobrança em
          dinheiro; novos prêmios ficam disponíveis quando o saldo voltar a ser suficiente.
        </p>
      )}
      {summary.active_reservation && (
        <Reservado shopId={shopId} reservation={summary.active_reservation} onDone={reload} />
      )}
      <Catalogo shopId={shopId} summary={summary} rewards={rewards} onDone={reload} />
      {ledger.length > 0 && (
        <Section title="Extrato de pontos" level={3}>
          <ul
            className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0"
            aria-label="Extrato de pontos"
          >
            {ledger.map((e) => (
              <li key={e.id} className="flex items-start justify-between gap-3 px-4 py-3">
                <span className="grid">
                  <span className="font-medium">{ledgerText[e.kind]}</span>
                  <span className="text-sm text-fg-muted tabular-nums">
                    {when.format(new Date(e.occurred_at))}
                    {e.held_delta !== 0 && <> · reservados {signed(e.held_delta)}</>}
                  </span>
                </span>
                <span className="font-semibold tabular-nums">{signed(e.balance_delta)} pontos</span>
              </li>
            ))}
          </ul>
        </Section>
      )}
    </section>
  );
}

function Reservado({
  shopId,
  reservation,
  onDone,
}: {
  shopId: string;
  reservation: Reservation;
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  async function cancel() {
    const out = await cmd.run(`cancelar-reserva:${reservation.id}:${reservation.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/reward-reservations/{reservationId}/cancel", {
        params: {
          path: { establishmentId: shopId, reservationId: reservation.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: reservation.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }
  const t = reservation.accepted_terms;
  return (
    <Card className="grid gap-3 border-success/40 p-4 sm:p-5" role="status" aria-label="Prêmio reservado">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <span className="inline-flex size-11 shrink-0 items-center justify-center rounded-xl bg-success-soft text-success">
            <Gift aria-hidden="true" className="size-6" />
          </span>
          <div className="grid gap-0.5">
            <p className="m-0 font-semibold">Prêmio reservado: {t.name}</p>
            <p className="m-0 text-sm text-fg-muted tabular-nums">
              {t.cost_points} pontos · válido até {when.format(new Date(reservation.expires_at))}
            </p>
          </div>
        </div>
        <Badge tone="success">Reservado</Badge>
      </div>
      <p className="m-0 text-sm">Mostre esta tela na loja para receber. {t.usage_instructions}</p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Button
        variant="secondary"
        className="justify-self-start"
        disabled={cmd.busy !== null}
        onClick={() => void cancel()}
      >
        Cancelar reserva (os pontos voltam)
      </Button>
    </Card>
  );
}

function Catalogo({
  shopId,
  summary,
  rewards,
  onDone,
}: {
  shopId: string;
  summary: LoyaltySummary;
  rewards: Reward[];
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  async function reserve(r: Reward) {
    // Envia a versão do prêmio que a pessoa viu: se o dono mudar as condições, o servidor recusa.
    const out = await cmd.run(`reservar:${r.id}:${r.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/reward-reservations", {
        params: { path: { establishmentId: shopId }, header: { "Idempotency-Key": key } },
        body: { reward_id: r.id, expected_reward_version: r.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }
  if (rewards.length === 0) return null;
  return (
    <Section
      title="Prêmios"
      level={3}
      description="Prêmio é uma cortesia separada: não dá desconto na comanda nem prioridade na fila. A reserva vale 24 horas."
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <ul className="m-0 grid list-none gap-3 p-0 sm:grid-cols-2">
        {rewards.map((r) => {
          const out = r.stock.kind === "limited" && (r.stock.units_available ?? 0) === 0;
          const blocked =
            !summary.program_enabled ||
            summary.active_reservation !== null ||
            summary.available < r.cost_points ||
            out;
          return (
            <li key={r.id} className="grid gap-3 rounded-xl border border-line bg-surface p-4">
              <div className="flex items-start gap-3">
                <span className="inline-flex size-11 shrink-0 items-center justify-center rounded-xl bg-surface-2 text-fg-muted">
                  <Gift aria-hidden="true" className="size-5" />
                </span>
                <div className="grid min-w-0 gap-0.5">
                  <h4 className="m-0 font-semibold">{r.name}</h4>
                  <p className="m-0 text-sm tabular-nums text-fg-muted">
                    {r.cost_points} pontos · {stockText(r)}
                  </p>
                </div>
              </div>
              <p className="m-0 text-sm">{r.description}</p>
              <p className="m-0 text-sm text-fg-muted">{r.usage_instructions}</p>
              <Button disabled={blocked || cmd.busy !== null} onClick={() => void reserve(r)}>
                Reservar {r.name}
              </Button>
              {!out &&
                summary.program_enabled &&
                !summary.active_reservation &&
                summary.available < r.cost_points && (
                  <p className="m-0 text-sm text-fg-muted">
                    Faltam {r.cost_points - summary.available} pontos.
                  </p>
                )}
            </li>
          );
        })}
      </ul>
    </Section>
  );
}
