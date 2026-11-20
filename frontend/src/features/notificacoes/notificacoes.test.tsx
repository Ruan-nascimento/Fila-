import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { StreamMessage } from "../../stream/client";
import { StreamContext } from "../../stream/context";
import { ConviteNotificacoes, NotificacoesProvider, Sino } from "./Notificacoes";
import { RegrasCliente, RegrasEquipe } from "./regras";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const Q = "0190a000-0000-7000-8000-0000000000f1";
const Q2 = "0190a000-0000-7000-8000-0000000000f2";
const P = "0190a000-0000-7000-8000-0000000000a1";

let seq = 0;
function msg(type: string, audience: string, data: unknown, id = P, version = 1): StreamMessage {
  return {
    schema_version: 1,
    event_id: `ev-${++seq}`,
    type,
    occurred_at: "2026-11-20T12:00:00Z",
    establishment_id: EST,
    audience,
    resource: { type: "x", id, version },
    data,
  } as unknown as StreamMessage;
}

const part = (o: Record<string, unknown> = {}) => ({
  id: P,
  establishment_id: EST,
  queue_id: Q,
  professional_id: "pro",
  ticket_code: "A003",
  status: "waiting",
  position: 3,
  nick: "Maria",
  version: 1,
  order_id: "ord",
  created_at: "2026-11-20T11:00:00Z",
  end_reason: null,
  called_at: null,
  tolerance_ends_at: null,
  ...o,
});
const order = (status: string, version: number) => ({ id: "ord", status, version });
const pub = (tickets: [string, string][], version: number) =>
  msg(
    "queue.public.updated",
    "public",
    {
      queue_id: Q,
      professional: { id: "pro", display_name: "Beto", photo: null },
      status: "open",
      waiting_count: tickets.length,
      tickets: tickets.map(([ticket_code, status]) => ({ ticket_code, status })),
      estimated_wait: { kind: "estimate", minutes: 10, computed_at: "" },
      version,
    },
    Q,
    version,
  );

function clienteComBase() {
  const r = new RegrasCliente();
  r.aplicar(
    msg("stream.snapshot", "visit", {
      reason: "initial",
      granted_channels: ["public", "visit", "account"],
      denied_channels: [],
      public_queues: [],
      visit: { participation: part(), order: order("open", 1) },
      loyalty: { program_enabled: true, balance: 20, held: 0, available: 20, active_reservation: null },
    }),
  );
  return r;
}
const titulos = (list: { title: string }[]) => list.map((n) => n.title);

describe("notificações do cliente", () => {
  // Cenários: C-WEB-05.
  it("avisa quando a fila anda, a vez chega, o atendimento começa e termina, sem repetir", () => {
    const r = clienteComBase();
    expect(
      r.aplicar(
        pub(
          [
            ["A001", "in_service"],
            ["A002", "waiting"],
            ["A004", "waiting"],
            ["A003", "waiting"],
          ],
          2,
        ),
      ),
    ).toEqual([]);
    // A002 foi chamada: falta 1 na frente.
    const andou = r.aplicar(
      pub(
        [
          ["A002", "called"],
          ["A004", "waiting"],
          ["A003", "waiting"],
        ],
        3,
      ),
    );
    expect(titulos(andou)).toEqual(["Falta 1 pessoa na sua frente"]);
    expect(andou[0]?.body).toContain("Beto");
    // Fila aumentou atrás: nada. Mesmo evento de novo (reconexão): nada.
    expect(
      r.aplicar(
        pub(
          [
            ["A002", "called"],
            ["A004", "waiting"],
            ["A003", "waiting"],
            ["A005", "waiting"],
          ],
          4,
        ),
      ),
    ).toEqual([]);
    expect(
      r.aplicar(
        pub(
          [
            ["A002", "called"],
            ["A004", "waiting"],
            ["A003", "waiting"],
          ],
          3,
        ),
      ),
    ).toEqual([]);
    expect(
      titulos(
        r.aplicar(
          pub(
            [
              ["A004", "called"],
              ["A003", "waiting"],
            ],
            5,
          ),
        ),
      ),
    ).toEqual(["Você é o próximo!"]);

    const chamada = r.aplicar(
      msg(
        "participation.updated",
        "visit",
        part({
          status: "called",
          version: 2,
          position: null,
          called_at: "c1",
          tolerance_ends_at: "2026-11-20T12:10:00Z",
        }),
        P,
        2,
      ),
    );
    expect(titulos(chamada)).toEqual(["Chegou a sua vez!"]);
    expect(chamada[0]?.important).toBe(true);
    // Mesmo evento repetido: não notifica de novo.
    expect(
      r.aplicar(
        msg("participation.updated", "visit", part({ status: "called", version: 2, called_at: "c1" }), P, 2),
      ),
    ).toEqual([]);
    expect(
      titulos(
        r.aplicar(msg("participation.updated", "visit", part({ status: "in_service", version: 3 }), P, 3)),
      ),
    ).toEqual(["Seu atendimento começou"]);
    expect(
      titulos(
        r.aplicar(msg("participation.updated", "visit", part({ status: "completed", version: 4 }), P, 4)),
      ),
    ).toEqual(["Seu atendimento terminou"]);
    expect(titulos(r.aplicar(msg("payment.updated", "visit", order("settled", 5), "ord", 5)))).toEqual([
      "Pagamento confirmado",
    ]);
    expect(
      titulos(
        r.aplicar(
          msg(
            "loyalty.updated",
            "account",
            { program_enabled: true, balance: 30, held: 0, available: 30, active_reservation: null },
            "acc",
            9,
          ),
        ),
      ),
    ).toEqual(["Você ganhou 10 pontos"]);
  });

  it("barbeiro desce a pessoa chamada, cancela ou decide o pedido de mudança", () => {
    const r = clienteComBase();
    r.aplicar(
      msg("participation.updated", "visit", part({ status: "called", version: 2, called_at: "c1" }), P, 2),
    );
    expect(
      titulos(
        r.aplicar(
          msg("participation.updated", "visit", part({ status: "waiting", version: 3, position: 1 }), P, 3),
        ),
      ),
    ).toEqual(["A barbearia chamou o próximo"]);
    const cr = {
      id: "cr1",
      status: "pending",
      add: [{ service_id: "s", name: "Barba", unit_price: {} }],
      remove_item_ids: [],
      created_at: "",
      decided_by_role: null,
      decision_reason: null,
    };
    expect(r.aplicar(msg("change_request.updated", "visit", cr, "cr1", 1))).toEqual([]);
    expect(
      titulos(
        r.aplicar(
          msg(
            "change_request.updated",
            "visit",
            { ...cr, status: "approved", decided_by_role: "professional" },
            "cr1",
            2,
          ),
        ),
      ),
    ).toEqual(["O barbeiro aceitou seu pedido de mudança"]);
    const cr2 = {
      ...cr,
      id: "cr2",
      status: "rejected",
      decided_by_role: "owner",
      decision_reason: "Sem horário",
    };
    const recusa = r.aplicar(msg("change_request.updated", "visit", cr2, "cr2", 2));
    expect(titulos(recusa)).toEqual(["O barbeiro recusou seu pedido de mudança"]);
    expect(recusa[0]?.body).toContain("Sem horário");
    expect(
      titulos(
        r.aplicar(
          msg(
            "participation.updated",
            "visit",
            part({ status: "cancelled", version: 4, end_reason: "Loja fechou" }),
            P,
            4,
          ),
        ),
      ),
    ).toEqual(["A barbearia cancelou sua senha"]);
  });

  it("o que a própria pessoa fez não notifica (desistir, avisar atraso)", () => {
    const r = clienteComBase();
    expect(
      r.aplicar(msg("participation.updated", "visit", part({ version: 2, late_notice_at: "x" }), P, 2)),
    ).toEqual([]);
    expect(
      r.aplicar(msg("participation.updated", "visit", part({ status: "cancelled", version: 3 }), P, 3)),
    ).toEqual([]);
  });
});

const entry = (id: string, nick: string, status: string, o: Record<string, unknown> = {}) => ({
  participation_id: id,
  order_id: `o-${id}`,
  ticket_code: `A${id}`,
  nick,
  status,
  service_names: ["Corte"],
  version: 1,
  has_pending_change_request: false,
  late_notice_at: null,
  ...o,
});
const staffQueue = (queue: string, nome: string, entries: unknown[], version = 1) => ({
  queue_id: queue,
  professional: { id: `p-${queue}`, display_name: nome, status: "active" },
  status: "open",
  version,
  unavailable_signal: { active: false },
  entries,
});

describe("notificações da equipe (barbeiro e dono)", () => {
  // Cenários: C-WEB-05.
  it("entrada, aviso de atraso, pedido de mudança e desistência; ações da equipe não notificam", () => {
    const r = new RegrasEquipe();
    r.aplicar(
      msg("stream.snapshot", "owner", {
        reason: "initial",
        granted_channels: ["owner"],
        denied_channels: [],
        staff_queues: [staffQueue(Q, "Beto", [entry("001", "Maria", "called")]), staffQueue(Q2, "Caio", [])],
      }),
    );
    const entrou = r.aplicar(
      msg(
        "queue.updated",
        "owner",
        staffQueue(Q, "Beto", [entry("001", "Maria", "called"), entry("002", "João", "waiting")]),
        Q,
        2,
      ),
    );
    expect(titulos(entrou)).toEqual(["Nova pessoa na fila"]);
    expect(entrou[0]?.body).toBe("João (A002) entrou na fila de Beto.");
    // Mesmo evento repetido (reconexão): nada.
    expect(
      r.aplicar(
        msg(
          "queue.updated",
          "owner",
          staffQueue(Q, "Beto", [entry("001", "Maria", "called"), entry("002", "João", "waiting")]),
          Q,
          2,
        ),
      ),
    ).toEqual([]);
    const atraso = r.aplicar(
      msg(
        "queue.updated",
        "owner",
        staffQueue(Q, "Beto", [
          entry("001", "Maria", "called", { late_notice_at: "2026-11-20T12:01:00Z" }),
          entry("002", "João", "waiting"),
        ]),
        Q,
        3,
      ),
    );
    expect(titulos(atraso)).toEqual(["Maria avisou que vai atrasar"]);
    expect(atraso[0]?.important).toBe(true);
    expect(
      titulos(
        r.aplicar(
          msg(
            "queue.updated",
            "owner",
            staffQueue(Q, "Beto", [
              entry("001", "Maria", "called", { late_notice_at: "2026-11-20T12:01:00Z" }),
              entry("002", "João", "waiting", { has_pending_change_request: true }),
            ]),
            Q,
            4,
          ),
        ),
      ),
    ).toEqual(["João pediu mudança na comanda"]);
    // A equipe iniciou o atendimento de Maria: sem notificação.
    expect(
      r.aplicar(
        msg(
          "queue.updated",
          "owner",
          staffQueue(Q, "Beto", [entry("001", "Maria", "in_service"), entry("002", "João", "waiting")]),
          Q,
          5,
        ),
      ),
    ).toEqual([]);
    // João desistiu (sem motivo); cancelamento pela loja (com motivo) não notifica a equipe.
    const saiu = r.aplicar(
      msg(
        "participation.updated",
        "owner",
        { ...part({ id: "002", nick: "João", ticket_code: "A002", status: "cancelled", version: 2 }) },
        "002",
        2,
      ),
    );
    expect(titulos(saiu)).toEqual(["Alguém saiu da fila"]);
    expect(saiu[0]?.body).toBe("João (A002) desistiu na fila de Beto.");
    expect(
      r.aplicar(
        msg(
          "participation.updated",
          "owner",
          part({ id: "009", status: "cancelled", version: 2, end_reason: "motivo" }),
          "009",
          2,
        ),
      ),
    ).toEqual([]);
  });
});

describe("tela de notificações", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  function Onde() {
    return <span data-testid="onde">{useLocation().pathname}</span>;
  }

  function renderCom(listeners: Set<(m: StreamMessage) => void>) {
    return render(
      <StreamContext.Provider
        value={{
          status: "live",
          subscribe: (l) => {
            listeners.add(l);
            return () => listeners.delete(l);
          },
        }}
      >
        <MemoryRouter initialEntries={["/gestao/x/comandas"]}>
          <NotificacoesProvider
            papel="equipe"
            escopo="equipe:x:conta"
            destino={(n) => (n.queueId ? `/gestao/x/filas/${n.queueId}` : "/gestao/x/filas")}
          >
            <Sino />
            <ConviteNotificacoes texto="Ative as notificações." />
          </NotificacoesProvider>
          <Onde />
        </MemoryRouter>
      </StreamContext.Provider>,
    );
  }

  it("aviso na tela, contador no sino e permissão pedida só pelo botão", async () => {
    const requestPermission = vi.fn(async () => "granted");
    const Fake = Object.assign(vi.fn(), { permission: "default", requestPermission });
    vi.stubGlobal("Notification", Fake);
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    const listeners = new Set<(m: StreamMessage) => void>();
    renderCom(listeners);
    expect(requestPermission).not.toHaveBeenCalled();
    const send = (m: StreamMessage) => listeners.forEach((l) => l(m));
    send(
      msg("stream.snapshot", "owner", {
        reason: "initial",
        granted_channels: ["owner"],
        denied_channels: [],
        staff_queues: [staffQueue(Q, "Beto", [])],
      }),
    );
    send(msg("queue.updated", "owner", staffQueue(Q, "Beto", [entry("002", "João", "waiting")]), Q, 2));
    expect(await screen.findByRole("status")).toHaveTextContent("João (A002) entrou na fila.");
    expect(screen.getByRole("button", { name: "Notificações: 1 nova" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Notificações: 1 nova" }));
    expect(screen.getByRole("region", { name: "Notificações" })).toHaveTextContent("Nova pessoa na fila");
    // Tocar no aviso leva à fila do barbeiro.
    fireEvent.click(
      within(screen.getByRole("region", { name: "Notificações" })).getByRole("button", {
        name: /Nova pessoa na fila/,
      }),
    );
    expect(screen.getByTestId("onde")).toHaveTextContent(`/gestao/x/filas/${Q}`);
    // Convite: só o toque pede a permissão.
    fireEvent.click(screen.getAllByRole("button", { name: "Ativar notificações" })[0]!);
    await waitFor(() => expect(requestPermission).toHaveBeenCalledTimes(1));
    expect(Fake).not.toHaveBeenCalled(); // olhando a página: aviso só na tela
  });

  it("com a página em segundo plano e permissão dada, vai para o sistema", () => {
    const Fake = Object.assign(vi.fn(), { permission: "granted", requestPermission: vi.fn() });
    vi.stubGlobal("Notification", Fake);
    vi.spyOn(document, "hasFocus").mockReturnValue(false);
    const listeners = new Set<(m: StreamMessage) => void>();
    renderCom(listeners);
    const send = (m: StreamMessage) => listeners.forEach((l) => l(m));
    send(
      msg("stream.snapshot", "owner", {
        reason: "initial",
        granted_channels: ["owner"],
        denied_channels: [],
        staff_queues: [staffQueue(Q, "Beto", [])],
      }),
    );
    send(msg("queue.updated", "owner", staffQueue(Q, "Beto", [entry("002", "João", "waiting")]), Q, 2));
    expect(Fake).toHaveBeenCalledWith(
      "Nova pessoa na fila",
      expect.objectContaining({ body: "João (A002) entrou na fila.", tag: expect.any(String) }),
    );
  });
});

describe("limpar e histórico", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  function Onde() {
    return <span data-testid="onde">{useLocation().pathname}</span>;
  }

  function montar(listeners: Set<(m: StreamMessage) => void>, escopo = "equipe:x:conta") {
    return render(
      <StreamContext.Provider
        value={{
          status: "live",
          subscribe: (l) => {
            listeners.add(l);
            return () => listeners.delete(l);
          },
        }}
      >
        <MemoryRouter initialEntries={["/gestao/x/comandas"]}>
          <NotificacoesProvider
            papel="equipe"
            escopo={escopo}
            destino={(n) => (n.queueId ? `/gestao/x/filas/${n.queueId}` : "/gestao/x/filas")}
          >
            <Sino />
          </NotificacoesProvider>
          <Onde />
        </MemoryRouter>
      </StreamContext.Provider>,
    );
  }

  it("“Limpar” esvazia o sino; o histórico mostra tudo, sobrevive a recarregar e leva ao lugar", async () => {
    vi.stubGlobal(
      "Notification",
      Object.assign(vi.fn(), { permission: "denied", requestPermission: vi.fn() }),
    );
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    const listeners = new Set<(m: StreamMessage) => void>();
    const send = (m: StreamMessage) => act(() => listeners.forEach((l) => l(m)));
    const { unmount } = montar(listeners);
    send(
      msg("stream.snapshot", "owner", {
        reason: "initial",
        granted_channels: ["owner"],
        denied_channels: [],
        staff_queues: [staffQueue(Q, "Beto", [])],
      }),
    );
    send(msg("queue.updated", "owner", staffQueue(Q, "Beto", [entry("002", "João", "waiting")]), Q, 2));
    send(
      msg(
        "queue.updated",
        "owner",
        staffQueue(Q, "Beto", [entry("002", "João", "waiting"), entry("003", "Lia", "waiting")]),
        Q,
        3,
      ),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Notificações: 2 novas" }));
    const painel = screen.getByRole("region", { name: "Notificações" });
    fireEvent.click(within(painel).getByRole("button", { name: "Limpar" }));
    expect(within(painel).getByText(/Nada por enquanto/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Notificações" })).toBeInTheDocument(); // sem contador

    // Recarregar a página (novo provedor): o histórico continua com as duas.
    unmount();
    listeners.clear();
    montar(listeners);
    fireEvent.click(screen.getByRole("button", { name: "Notificações" }));
    expect(
      within(screen.getByRole("region", { name: "Notificações" })).getByText(/Nada por enquanto/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Ver histórico (2)" }));
    const dialogo = screen.getByRole("dialog", { name: "Histórico de notificações" });
    expect(within(dialogo).getByText("Hoje")).toBeInTheDocument();
    expect(within(dialogo).getByText("João (A002) entrou na fila.")).toBeInTheDocument();
    expect(within(dialogo).getByText("Lia (A003) entrou na fila.")).toBeInTheDocument();
    // Busca.
    fireEvent.change(within(dialogo).getByLabelText("Buscar no histórico"), { target: { value: "lia" } });
    expect(within(dialogo).queryByText("João (A002) entrou na fila.")).toBeNull();
    // Tocar leva à fila e fecha o histórico.
    fireEvent.click(within(dialogo).getByRole("button", { name: /Lia \(A003\)/ }));
    expect(screen.getByTestId("onde")).toHaveTextContent(`/gestao/x/filas/${Q}`);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("apagar o histórico pede confirmação; outra pessoa no aparelho não vê o histórico", async () => {
    vi.stubGlobal(
      "Notification",
      Object.assign(vi.fn(), { permission: "denied", requestPermission: vi.fn() }),
    );
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    const listeners = new Set<(m: StreamMessage) => void>();
    const send = (m: StreamMessage) => act(() => listeners.forEach((l) => l(m)));
    const { unmount } = montar(listeners);
    send(
      msg("stream.snapshot", "owner", {
        reason: "initial",
        granted_channels: ["owner"],
        denied_channels: [],
        staff_queues: [staffQueue(Q, "Beto", [])],
      }),
    );
    send(msg("queue.updated", "owner", staffQueue(Q, "Beto", [entry("002", "João", "waiting")]), Q, 2));
    unmount();
    // Outra conta no mesmo computador: histórico próprio, vazio.
    const outro = montar(listeners, "equipe:x:outra-conta");
    fireEvent.click(screen.getByRole("button", { name: "Notificações" }));
    expect(screen.queryByRole("button", { name: /Ver histórico/ })).toBeNull();
    outro.unmount();
    montar(listeners);
    fireEvent.click(screen.getByRole("button", { name: "Notificações: 1 nova" }));
    fireEvent.click(screen.getByRole("button", { name: "Ver histórico (1)" }));
    const dialogo = screen.getByRole("dialog", { name: "Histórico de notificações" });
    fireEvent.click(within(dialogo).getByRole("button", { name: "Apagar histórico" }));
    expect(within(dialogo).getByText("Apagar todo o histórico deste aparelho?")).toBeInTheDocument();
    fireEvent.click(within(dialogo).getByRole("button", { name: "Apagar" }));
    expect(within(dialogo).getByText("Nenhuma notificação guardada neste aparelho.")).toBeInTheDocument();
  });
});
