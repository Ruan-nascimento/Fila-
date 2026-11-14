import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";
import { SessionProvider } from "../session/session";
import { anonymousSession, fakeApi, problem } from "../test/fake-api";
import { AvisoSemInternet, AvisoSessaoEncerrada } from "../ui/estados";
import { Conta } from "./Conta";
import { Auditoria } from "./gestao/Auditoria";

const EST = "0190a000-0000-7000-8000-0000000000e1";

function logged(name = "Maria") {
  return {
    data: {
      account: { id: "a1", name, email: "maria@exemplo.com", status: "active" },
      session: { kind: "customer", expires_at: "2026-11-20T12:00:00Z", reauth_valid_until: null },
      visitors: [],
      memberships: [],
      csrf_token: "csrf",
    },
  };
}

function renderConta() {
  return render(
    <MemoryRouter>
      <SessionProvider>
        <AvisoSessaoEncerrada />
        <Conta />
      </SessionProvider>
    </MemoryRouter>,
  );
}

describe("Minha conta", () => {
  it("salva o nome com chave de idempotência e relê a sessão", async () => {
    let name = "Maria";
    const bodies: unknown[] = [];
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(logged(name)),
      "PATCH /v1/me": (_r, body) => {
        bodies.push(body);
        name = "Maria Souza";
        return Response.json({ data: { id: "a1", name, email: "maria@exemplo.com", status: "active" } });
      },
    });
    renderConta();
    const field = await screen.findByLabelText("Nome exibido");
    fireEvent.change(field, { target: { value: "  Maria Souza " } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar nome" }));
    expect(await screen.findByText("Nome atualizado.")).toBeInTheDocument();
    expect(bodies).toEqual([{ name: "Maria Souza" }]);
    expect(requests.find((r) => r.method === "PATCH")?.headers.get("Idempotency-Key")).toBeTruthy();
    await waitFor(() => expect(screen.getByText("Maria Souza", { selector: "dd" })).toBeInTheDocument());
  });

  it("sessão encerrada em outro aparelho: explica no topo, não aplica e oferece entrar de novo", async () => {
    let session: object = logged();
    fakeApi({
      "GET /v1/session": () => Response.json(session),
      "PATCH /v1/me": () => {
        session = anonymousSession;
        return problem(401, "unauthenticated", "Sua sessão não é válida ou expirou.");
      },
    });
    renderConta();
    fireEvent.change(await screen.findByLabelText("Nome exibido"), { target: { value: "Outra" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar nome" }));
    // A tela da conta passa a "não conectado", mas o aviso continua visível.
    expect(await screen.findByText(/Você não está conectado/)).toBeInTheDocument();
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/Sua sessão terminou e a ação não foi feita/);
    expect(within(alert).getByRole("link", { name: "Entre de novo" })).toHaveAttribute("href", "/entrar");
  });
});

describe("Avisos gerais", () => {
  it("sem internet: aviso aparece e some com a rede", () => {
    render(<AvisoSemInternet />);
    expect(screen.queryByText(/Sem internet/)).toBeNull();
    act(() => {
      window.dispatchEvent(new Event("offline"));
    });
    expect(screen.getByRole("alert")).toHaveTextContent(/não será aplicada duas vezes/);
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    expect(screen.queryByText(/Sem internet/)).toBeNull();
  });
});

describe("Auditoria", () => {
  it("lista sem dados pessoais, com tipo do ator, alvo traduzido e paginação", async () => {
    const ev = (id: string, action: string) => ({
      id,
      occurred_at: "2026-11-13T12:00:00Z",
      actor: { kind: "owner", id: "x" },
      action,
      target: { type: "invite", id: "i1" },
      from_state: "pending",
      to_state: "revoked",
      reason: null,
      request_id: null,
    });
    const { requests } = fakeApi({
      [`GET /v1/establishments/${EST}/audit-events`]: (req) =>
        new URL(req.url).searchParams.get("cursor")
          ? Response.json({ data: [ev("e2", "T-IN-01")], page: { next_cursor: null, has_more: false } })
          : Response.json({ data: [ev("e1", "T-IN-03")], page: { next_cursor: "cur1", has_more: true } }),
    });
    render(<Auditoria ctx={{ estId: EST, estName: "Loja", isOwner: true, professionalId: null }} />);
    expect(await screen.findByText("T-IN-03")).toBeInTheDocument();
    expect(screen.getByText(/convite · Dono/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Carregar mais" }));
    expect(await screen.findByText("T-IN-01")).toBeInTheDocument();
    expect(requests.at(-1)?.url).toContain("cursor=cur1");
    expect(screen.queryByRole("button", { name: "Carregar mais" })).toBeNull();
  });
});
