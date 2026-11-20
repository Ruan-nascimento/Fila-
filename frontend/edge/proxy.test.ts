import { describe, expect, it } from "vitest";
import { buildUpstream, parseApiOrigin } from "./proxy";

const env = { apiOrigin: "https://fila-api-hml.onrender.com", secret: "s".repeat(40) };

describe("proxy de borda", () => {
  it("encaminha /v1 com caminho, query, segredo e IP do cliente", () => {
    const req = new Request("https://teste.exemplo.com/v1/stream/x?cursor=9", {
      headers: { cookie: "__Host-s=1", origin: "https://teste.exemplo.com" },
    });
    const up = buildUpstream(req, env, "203.0.113.9");
    if ("error" in up) throw new Error(up.error);
    expect(up.url.toString()).toBe("https://fila-api-hml.onrender.com/v1/stream/x?cursor=9");
    expect(up.headers.get("x-fila-proxy-auth")).toBe(env.secret);
    expect(up.headers.get("x-fila-client-ip")).toBe("203.0.113.9");
    expect(up.headers.get("cookie")).toBe("__Host-s=1");
    expect(up.headers.get("origin")).toBe("https://teste.exemplo.com");
  });

  it("descarta cabeçalhos X-Fila-* enviados pelo navegador", () => {
    const req = new Request("https://t.exemplo.com/healthz/ready", {
      headers: { "X-Fila-Client-Ip": "1.2.3.4", "X-Fila-Proxy-Auth": "chute" },
    });
    const up = buildUpstream(req, env, undefined);
    if ("error" in up) throw new Error(up.error);
    expect(up.headers.get("x-fila-client-ip")).toBeNull();
    expect(up.headers.get("x-fila-proxy-auth")).toBe(env.secret);
  });

  it("recusa configuração ausente ou insegura (preview nunca cai na produção por acidente)", () => {
    const req = new Request("https://t.exemplo.com/v1/x");
    expect("error" in buildUpstream(req, {}, "1.1.1.1")).toBe(true);
    expect("error" in buildUpstream(req, { apiOrigin: env.apiOrigin, secret: "curto" }, "1.1.1.1")).toBe(
      true,
    );
    expect(parseApiOrigin("http://api.exemplo.com")).toBeNull();
    expect(parseApiOrigin("https://api.exemplo.com/v1")).toBeNull();
    expect(parseApiOrigin("https://u:p@api.exemplo.com")).toBeNull();
    expect(parseApiOrigin("https://api.exemplo.com")?.host).toBe("api.exemplo.com");
  });

  it("não aceita lixo como IP nem caminho fora da API", () => {
    const up = buildUpstream(new Request("https://t.exemplo.com/v1/x"), env, "999.1.1.1");
    if ("error" in up) throw new Error(up.error);
    expect(up.headers.get("x-fila-client-ip")).toBeNull();
    expect("error" in buildUpstream(new Request("https://t.exemplo.com/conta"), env, "1.1.1.1")).toBe(true);
  });
});
