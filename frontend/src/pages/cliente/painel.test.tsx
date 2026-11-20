import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionProvider } from "../../session/session";
import { anonymousSession, fakeApi } from "../../test/fake-api";
import { espera } from "../../tempo";
import { distancia } from "./formato";
import { PainelCliente } from "./Painel";

const EST = "0190a000-0000-7000-8000-0000000000e1";

const item = (slug: string, name: string, o: Record<string, unknown> = {}) => ({
  id: `0190a000-0000-7000-8000-${slug.padStart(12, "0").slice(-12)}`,
  slug,
  name,
  palette: "blue",
  public_address: "Rua das Flores, 10",
  logo: null,
  distance_meters: null,
  open_queues: 1,
  waiting_count: 2,
  shortest_wait_minutes: 20,
  ...o,
});

const visitante = {
  data: {
    account: null,
    session: null,
    visitors: [{ id: "v1", establishment_id: EST, nick: "Lia" }],
    memberships: [],
    csrf_token: "c",
  },
};

function renderPainel() {
  return render(
    <MemoryRouter>
      <SessionProvider>
        <PainelCliente />
      </SessionProvider>
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("formato", () => {
  it("distância curta e legível", () => {
    expect(distancia(347)).toBe("350\u00a0m");
    expect(distancia(1240)).toBe("1,2\u00a0km");
    expect(distancia(85_300)).toBe("85\u00a0km");
  });
  it("espera em minutos ou horas", () => {
    expect(espera(40)).toBe("40\u00a0min");
    expect(espera(120)).toBe("2\u00a0h");
    expect(espera(152)).toBe("2\u00a0h 30\u00a0min");
  });
});

describe("Painel do cliente", () => {
  // Cenários: C-CLI-01.
  it("sem localização: abertas primeiro, busca e páginas; “Mais perto” pede a localização só no toque", async () => {
    const geo = {
      getCurrentPosition: vi.fn((ok: PositionCallback) =>
        ok({ coords: { latitude: -23.550523, longitude: -46.633312 } } as GeolocationPosition),
      ),
    };
    vi.stubGlobal("navigator", { ...navigator, geolocation: geo, permissions: undefined });
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/establishments": (r) => {
        const u = new URL(r.url);
        if (u.searchParams.get("cursor") === "p2")
          return Response.json({
            data: [item("c", "Corte Álvaro")],
            page: { next_cursor: null, has_more: false },
          });
        if (u.searchParams.get("sort") === "distance")
          return Response.json({
            data: [item("perto", "Barbearia Perto", { distance_meters: 420 })],
            page: { next_cursor: null, has_more: false },
          });
        return Response.json({
          data: [item("a", "Barbearia A"), item("b", "Barbearia B", { open_queues: 0 })],
          page: { next_cursor: "p2", has_more: true },
        });
      },
    });
    renderPainel();
    expect(await screen.findByText("Barbearia A")).toBeInTheDocument();
    expect(screen.getByText("Fechada agora")).toBeInTheDocument();
    expect(screen.getByText(/2 pessoas na fila · menor espera ~20\smin/)).toBeInTheDocument();
    // Sem pedir localização ao abrir.
    expect(geo.getCurrentPosition).not.toHaveBeenCalled();
    expect(requests.find((r) => r.path === "/v1/public/establishments")?.url).toContain("sort=open");
    // Página seguinte acrescenta, sem repetir.
    fireEvent.click(screen.getByRole("button", { name: "Ver mais barbearias" }));
    expect(await screen.findByText("Corte Álvaro")).toBeInTheDocument();
    expect(screen.getByText("Barbearia A")).toBeInTheDocument();
    // "Mais perto": pede a localização (arredondada) e ordena por distância.
    fireEvent.click(screen.getByRole("radio", { name: "Mais perto" }));
    expect(await screen.findByText("Barbearia Perto")).toBeInTheDocument();
    expect(screen.getByText("420 m")).toBeInTheDocument();
    const perto = requests.filter((r) => r.url.includes("sort=distance")).at(-1)!;
    expect(perto.url).toContain("lat=-23.551");
    expect(perto.url).toContain("lng=-46.633");
    // Busca com pausa ao digitar.
    fireEvent.change(screen.getByLabelText("Buscar por nome ou endereço"), { target: { value: "álvaro" } });
    await waitFor(() => expect(requests.some((r) => r.url.includes("q=%C3%A1lvaro"))).toBe(true));
  });

  it("localização negada: explica e mantém as abertas primeiro", async () => {
    const geo = {
      getCurrentPosition: vi.fn((_ok: PositionCallback, fail: PositionErrorCallback) =>
        fail({ code: 1 } as GeolocationPositionError),
      ),
    };
    vi.stubGlobal("navigator", { ...navigator, geolocation: geo, permissions: undefined });
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/establishments": () =>
        Response.json({ data: [item("a", "Barbearia A")], page: { next_cursor: null, has_more: false } }),
    });
    renderPainel();
    await screen.findByText("Barbearia A");
    fireEvent.click(screen.getByRole("radio", { name: "Mais perto" }));
    expect(await screen.findByText(/Sem acesso à localização/)).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Abertas agora" })).toBeChecked();
  });

  // Cenários: C-CLI-02.
  it("mostra as filas em que a pessoa está, a chamada em destaque e o link da loja", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(visitante),
      "GET /v1/public/establishments": () =>
        Response.json({ data: [], page: { next_cursor: null, has_more: false } }),
      "GET /v1/my-visits": () =>
        Response.json({
          data: [
            {
              establishment: {
                id: EST,
                slug: "barbearia-ze",
                name: "Barbearia do Zé",
                palette: "blue",
                logo: null,
              },
              professional_name: "Beto",
              visit: {
                participation: {
                  id: "p1",
                  status: "called",
                  ticket_code: "A007",
                  position: null,
                  tolerance_ends_at: "2026-11-20T15:10:00Z",
                },
                order: { status: "open" },
              },
            },
            {
              establishment: { id: "e2", slug: "salao", name: "Salão Paulista", palette: "blue", logo: null },
              professional_name: "Caio",
              visit: {
                participation: {
                  id: "p2",
                  status: "waiting",
                  ticket_code: "B003",
                  position: 3,
                  estimated_wait: { minutes: 40 },
                },
                order: { status: "open" },
              },
            },
          ],
        }),
    });
    renderPainel();
    const lista = await screen.findByRole("region", { name: "Minhas filas" });
    const zé = await within(lista).findByRole("link", { name: /Barbearia do Zé/ });
    expect(zé).toHaveAttribute("href", "/loja/barbearia-ze");
    expect(within(zé).getByText("Chegou a sua vez!")).toBeInTheDocument();
    const salao = within(lista).getByRole("link", { name: /Salão Paulista/ });
    expect(within(salao).getByText("3º na fila")).toBeInTheDocument();
    expect(within(salao).getByText(/~40\smin/)).toBeInTheDocument();
  });

  it("sem conta e sem fila: não consulta “minhas filas”", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/establishments": () =>
        Response.json({ data: [], page: { next_cursor: null, has_more: false } }),
    });
    renderPainel();
    expect(await screen.findByText("Nenhuma barbearia encontrada")).toBeInTheDocument();
    expect(requests.some((r) => r.path === "/v1/my-visits")).toBe(false);
  });
});
