import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fakeApi, problem } from "../../test/fake-api";
import { Premios } from "../gestao/Premios";
import { Pontos } from "./Pontos";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const REWARD = "0190a000-0000-7000-8000-0000000000d1";
const RES = "0190a000-0000-7000-8000-0000000000d9";
const owner = { estId: EST, estName: "Loja", isOwner: true, professionalId: null };
const pro = { estId: EST, estName: "Loja", isOwner: false, professionalId: "pro1" };

const reward = {
  id: REWARD,
  name: "Pomada",
  description: "Pomada modeladora",
  usage_instructions: "Peça na recepção. Não dá prioridade na fila.",
  cost_points: 10,
  stock: { kind: "limited", units_available: 2 },
  status: "active",
  version: 3,
};

function reservation(extra: object = {}) {
  return {
    id: RES,
    reward_id: REWARD,
    accepted_terms: {
      name: "Pomada",
      description: "Pomada modeladora",
      usage_instructions: "Peça na recepção.",
      cost_points: 10,
    },
    status: "active",
    holder_display_name: "Maria",
    created_at: "2026-11-12T12:00:00Z",
    expires_at: "2026-11-13T12:00:00Z",
    resolved_at: null,
    version: 1,
    ...extra,
  };
}

function summary(extra: object = {}) {
  return { program_enabled: true, balance: 15, held: 0, available: 15, active_reservation: null, ...extra };
}

const base = {
  [`GET /v1/establishments/${EST}/reward-catalog`]: () => Response.json({ data: [reward] }),
  [`GET /v1/me/loyalty/${EST}/ledger`]: () =>
    Response.json({
      data: [
        {
          id: "l1",
          kind: "credit",
          balance_delta: 15,
          held_delta: 0,
          occurred_at: "2026-11-12T11:00:00Z",
          source: { type: "order", id: "o1" },
          note: null,
        },
      ],
      page: { next_cursor: null, has_more: false },
    }),
};

describe("Pontos do cliente", () => {
  it("mostra saldo explicado pelo extrato e reserva enviando a versão do prêmio que a pessoa viu", async () => {
    const bodies: unknown[] = [];
    let current = summary();
    const { requests } = fakeApi({
      ...base,
      [`GET /v1/me/loyalty/${EST}`]: () => Response.json({ data: current }),
      [`POST /v1/establishments/${EST}/reward-reservations`]: (_r, body) => {
        bodies.push(body);
        current = summary({
          held: 10,
          available: 5,
          active_reservation: reservation({ holder_display_name: undefined }),
        });
        return Response.json({ data: reservation({ holder_display_name: undefined }) }, { status: 201 });
      },
    });
    render(<Pontos shopId={EST} />);
    const saldo = await screen.findByRole("list", { name: "Saldo" });
    expect(
      within(saldo)
        .getByText(/disponíveis/)
        .closest("li"),
    ).toHaveTextContent("15");
    expect(screen.getByRole("list", { name: "Extrato de pontos" })).toHaveTextContent(
      /Pontos ganhos no atendimento.*\+15/,
    );
    expect(screen.getByText(/não dá desconto na comanda nem prioridade na fila/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Reservar Pomada" }));
    await waitFor(() => expect(bodies).toEqual([{ reward_id: REWARD, expected_reward_version: 3 }]));
    expect(await screen.findByRole("status", { name: "Prêmio reservado" })).toHaveTextContent(
      /Pomada.*Válido até/,
    );
    expect(
      requests.find((r) => r.path.endsWith("/reward-reservations"))?.headers.get("Idempotency-Key"),
    ).toBeTruthy();
  });

  it("sem pontos suficientes, com reserva ativa ou programa desligado não deixa reservar", async () => {
    fakeApi({
      ...base,
      [`GET /v1/me/loyalty/${EST}`]: () => Response.json({ data: summary({ balance: 4, available: 4 }) }),
    });
    const { unmount } = render(<Pontos shopId={EST} />);
    expect(await screen.findByRole("button", { name: "Reservar Pomada" })).toBeDisabled();
    expect(screen.getByText("Faltam 6 pontos.")).toBeInTheDocument();
    unmount();
    fakeApi({
      ...base,
      [`GET /v1/me/loyalty/${EST}`]: () =>
        Response.json({ data: summary({ program_enabled: false, balance: -10, available: -10 }) }),
    });
    render(<Pontos shopId={EST} />);
    expect(await screen.findByText(/programa de pontos está desligado/)).toBeInTheDocument();
    expect(screen.getByText(/Não há cobrança em dinheiro/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reservar Pomada" })).toBeDisabled();
  });

  it("cancelar a reserva envia a versão; conflito relê o estado", async () => {
    const bodies: unknown[] = [];
    fakeApi({
      ...base,
      [`GET /v1/me/loyalty/${EST}`]: () =>
        Response.json({ data: summary({ held: 10, available: 5, active_reservation: reservation() }) }),
      [`POST /v1/establishments/${EST}/reward-reservations/${RES}/cancel`]: (_r, body) => {
        bodies.push(body);
        return problem(409, "invalid_transition", "Reserva já encerrada");
      },
    });
    render(<Pontos shopId={EST} />);
    fireEvent.click(await screen.findByRole("button", { name: /Cancelar reserva/ }));
    await waitFor(() => expect(bodies).toEqual([{ expected_version: 1 }]));
  });
});

describe("Prêmios na gestão", () => {
  it("profissional entrega a reserva ativa; não vê programa, catálogo nem correção", async () => {
    const bodies: unknown[] = [];
    fakeApi({
      [`GET /v1/establishments/${EST}/reward-reservations`]: () =>
        Response.json({
          data: [
            reservation(),
            reservation({ id: "outra", status: "delivered", holder_display_name: "João" }),
          ],
          page: { next_cursor: null, has_more: false },
        }),
      // Mesmo que o servidor respondesse, a tela da equipe não mostra configuração do dono.
      [`GET /v1/establishments/${EST}/loyalty-program`]: () =>
        Response.json({ data: { enabled: true, version: 2, updated_at: null } }),
      [`POST /v1/establishments/${EST}/reward-reservations/${RES}/deliver`]: (_r, body) => {
        bodies.push(body);
        return Response.json({ data: reservation({ status: "delivered", version: 2 }) });
      },
    });
    render(<Premios ctx={pro} />);
    expect(await screen.findByText(/Pomada — Maria/)).toBeInTheDocument();
    expect(screen.queryByText("Programa de pontos")).toBeNull();
    expect(screen.queryByRole("button", { name: "Corrigir entrega" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Novo prêmio" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Confirmar entrega de Pomada" }));
    await waitFor(() => expect(bodies).toEqual([{ expected_version: 1 }]));
    expect(screen.queryByRole("button", { name: "Corrigir entrega" })).toBeNull();
  });

  it("dono liga o programa, cria prêmio limitado e corrige entrega só com motivo", async () => {
    const bodies: Record<string, unknown[]> = { program: [], reward: [], revert: [] };
    fakeApi({
      [`GET /v1/establishments/${EST}/reward-reservations`]: () =>
        Response.json({
          data: [reservation({ status: "delivered" })],
          page: { next_cursor: null, has_more: false },
        }),
      [`GET /v1/establishments/${EST}/loyalty-program`]: () =>
        Response.json({ data: { enabled: false, version: 1, updated_at: null } }),
      [`PUT /v1/establishments/${EST}/loyalty-program`]: (_r, body) => {
        bodies.program?.push(body);
        return Response.json({ data: { enabled: true, version: 2, updated_at: "2026-11-12T12:00:00Z" } });
      },
      [`GET /v1/establishments/${EST}/rewards`]: () => Response.json({ data: [] }),
      [`POST /v1/establishments/${EST}/rewards`]: (_r, body) => {
        bodies.reward?.push(body);
        return Response.json({ data: reward }, { status: 201 });
      },
      [`POST /v1/establishments/${EST}/reward-reservations/${RES}/revert-delivery`]: (_r, body) => {
        bodies.revert?.push(body);
        return Response.json({ data: reservation({ status: "delivery_reverted", version: 3 }) });
      },
    });
    render(<Premios ctx={owner} />);
    fireEvent.click(await screen.findByRole("button", { name: "Ligar programa" }));
    expect(await screen.findByRole("button", { name: "Desligar programa" })).toBeInTheDocument();
    expect(bodies.program).toEqual([{ expected_version: 1, enabled: true }]);

    fireEvent.click(screen.getByRole("button", { name: "Novo prêmio" }));
    const form = screen.getByRole("form", { name: "Novo prêmio" });
    fireEvent.change(within(form).getByLabelText(/Nome/), { target: { value: "Pomada" } });
    fireEvent.change(within(form).getByLabelText(/Descrição/), { target: { value: "Pomada modeladora" } });
    fireEvent.change(within(form).getByLabelText(/Custo em pontos/), { target: { value: "0" } });
    expect(within(form).getByText(/1 ou mais/)).toBeInTheDocument();
    fireEvent.change(within(form).getByLabelText(/Custo em pontos/), { target: { value: "10" } });
    fireEvent.click(within(form).getByLabelText("Quantidade limitada"));
    fireEvent.change(within(form).getByLabelText(/Unidades disponíveis/), { target: { value: "2" } });
    fireEvent.click(within(form).getByRole("button", { name: "Criar prêmio" }));
    await waitFor(() => expect(bodies.reward).toHaveLength(1));
    expect(bodies.reward?.[0]).toMatchObject({
      name: "Pomada",
      cost_points: 10,
      stock: { kind: "limited", units_available: 2 },
    });

    fireEvent.click(await screen.findByRole("button", { name: "Corrigir entrega" }));
    fireEvent.click(screen.getByRole("button", { name: "Desfazer entrega" }));
    expect(bodies.revert).toHaveLength(0);
    fireEvent.change(screen.getByLabelText(/Motivo da correção/), {
      target: { value: "registrado por engano" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Desfazer entrega" }));
    await waitFor(() =>
      expect(bodies.revert).toEqual([{ expected_version: 1, reason: "registrado por engano" }]),
    );
  });
});
