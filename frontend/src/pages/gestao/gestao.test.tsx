import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it } from "vitest";
import { setCsrfToken } from "../../api/client";
import { centsToInput, formatCents, parseCents } from "../../money";
import { SessionProvider } from "../../session/session";
import { fakeApi, problem } from "../../test/fake-api";
import { GestaoLoja } from "./Gestao";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const PRO = "0190a000-0000-7000-8000-0000000000p1".replace("p", "a");
const QUEUE = "0190a000-0000-7000-8000-0000000000f1";

function staffSession(role: "owner" | "professional") {
  return {
    data: {
      account: {
        id: "0190a000-0000-7000-8000-0000000000c1",
        name: "Ana",
        email: "ana@exemplo.com",
        status: "active",
      },
      session: { kind: "staff", expires_at: "2026-11-13T00:00:00Z", reauth_valid_until: null },
      visitors: [],
      memberships: [
        {
          establishment_id: EST,
          establishment_name: "Barbearia Teste",
          role,
          professional_id: role === "professional" ? PRO : null,
        },
      ],
      csrf_token: "csrf-teste",
    },
  };
}

function renderGestao(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SessionProvider>
        <Routes>
          <Route path="/gestao/:estId/*" element={<GestaoLoja />} />
        </Routes>
      </SessionProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => setCsrfToken(null));

describe("dinheiro", () => {
  it("converte reais digitados em centavos inteiros, sem ponto flutuante", () => {
    expect(parseCents("45")).toBe(4500);
    expect(parseCents("45,5")).toBe(4550);
    expect(parseCents("45,50")).toBe(4550);
    expect(parseCents("R$ 0,10")).toBe(10);
    expect(parseCents("1.234,56")).toBe(123456);
    expect(parseCents("0,29")).toBe(29);
    for (const bad of ["", "abc", "-1", "45,555", "45.5", "1.23,00", "4 5"])
      expect(parseCents(bad)).toBeNull();
    expect(formatCents(4550)).toMatch(/R\$\s45,50/);
    expect(centsToInput(4505)).toBe("45,05");
  });
});

describe("Serviços", () => {
  it("dono cria serviço: preço vai em centavos inteiros com Idempotency-Key e CSRF", async () => {
    const created: unknown[] = [];
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(staffSession("owner")),
      [`GET /v1/establishments/${EST}/services`]: () => Response.json({ data: [] }),
      [`GET /v1/establishments/${EST}/professionals`]: () => Response.json({ data: [] }),
      [`POST /v1/establishments/${EST}/services`]: (_r, body) => {
        created.push(body);
        return Response.json({ data: {} }, { status: 201 });
      },
    });
    renderGestao(`/gestao/${EST}/servicos`);
    const form = await screen.findByRole("form", { name: "Novo serviço" });
    fireEvent.change(within(form).getByLabelText("Nome"), { target: { value: "Corte" } });
    fireEvent.change(within(form).getByLabelText(/Preço/), { target: { value: "45,50" } });
    fireEvent.change(within(form).getByLabelText(/Duração/), { target: { value: "30" } });
    fireEvent.click(within(form).getByRole("button", { name: "Criar serviço" }));
    await waitFor(() => expect(created).toHaveLength(1));
    expect(created[0]).toEqual({
      name: "Corte",
      price_cents: 4550,
      duration_minutes: 30,
      points: 0,
      professional_ids: [],
    });
    const post = requests.find((r) => r.method === "POST");
    expect(post?.headers.get("Idempotency-Key")).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
    expect(post?.headers.get("X-CSRF-Token")).toBe("csrf-teste");
  });

  it("preço inválido é recusado no formulário, sem enviar nada", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(staffSession("owner")),
      [`GET /v1/establishments/${EST}/services`]: () => Response.json({ data: [] }),
      [`GET /v1/establishments/${EST}/professionals`]: () => Response.json({ data: [] }),
    });
    renderGestao(`/gestao/${EST}/servicos`);
    const form = await screen.findByRole("form", { name: "Novo serviço" });
    fireEvent.change(within(form).getByLabelText(/Preço/), { target: { value: "45.5" } });
    fireEvent.change(within(form).getByLabelText(/Duração/), { target: { value: "30" } });
    fireEvent.click(within(form).getByRole("button", { name: "Criar serviço" }));
    expect(await within(form).findByText(/maior que zero/)).toBeInTheDocument();
    expect(requests.some((r) => r.method === "POST")).toBe(false);
  });

  it("profissional só lê: sem formulário de criação nem abas do dono", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(staffSession("professional")),
      [`GET /v1/establishments/${EST}/services`]: () =>
        Response.json({
          data: [
            {
              id: "s1",
              name: "Corte",
              price: { amount_cents: 4500, currency: "BRL" },
              duration_minutes: 30,
              points: 10,
              status: "active",
              professional_ids: [],
              version: 1,
            },
          ],
        }),
    });
    renderGestao(`/gestao/${EST}/servicos`);
    expect(await screen.findByText("Corte")).toBeInTheDocument();
    expect(screen.queryByRole("form", { name: "Novo serviço" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Profissionais" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Editar" })).toBeNull();
  });
});

describe("Filas", () => {
  const queue = (status: string, version: number) => ({
    queue_id: QUEUE,
    professional: { id: PRO, display_name: "Beto", status: "active" },
    status,
    version,
    unavailable_signal: { active: false, note: null, since: null },
    entries: [],
  });

  it("conflito de versão mostra o aviso e relê a fila", async () => {
    let current = queue("closed", 1);
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(staffSession("owner")),
      [`GET /v1/establishments/${EST}/queues`]: () => Response.json({ data: [current], cursor: null }),
      [`POST /v1/establishments/${EST}/queues/${QUEUE}/open`]: () => {
        current = queue("open", 2); // outra aba abriu antes
        return problem(409, "version_conflict", "Outra pessoa alterou este registro.", { current });
      },
    });
    renderGestao(`/gestao/${EST}/filas`);
    fireEvent.click(await screen.findByRole("button", { name: "Abrir" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Outra pessoa alterou");
    expect(await screen.findByRole("button", { name: "Pausar" })).toBeInTheDocument();
    const open = requests.find((r) => r.path.endsWith("/open"));
    expect(open?.body).toEqual({ expected_version: 1 });
  });
});

describe("Profissionais", () => {
  it("pede a senha quando o servidor exige reautenticação e repete com a mesma chave", async () => {
    const pro = {
      id: PRO,
      display_name: "Beto",
      queue_id: QUEUE,
      queue_letter: "A",
      status: "active",
      has_access: true,
      pending_invite: null,
      version: 1,
    };
    let attempts = 0;
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(staffSession("owner")),
      [`GET /v1/establishments/${EST}/professionals`]: () => Response.json({ data: [pro] }),
      [`POST /v1/establishments/${EST}/professionals/${PRO}/deactivate`]: () => {
        attempts++;
        if (attempts === 1) return problem(403, "reauthentication_required", "Digite sua senha de novo.");
        return Response.json({ data: { ...pro, status: "inactive", version: 2 } });
      },
      "POST /v1/auth/reauthenticate": () => Response.json(staffSession("owner")),
    });
    renderGestao(`/gestao/${EST}/profissionais`);
    fireEvent.click(await screen.findByRole("button", { name: "Desativar" }));
    fireEvent.change(screen.getByLabelText("Motivo"), { target: { value: "Saiu da equipe" } });
    fireEvent.click(screen.getByRole("button", { name: "Desativar" }));
    fireEvent.change(await screen.findByLabelText("Sua senha"), { target: { value: "senha-forte-123" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirmar e continuar" }));
    await waitFor(() => expect(attempts).toBe(2));
    const calls = requests.filter((r) => r.path.endsWith("/deactivate"));
    expect(calls[0]?.headers.get("Idempotency-Key")).toBe(calls[1]?.headers.get("Idempotency-Key"));
    expect(calls[1]?.body).toEqual({ expected_version: 1, reason: "Saiu da equipe" });
  });
});
