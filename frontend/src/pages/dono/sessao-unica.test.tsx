import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { beforeEach, describe, expect, it } from "vitest";
import { setCsrfToken } from "../../api/client";
import { SessionProvider } from "../../session/session";
import { anonymousSession, fakeApi, problem } from "../../test/fake-api";
import { AvisosGerais } from "../../ui/estados";
import { GestaoLoja } from "../gestao/Gestao";
import { AcessoDono } from "./Dono";

const LOJA = "0190a000-0000-7000-8000-0000000000a1";
const conta = {
  id: "0190a000-0000-7000-8000-0000000000c1",
  name: "Ana",
  email: "ana@exemplo.com",
  status: "active",
};
const donoSessao = (authority = true) => ({
  data: {
    account: conta,
    session: {
      kind: "staff",
      expires_at: "2026-11-30T00:00:00Z",
      reauth_valid_until: null,
      owner_authority: authority,
    },
    visitors: [],
    memberships: [
      { establishment_id: LOJA, establishment_name: "Loja A", role: "owner", professional_id: null },
    ],
    csrf_token: "csrf",
  },
});
const conflito = {
  current: {
    challenge: "desafio-secreto-de-teste-1234567890",
    expires_at: new Date(Date.now() + 120_000).toISOString(),
    other_session: {
      channel: "desktop_authorized",
      installation_name: "PC do balcão",
      last_used_at: "2026-11-18T13:05:00Z",
    },
  },
};
const gestaoVazia = {
  [`GET /v1/establishments/${LOJA}/queues`]: () => Response.json({ data: [] }),
  [`GET /v1/establishments/${LOJA}/professionals`]: () => Response.json({ data: [] }),
  [`GET /v1/establishments/${LOJA}/services`]: () => Response.json({ data: [] }),
};

function Where() {
  return <output data-testid="endereco">{useLocation().pathname}</output>;
}

function abrir(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SessionProvider>
        <AvisosGerais />
        <Routes>
          <Route path="/dono" element={<AcessoDono />} />
          <Route path="/gestao/:estId/*" element={<GestaoLoja />} />
        </Routes>
        <Where />
      </SessionProvider>
    </MemoryRouter>,
  );
}

async function entrar() {
  fireEvent.change(await screen.findByLabelText("E-mail"), { target: { value: "ana@exemplo.com" } });
  fireEvent.change(screen.getByLabelText("Senha"), { target: { value: "senha-boa-123" } });
  fireEvent.click(screen.getByRole("button", { name: "Entrar" }));
}

beforeEach(() => {
  setCsrfToken(null);
  sessionStorage.clear();
});

describe("sessão única do dono", () => {
  it("conflito: mostra só o contexto confiável; cancelar mantém a outra sessão e não autentica", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "POST /v1/auth/login": () => problem(409, "owner_session_active", "Acesso aberto", conflito),
      "POST /v1/auth/owner-session/cancel": () => new Response(null, { status: 204 }),
    });
    abrir("/dono");
    await entrar();
    expect(
      await screen.findByRole("alertdialog", { name: /Acesso de dono aberto em outro lugar/ }),
    ).toBeInTheDocument();
    expect(screen.getByText(/no programa Fila Gestão \(“PC do balcão”\)/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancelar e manter a outra sessão" }));
    expect(await screen.findByText(/A outra sessão continua ativa/)).toBeInTheDocument();
    expect(requests.find((r) => r.path === "/v1/auth/owner-session/cancel")?.body).toEqual({
      challenge: conflito.current.challenge,
    });
    expect(requests.some((r) => r.path === "/v1/auth/owner-session/takeover")).toBe(false);
    // O desafio nunca vai para a URL.
    expect(screen.getByTestId("endereco")).toHaveTextContent("/dono");
  });

  it("confirmar transfere e abre a gestão; queda de rede oferece verificar de novo com o MESMO desafio", async () => {
    let logado = false;
    let tentativas = 0;
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(logado ? donoSessao() : anonymousSession),
      "POST /v1/auth/login": () => problem(409, "owner_session_active", "Acesso aberto", conflito),
      "POST /v1/auth/owner-session/takeover": () => {
        tentativas++;
        if (tentativas === 1) throw new TypeError("rede caiu");
        logado = true;
        return Response.json(donoSessao());
      },
      ...gestaoVazia,
    });
    abrir("/dono");
    await entrar();
    fireEvent.click(
      await screen.findByRole("button", { name: "Continuar neste aparelho e encerrar a outra sessão" }),
    );
    expect(await screen.findByText("Não sabemos se a troca foi concluída.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Verificar de novo" }));
    await waitFor(() => expect(screen.getByTestId("endereco")).toHaveTextContent(`/gestao/${LOJA}/filas`));
    const enviados = requests.filter((r) => r.path === "/v1/auth/owner-session/takeover").map((r) => r.body);
    expect(enviados).toEqual([
      { challenge: conflito.current.challenge },
      { challenge: conflito.current.challenge },
    ]);
  });

  it("sessão encerrada por outro aparelho: a gestão sai de cena e explica o motivo", async () => {
    fakeApi({
      "GET /v1/session": () =>
        Response.json({ data: { ...anonymousSession.data, ended_reason: "owner_session_replaced" } }),
    });
    sessionStorage.setItem("fila.modo", "dono");
    abrir(`/gestao/${LOJA}/filas`);
    expect(
      await screen.findByText("Sua sessão foi encerrada porque você entrou em outro aparelho."),
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("endereco")).toHaveTextContent("/dono"));
  });
});
