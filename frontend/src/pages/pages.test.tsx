import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, setCsrfToken } from "../api/client";
import { SessionProvider } from "../session/session";
import { anonymousSession, fakeApi, problem } from "../test/fake-api";
import { resetFragmentCache } from "../ui/fragment";
import { Entrar } from "./Entrar";
import { VerificarEmail } from "./Links";
import { Loja } from "./Loja";

function renderAt(path: string, element: React.ReactNode, routePath = path) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SessionProvider>
        <Routes>
          <Route path={routePath} element={element} />
          <Route path="/conta" element={<p>página da conta</p>} />
        </Routes>
      </SessionProvider>
    </MemoryRouter>,
  );
}

let storageWrites: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  setCsrfToken(null);
  resetFragmentCache();
  storageWrites = vi.spyOn(Storage.prototype, "setItem");
});
afterEach(() => {
  // R-SEG-02: nada de token (nem de nada) em localStorage/sessionStorage.
  expect(storageWrites).not.toHaveBeenCalled();
});

describe("cliente da API", () => {
  it("envia X-CSRF-Token só em métodos que alteram dados", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/logout": () => new Response(null, { status: 204 }),
    });
    setCsrfToken("token-csrf-teste");
    await api.GET("/v1/session");
    await api.POST("/v1/auth/logout");
    expect(requests[0]?.headers.get("X-CSRF-Token")).toBeNull();
    expect(requests[1]?.headers.get("X-CSRF-Token")).toBe("token-csrf-teste");
  });
});

describe("Entrar", () => {
  it("mostra o erro do servidor e oferece reenviar a confirmação", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/login": () => problem(403, "email_not_verified", "Confirme seu e-mail para entrar."),
    });
    renderAt("/entrar", <Entrar />);
    fireEvent.change(screen.getByLabelText("E-mail"), { target: { value: "ana@exemplo.com" } });
    fireEvent.change(screen.getByLabelText("Senha"), { target: { value: "senha-forte-123" } });
    fireEvent.click(screen.getByRole("button", { name: "Entrar" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Confirme seu e-mail para entrar.");
    expect(screen.getByRole("link", { name: /novo link de confirmação/ })).toBeInTheDocument();
  });

  it("mostra erros junto aos campos e o contexto escolhido vai no corpo", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/login": () =>
        problem(400, "validation_failed", "Confira os campos.", {
          errors: [{ field: "/email", code: "invalid_format" }],
        }),
    });
    renderAt("/entrar", <Entrar />);
    fireEvent.click(screen.getByLabelText("Equipe da barbearia"));
    fireEvent.click(screen.getByRole("button", { name: "Entrar" }));
    const email = screen.getByLabelText("E-mail");
    await waitFor(() => expect(email).toHaveAttribute("aria-invalid", "true"));
    expect(screen.getByText("Formato inválido.")).toBeInTheDocument();
    const login = requests.find((r) => r.path === "/v1/auth/login");
    expect(login?.body).toMatchObject({ context: "staff" });
  });

  it("após entrar, relê a sessão e vai para a conta", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/login": () => Response.json(anonymousSession),
    });
    renderAt("/entrar", <Entrar />);
    fireEvent.change(screen.getByLabelText("E-mail"), { target: { value: "ana@exemplo.com" } });
    fireEvent.change(screen.getByLabelText("Senha"), { target: { value: "senha-forte-123" } });
    fireEvent.click(screen.getByRole("button", { name: "Entrar" }));
    expect(await screen.findByText("página da conta")).toBeInTheDocument();
    expect(requests.filter((r) => r.path === "/v1/session").length).toBeGreaterThanOrEqual(2);
  });
});

describe("link de verificação de e-mail", () => {
  it("lê o token do fragmento, apaga da barra de endereço e envia no corpo", async () => {
    const token = "A".repeat(43);
    window.history.replaceState(null, "", `/verificar-email#token=${token}`);
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/email-verification/confirm": () => new Response(null, { status: 204 }),
    });
    renderAt("/verificar-email", <VerificarEmail />);
    expect(await screen.findByText(/E-mail confirmado/)).toBeInTheDocument();
    expect(window.location.hash).toBe("");
    const confirm = requests.filter((r) => r.path === "/v1/auth/email-verification/confirm");
    expect(confirm).toHaveLength(1);
    expect(confirm[0]?.body).toEqual({ token });
    expect(confirm[0]?.url).not.toContain(token);
  });

  it("explica link vencido sem detalhes técnicos", async () => {
    window.history.replaceState(null, "", `/verificar-email#token=${"B".repeat(43)}`);
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/email-verification/confirm": () =>
        problem(409, "token_invalid", "Este link já foi usado, venceu ou não existe."),
    });
    renderAt("/verificar-email", <VerificarEmail />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Este link já foi usado");
  });
});

describe("Loja (visitante)", () => {
  const publicCatalog = {
    "GET /v1/public/establishments/0190a000-0000-7000-8000-000000000001/services": () =>
      Response.json({ data: [] }),
    "GET /v1/public/establishments/0190a000-0000-7000-8000-000000000001/queues": () =>
      Response.json({ data: [], cursor: null }),
  };
  const shop = {
    id: "0190a000-0000-7000-8000-000000000001",
    slug: "barbearia-teste",
    name: "Barbearia Teste",
    timezone: "America/Sao_Paulo",
  };
  const created = {
    data: {
      visitor: {
        id: "0190a000-0000-7000-8000-000000000002",
        establishment_id: shop.id,
        nick: "<b>Lia</b>",
        expires_at: "2026-11-19T12:00:00Z",
      },
      recovery_code: "K7M2-9QXA-3HTZ-PW4C",
      recovery_code_valid_until: "2026-12-12T12:00:00Z",
    },
  };

  it("cria visitante, mostra o código uma vez e trata o nick como texto", async () => {
    let session = anonymousSession;
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(session),
      "GET /v1/public/slugs/barbearia-teste": () => Response.json({ data: shop }),
      ...publicCatalog,
      [`POST /v1/establishments/${shop.id}/visitor-sessions`]: () => {
        session = { data: { ...anonymousSession.data, visitors: [created.data.visitor] as never[] } };
        return Response.json(created, { status: 201 });
      },
    });
    renderAt("/loja/barbearia-teste", <Loja />, "/loja/:slug");
    fireEvent.change(await screen.findByLabelText("Como quer ser chamado"), {
      target: { value: "<b>Lia</b>" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
    expect(await screen.findByText("K7M2-9QXA-3HTZ-PW4C")).toBeInTheDocument();
    expect(await screen.findByText("<b>Lia</b>")).toBeInTheDocument(); // literal, não HTML
    expect(document.querySelector("b")).toBeNull();
    const post = requests.find((r) => r.method === "POST");
    expect(post?.body).toEqual({ nick: "<b>Lia</b>" });
  });

  it("recupera com o código no corpo (nunca na URL)", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/slugs/barbearia-teste": () => Response.json({ data: shop }),
      ...publicCatalog,
      [`POST /v1/establishments/${shop.id}/visitor-sessions/recover`]: () =>
        problem(401, "recovery_code_invalid", "Código incorreto, já usado ou vencido."),
    });
    renderAt("/loja/barbearia-teste", <Loja />, "/loja/:slug");
    fireEvent.change(await screen.findByLabelText("Código de recuperação"), {
      target: { value: "AAAA-BBBB-CCCC-DDDD" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Recuperar" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Código incorreto");
    const rec = requests.find((r) => r.path.endsWith("/recover"));
    expect(rec?.body).toEqual({ recovery_code: "AAAA-BBBB-CCCC-DDDD" });
    expect(rec?.url).not.toContain("AAAA");
  });

  it("loja inexistente mostra mensagem clara", async () => {
    fakeApi({ "GET /v1/session": () => Response.json(anonymousSession) });
    renderAt("/loja/nao-existe", <Loja />, "/loja/:slug");
    expect(await screen.findByRole("alert")).toHaveTextContent("Loja não encontrada");
  });
});
