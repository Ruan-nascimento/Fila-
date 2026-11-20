import { ipAddress, rewrite } from "@vercel/functions";
import { buildUpstream } from "./edge/proxy";

// Só a API passa pelo middleware; telas e arquivos estáticos são servidos direto pela Vercel.
export const config = { matcher: ["/v1/:path*", "/healthz/:path*"] };

export default function middleware(request: Request): Response {
  const up = buildUpstream(
    request,
    { apiOrigin: process.env.FILA_API_ORIGIN, secret: process.env.FILA_PROXY_SHARED_SECRET },
    ipAddress(request),
  );
  if ("error" in up) {
    // Sem detalhes da configuração na resposta pública; o motivo fica só no log da Vercel.
    console.error("proxy da API não configurado:", up.error);
    return new Response(
      JSON.stringify({
        type: "about:blank",
        title: "Serviço indisponível",
        status: 503,
        code: "proxy_not_configured",
      }),
      { status: 503, headers: { "content-type": "application/problem+json", "cache-control": "no-store" } },
    );
  }
  return rewrite(up.url, { request: { headers: up.headers } });
}
