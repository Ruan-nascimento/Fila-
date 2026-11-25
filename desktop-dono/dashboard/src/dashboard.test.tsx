import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

const A = "0190a000-0000-7000-8000-0000000000a1";
const B = "0190a000-0000-7000-8000-0000000000b1";
const ctx = {
  account: { id: "c1", name: "Ana" },
  installation: { id: "i1", label: "PC" },
  access_expires_at: "2026-11-18T20:00:00Z",
  establishments: [
    { id: A, name: "Loja A", timezone: "America/Sao_Paulo" },
    { id: B, name: "Loja B", timezone: "America/Sao_Paulo" },
  ],
};
const ok = (v: number, sample?: number) => ({
  value: v,
  status: "ok",
  ...(sample !== undefined ? { sample } : {}),
});
const meta = (est: string) => ({
  establishment_id: est,
  establishment_name: est === A ? "Loja A" : "Loja B",
  timezone: "America/Sao_Paulo",
  period: {
    from: "2026-11-18",
    to: "2026-11-18",
    start: "",
    end: "",
    effective_end: "",
  },
  compare_period: null,
  generated_at: "2026-11-18T15:00:00Z",
  collection_started_at: "2026-01-01T00:00:00Z",
  ignored_filters: [],
});
const resumo = (est: string, net: number) => ({
  data: {
    current: {
      cash: {
        net_cents: ok(net),
        received_cents: ok(net),
        refunded_cents: ok(0),
      },
      completed: { count: ok(3) },
      wait: { median_seconds: ok(600, 3), p90_seconds: ok(900, 3) },
    },
    compare: null,
    pending_now: {
      snapshot_at: "2026-11-18T15:00:00Z",
      total: 1,
      open_financial_issues: 1,
      open_financial_issues_cents: 0,
      orders_awaiting_payment: 0,
      orders_awaiting_payment_cents: 0,
      orders_awaiting_confirmation: 0,
      charges_unknown: 0,
    },
  },
  meta: meta(est),
});
const serie = (est: string) => ({
  data: {
    metric: "cash",
    series: [
      { key: "received_cents", label: "Recebido", unit: "cents" },
      { key: "refunded_cents", label: "Estornado", unit: "cents" },
      { key: "net_cents", label: "Líquido", unit: "cents" },
    ],
    buckets: [
      {
        start: "2026-11-18T03:00:00Z",
        end: "2026-11-19T03:00:00Z",
        label: "18/11",
        collected: true,
        partial: true,
        values: {
          received_cents: ok(15500),
          refunded_cents: ok(3500),
          net_cents: ok(12000),
        },
      },
    ],
    totals: {
      received_cents: ok(15500),
      refunded_cents: ok(3500),
      net_cents: ok(12000),
    },
    compare_totals: null,
  },
  meta: meta(est),
});

type Handler = (
  cmd: string,
  args?: Record<string, unknown>,
) => Promise<unknown>;
let handler: Handler;
let ouvintes: ((e: { payload: unknown }) => void)[] = [];

beforeEach(() => {
  ouvintes = [];
  (globalThis as Record<string, unknown>).__TAURI__ = {
    core: {
      invoke: (cmd: string, args?: Record<string, unknown>) =>
        handler(cmd, args),
    },
    event: {
      listen: async (_e: string, cb: (e: { payload: unknown }) => void) => {
        ouvintes.push(cb);
        return () => {};
      },
    },
  };
  (globalThis as Record<string, unknown>).ResizeObserver = class {
    observe() {}
    disconnect() {}
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function base(
  consultar: (c: {
    tipo: string;
    loja: string;
    metrica?: string;
  }) => Promise<unknown>,
): Handler {
  return async (cmd, args) => {
    switch (cmd) {
      case "estado_dashboard":
        return {
          instalacao: { id: "i1", label: "PC" },
          vinculado: false,
          ticket_pendente: true,
          loja_pedida: A,
          painel: "",
        };
      case "vincular_sessao":
        return ctx;
      case "consultar":
        return consultar(args?.consulta as { tipo: string; loja: string });
      default:
        return null;
    }
  };
}

describe("Dashboard local", () => {
  it("mostra os quatro indicadores e a tabela usa os mesmos números do gráfico", async () => {
    handler = base(async (c) =>
      c.tipo === "resumo"
        ? resumo(c.loja, 12000)
        : c.tipo === "serie"
          ? serie(c.loja)
          : {
              data: { dimension: "x", status: "ok", rows: [] },
              meta: meta(c.loja),
            },
    );
    render(<App />);
    expect(
      await screen.findByText("Recebimentos líquidos"),
    ).toBeInTheDocument();
    expect(await screen.findAllByText(/R\$\s?120,00/)).not.toHaveLength(0);
    expect(screen.getByText("AGORA")).toBeInTheDocument();
    const caixa = (
      await screen.findByRole("heading", { name: "Caixa" })
    ).closest("section")!;
    fireEvent.click(within(caixa).getByRole("button", { name: "Ver tabela" }));
    const tabela = within(caixa).getByRole("table");
    expect(within(tabela).getByText(/155,00/)).toBeInTheDocument();
    expect(within(tabela).getByText(/35,00/)).toBeInTheDocument();
  });

  it("resposta atrasada da loja A depois de trocar para B é ignorada", async () => {
    let soltarA: (v: unknown) => void = () => {};
    handler = base((c) => {
      if (c.tipo !== "resumo")
        return Promise.resolve({
          data: { dimension: "x", status: "ok", rows: [] },
          meta: meta(c.loja),
        });
      if (c.loja === A) return new Promise((r) => (soltarA = r)); // A demora
      return Promise.resolve(resumo(B, 2200));
    });
    render(<App />);
    const seletor = await screen.findByRole("combobox", { name: "Loja" });
    fireEvent.change(seletor, { target: { value: B } });
    expect(await screen.findAllByText(/R\$\s?22,00/)).not.toHaveLength(0);
    await act(async () => soltarA(resumo(A, 99900)));
    expect(screen.queryByText(/999,00/)).not.toBeInTheDocument();
    expect(screen.getAllByText(/Loja B/).length).toBeGreaterThan(0);
  });

  it("sessão tomada por outro aparelho: limpa os números e explica", async () => {
    handler = base(async (c) =>
      c.tipo === "resumo"
        ? resumo(c.loja, 12000)
        : {
            data: { dimension: "x", status: "ok", rows: [] },
            meta: meta(c.loja),
          },
    );
    render(<App />);
    expect(await screen.findAllByText(/R\$\s?120,00/)).not.toHaveLength(0);
    await waitFor(() => expect(ouvintes.length).toBeGreaterThan(0));
    act(() =>
      ouvintes.forEach((o) =>
        o({
          payload: {
            event: "session_ended",
            loja: A,
            data: { reason: "owner_session_replaced" },
          },
        }),
      ),
    );
    expect(
      await screen.findByText(
        "Sua sessão foi encerrada porque você entrou em outro aparelho.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/120,00/)).not.toBeInTheDocument();
  });

  it("Agendamentos: taxa de falta, sinal descontado e detalhamento das faltas", async () => {
    const parte = {
      counts: {
        scheduled: ok(3),
        completed: ok(1),
        interrupted: ok(0),
        no_show: ok(1),
        cancelled_by_customer: ok(1),
        cancelled_by_store: ok(0),
        expired_unpaid: ok(1),
        store_late: ok(0),
        reschedules: ok(1),
        no_show_rate: ok(0.5, 2),
        start_delay_median_seconds: { value: null, status: "empty" },
        start_delay_p90_seconds: { value: null, status: "empty" },
      },
      money: {
        deposits_received_cents: ok(4500),
        fees_received_cents: ok(0),
        late_payments_cents: ok(0),
        credits_applied_cents: ok(1500),
        refunds_confirmed_cents: ok(3000),
      },
      modality: [
        { origin: "queue", completed: 2, no_show: 0 },
        { origin: "appointment", completed: 1, no_show: 1 },
      ],
      occupancy: {
        available_seconds: ok(64800),
        committed_seconds: ok(5400),
        planned: ok(5400 / 64800, 1),
        in_service_seconds: ok(1800),
        real: ok(0.1, 1),
        overtime_seconds: ok(0),
        days_with_schedule: 1,
        days: 1,
      },
    };
    const pedidos: {
      tipo: string;
      registro?: string;
      recorte?: Record<string, string>;
    }[] = [];
    handler = base(async (c) => {
      pedidos.push(c as never);
      if (c.tipo === "agendamentos")
        return {
          data: {
            current: parte,
            compare: null,
            daily: [
              {
                date: "2026-11-18",
                start: "2026-11-18T03:00:00Z",
                scheduled: 3,
                completed: 1,
                no_show: 1,
                cancelled: 1,
              },
            ],
            pending_now: {
              snapshot_at: "2026-11-18T15:00:00Z",
              refunds_pending: 0,
              refunds_pending_cents: 0,
              open_balance_cents: 0,
              open_balance_orders: 0,
              awaiting_payment: 0,
            },
          },
          meta: meta(c.loja),
        };
      if (c.tipo === "registros")
        return {
          data: {
            kind: "appointments",
            total: 1,
            total_cents: 4000,
            items: [],
            next_cursor: null,
            window: {
              start: "2026-11-18T03:00:00Z",
              end: "2026-11-19T03:00:00Z",
            },
          },
          meta: meta(c.loja),
        };
      return c.tipo === "resumo"
        ? resumo(c.loja, 0)
        : {
            data: { dimension: "x", status: "ok", rows: [] },
            meta: meta(c.loja),
          };
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("tab", { name: "Agendamentos" }));
    const reservas = (
      await screen.findByRole("heading", { name: "Reservas" })
    ).closest("section")!;
    expect(within(reservas).getByText("Taxa de falta")).toBeInTheDocument();
    expect(within(reservas).getAllByText(/50/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/15,00/).length).toBeGreaterThan(0); // sinal descontado
    fireEvent.click(within(reservas).getByRole("button", { name: /Faltas/ }));
    await waitFor(() =>
      expect(
        pedidos.some(
          (p) =>
            p.tipo === "registros" &&
            p.registro === "appointments" &&
            p.recorte?.state === "no_show",
        ),
      ).toBe(true),
    );
  });

  it("primeiro uso: pede o código de ativação", async () => {
    handler = async (cmd) =>
      cmd === "estado_dashboard"
        ? {
            instalacao: null,
            vinculado: false,
            ticket_pendente: true,
            loja_pedida: null,
            painel: "",
          }
        : null;
    render(<App />);
    expect(
      await screen.findByRole("heading", { name: /Autorizar este computador/ }),
    ).toBeInTheDocument();
  });
});
