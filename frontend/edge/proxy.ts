// Proxy de borda (Vercel Routing Middleware): encaminha /v1 e /healthz para a API Go no Render
// mantendo a MESMA origem para o navegador (cookies __Host-, CSRF e Origin continuam iguais).
//
// Segurança:
//   - O destino vem de FILA_API_ORIGIN, variável do PROJETO na Vercel (separada por ambiente:
//     Production aponta para a API de produção; Preview para homologação ou fica vazia).
//     Sem destino válido, responde 503: um preview nunca cai na produção por acidente.
//   - O IP do cliente é o que a VERCEL observou (x-real-ip, definido pela plataforma). Ele vai em
//     X-Fila-Client-Ip junto com o segredo compartilhado (FILA_PROXY_SHARED_SECRET). A API só
//     acredita nesse IP quando o segredo confere; quem chama a API direto não consegue forjá-lo.
//   - Cabeçalhos X-Fila-* enviados pelo próprio navegador são descartados.
//   - Nada aqui vai para o bundle do site (não é VITE_ e não é importado por src/).

export const HEADER_PROXY_AUTH = "x-fila-proxy-auth";
export const HEADER_CLIENT_IP = "x-fila-client-ip";

export interface ProxyEnv {
  apiOrigin?: string;
  secret?: string;
}

export type Upstream = { url: URL; headers: Headers } | { error: string };

export function parseApiOrigin(raw: string | undefined): URL | null {
  if (!raw) return null;
  let u: URL;
  try {
    u = new URL(raw.trim());
  } catch {
    return null;
  }
  if (
    u.protocol !== "https:" ||
    (u.pathname !== "/" && u.pathname !== "") ||
    u.search ||
    u.hash ||
    u.username
  ) {
    return null;
  }
  return u;
}

const IPV4 = /^(\d{1,3})(\.\d{1,3}){3}$/;
const IPV6 = /^[0-9a-fA-F:.]{2,45}$/;

function plausibleIP(v: string | null): string | null {
  if (!v) return null;
  const ip = v.trim();
  if (IPV4.test(ip) && ip.split(".").every((p) => Number(p) <= 255)) return ip;
  if (ip.includes(":") && IPV6.test(ip)) return ip;
  return null;
}

/** Monta o pedido para a API. `clientIP` é o IP que a Vercel informou (x-real-ip). */
export function buildUpstream(request: Request, env: ProxyEnv, clientIP: string | undefined): Upstream {
  const origin = parseApiOrigin(env.apiOrigin);
  if (!origin) return { error: "FILA_API_ORIGIN ausente ou inválida (use https://servico.onrender.com)" };
  const secret = env.secret ?? "";
  if (secret.length < 32) return { error: "FILA_PROXY_SHARED_SECRET ausente ou curta" };

  const incoming = new URL(request.url);
  if (!/^\/(v1|healthz)(\/|$)/.test(incoming.pathname)) return { error: "caminho fora do proxy" };
  const url = new URL(incoming.pathname + incoming.search, origin);

  const headers = new Headers(request.headers);
  for (const name of [...headers.keys()]) {
    if (name.toLowerCase().startsWith("x-fila-")) headers.delete(name);
  }
  headers.set(HEADER_PROXY_AUTH, secret);
  const ip = plausibleIP(clientIP ?? null);
  if (ip) headers.set(HEADER_CLIENT_IP, ip);
  return { url, headers };
}
