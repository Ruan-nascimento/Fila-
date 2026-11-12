import createClient from "openapi-fetch";
import type { paths } from "./schema.gen";

// Cliente HTTP tipado a partir de docs/contrato/openapi.yaml (ADR-0008).
// - Mesma origem (o Vite em desenvolvimento e o proxy em produção repassam /v1 e /healthz).
// - Cookies de sessão são HttpOnly: o JavaScript nunca lê nem guarda tokens.
export const api = createClient<paths>({
  baseUrl: globalThis.location?.origin ?? "",
  credentials: "same-origin",
  // Resolve o fetch a cada chamada (não na importação), para respeitar substituições feitas depois.
  fetch: (request) => globalThis.fetch(request),
});
