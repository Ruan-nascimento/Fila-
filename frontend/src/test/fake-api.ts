import { vi } from "vitest";

type Handler = (request: Request, body: unknown) => Response | Promise<Response>;

/** Servidor falso: responde por "MÉTODO /caminho" e guarda os pedidos recebidos. */
export function fakeApi(routes: Record<string, Handler>) {
  const requests: { method: string; path: string; headers: Headers; body: unknown; url: string }[] = [];
  const fetchMock = vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    const text = request.method === "GET" ? "" : await request.text();
    const body = text ? (JSON.parse(text) as unknown) : undefined;
    requests.push({
      method: request.method,
      path: url.pathname,
      headers: request.headers,
      body,
      url: request.url,
    });
    const key = `${request.method} ${url.pathname}`;
    const handler = routes[key];
    if (!handler) return problem(404, "not_found", "Não encontrado");
    return handler(request, body);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { requests, fetchMock };
}

export function problem(status: number, code: string, detail: string, extra: Record<string, unknown> = {}) {
  return Response.json(
    { type: `/problems/${code}`, title: detail, status, code, detail, request_id: "req-teste", ...extra },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

export const anonymousSession = {
  data: { account: null, session: null, visitors: [], memberships: [], csrf_token: null },
};
