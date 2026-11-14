import { useCallback, useEffect, useState } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useStreamMessages } from "../../stream/context";
import { Notice } from "../../ui/ui";
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

  if (error) return <Notice kind="erro">{error}</Notice>;
  if (!summary) return <p role="status">Carregando seus pontos…</p>;
  return (
    <section className="card" aria-labelledby="titulo-pontos">
      <h3 id="titulo-pontos">Seus pontos nesta loja</h3>
      {!summary.program_enabled && (
        <Notice kind="info">
          O programa de pontos está desligado no momento. Seu saldo continua guardado e uma reserva já feita
          continua valendo.
        </Notice>
      )}
      <ul className="cards" aria-label="Saldo">
        <li className="card">
          <strong>{summary.available}</strong> disponíveis
        </li>
        <li className="card">
          <strong>{summary.held}</strong> reservados
        </li>
        <li className="card">
          <strong>{summary.balance}</strong> no saldo
        </li>
      </ul>
      {summary.balance < 0 && (
        <p className="muted">
          Seu saldo ficou negativo porque um pagamento com pontos já usados foi devolvido. Não há cobrança em
          dinheiro; novos prêmios ficam disponíveis quando o saldo voltar a ser suficiente.
        </p>
      )}
      {summary.active_reservation && (
        <Reservado shopId={shopId} reservation={summary.active_reservation} onDone={reload} />
      )}
      <Catalogo shopId={shopId} summary={summary} rewards={rewards} onDone={reload} />
      {ledger.length > 0 && (
        <>
          <h4>Extrato</h4>
          <ul className="order__items" aria-label="Extrato de pontos">
            {ledger.map((e) => (
              <li key={e.id}>
                <span>
                  {ledgerText[e.kind]} · {when.format(new Date(e.occurred_at))}
                  {e.held_delta !== 0 && <> · reservados {signed(e.held_delta)}</>}
                </span>
                <span className="order__price">{signed(e.balance_delta)}</span>
              </li>
            ))}
          </ul>
        </>
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
    <div className="notice notice--ok" role="status" aria-label="Prêmio reservado">
      <p>
        <strong>Prêmio reservado: {t.name}</strong> ({t.cost_points} pontos). Válido até{" "}
        {when.format(new Date(reservation.expires_at))}. Mostre esta tela na loja para receber.
      </p>
      <p>{t.usage_instructions}</p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <button
        type="button"
        className="button button--secondary"
        disabled={cmd.busy !== null}
        onClick={() => void cancel()}
      >
        Cancelar reserva (os pontos voltam)
      </button>
    </div>
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
    <>
      <h4>Prêmios</h4>
      <p className="muted">
        Prêmio é uma cortesia separada: não dá desconto na comanda nem prioridade na fila. A reserva vale 24
        horas.
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <ul className="cards">
        {rewards.map((r) => {
          const out = r.stock.kind === "limited" && (r.stock.units_available ?? 0) === 0;
          const blocked =
            !summary.program_enabled ||
            summary.active_reservation !== null ||
            summary.available < r.cost_points ||
            out;
          return (
            <li key={r.id} className="card">
              <h5>
                {r.name} — {r.cost_points} pontos
              </h5>
              <p>{r.description}</p>
              <p className="muted">
                {r.usage_instructions} · {stockText(r)}
              </p>
              <button
                type="button"
                className="button"
                disabled={blocked || cmd.busy !== null}
                onClick={() => void reserve(r)}
              >
                Reservar {r.name}
              </button>
              {!out &&
                summary.program_enabled &&
                !summary.active_reservation &&
                summary.available < r.cost_points && (
                  <p className="muted">Faltam {r.cost_points - summary.available} pontos.</p>
                )}
            </li>
          );
        })}
      </ul>
    </>
  );
}
