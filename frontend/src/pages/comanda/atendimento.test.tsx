import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi, problem } from "../../test/fake-api";
import { MinhaVisita } from "../fila/MinhaVisita";
import { Atendimento } from "../gestao/Atendimento";
import { Comandas } from "../gestao/Comandas";
import { ItensDaComanda } from "./comanda";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const Q1 = "0190a000-0000-7000-8000-0000000000a1";
const PRO1 = "0190a000-0000-7000-8000-0000000000b1";
const CORTE = "0190a000-0000-7000-8000-0000000000c1";
const BARBA = "0190a000-0000-7000-8000-0000000000c2";
const PART = "0190a000-0000-7000-8000-0000000000d1";
const ORDER = "0190a000-0000-7000-8000-0000000000f1";
const I_CORTE = "0190a000-0000-7000-8000-000000000011";
const I_BARBA = "0190a000-0000-7000-8000-000000000012";
const brl = (c: number) => ({ amount_cents: c, currency: "BRL" });

function item(id: string, service: string, name: string, price: number, extra: object = {}) {
  return {
    id,
    service_id: service,
    name,
    unit_price: brl(price),
    duration_minutes: 30,
    points: 0,
    status: "accepted",
    charged: false,
    added_at: "2026-11-12T12:00:00Z",
    performed_at: null,
    reported_outcome: null,
    outcome_reason: null,
    ...extra,
  };
}

function order(extra: object = {}) {
  return {
    id: ORDER,
    establishment_id: EST,
    participation_id: PART,
    status: "open",
    version: 3,
    items: [item(I_CORTE, CORTE, "Corte", 4500)],
    total: brl(4500),
    total_frozen_at: null,
    pending_change_request: null,
    change_requests: [],
    closure_reason: null,
    interrupted_at: null,
    review_note: null,
    adjustments: [],
    points_expected: null,
    payment: {
      method_choice: null,
      amounts: { due: brl(0), received: brl(0), conferred: brl(0), refunded: brl(0), outstanding: brl(0) },
      verifying: false,
      charges: [],
      receipts: [],
      refunds: [],
      open_issue_kinds: [],
    },
    created_at: "2026-11-12T12:00:00Z",
    closed_at: null,
    ...extra,
  };
}

function visit(status: string, o: object = order()) {
  return {
    participation: {
      id: PART,
      establishment_id: EST,
      queue_id: Q1,
      professional_id: PRO1,
      ticket_code: "A007",
      status,
      position: status === "waiting" ? 1 : null,
      estimated_wait: null,
      nick: "Lia",
      loyalty_eligible: false,
      called_at: null,
      tolerance_ends_at: null,
      started_at: null,
      finished_at: null,
      end_reason: null,
      transfers: [],
      version: 2,
      order_id: ORDER,
      created_at: "2026-11-12T12:00:00Z",
    },
    order: o,
  };
}

const services = {
  [`GET /v1/public/establishments/${EST}/services`]: () =>
    Response.json({
      data: [
        { id: CORTE, name: "Corte", price: brl(5000), duration_minutes: 30, professional_ids: [PRO1] },
        { id: BARBA, name: "Barba", price: brl(3000), duration_minutes: 20, professional_ids: [PRO1] },
      ],
    }),
};

describe("Comanda do cliente", () => {
  it("enquanto aguarda, troca serviços enviando só a lista; preço do item aceito é mantido", async () => {
    const bodies: unknown[] = [];
    fakeApi({
      ...services,
      [`GET /v1/establishments/${EST}/my-visit`]: () =>
        Response.json({ data: visit("waiting"), cursor: null }),
      [`PUT /v1/establishments/${EST}/orders/${ORDER}/items`]: (_r, body) => {
        bodies.push(body);
        return Response.json({
          data: order({
            version: 4,
            items: [item(I_CORTE, CORTE, "Corte", 4500), item(I_BARBA, BARBA, "Barba", 3000)],
            total: brl(7500),
          }),
        });
      },
    });
    render(<MinhaVisita shopId={EST} />);
    fireEvent.click(await screen.findByRole("button", { name: "Alterar serviços" }));
    const form = await screen.findByRole("form", { name: "Alterar serviços" });
    // O Corte aceito continua pelo preço aceito (4500), mesmo com o catálogo em 5000.
    expect(within(form).getByLabelText(/Corte — R\$\s*45,00/)).toBeChecked();
    fireEvent.click(within(form).getByLabelText(/Barba/));
    expect(within(form).getByText(/Novo total previsto: R\$\s*75,00/)).toBeInTheDocument();
    fireEvent.click(within(form).getByRole("button", { name: "Salvar serviços" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({ expected_version: 3, service_ids: [CORTE, BARBA] });
    expect(await screen.findByText("R$ 75,00", { exact: false })).toBeInTheDocument();
  });

  it("depois da chamada, pede alteração e vê que o total só muda com a aprovação", async () => {
    const bodies: unknown[] = [];
    const pending = {
      id: "cr1",
      status: "pending",
      add: [{ service_id: BARBA, name: "Barba", unit_price: brl(3000) }],
      remove_item_ids: [],
      created_at: "2026-11-12T12:00:00Z",
      decided_at: null,
      decided_by_role: null,
      decision_reason: null,
    };
    fakeApi({
      ...services,
      [`GET /v1/establishments/${EST}/my-visit`]: () =>
        Response.json({ data: visit("in_service"), cursor: null }),
      [`POST /v1/establishments/${EST}/orders/${ORDER}/change-requests`]: (_r, body) => {
        bodies.push(body);
        return Response.json(
          { data: order({ version: 4, pending_change_request: pending, change_requests: [pending] }) },
          { status: 201 },
        );
      },
    });
    render(<MinhaVisita shopId={EST} />);
    fireEvent.click(await screen.findByRole("button", { name: "Pedir alteração ao profissional" }));
    const form = await screen.findByRole("form", { name: "Pedir alteração" });
    fireEvent.click(await within(form).findByLabelText(/Barba/));
    fireEvent.click(within(form).getByRole("button", { name: "Enviar pedido" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({ expected_version: 3, add_service_ids: [BARBA], remove_item_ids: [] });
    expect(
      await screen.findByText(/Aguardando o profissional\. O total continua R\$\s*45,00/),
    ).toBeInTheDocument();
  });

  it("valor fechado é explicado item a item", () => {
    const frozen = order({
      status: "awaiting_payment",
      total_frozen_at: "2026-11-12T12:30:00Z",
      items: [
        item(I_CORTE, CORTE, "Corte", 4500, { status: "performed", charged: true }),
        item(I_BARBA, BARBA, "Barba", 3000, { status: "not_performed", outcome_reason: "cliente desistiu" }),
      ],
      total: brl(4500),
    });
    render(<ItensDaComanda order={frozen as never} />);
    expect(screen.getByText(/Barba · não realizado · motivo: cliente desistiu/)).toBeInTheDocument();
    expect(screen.getByText(/não cobrado/)).toBeInTheDocument();
    expect(screen.getByText("Total cobrado")).toBeInTheDocument();
  });
});

describe("Atendimento pela equipe", () => {
  const ctx = { estId: EST, estName: "Loja", isOwner: false, professionalId: PRO1 };
  const entry = {
    participation_id: PART,
    order_id: ORDER,
    ticket_code: "A007",
    nick: "Lia",
    status: "in_service",
    service_names: ["Corte", "Barba"],
    called_at: "2026-11-12T12:00:00Z",
    tolerance_ends_at: "2026-11-12T12:10:00Z",
    has_pending_change_request: false,
    version: 4,
  };
  const two = order({
    items: [item(I_CORTE, CORTE, "Corte", 4500), item(I_BARBA, BARBA, "Barba", 3000)],
    total: brl(7500),
  });

  it("concluir exige o resultado de cada serviço e motivo para o não realizado", async () => {
    const bodies: unknown[] = [];
    let done = 0;
    fakeApi({
      [`GET /v1/establishments/${EST}/orders/${ORDER}`]: () => Response.json({ data: two }),
      [`POST /v1/establishments/${EST}/participations/${PART}/complete`]: (_r, body) => {
        bodies.push(body);
        return Response.json({ data: visit("completed") });
      },
    });
    render(<Atendimento ctx={ctx} entry={entry as never} onDone={async () => void done++} />);
    fireEvent.click(await screen.findByRole("button", { name: "Concluir atendimento" }));
    const form = screen.getByRole("form", { name: "Concluir atendimento" });
    const [corte, barba] = within(form).getAllByRole("group");
    fireEvent.click(within(corte!).getByLabelText("realizado"));
    fireEvent.click(within(barba!).getByLabelText("não realizado"));
    fireEvent.click(within(form).getByRole("button", { name: "Confirmar conclusão" }));
    await new Promise((r) => setTimeout(r, 20));
    expect(bodies).toHaveLength(0); // falta o motivo da Barba
    fireEvent.change(within(form).getByLabelText(/Por que Barba/), { target: { value: "cliente desistiu" } });
    fireEvent.click(within(form).getByRole("button", { name: "Confirmar conclusão" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      expected_version: 4,
      order_expected_version: 3,
      items: [
        { item_id: I_CORTE, outcome: "performed" },
        { item_id: I_BARBA, outcome: "not_performed", reason: "cliente desistiu" },
      ],
    });
    await waitFor(() => expect(done).toBe(1));
  });

  it("com pedido pendente, concluir fica bloqueado; aprovar envia a versão da comanda", async () => {
    const bodies: unknown[] = [];
    const pending = {
      id: "cr1",
      status: "pending",
      add: [{ service_id: BARBA, name: "Barba", unit_price: brl(3000) }],
      remove_item_ids: [],
      created_at: "2026-11-12T12:00:00Z",
    };
    fakeApi({
      [`GET /v1/establishments/${EST}/orders/${ORDER}`]: () =>
        Response.json({ data: order({ pending_change_request: pending, change_requests: [pending] }) }),
      [`POST /v1/establishments/${EST}/orders/${ORDER}/change-requests/cr1/approve`]: (_r, body) => {
        bodies.push(body);
        return Response.json({ data: two });
      },
    });
    render(
      <Atendimento
        ctx={ctx}
        entry={{ ...entry, has_pending_change_request: true } as never}
        onDone={async () => {}}
      />,
    );
    expect(await screen.findByText(/incluir Barba \(R\$\s*30,00\)/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Concluir atendimento" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Aprovar pedido" }));
    await waitFor(() => expect(bodies).toEqual([{ expected_version: 3 }]));
    expect(await screen.findByText("R$ 75,00", { exact: false })).toBeInTheDocument();
  });

  it("conflito de versão relê a comanda em vez de sobrescrever", async () => {
    let reads = 0;
    fakeApi({
      [`GET /v1/establishments/${EST}/orders/${ORDER}`]: () => {
        reads++;
        return Response.json({ data: two });
      },
      [`POST /v1/establishments/${EST}/orders/${ORDER}/items/${I_CORTE}/mark-performed`]: () =>
        problem(409, "version_conflict", "Outra pessoa alterou esta comanda."),
    });
    render(<Atendimento ctx={ctx} entry={entry as never} onDone={async () => {}} />);
    fireEvent.click(await screen.findByRole("button", { name: "Marcar Corte como realizado" }));
    expect(await screen.findByText("Outra pessoa alterou esta comanda.")).toBeInTheDocument();
    await waitFor(() => expect(reads).toBe(2));
  });
});

describe("Comandas do dono", () => {
  const ctx = { estId: EST, estName: "Loja", isOwner: true, professionalId: null };
  const participation = {
    [`GET /v1/establishments/${EST}/participations/${PART}`]: () =>
      Response.json({ data: { ...visit("interrupted").participation, end_reason: "cliente precisou sair" } }),
  };

  it("revisão parte do que o profissional informou e cobra só o feito por inteiro", async () => {
    const bodies: unknown[] = [];
    const review = order({
      status: "pending_review",
      interrupted_at: "2026-11-12T12:30:00Z",
      items: [
        item(I_CORTE, CORTE, "Corte", 4500, { reported_outcome: "fully_performed" }),
        item(I_BARBA, BARBA, "Barba", 3000, { reported_outcome: "partially_performed" }),
      ],
      total: brl(7500),
    });
    const { requests } = fakeApi({
      ...participation,
      [`GET /v1/establishments/${EST}/orders`]: () =>
        Response.json({ data: [review], page: { next_cursor: null, has_more: false } }),
      [`POST /v1/establishments/${EST}/orders/${ORDER}/close-interruption`]: (_r, body) => {
        bodies.push(body);
        return Response.json({
          data: order({
            status: "awaiting_payment",
            total: brl(4500),
            total_frozen_at: "2026-11-12T13:00:00Z",
          }),
        });
      },
    });
    render(<Comandas ctx={ctx} />);
    expect(await screen.findByText(/motivo informado: cliente precisou sair/)).toBeInTheDocument();
    expect(requests[0]?.url).toContain("status=pending_review");
    const form = screen.getByRole("form", { name: "Revisar interrupção" });
    expect(within(form).getByText(/Valor depois da revisão:/).textContent).toMatch(/45,00/);
    fireEvent.change(within(form).getByLabelText("Nota da revisão"), { target: { value: "Corte inteiro" } });
    fireEvent.click(within(form).getByRole("button", { name: "Fechar revisão" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      expected_version: 3,
      review_note: "Corte inteiro",
      items: [
        { item_id: I_CORTE, outcome: "fully_performed" },
        { item_id: I_BARBA, outcome: "partially_performed" },
      ],
    });
  });

  it("correção pede a senha e repete a mesma intenção com a mesma chave", async () => {
    const keys: (string | null)[] = [];
    let reauthed = false;
    const closed = order({
      status: "awaiting_payment",
      total_frozen_at: "2026-11-12T12:30:00Z",
      items: [item(I_CORTE, CORTE, "Corte", 4500, { status: "performed", charged: true })],
    });
    fakeApi({
      ...participation,
      [`GET /v1/establishments/${EST}/orders`]: () =>
        Response.json({ data: [closed], page: { next_cursor: null, has_more: false } }),
      [`GET /v1/establishments/${EST}/services`]: () => Response.json({ data: [] }),
      "POST /v1/auth/reauthenticate": () => {
        reauthed = true;
        return Response.json({ data: {} });
      },
      [`POST /v1/establishments/${EST}/orders/${ORDER}/adjustments`]: (r) => {
        keys.push(r.headers.get("Idempotency-Key"));
        if (!reauthed) return problem(403, "reauthentication_required", "Confirme sua senha.");
        return Response.json({ data: { ...closed, status: "closed_no_charge", total: brl(0) } });
      },
    });
    render(<Comandas ctx={{ ...ctx }} />);
    fireEvent.change(await screen.findByLabelText("Mostrar"), { target: { value: "awaiting_payment" } });
    fireEvent.click(await screen.findByRole("button", { name: "Corrigir valor" }));
    const form = screen.getByRole("form", { name: "Corrigir valor" });
    fireEvent.click(within(form).getByLabelText(/Corte/));
    fireEvent.change(within(form).getByLabelText("Motivo da correção"), { target: { value: "cortesia" } });
    fireEvent.click(within(form).getByRole("button", { name: "Salvar correção" }));
    fireEvent.change(await screen.findByLabelText("Sua senha"), { target: { value: "senha-de-teste" } }); // gitleaks:allow
    fireEvent.click(screen.getByRole("button", { name: "Confirmar e continuar" }));
    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[0]).toBe(keys[1]);
    expect(await screen.findByText(/Encerrada sem cobrança/)).toBeInTheDocument();
  });
});
