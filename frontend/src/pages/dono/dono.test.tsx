import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { beforeEach, describe, expect, it } from "vitest";
import { setCsrfToken } from "../../api/client";
import { SessionProvider } from "../../session/session";
import { anonymousSession, fakeApi, problem } from "../../test/fake-api";
import { GestaoInicio, GestaoLoja } from "../gestao/Gestao";
import { AcessoDono } from "./Dono";
import { modoDono } from "./modo";

const LOJA_A = "0190a000-0000-7000-8000-0000000000a1";
const LOJA_B = "0190a000-0000-7000-8000-0000000000b1";
const PRO_B = "0190a000-0000-7000-8000-0000000000b2";

type Vinculo = {
  establishment_id: string;
  establishment_name: string;
  role: string;
  professional_id: string | null;
};

const donoA: Vinculo = {
  establishment_id: LOJA_A,
  establishment_name: "Loja A",
  role: "owner",
  professional_id: null,
};
const proB: Vinculo = {
  establishment_id: LOJA_B,
  establishment_name: "Loja B",
  role: "professional",
  professional_id: PRO_B,
};

function sessao(kind: "staff" | "customer", memberships: Vinculo[]) {
  return {
    data: {
      account: {
        id: "0190a000-0000-7000-8000-0000000000c1",
        name: "Ana",
        email: "ana@exemplo.com",
        status: "active",
      },
      session: { kind, expires_at: "2026-11-30T00:00:00Z", reauth_valid_until: null },
      visitors: [],
      memberships,
      csrf_token: "csrf-teste",
    },
  };
}

function Where() {
  return <output data-testid="endereco">{useLocation().pathname}</output>;
}

function abrir(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SessionProvider>
        <Routes>
          <Route path="/dono" element={<AcessoDono />} />
          <Route path="/gestao" element={<GestaoInicio />} />
          <Route path="/gestao/:estId/*" element={<GestaoLoja />} />
        </Routes>
        <Where />
      </SessionProvider>
    </MemoryRouter>,
  );
}

// A gestão da loja pede filas e afins; respostas vazias bastam para a moldura aparecer.
const gestaoVazia = (est: string) => ({
  [`GET /v1/establishments/${est}/queues`]: () => Response.json({ data: [] }),
  [`GET /v1/establishments/${est}/professionals`]: () => Response.json({ data: [] }),
  [`GET /v1/establishments/${est}/services`]: () => Response.json({ data: [] }),
});

beforeEach(() => {
  setCsrfToken(null);
  sessionStorage.clear();
});

describe("acesso do dono (/dono)", () => {
  it("sem sessão mostra o login de equipe, sem escolha de cliente, e liga o modo dono", async () => {
    fakeApi({ "GET /v1/session": () => Response.json(anonymousSession) });
    abrir("/dono");
    expect(await screen.findByRole("heading", { name: "Fila Gestão" })).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: /Cliente/ })).not.toBeInTheDocument();
    expect(screen.queryByText("Criar conta de cliente")).not.toBeInTheDocument();
    expect(modoDono()).toBe(true);
  });

  it("login envia contexto staff e o dono de uma loja vai direto ao painel dela", async () => {
    let logado = false;
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(logado ? sessao("staff", [donoA]) : anonymousSession),
      "POST /v1/auth/login": () => {
        logado = true;
        return Response.json({ data: {} });
      },
      ...gestaoVazia(LOJA_A),
    });
    abrir("/dono");
    fireEvent.change(await screen.findByLabelText("E-mail"), { target: { value: "ana@exemplo.com" } });
    fireEvent.change(screen.getByLabelText("Senha"), { target: { value: "senha-boa-123" } });
    fireEvent.click(screen.getByRole("button", { name: "Entrar" }));
    await waitFor(() => expect(screen.getByTestId("endereco")).toHaveTextContent(`/gestao/${LOJA_A}`));
    expect(requests.find((r) => r.path === "/v1/auth/login")?.body).toMatchObject({ context: "staff" });
  });

  it("dono de A e profissional de B: só A é oferecida (vai direto para A)", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(sessao("staff", [donoA, proB])),
      ...gestaoVazia(LOJA_A),
    });
    abrir("/dono");
    await waitFor(() => expect(screen.getByTestId("endereco")).toHaveTextContent(`/gestao/${LOJA_A}`));
    expect(screen.queryByText("Trocar de loja")).not.toBeInTheDocument();
  });

  it("dono de duas lojas escolhe entre elas; lojas em que é só profissional não aparecem", async () => {
    const donoB = { ...proB, role: "owner", professional_id: null };
    const loja3 = {
      ...proB,
      establishment_id: "0190a000-0000-7000-8000-0000000000d1",
      establishment_name: "Loja C",
    };
    fakeApi({ "GET /v1/session": () => Response.json(sessao("staff", [donoA, donoB, loja3])) });
    abrir("/dono");
    expect(await screen.findByRole("link", { name: /Loja A/ })).toHaveAttribute("href", `/gestao/${LOJA_A}`);
    expect(screen.getByRole("link", { name: /Loja B/ })).toBeInTheDocument();
    expect(screen.queryByText("Loja C")).not.toBeInTheDocument();
  });

  it("conta só profissional: mensagem clara e Trocar de conta sai e volta ao login", async () => {
    let logado = true;
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(logado ? sessao("staff", [proB]) : anonymousSession),
      "POST /v1/auth/logout": () => {
        logado = false;
        return new Response(null, { status: 204 });
      },
    });
    abrir("/dono");
    expect(await screen.findByText("Esta conta não é dona de nenhuma loja.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Loja B/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Trocar de conta" }));
    expect(await screen.findByLabelText("E-mail")).toBeInTheDocument();
    expect(requests.find((r) => r.path === "/v1/auth/logout")?.headers.get("X-CSRF-Token")).toBe(
      "csrf-teste",
    );
    expect(screen.getByTestId("endereco")).toHaveTextContent("/dono");
  });

  it("sessão de cliente não basta", async () => {
    fakeApi({ "GET /v1/session": () => Response.json(sessao("customer", [donoA])) });
    abrir("/dono");
    expect(await screen.findByText("Esta conta entrou como cliente.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Trocar de conta" })).toBeInTheDocument();
  });

  it("login recusado pelo servidor (sem vínculo de equipe) mostra o motivo", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/login": () =>
        problem(403, "forbidden", "Esta conta não tem acesso de equipe ativo. Entre como cliente."),
    });
    abrir("/dono");
    fireEvent.change(await screen.findByLabelText("E-mail"), { target: { value: "c@exemplo.com" } });
    fireEvent.change(screen.getByLabelText("Senha"), { target: { value: "senha-boa-123" } });
    fireEvent.click(screen.getByRole("button", { name: "Entrar" }));
    expect(await screen.findByText(/não tem acesso de equipe ativo/)).toBeInTheDocument();
  });
});

describe("modo dono na gestão", () => {
  it("endereço direto da loja em que é só profissional é recusado, sem abrir a fila", async () => {
    sessionStorage.setItem("fila.modo", "dono");
    const { requests } = fakeApi({ "GET /v1/session": () => Response.json(sessao("staff", [donoA, proB])) });
    abrir(`/gestao/${LOJA_B}/filas`);
    expect(await screen.findByText("Nesta janela só aparecem lojas em que você é dono.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Ver minhas lojas" })).toHaveAttribute("href", "/dono");
    expect(requests.some((r) => r.path.includes(LOJA_B))).toBe(false);
  });

  it("Trocar de loja (/gestao) leva à escolha do dono", async () => {
    sessionStorage.setItem("fila.modo", "dono");
    fakeApi({
      "GET /v1/session": () => Response.json(sessao("staff", [donoA, proB])),
      ...gestaoVazia(LOJA_A),
    });
    abrir("/gestao");
    await waitFor(() => expect(screen.getByTestId("endereco")).toHaveTextContent(`/gestao/${LOJA_A}`));
  });

  it("fora do modo dono, a profissional de B continua acessando B pelo navegador", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(sessao("staff", [donoA, proB])),
      ...gestaoVazia(LOJA_B),
    });
    abrir(`/gestao/${LOJA_B}/filas`);
    expect(await screen.findAllByText("Loja B")).not.toHaveLength(0);
    expect(screen.queryByText("Nesta janela só aparecem lojas em que você é dono.")).not.toBeInTheDocument();
  });
});
