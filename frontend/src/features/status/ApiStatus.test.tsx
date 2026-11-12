import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiStatus } from "./ApiStatus";

function mockFetch(impl: (request: Request) => Promise<Response>) {
  const fn = vi.fn(impl);
  vi.stubGlobal("fetch", fn);
  return fn;
}

describe("ApiStatus", () => {
  it("mostra API pronta quando o readiness responde 200", async () => {
    const fetchMock = mockFetch(async () => Response.json({ status: "ok" }));
    render(<ApiStatus />);
    expect(await screen.findByText("API pronta")).toBeInTheDocument();
    const request = fetchMock.mock.calls[0]?.[0];
    expect(request).toBeDefined();
    expect(new URL(request!.url).pathname).toBe("/healthz/ready");
    expect(request!.credentials).toBe("same-origin");
  });

  it("explica quando a API não está pronta (503)", async () => {
    mockFetch(async () =>
      Response.json(
        { type: "/problems/service_unavailable", title: "x", status: 503, code: "service_unavailable" },
        { status: 503, headers: { "Content-Type": "application/problem+json" } },
      ),
    );
    render(<ApiStatus />);
    expect(await screen.findByText(/ainda não está pronta/)).toBeInTheDocument();
  });

  it("informa falta de conexão sem quebrar a tela", async () => {
    mockFetch(async () => {
      throw new TypeError("Failed to fetch");
    });
    render(<ApiStatus />);
    expect(await screen.findByRole("status")).toHaveTextContent("Sem conexão com a API");
  });
});
