import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { SessionProvider } from "../../session/session";
import { anonymousSession, fakeApi, problem } from "../../test/fake-api";
import { Conta } from "../Conta";
import { Historico } from "../fila/Historico";
import { Exclusoes } from "../gestao/Exclusoes";
import { ConsultarExclusao } from "./Exclusao";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const PROTOCOL = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

const logged = {
  data: {
    account: {
      id: "0190a000-0000-7000-8000-0000000000c1",
      name: "Maria",
      email: "maria@exemplo.com",
      status: "active",
    },
    session: { kind: "customer", expires_at: "2026-12-13T00:00:00Z", reauth_valid_until: null },
    visitors: [],
    memberships: [],
    csrf_token: "csrf-teste",
  },
};

function request(extra: object = {}) {
  return {
    protocol: PROTOCOL,
    status: "processing",
    blockers: [],
    requested_at: "2026-11-13T12:00:00Z",
    completed_at: null,
    target_days: 30,
    ...extra,
  };
}

function renderConta() {
  return render(
    <MemoryRouter>
      <SessionProvider>
        <Conta />
      </SessionProvider>
    </MemoryRouter>,
  );
}

describe("Excluir conta", () => {
  it("mostra os efeitos, pede a senha, reautentica antes e exibe o protocolo uma vez", async () => {
    let loggedIn = true;
    const order: string[] = [];
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(loggedIn ? logged : anonymousSession),
      "POST /v1/auth/reauthenticate": () => {
        order.push("reauth");
        return Response.json({ data: {} });
      },
      "POST /v1/me/deletion-request": (_r, body) => {
        order.push("exclusao");
        expect(body).toEqual({ confirm_effects: true });
        loggedIn = false; // o servidor revoga as sessões
        return Response.json({ data: request({ blockers: ["service_in_progress"] }) }, { status: 202 });
      },
    });
    renderConta();
    fireEvent.click(await screen.findByRole("button", { name: "Quero excluir minha conta…" }));
    expect(screen.getByText(/Pontos e prêmios, quando existirem, são encerrados/)).toBeInTheDocument();
    expect(screen.getByText(/não é prazo legal/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Excluir minha conta" }));
    await new Promise((r) => setTimeout(r, 20));
    expect(order).toEqual([]); // sem marcar os efeitos e sem senha, nada é enviado
    fireEvent.click(screen.getByLabelText(/Li e entendi os efeitos/));
    fireEvent.change(screen.getByLabelText("Sua senha (confirmação)"), {
      target: { value: "senha-de-teste" },
    }); // gitleaks:allow
    fireEvent.click(screen.getByRole("button", { name: "Excluir minha conta" }));
    expect(await screen.findByText(PROTOCOL)).toBeInTheDocument();
    expect(order).toEqual(["reauth", "exclusao"]);
    expect(
      requests.find((r) => r.path === "/v1/me/deletion-request")?.headers.get("Idempotency-Key"),
    ).toMatch(/^[A-Za-z0-9_-]{16,}$/);
    expect(screen.getByText(/Em processamento/)).toBeInTheDocument();
    expect(screen.getByText(/Atendimento em andamento: a equipe da loja/)).toBeInTheDocument();
    // A sessão acabou, mas o resultado com o protocolo continua na tela.
    await waitFor(() => expect(requests.filter((r) => r.path === "/v1/session").length).toBeGreaterThan(1));
    expect(screen.getByText(PROTOCOL)).toBeInTheDocument();
  });

  it("dona única: mostra a orientação do servidor e não mostra protocolo", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(logged),
      "POST /v1/auth/reauthenticate": () => Response.json({ data: {} }),
      "POST /v1/me/deletion-request": () =>
        problem(
          409,
          "sole_owner",
          "Antes de excluir a conta, peça ao suporte para transferir a administração.",
        ),
    });
    renderConta();
    fireEvent.click(await screen.findByRole("button", { name: "Quero excluir minha conta…" }));
    fireEvent.click(screen.getByLabelText(/Li e entendi os efeitos/));
    fireEvent.change(screen.getByLabelText("Sua senha (confirmação)"), {
      target: { value: "senha-de-teste" },
    }); // gitleaks:allow
    fireEvent.click(screen.getByRole("button", { name: "Excluir minha conta" }));
    expect(await screen.findByText(/transferir a administração/)).toBeInTheDocument();
    expect(screen.queryByText(/Guarde o protocolo/)).toBeNull();
  });
});

describe("Consulta pelo protocolo", () => {
  it("envia o protocolo no corpo (nunca no endereço) e mostra a situação real", async () => {
    const { requests } = fakeApi({
      "POST /v1/deletion-requests/status": (_r, body) => {
        expect(body).toEqual({ protocol: PROTOCOL });
        return Response.json({ data: request({ protocol: "…567ABC", blockers: ["financial_pending"] }) });
      },
    });
    render(<ConsultarExclusao />);
    fireEvent.change(screen.getByLabelText("Protocolo"), { target: { value: PROTOCOL } });
    fireEvent.click(screen.getByRole("button", { name: "Consultar" }));
    expect(await screen.findByText(/Pagamento pendente/)).toBeInTheDocument();
    expect(screen.queryByText(/concluída/i)).toBeNull();
    expect(requests[0]?.url).not.toContain(PROTOCOL);
  });

  it("protocolo desconhecido", async () => {
    fakeApi({ "POST /v1/deletion-requests/status": () => problem(404, "not_found", "Não encontrado") });
    render(<ConsultarExclusao />);
    fireEvent.change(screen.getByLabelText("Protocolo"), { target: { value: PROTOCOL } });
    fireEvent.click(screen.getByRole("button", { name: "Consultar" }));
    expect(await screen.findByText(/Protocolo não encontrado/)).toBeInTheDocument();
  });
});

describe("Exclusões na gestão", () => {
  // Cenários: C-EXC-04.
  it("destaca o que está perto da meta de 30 dias, sem chamar de prazo legal", async () => {
    const old = new Date(Date.now() - 26 * 24 * 60 * 60 * 1000).toISOString();
    fakeApi({
      [`GET /v1/establishments/${EST}/deletion-requests`]: () =>
        Response.json({
          data: [
            request({
              protocol: "…XYZ234",
              requested_at: old,
              blockers: ["professional_queue_has_participants"],
            }),
          ],
          page: { next_cursor: null, has_more: false },
        }),
    });
    render(<Exclusoes ctx={{ estId: EST, estName: "Loja", isOwner: true, professionalId: null }} />);
    expect(await screen.findByText("…XYZ234")).toBeInTheDocument();
    expect(screen.getByText(/26 dias/)).toBeInTheDocument();
    expect(screen.getByText(/Perto da meta operacional de 30 dias/)).toBeInTheDocument();
    expect(screen.getByText(/o dono da loja transfere ou cancela/)).toBeInTheDocument();
    // "prazo legal" só aparece negado ("não é prazo legal").
    expect(document.body.textContent?.replaceAll("não é prazo legal", "")).not.toMatch(/prazo legal/);
  });
});

describe("Histórico", () => {
  it("lista as visitas da conta nesta loja e carrega mais pelo cursor", async () => {
    const entry = (id: string, name: string) => ({
      order_id: id,
      visited_at: "2026-11-12T12:00:00Z",
      professional_name: "Beto",
      items: [
        { name, unit_price: { amount_cents: 4500, currency: "BRL" }, charged: true },
        { name: "Barba", unit_price: { amount_cents: 3000, currency: "BRL" }, charged: false },
      ],
      total: { amount_cents: 4500, currency: "BRL" },
      order_status: "settled",
      participation_status: "completed",
      points_credited: 0,
    });
    const { requests } = fakeApi({
      "GET /v1/me/history": (r) => {
        const cursor = new URL(r.url).searchParams.get("cursor");
        return Response.json(
          cursor
            ? { data: [entry("o2", "Sobrancelha")], page: { next_cursor: null, has_more: false } }
            : { data: [entry("o1", "Corte")], page: { next_cursor: "c1", has_more: true } },
        );
      },
    });
    render(<Historico shopId={EST} />);
    expect(await screen.findByText("Corte")).toBeInTheDocument();
    expect(screen.getByText(/não cobrado/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Carregar mais" }));
    expect(await screen.findByText("Sobrancelha")).toBeInTheDocument();
    expect(requests[0]?.url).toContain(`establishment_id=${EST}`);
    expect(requests[1]?.url).toContain("cursor=c1");
    expect(screen.queryByRole("button", { name: "Carregar mais" })).toBeNull();
  });
});
