import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
const ok = (v: number, sample?: number) => ({ value: v, status: "ok", ...(sample !== undefined ? { sample } : {}) });
const meta = (est: string) => ({
  establishment_id: est, establishment_name: est === A ? "Loja A" : "Loja B", timezone: "America/Sao_Paulo",
  period: { from: "2026-11-18", to: "2026-11-18", start: "", end: "", effective_end: "" }, compare_period: null,
  generated_at: "2026-11-18T15:00:00Z", collection_started_at: "2026-01-01T00:00:00Z", ignored_filters: [],
});
const resumo = (est: string, net: number) => ({
  data: {
    current: { cash: { net_cents: ok(net), received_cents: ok(net), refunded_cents: ok(0) }, completed: { count: ok(3) }, wait: { median_seconds: ok(600, 3), p90_seconds: ok(900, 3) } },
    compare: null,
    pending_now: { snapshot_at: "2026-11-18T15:00:00Z", total: 1, open_financial_issues: 1, open_financial_issues_cents: 0, orders_awaiting_payment: 0, orders_awaiting_payment_cents: 0, orders_awaiting_confirmation: 0, charges_unknown: 0 },
  },
  meta: meta(est),
});
const serie = (est: string) => ({
  data: {
    metric: "cash",
    series: [{ key: "received_cents", label: "Recebido", unit: "cents" }, { key: "refunded_cents", label: "Estornado", unit: "cents" }, { key: "net_cents", label: "Líquido", unit: "cents" }],
    buckets: [{ start: "2026-11-18T03:00:00Z", end: "2026-11-19T03:00:00Z", label: "18/11", collected: true, partial: true, values: { received_cents: ok(15500), refunded_cents: ok(3500), net_cents: ok(12000) } }],
    totals: { received_cents: ok(15500), refunded_cents: ok(3500), net_cents: ok(12000) },
    compare_totals: null,
  },
  meta: meta(est),
});

type Handler = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;
let handler: Handler;
let ouvintes: ((e: { payload: unknown }) => void)[] = [];

beforeEach(() => {
  ouvintes = [];
  (globalThis as Record<string, unknown>).__TAURI__ = {
    core: { invoke: (cmd: string, args?: Record<string, unknown>) => handler(cmd, args) },
    event: { listen: async (_e: string, cb: (e: { payload: unknown }) => void) => { ouvintes.push(cb); return () => {}; } },
  };
  (globalThis as Record<string, unknown>).ResizeObserver = class { observe() {} disconnect() {} };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function base(consultar: (c: { tipo: string; loja: string; metrica?: string }) => Promise<unknown>): Handler {
  return async (cmd, args) => {
    switch (cmd) {
      case "estado_dashboard":
        return { instalacao: { id: "i1", label: "PC" }, vinculado: false, ticket_pendente: true, loja_pedida: A, painel: "" };
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
    handler = base(async (c) => (c.tipo === "resumo" ? resumo(c.loja, 12000) : c.tipo === "serie" ? serie(c.loja) : { data: { dimension: "x", status: "ok", rows: [] }, meta: meta(c.loja) }));
    render(<App />);
    expect(await screen.findByText("Recebimentos líquidos")).toBeInTheDocument();
    expect(await screen.findAllByText(/R\$\s?120,00/)).not.toHaveLength(0);
    expect(screen.getByText("AGORA")).toBeInTheDocument();
    const caixa = (await screen.findByRole("heading", { name: "Caixa" })).closest("section")!;
    fireEvent.click(within(caixa).getByRole("button", { name: "Ver tabela" }));
    const tabela = within(caixa).getByRole("table");
    expect(within(tabela).getByText(/155,00/)).toBeInTheDocument();
    expect(within(tabela).getByText(/35,00/)).toBeInTheDocument();
  });

  it("resposta atrasada da loja A depois de trocar para B é ignorada", async () => {
    let soltarA: (v: unknown) => void = () => {};
    handler = base((c) => {
      if (c.tipo !== "resumo") return Promise.resolve({ data: { dimension: "x", status: "ok", rows: [] }, meta: meta(c.loja) });
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
    handler = base(async (c) => (c.tipo === "resumo" ? resumo(c.loja, 12000) : { data: { dimension: "x", status: "ok", rows: [] }, meta: meta(c.loja) }));
    render(<App />);
    expect(await screen.findAllByText(/R\$\s?120,00/)).not.toHaveLength(0);
    await waitFor(() => expect(ouvintes.length).toBeGreaterThan(0));
    act(() => ouvintes.forEach((o) => o({ payload: { event: "session_ended", loja: A, data: { reason: "owner_session_replaced" } } })));
    expect(await screen.findByText("Sua sessão foi encerrada porque você entrou em outro aparelho.")).toBeInTheDocument();
    expect(screen.queryByText(/120,00/)).not.toBeInTheDocument();
  });

  it("primeiro uso: pede o código de ativação", async () => {
    handler = async (cmd) => (cmd === "estado_dashboard" ? { instalacao: null, vinculado: false, ticket_pendente: true, loja_pedida: null, painel: "" } : null);
    render(<App />);
    expect(await screen.findByRole("heading", { name: /Autorizar este computador/ })).toBeInTheDocument();
  });
});
