import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi, problem } from "../../test/fake-api";
import { Entradas } from "../gestao/Entradas";
import { MinhaVisita } from "./MinhaVisita";
import { StoreDataProvider } from "./dados";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const Q1 = "0190a000-0000-7000-8000-0000000000a1";
const Q2 = "0190a000-0000-7000-8000-0000000000a2";
const PRO1 = "0190a000-0000-7000-8000-0000000000b1";
const PRO2 = "0190a000-0000-7000-8000-0000000000b2";
const SVC = "0190a000-0000-7000-8000-0000000000c1";
const PART = "0190a000-0000-7000-8000-0000000000d1";

function visit(status: string, version: number) {
  return {
    participation: {
      id: PART,
      establishment_id: EST,
      queue_id: Q1,
      professional_id: PRO1,
      ticket_code: "A007",
      status,
      position: status === "waiting" ? 3 : null,
      estimated_wait:
        status === "waiting" ? { kind: "estimate", minutes: 40, computed_at: "2026-11-12T12:00:00Z" } : null,
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
      created_at: "2026-11-12T12:00:00Z",
    },
    order: {
      id: "o1",
      establishment_id: EST,
      participation_id: PART,
      status: status === "cancelled" ? "closed_no_charge" : "open",
      version: 1,
      items: [
        {
          id: "i1",
          service_id: SVC,
          name: "Corte",
          unit_price: { amount_cents: 4500, currency: "BRL" },
          duration_minutes: 30,
          points: 0,
          status: "accepted",
          charged: false,
        },
      ],
      total: { amount_cents: 4500, currency: "BRL" },
      payment: {
        method_choice: null,
        amounts: {},
        verifying: false,
        charges: [],
        receipts: [],
        refunds: [],
        open_issue_kinds: [],
      },
      created_at: "2026-11-12T12:00:00Z",
    },
  };
}

const publicData = {
  [`GET /v1/public/establishments/${EST}/services`]: () =>
    Response.json({
      data: [
        {
          id: SVC,
          name: "Corte",
          price: { amount_cents: 4500, currency: "BRL" },
          duration_minutes: 30,
          professional_ids: [PRO1],
        },
      ],
    }),
  [`GET /v1/public/establishments/${EST}/queues`]: () =>
    Response.json({
      data: [
        {
          queue_id: Q1,
          professional: { id: PRO1, display_name: "Beto" },
          status: "open",
          waiting_count: 2,
          tickets: [],
          estimated_wait: { kind: "estimate", minutes: 40, computed_at: "2026-11-12T12:00:00Z" },
          version: 2,
        },
      ],
      cursor: null,
    }),
};

describe("Minha visita", () => {
  it("entra na fila enviando só fila e serviços, e mostra senha, posição e estimativa", async () => {
    const posts: unknown[] = [];
    const { requests } = fakeApi({
      ...publicData,
      [`GET /v1/establishments/${EST}/my-visit`]: () => Response.json({ data: null, cursor: null }),
      [`POST /v1/establishments/${EST}/participations`]: (_r, body) => {
        posts.push(body);
        return Response.json({ data: visit("waiting", 1) }, { status: 201 });
      },
    });
    render(
      <StoreDataProvider shopId={EST}>
        <MinhaVisita shopId={EST} />
      </StoreDataProvider>,
    );
    fireEvent.click(await screen.findByLabelText(/Beto/));
    fireEvent.click(await screen.findByRole("button", { name: "Adicionar Corte" }));
    fireEvent.click(screen.getByRole("button", { name: "Entrar na fila" }));
    expect(await screen.findByText("A007")).toBeInTheDocument();
    expect(posts).toEqual([{ queue_id: Q1, items: [{ service_id: SVC, quantity: 1 }] }]);
    // Só depois de a participação existir: "Você entrou na fila".
    expect(screen.getByText("Você entrou na fila.")).toBeInTheDocument();
    expect(screen.getByText(/você é o 3º da fila/)).toBeInTheDocument(); // frase para o leitor de tela
    expect(screen.getByText("3º")).toBeInTheDocument(); // destaque visual
    expect(screen.getByText("Espera (estimativa)")).toBeInTheDocument();
    const post = requests.find((r) => r.method === "POST");
    expect(post?.headers.get("Idempotency-Key")).toMatch(/^[A-Za-z0-9_-]{16,}$/);
  });

  it("produto é complemento: sem serviço não entra; com serviço, vai junto e a fila cheia é explicada", async () => {
    const posts: unknown[] = [];
    fakeApi({
      ...publicData,
      [`GET /v1/public/establishments/${EST}/products`]: () =>
        Response.json({
          data: [
            {
              id: "0190a000-0000-7000-8000-0000000000f1",
              name: "Pomada",
              description: null,
              category_id: null,
              price: { amount_cents: 2500, currency: "BRL" },
              image: null,
              display_order: 0,
            },
          ],
        }),
      [`GET /v1/establishments/${EST}/my-visit`]: () => Response.json({ data: null, cursor: null }),
      [`POST /v1/establishments/${EST}/participations`]: (_r, body) => {
        posts.push(body);
        return problem(
          409,
          "queue_full",
          "A fila está cheia no momento. Tente novamente quando uma vaga for liberada.",
        );
      },
    });
    render(
      <StoreDataProvider
        shopId={EST}
        ordering={{
          service_mode: "queue",
          max_service_quantity: 2,
          products_enabled: true,
          visitors_enabled: true,
          payment_methods: ["cash"],
        }}
      >
        <MinhaVisita shopId={EST} />
      </StoreDataProvider>,
    );
    fireEvent.click(await screen.findByLabelText(/Beto/));
    fireEvent.click(await screen.findByRole("button", { name: "Ver produtos (1)" }));
    fireEvent.click(screen.getByRole("button", { name: "Adicionar Pomada" }));
    fireEvent.click(screen.getByRole("button", { name: "Entrar na fila" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Escolha um profissional e pelo menos um serviço.",
    );
    expect(posts).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Adicionar Corte" }));
    fireEvent.click(screen.getByRole("button", { name: "Aumentar quantidade de corte" }));
    // Limite da loja (2): o botão para no limite.
    expect(screen.getByRole("button", { name: "Aumentar quantidade de corte" })).toBeDisabled();
    expect(screen.getByText(/Total estimado · 2 serviços · 1 produto · ~60 min/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Entrar na fila" }));
    expect(await screen.findByText(/A fila está cheia no momento/)).toBeInTheDocument();
    expect(posts).toEqual([
      {
        queue_id: Q1,
        items: [
          { service_id: SVC, quantity: 2 },
          { product_id: "0190a000-0000-7000-8000-0000000000f1", quantity: 1 },
        ],
      },
    ]);
  });

  it("sair da fila envia a versão esperada", async () => {
    const bodies: unknown[] = [];
    fakeApi({
      ...publicData,
      [`GET /v1/establishments/${EST}/my-visit`]: () =>
        Response.json({ data: visit("waiting", 4), cursor: null }),
      [`POST /v1/establishments/${EST}/participations/${PART}/cancel`]: (_r, body) => {
        bodies.push(body);
        return Response.json({ data: visit("cancelled", 5) });
      },
    });
    render(
      <StoreDataProvider shopId={EST}>
        <MinhaVisita shopId={EST} />
      </StoreDataProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Sair da fila" }));
    // A consequência aparece antes: nada é enviado sem confirmar.
    expect(screen.getByText(/Você perde a posição atual/)).toBeInTheDocument();
    expect(bodies).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Sim, sair da fila" }));
    expect(await screen.findByText("Você saiu da fila.")).toBeInTheDocument();
    expect(bodies).toEqual([{ expected_version: 4 }]);
  });
});

describe("Aviso de atraso", () => {
  // Cenários: C-FILA-15.
  it("cliente chamado avisa que vai atrasar; a tela confirma e não oferece de novo", async () => {
    const bodies: unknown[] = [];
    const chamada = visit("called", 6);
    fakeApi({
      ...publicData,
      [`GET /v1/establishments/${EST}/my-visit`]: () => Response.json({ data: chamada, cursor: null }),
      [`POST /v1/establishments/${EST}/participations/${PART}/late-notice`]: (_r, body) => {
        bodies.push(body);
        const v = visit("called", 7);
        return Response.json({
          data: { ...v, participation: { ...v.participation, late_notice_at: "2026-11-12T12:05:00Z" } },
        });
      },
    });
    render(
      <StoreDataProvider shopId={EST}>
        <MinhaVisita shopId={EST} />
      </StoreDataProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Vou me atrasar" }));
    expect(await screen.findByText(/Você avisou a barbearia que vai se atrasar/)).toBeInTheDocument();
    expect(bodies).toEqual([{ expected_version: 6 }]);
    expect(screen.queryByRole("button", { name: "Vou me atrasar" })).toBeNull();
  });
});

describe("Fila da equipe", () => {
  const ctx = { estId: EST, estName: "Loja", isOwner: true, professionalId: null };
  const tolerance = "2026-11-12T12:10:00Z";
  const queue = (entries: object[]) => ({
    queue_id: Q1,
    professional: { id: PRO1, display_name: "Beto", status: "active" },
    status: "open",
    version: 2,
    unavailable_signal: { active: false, note: null, since: null },
    entries,
  });
  const other = {
    ...queue([]),
    queue_id: Q2,
    professional: { id: PRO2, display_name: "Caio", status: "active" },
  };
  const called = {
    participation_id: PART,
    order_id: "o1",
    ticket_code: "A007",
    nick: "Lia",
    status: "called",
    service_names: ["Corte"],
    called_at: "2026-11-12T12:00:00Z",
    tolerance_ends_at: tolerance,
    has_pending_change_request: false,
    version: 2,
  };

  it("ausência só é oferecida depois da tolerância", () => {
    fakeApi({});
    const q = queue([called]);
    const { rerender } = render(
      <Entradas
        ctx={ctx}
        queue={q as never}
        queues={[q] as never}
        now={new Date("2026-11-12T12:09:59Z")}
        onDone={async () => {}}
      />,
    );
    expect(screen.getByRole("button", { name: "Marcar ausência" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Chamar próximo" })).toBeDisabled(); // cadeira ocupada
    rerender(
      <Entradas
        ctx={ctx}
        queue={q as never}
        queues={[q] as never}
        now={new Date(tolerance)}
        onDone={async () => {}}
      />,
    );
    expect(screen.getByRole("button", { name: "Marcar ausência" })).toBeEnabled();
  });

  it("transferência exige destino, motivo e concordância antes de enviar", async () => {
    const waiting = { ...called, status: "waiting", called_at: null, tolerance_ends_at: null };
    const bodies: unknown[] = [];
    fakeApi({
      [`POST /v1/establishments/${EST}/participations/${PART}/transfer`]: (_r, body) => {
        bodies.push(body);
        return Response.json({ data: {} });
      },
    });
    const q = queue([waiting]);
    render(
      <Entradas
        ctx={ctx}
        queue={q as never}
        queues={[q, other] as never}
        now={new Date()}
        onDone={async () => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Transferir" }));
    const form = screen.getByRole("form", { name: "Transferir participação" });
    fireEvent.change(within(form).getByLabelText("Fila de destino"), { target: { value: Q2 } });
    fireEvent.change(within(form).getByLabelText("Motivo"), { target: { value: "Equilibrar filas" } });
    fireEvent.click(within(form).getByRole("button", { name: "Confirmar transferência" }));
    await new Promise((r) => setTimeout(r, 20));
    expect(bodies).toHaveLength(0); // sem a concordância marcada, nada é enviado
    fireEvent.click(within(form).getByLabelText(/concordou/));
    fireEvent.click(within(form).getByRole("button", { name: "Confirmar transferência" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      expected_version: 2,
      target_queue_id: Q2,
      reason: "Equilibrar filas",
      customer_consent: { given: true, method: "in_person_verbal" },
    });
  });

  it("profissional de outra fila não vê ações", () => {
    fakeApi({});
    const q = queue([called]);
    render(
      <Entradas
        ctx={{ ...ctx, isOwner: false, professionalId: PRO2 }}
        queue={q as never}
        queues={[q] as never}
        now={new Date()}
        onDone={async () => {}}
      />,
    );
    expect(screen.queryByRole("button", { name: "Chamar próximo" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Iniciar atendimento" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Transferir" })).toBeNull();
  });
});
