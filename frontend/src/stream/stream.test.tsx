import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MinhaVisita } from "../pages/fila/MinhaVisita";
import { SessionProvider } from "../session/session";
import { fakeApi } from "../test/fake-api";
import { backoffDelay, StreamClient, type StreamStatus } from "./client";
import { StreamProvider, StreamStatusNotice } from "./StreamProvider";

/** EventSource falso: guarda as conexões abertas e deixa o teste emitir mensagens. */
class FakeES {
  static all: FakeES[] = [];
  url: string;
  closed = false;
  onerror: ((ev: Event) => void) | null = null;
  onopen: ((ev: Event) => void) | null = null;
  private listeners = new Map<string, ((ev: MessageEvent) => void)[]>();
  constructor(url: string) {
    this.url = url;
    FakeES.all.push(this);
  }
  addEventListener(type: string, l: (ev: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), l]);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown, id = "") {
    const ev = new MessageEvent(type, { data: JSON.stringify(data), lastEventId: id });
    for (const l of this.listeners.get(type) ?? []) l(ev);
  }
  static open() {
    return FakeES.all.filter((e) => !e.closed);
  }
  static last() {
    const e = FakeES.all.at(-1);
    if (!e) throw new Error("nenhuma conexão");
    return e;
  }
}

const EST = "0190a000-0000-7000-8000-0000000000e1";
const PART = "0190a000-0000-7000-8000-0000000000d1";

function envelope(
  type: string,
  resource: { type: string; id: string; version: number },
  data: unknown,
  audience = "visit",
) {
  return {
    schema_version: 1,
    event_id: crypto.randomUUID(),
    type,
    occurred_at: "2026-11-13T12:00:00Z",
    establishment_id: EST,
    audience,
    resource,
    data,
  };
}

beforeEach(() => {
  FakeES.all = [];
});
afterEach(() => {
  vi.useRealTimers();
});

describe("cliente SSE", () => {
  it("espera com jitter total, limitada a 30 s", () => {
    expect(backoffDelay(0, () => 0)).toBe(0);
    expect(backoffDelay(0, () => 0.999)).toBeLessThan(1000);
    expect(backoffDelay(3, () => 0.5)).toBe(4000);
    expect(backoffDelay(20, () => 0.999)).toBeLessThan(30_000);
  });

  it("reconecta com o cursor, com backoff; volta na hora com rede ou aba; uma conexão por vez", () => {
    vi.useFakeTimers();
    const statuses: StreamStatus[] = [];
    const messages: string[] = [];
    const client = new StreamClient({
      estId: EST,
      channels: ["public", "visit"],
      onMessage: (m) => messages.push(m.type),
      onStatus: (s) => statuses.push(s),
      onSessionEnded: () => {},
      open: (url) => new FakeES(url),
      random: () => 0.5,
    });
    client.start();
    expect(FakeES.all).toHaveLength(1);
    expect(FakeES.last().url).toBe(`/v1/stream?establishment_id=${EST}&channels=public%2Cvisit`);
    expect(FakeES.last().url).not.toContain("cursor");
    FakeES.last().emit(
      "stream.snapshot",
      envelope("stream.snapshot", { type: "stream", id: EST, version: 7 }, {}),
      "c7",
    );
    expect(statuses.at(-1)).toBe("live");

    // Queda: espera aleatória (0,5 × 1 s) e volta com o cursor recebido.
    FakeES.last().onerror?.(new Event("error"));
    expect(statuses.at(-1)).toBe("reconnecting");
    expect(FakeES.open()).toHaveLength(0);
    vi.advanceTimersByTime(499);
    expect(FakeES.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeES.all).toHaveLength(2);
    expect(FakeES.last().url).toContain("cursor=c7");
    // Segunda queda seguida: espera dobra (0,5 × 2 s).
    FakeES.last().onerror?.(new Event("error"));
    vi.advanceTimersByTime(999);
    expect(FakeES.all).toHaveLength(2);
    // A rede voltou: reconecta já, sem esperar o resto.
    window.dispatchEvent(new Event("online"));
    expect(FakeES.all).toHaveLength(3);
    expect(FakeES.open()).toHaveLength(1);
    FakeES.last().onopen?.(new Event("open")); // retomada aceita pelo servidor
    expect(statuses.at(-1)).toBe("live");
    // Voltou para a aba: reconecta (a conexão pode ter morrido em silêncio), fechando a anterior.
    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    expect(FakeES.all).toHaveLength(4);
    expect(FakeES.open()).toHaveLength(1);
    // Mensagem atrasada da conexão antiga é ignorada.
    FakeES.all[2]?.emit(
      "queue.public.updated",
      envelope("queue.public.updated", { type: "queue", id: "q", version: 1 }, {}),
      "c8",
    );
    expect(messages).toEqual(["stream.snapshot"]);
    client.stop();
    expect(FakeES.open()).toHaveLength(0);
  });

  it("sessão encerrada no servidor: para e avisa (sem reconectar com a sessão velha)", () => {
    const ended = vi.fn();
    const client = new StreamClient({
      estId: EST,
      channels: ["visit"],
      onMessage: () => {},
      onStatus: () => {},
      onSessionEnded: ended,
      open: (url) => new FakeES(url),
    });
    client.start();
    FakeES.last().emit(
      "stream.session_ended",
      envelope(
        "stream.session_ended",
        { type: "stream", id: EST, version: 0 },
        { reason: "session_revoked" },
      ),
    );
    expect(ended).toHaveBeenCalledOnce();
    window.dispatchEvent(new Event("online"));
    expect(FakeES.all).toHaveLength(1);
  });
});

function visit(status: string, version: number) {
  return {
    participation: {
      id: PART,
      establishment_id: EST,
      queue_id: "q1",
      professional_id: "p1",
      ticket_code: "A007",
      status,
      position: status === "waiting" ? 2 : null,
      estimated_wait:
        status === "waiting" ? { kind: "estimate", minutes: 30, computed_at: "2026-11-13T12:00:00Z" } : null,
      nick: "Lia",
      loyalty_eligible: false,
      called_at: null,
      tolerance_ends_at: null,
      started_at: null,
      finished_at: null,
      end_reason: null,
      transfers: [],
      version,
      order_id: "o1",
      created_at: "2026-11-13T12:00:00Z",
    },
    order: {
      id: "o1",
      establishment_id: EST,
      participation_id: PART,
      status: "open",
      version: 1,
      items: [],
      total: { amount_cents: 0, currency: "BRL" },
      total_frozen_at: null,
      pending_change_request: null,
      change_requests: [],
      adjustments: [],
      payment: {
        method_choice: null,
        amounts: {
          due: { amount_cents: 0, currency: "BRL" },
          received: { amount_cents: 0, currency: "BRL" },
          conferred: { amount_cents: 0, currency: "BRL" },
          refunded: { amount_cents: 0, currency: "BRL" },
          outstanding: { amount_cents: 0, currency: "BRL" },
        },
        verifying: false,
        charges: [],
        receipts: [],
        refunds: [],
        open_issue_kinds: [],
      },
      created_at: "2026-11-13T12:00:00Z",
    },
  };
}

describe("visita atualizada pelo fluxo", () => {
  it("aplica só versões mais novas, troca pelo snapshot e não consulta periodicamente", async () => {
    vi.stubGlobal("EventSource", FakeES);
    const { requests } = fakeApi({
      "GET /v1/session": () =>
        Response.json({
          data: { account: null, session: null, visitors: [], memberships: [], csrf_token: null },
        }),
      [`GET /v1/establishments/${EST}/my-visit`]: () =>
        Response.json({ data: visit("waiting", 1), cursor: "c1" }),
      [`GET /v1/public/establishments/${EST}/queues`]: () => Response.json({ data: [], cursor: "c1" }),
      [`GET /v1/public/establishments/${EST}/services`]: () => Response.json({ data: [] }),
    });
    render(
      <SessionProvider>
        <StreamProvider estId={EST} channels={["public", "visit"]}>
          <StreamStatusNotice />
          <MinhaVisita shopId={EST} />
        </StreamProvider>
      </SessionProvider>,
    );
    expect(await screen.findByText(/Aguardando/)).toBeInTheDocument();
    expect(screen.getByText(/Conectando para receber atualizações/)).toBeInTheDocument();
    const es = FakeES.last();
    act(() =>
      es.emit(
        "stream.snapshot",
        envelope(
          "stream.snapshot",
          { type: "stream", id: EST, version: 3 },
          {
            reason: "initial",
            granted_channels: ["public", "visit"],
            denied_channels: [],
            public_queues: [],
            visit: visit("waiting", 1),
          },
        ),
        "c3",
      ),
    );
    expect(screen.queryByText(/Conectando/)).toBeNull();
    const part = (status: string, version: number) =>
      envelope(
        "participation.updated",
        { type: "participation", id: PART, version },
        visit(status, version).participation,
      );
    act(() => es.emit("participation.updated", part("called", 2), "c4"));
    expect(screen.getByText(/Chamado!/)).toBeInTheDocument();
    // Repetido ou atrasado (versão menor/igual): nada muda.
    act(() => es.emit("participation.updated", part("waiting", 1), "c5"));
    expect(screen.getByText(/Chamado!/)).toBeInTheDocument(); // versão velha ignorada
    act(() => es.emit("participation.updated", part("called", 2), "c5"));
    expect(screen.getByText(/Chamado!/)).toBeInTheDocument();
    // Reconexão com cursor vencido: snapshot novo manda.
    act(() =>
      es.emit(
        "stream.snapshot",
        envelope(
          "stream.snapshot",
          { type: "stream", id: EST, version: 9 },
          {
            reason: "cursor_expired",
            granted_channels: ["public", "visit"],
            denied_channels: [],
            public_queues: [],
            visit: visit("in_service", 3),
          },
        ),
        "c9",
      ),
    );
    expect(screen.getByText(/Em atendimento/)).toBeInTheDocument();
    expect(requests.filter((r) => r.path.endsWith("/my-visit"))).toHaveLength(1);
    expect(FakeES.all).toHaveLength(1); // uma conexão para a página inteira
  });
});
