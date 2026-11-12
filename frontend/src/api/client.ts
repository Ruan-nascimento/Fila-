import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "./schema.gen";

// Cliente HTTP tipado a partir de docs/contrato/openapi.yaml (ADR-0008).
// - Mesma origem (o Vite em desenvolvimento e o proxy em produção repassam /v1 e /healthz).
// - Cookies de sessão são HttpOnly: o JavaScript nunca lê nem guarda tokens de sessão.
// - O token CSRF fica só em memória (variável abaixo), nunca em localStorage/sessionStorage
//   (R-SEG-02). Ele é obtido em GET /v1/session e enviado em todo método que altera dados.
let csrfToken: string | null = null;

export function setCsrfToken(token: string | null) {
  csrfToken = token;
}

const safeMethods = new Set(["GET", "HEAD", "OPTIONS"]);

export const csrfMiddleware: Middleware = {
  onRequest({ request }) {
    if (!safeMethods.has(request.method) && csrfToken) {
      request.headers.set("X-CSRF-Token", csrfToken);
    }
    return request;
  },
};

export const api = createClient<paths>({
  baseUrl: globalThis.location?.origin ?? "",
  credentials: "same-origin",
  // Resolve o fetch a cada chamada (não na importação), para respeitar substituições feitas depois.
  fetch: (request) => globalThis.fetch(request),
});
api.use(csrfMiddleware);
