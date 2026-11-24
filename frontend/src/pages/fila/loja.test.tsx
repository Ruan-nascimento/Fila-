// Regressões do redesign da loja pública: estados das filas, seleção integrada ao catálogo e
// escolha mantida durante a identificação do visitante.
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { describe, expect, it } from "vitest";
import { SessionProvider } from "../../session/session";
import { anonymousSession, fakeApi, problem } from "../../test/fake-api";
import { Loja } from "../Loja";
import { reconcile } from "./selecao";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const Q1 = "0190a000-0000-7000-8000-0000000000a1";
const Q2 = "0190a000-0000-7000-8000-0000000000a2";
const PRO1 = "0190a000-0000-7000-8000-0000000000b1";
const PRO2 = "0190a000-0000-7000-8000-0000000000b2";
const CORTE = "0190a000-0000-7000-8000-0000000000c1";
const BARBA = "0190a000-0000-7000-8000-0000000000c2";

const shop = { id: EST, slug: "barbearia-teste", name: "Barbearia Teste", timezone: "America/Sao_Paulo" };
const brl = (amount_cents: number) => ({ amount_cents, currency: "BRL" });
const estimate = { kind: "estimate", minutes: 20, computed_at: "2026-11-12T12:00:00Z" };
const queue = (id: string, pro: string, name: string, status = "open") => ({
  queue_id: id,
  professional: { id: pro, display_name: name },
  status,
  waiting_count: 1,
  tickets: [],
  estimated_wait: estimate,
  version: 1,
});
const services = [
  { id: CORTE, name: "Corte", price: brl(4500), duration_minutes: 30, professional_ids: [PRO1, PRO2] },
  { id: BARBA, name: "Barba", price: brl(3000), duration_minutes: 20, professional_ids: [PRO1] },
];

function renderLoja() {
  return render(
    <MemoryRouter initialEntries={["/loja/barbearia-teste"]}>
      <SessionProvider>
        <Routes>
          <Route path="/loja/:slug/*" element={<Loja />} />
        </Routes>
      </SessionProvider>
    </MemoryRouter>,
  );
}

describe("Loja: situação das filas", () => {
  it("catálogo carregado antes das filas não vira falso “sem filas”; falha oferece nova tentativa", async () => {
    let attempt = 0;
    let release: (r: Response) => void = () => {};
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/slugs/barbearia-teste": () => Response.json({ data: shop }),
      [`GET /v1/public/establishments/${EST}/services`]: () => Response.json({ data: services }),
      [`GET /v1/public/establishments/${EST}/queues`]: () => {
        attempt++;
        if (attempt === 1) return new Promise<Response>((r) => (release = r));
        return Response.json({ data: [queue(Q1, PRO1, "Beto")] });
      },
    });
    renderLoja();
    // Serviços já chegaram; filas ainda não: área de filas segue "carregando".
    expect(await screen.findByText("Corte")).toBeInTheDocument();
    expect(screen.getAllByText("Carregando filas…").length).toBeGreaterThan(0);
    expect(screen.queryByText(/Nenhuma fila aberta/)).toBeNull();
    // A leitura das filas falha: erro com recuperação, ainda sem afirmar que não há filas.
    release(problem(503, "service_unavailable", "Serviço indisponível."));
    expect(await screen.findByRole("button", { name: "Tentar de novo" })).toBeInTheDocument();
    expect(screen.queryByText(/Nenhuma fila aberta/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Tentar de novo" }));
    expect(await screen.findByLabelText(/Beto/)).toBeInTheDocument();
  });

  it("lista vazia confirmada pelo servidor mostra “Nenhuma fila aberta no momento”", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/slugs/barbearia-teste": () => Response.json({ data: shop }),
      [`GET /v1/public/establishments/${EST}/services`]: () => Response.json({ data: services }),
      [`GET /v1/public/establishments/${EST}/queues`]: () => Response.json({ data: [] }),
    });
    renderLoja();
    expect((await screen.findAllByText("Nenhuma fila aberta no momento")).length).toBeGreaterThan(0);
  });
});

describe("Loja: escolha e identificação", () => {
  it("a escolha feita antes do apelido é mantida e revalidada; só entra na fila depois de confirmar", async () => {
    let session: object = anonymousSession;
    const visitor = { id: "v1", establishment_id: EST, nick: "Lia", expires_at: "2026-11-19T12:00:00Z" };
    const posts: unknown[] = [];
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(session),
      "GET /v1/public/slugs/barbearia-teste": () => Response.json({ data: shop }),
      [`GET /v1/public/establishments/${EST}/services`]: () => Response.json({ data: services }),
      [`GET /v1/public/establishments/${EST}/queues`]: () =>
        Response.json({ data: [queue(Q1, PRO1, "Beto"), queue(Q2, PRO2, "Caio")] }),
      [`GET /v1/establishments/${EST}/my-visit`]: () => Response.json({ data: null }),
      [`POST /v1/establishments/${EST}/visitor-sessions`]: () => {
        session = { data: { ...anonymousSession.data, visitors: [visitor] } };
        return Response.json(
          {
            data: {
              visitor,
              recovery_code: "K7M2-9QXA-3HTZ-PW4C",
              recovery_code_valid_until: "2026-12-12T12:00:00Z",
            },
          },
          { status: 201 },
        );
      },
      [`POST /v1/establishments/${EST}/participations`]: (_r, body) => {
        posts.push(body);
        return problem(409, "queue_not_open", "A fila não está aberta.");
      },
    });
    renderLoja();
    fireEvent.click(await screen.findByLabelText(/Beto/));
    fireEvent.click(await screen.findByRole("button", { name: "Adicionar Barba" }));
    fireEvent.click(screen.getByRole("button", { name: "Adicionar Corte" }));
    expect(screen.getByText(/Total estimado · 2 serviços/)).toBeInTheDocument();
    expect(
      screen.getByText(
        "Este pedido ocupa uma posição na fila. Os serviços serão realizados pelo profissional escolhido, em sequência.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("R$ 75,00", { exact: false })).toBeInTheDocument();
    // Sem identificação: "Entrar na fila" leva ao apelido, sem criar participação.
    fireEvent.click(screen.getByRole("button", { name: "Entrar na fila" }));
    expect(screen.getByLabelText("Apelido")).toHaveFocus();
    expect(screen.getByText(/Sua escolha de profissional e serviços fica guardada/)).toBeInTheDocument();
    expect(posts).toHaveLength(0);
    fireEvent.change(screen.getByLabelText("Apelido"), { target: { value: "Lia" } });
    fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
    // Código de recuperação aparece e a escolha continua marcada.
    expect(await screen.findByText("K7M2-9QXA-3HTZ-PW4C")).toBeInTheDocument();
    expect(screen.getByText(/Sua escolha foi mantida/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText(/Beto/)).toBeChecked());
    expect(screen.getByRole("group", { name: "Quantidade de Barba" })).toBeInTheDocument();
    expect(screen.queryByText("Você entrou na fila.")).toBeNull();
    // Confirma: a resposta real (fila fechou) é mostrada; nada de "Você entrou na fila".
    fireEvent.click(screen.getByRole("button", { name: "Entrar na fila" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toEqual({
      queue_id: Q1,
      items: [
        { service_id: BARBA, quantity: 1 },
        { service_id: CORTE, quantity: 1 },
      ],
    });
    expect(await screen.findByText("A fila não está aberta.")).toBeInTheDocument();
    expect(screen.queryByText("Você entrou na fila.")).toBeNull();
    expect(requests.filter((r) => r.path.endsWith("/visitor-sessions"))).toHaveLength(1);
  });

  it("trocar de profissional tira na hora o serviço que ele não faz e atualiza o total", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/slugs/barbearia-teste": () => Response.json({ data: shop }),
      [`GET /v1/public/establishments/${EST}/services`]: () => Response.json({ data: services }),
      [`GET /v1/public/establishments/${EST}/queues`]: () =>
        Response.json({ data: [queue(Q1, PRO1, "Beto"), queue(Q2, PRO2, "Caio")] }),
    });
    renderLoja();
    fireEvent.click(await screen.findByLabelText(/Beto/));
    fireEvent.click(await screen.findByRole("button", { name: "Adicionar Barba" }));
    fireEvent.click(screen.getByRole("button", { name: "Adicionar Corte" }));
    fireEvent.click(screen.getByRole("button", { name: "Aumentar quantidade de corte" }));
    fireEvent.click(screen.getByLabelText(/Caio/));
    expect(await screen.findByText("Barba não é feito por Caio e saiu da seleção.")).toBeInTheDocument();
    const servicos = screen.getByRole("group", { name: "Serviços com Caio" });
    // A quantidade escolhida sobrevive à troca de profissional.
    expect(within(servicos).getByRole("group", { name: "Quantidade de Corte" })).toBeInTheDocument();
    expect(within(servicos).getByLabelText("Quantidade de Corte", { selector: "input" })).toHaveValue("2");
    expect(within(servicos).queryByText("Barba")).toBeNull();
    expect(screen.getByText(/Total estimado · 2 serviços /)).toBeInTheDocument();
  });

  it("fila pausada enquanto a tela está aberta sai da seleção com o motivo", () => {
    const sel = { queueId: Q1, serviceIds: [CORTE] };
    const paused = [queue(Q1, PRO1, "Beto", "paused"), queue(Q2, PRO2, "Caio")];
    const out = reconcile(sel, paused as never, services as never);
    expect(out.next).toEqual({ queueId: "", serviceIds: [CORTE] });
    expect(out.note).toBe("A fila de Beto foi pausada. Escolha outro profissional.");
    // Dados ainda desconhecidos não mexem na escolha.
    expect(reconcile(sel, null, services as never).next).toBe(sel);
  });
});

describe("Loja: endereço antigo", () => {
  it("link/QR com o nome antigo leva ao endereço atual e o topo mostra o nome da loja", async () => {
    const atual = { ...shop, slug: "barbearia-nova", name: "Barbearia Nova" };
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/slugs/barbearia-antiga": () => Response.json({ data: atual }),
      "GET /v1/public/slugs/barbearia-nova": () => Response.json({ data: atual }),
      [`GET /v1/public/establishments/${EST}/services`]: () => Response.json({ data: services }),
      [`GET /v1/public/establishments/${EST}/queues`]: () => Response.json({ data: [] }),
    });
    function Where() {
      const l = useLocation();
      return <output data-testid="endereco">{l.pathname + l.search}</output>;
    }
    render(
      <MemoryRouter initialEntries={["/loja/barbearia-antiga?entrada=visitante"]}>
        <SessionProvider>
          <Routes>
            <Route path="/loja/:slug/*" element={<Loja />} />
          </Routes>
          <Where />
        </SessionProvider>
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("endereco")).toHaveTextContent("/loja/barbearia-nova?entrada=visitante"),
    );
    const topo = await screen.findByRole("link", { name: /Barbearia Nova/ });
    expect(topo).toHaveAttribute("href", "/loja/barbearia-nova");
  });
});
