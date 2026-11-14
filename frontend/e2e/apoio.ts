// Apoio dos testes em navegador real. Prepara dados pela API REAL (mesmas regras que a tela usa) e
// lê os e-mails gravados em arquivo pelo worker (FILA_MAIL_MODE=file). Nada aqui é segredo: as
// contas são criadas a cada execução, com senha de teste.
import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { expect, request, test, type APIRequestContext, type Page } from "@playwright/test";

export const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:15173";
const ignoreHTTPSErrors = process.env.E2E_IGNORAR_TLS === "1";

/**
 * Falha o teste se o navegador recusar algo pela política de segurança de conteúdo (CSP) do
 * servidor de produção (Caddy). Contra o Vite de desenvolvimento não há CSP: nada a recusar.
 */
export function vigiarCSP(): void {
  const violacoes: string[] = [];
  test.beforeEach(({ context }) => {
    violacoes.length = 0;
    context.on("console", (m) => {
      if (m.type() === "error" && /Content Security Policy|Refused to/.test(m.text()))
        violacoes.push(m.text());
    });
  });
  test.afterEach(() => {
    expect(violacoes, "violações de CSP no navegador").toEqual([]);
  });
}
export const senha = "senha-e2e-forte-1";

function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`defina ${name} (veja docs/web-validacao.md)`);
  return v;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Zera os limites de tentativa por IP no banco DO AMBIENTE DE TESTE (todos os navegadores do teste
 * saem do mesmo 127.0.0.1 e esbarrariam nos limites anti-abuso de cadastro e recuperação de senha).
 * Só roda se E2E_PSQL e E2E_DB_URL estiverem definidos.
 */
export function limparLimitesDoTeste(): void {
  const psql = process.env.E2E_PSQL;
  const db = process.env.E2E_DB_URL;
  if (!psql || !db) return;
  execFileSync(psql, [db, "-c", "DELETE FROM rate_limit"], { stdio: "pipe" });
}

/** Link de e-mail (verificação, convite, redefinição) enviado para `to` com `subject`. */
export async function linkDoEmail(to: string, subject: string): Promise<string> {
  const dir = env("E2E_MAIL_DIR");
  for (let i = 0; i < 60; i++) {
    const files = readdirSync(dir)
      .filter((f) => f.endsWith(".txt"))
      .sort()
      .reverse();
    for (const f of files) {
      const text = readFileSync(join(dir, f), "utf-8");
      if (text.includes(`Para: ${to}`) && text.includes(subject)) {
        const m = text.match(/https?:\/\/\S+#token=[A-Za-z0-9_-]{43}/);
        if (m) return m[0];
      }
    }
    await sleep(500);
  }
  throw new Error(`e-mail "${subject}" para ${to} não chegou`);
}

export function tokenDe(link: string): string {
  return link.split("#token=")[1] ?? "";
}

/** Cliente da API com cookies próprios (como um navegador), Origin e CSRF. */
export class Api {
  private constructor(readonly ctx: APIRequestContext) {}

  static async nova(): Promise<Api> {
    return new Api(
      await request.newContext({ baseURL, ignoreHTTPSErrors, extraHTTPHeaders: { Origin: baseURL } }),
    );
  }

  async call(method: string, path: string, body?: unknown): Promise<{ status: number; json: any }> {
    const s = await this.ctx.get("/v1/session");
    const csrf = ((await s.json()) as { data: { csrf_token: string | null } }).data.csrf_token;
    const headers: Record<string, string> = { "Idempotency-Key": crypto.randomUUID().replaceAll("-", "") };
    if (csrf) headers["X-CSRF-Token"] = csrf;
    const res = await this.ctx.fetch(path, {
      method,
      headers,
      ...(body === undefined ? {} : { data: body }),
    });
    const text = await res.text();
    return { status: res.status(), json: text ? JSON.parse(text) : null };
  }

  async ok(method: string, path: string, body?: unknown): Promise<any> {
    const r = await this.call(method, path, body);
    expect(r.status, `${method} ${path}: ${JSON.stringify(r.json)}`).toBeLessThan(300);
    return r.json?.data;
  }

  async login(email: string, kind: "customer" | "staff"): Promise<void> {
    await this.ok("POST", "/v1/auth/login", { email, password: senha, context: kind });
  }
}

/** Conta de cliente verificada (pela API). */
export async function clienteVerificado(email: string, nome = "Cliente Teste"): Promise<Api> {
  const c = await Api.nova();
  await c.ok("POST", "/v1/auth/register", { name: nome, email, password: senha });
  await c.ok("POST", "/v1/auth/email-verification/confirm", {
    token: tokenDe(await linkDoEmail(email, "Confirme")),
  });
  await c.login(email, "customer");
  return c;
}

export type Loja = {
  run: string;
  slug: string;
  estId: string;
  filaBeto: string;
  betoId: string;
  corte: string;
  barba: string;
  donaEmail: string;
  betoEmail: string;
  dona: Api;
  beto: Api;
};

/**
 * Loja nova com dona, profissional Beto (com acesso), fila aberta e dois serviços com pontos.
 * A loja é criada pelo comando de operação (fila-admin bootstrap), como em produção.
 */
export async function montarLoja(tag: string): Promise<Loja> {
  const run = `${tag}${Date.now().toString(36)}`;
  const slug = `loja-${run}`;
  const donaEmail = `dona-${run}@exemplo.com`;
  const betoEmail = `beto-${run}@exemplo.com`;
  try {
    execFileSync(
      env("E2E_ADMIN"),
      ["bootstrap", "-nome", `Barbearia ${run}`, "-slug", slug, "-email", donaEmail],
      {
        cwd: env("E2E_ADMIN_CWD"),
        stdio: "pipe",
      },
    );
  } catch (e) {
    const err = e as { stderr?: Buffer; stdout?: Buffer };
    throw new Error(
      `fila-admin bootstrap falhou: ${err.stderr?.toString() ?? ""} ${err.stdout?.toString() ?? ""}`,
      { cause: e },
    );
  }
  const dona = await Api.nova();
  await dona.ok("POST", "/v1/auth/invites/accept", {
    token: tokenDe(await linkDoEmail(donaEmail, "Convite")),
    name: "Dona Ana",
    password: senha,
  });
  await dona.login(donaEmail, "staff");
  const sess = (await dona.ctx.get("/v1/session").then((r) => r.json())).data;
  const estId: string = sess.memberships[0].establishment_id;
  const beto = await dona.ok("POST", `/v1/establishments/${estId}/professionals`, {
    display_name: "Beto",
    queue_letter: "A",
  });
  const corte = await dona.ok("POST", `/v1/establishments/${estId}/services`, {
    name: "Corte",
    price_cents: 4500,
    duration_minutes: 30,
    points: 10,
    professional_ids: [beto.id],
  });
  const barba = await dona.ok("POST", `/v1/establishments/${estId}/services`, {
    name: "Barba",
    price_cents: 3000,
    duration_minutes: 20,
    points: 5,
    professional_ids: [beto.id],
  });
  await dona.ok("POST", `/v1/establishments/${estId}/queues/${beto.queue_id}/open`, { expected_version: 1 });
  await dona.ok("POST", "/v1/auth/reauthenticate", { password: senha });
  await dona.ok("POST", `/v1/establishments/${estId}/professionals/${beto.id}/invites`, { email: betoEmail });
  const b = await Api.nova();
  await b.ok("POST", "/v1/auth/invites/accept", {
    token: tokenDe(await linkDoEmail(betoEmail, "Convite")),
    name: "Beto",
    password: senha,
  });
  await b.login(betoEmail, "staff");
  return {
    run,
    slug,
    estId,
    filaBeto: beto.queue_id,
    betoId: beto.id,
    corte: corte.id,
    barba: barba.id,
    donaEmail,
    betoEmail,
    dona,
    beto: b,
  };
}

/** Entra pela tela de login. */
export async function entrarNaTela(page: Page, email: string, comoEquipe: boolean): Promise<void> {
  await page.goto("/entrar");
  await page.getByLabel("E-mail").fill(email);
  await page.getByLabel("Senha").fill(senha);
  await page.getByRole("radio", { name: comoEquipe ? /Equipe/ : /Cliente/ }).check();
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("link", { name: "Minha conta" })).toBeVisible();
}

/** Controle do simulador de Pix do ambiente local (nunca a AbacatePay real). */
export async function pixControle(path: string): Promise<void> {
  const url = env("E2E_PIX_CONTROL");
  const res = await fetch(url + path, { method: "POST" });
  if (!res.ok) throw new Error(`simulador: ${res.status} ${path}`);
}

/** Cliente entra na fila do Beto (pela API). Devolve participação e comanda. */
export async function entrarNaFila(
  loja: Loja,
  cliente: Api,
  servicos: string[],
): Promise<{ participation: any; order: any }> {
  return cliente.ok("POST", `/v1/establishments/${loja.estId}/participations`, {
    queue_id: loja.filaBeto,
    service_ids: servicos,
  });
}

/** Beto chama, inicia e conclui tudo como realizado; devolve a comanda aguardando pagamento. */
export async function atenderTudo(loja: Loja, participationId: string): Promise<any> {
  const est = `/v1/establishments/${loja.estId}`;
  const called = await loja.beto.ok("POST", `${est}/queues/${loja.filaBeto}/call-next`);
  expect(called.participation.id).toBe(participationId);
  const started = await loja.beto.ok("POST", `${est}/participations/${participationId}/start`, {
    expected_version: called.participation.version,
  });
  const order = await loja.beto.ok("GET", `${est}/orders/${started.participation.order_id}`);
  const items = order.items
    .filter((i: any) => i.status !== "removed")
    .map((i: any) => ({ item_id: i.id, outcome: "performed" }));
  const done = await loja.beto.ok("POST", `${est}/participations/${participationId}/complete`, {
    expected_version: started.participation.version,
    order_expected_version: order.version,
    items,
  });
  return done.order;
}

/** Beto recebe em dinheiro o valor devido (pago e conferido na mesma ação). */
export async function receberDinheiro(loja: Loja, order: any): Promise<any> {
  const due = order.payment.amounts.due.amount_cents;
  return loja.beto.ok("POST", `/v1/establishments/${loja.estId}/orders/${order.id}/receipts`, {
    expected_version: order.version,
    method: "cash",
    amount_cents: due,
    cash_tendered_cents: due,
  });
}

/** Beto atende o próximo da fila (chama, inicia, conclui tudo). Devolve a comanda aguardando pagamento. */
export async function atenderProximo(loja: Loja): Promise<any> {
  const called = await loja.beto.ok(
    "POST",
    `/v1/establishments/${loja.estId}/queues/${loja.filaBeto}/call-next`,
  );
  const est = `/v1/establishments/${loja.estId}`;
  const id = called.participation.id;
  const started = await loja.beto.ok("POST", `${est}/participations/${id}/start`, {
    expected_version: called.participation.version,
  });
  const order = await loja.beto.ok("GET", `${est}/orders/${started.participation.order_id}`);
  const items = order.items
    .filter((i: any) => i.status !== "removed")
    .map((i: any) => ({ item_id: i.id, outcome: "performed" }));
  const done = await loja.beto.ok("POST", `${est}/participations/${id}/complete`, {
    expected_version: started.participation.version,
    order_expected_version: order.version,
    items,
  });
  return done.order;
}
