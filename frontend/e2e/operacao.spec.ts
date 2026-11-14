// Exercícios reais de operação (etapa 12) contra a HOMOLOGAÇÃO em Docker (deploy/compose.yaml):
// implantar com gente na fila, worker morto no meio do trabalho e restauração de backup com
// conciliação. Só rodam com E2E_OPERACAO=1 (mexem em contêineres). Os tempos medidos vão para
// E2E_EVIDENCIAS (JSON), citados em docs/operacao/evidencias.md.
import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, request, test, type APIRequestContext } from "@playwright/test";
import {
  Api,
  atenderProximo,
  baseURL,
  clienteVerificado,
  limparLimitesDoTeste,
  linkDoEmail,
  montarLoja,
  pixControle,
  receberDinheiro,
  senha,
  vigiarCSP,
  type Loja,
} from "./apoio";

vigiarCSP();
test.skip(
  process.env.E2E_OPERACAO !== "1",
  "exercícios de operação: defina E2E_OPERACAO=1 (veja docs/operacao/evidencias.md)",
);
test.describe.configure({ mode: "serial", timeout: 600_000 });

function env(n: string): string {
  const v = process.env[n];
  if (!v) throw new Error(`defina ${n}`);
  return v;
}
const ignoreHTTPSErrors = process.env.E2E_IGNORAR_TLS === "1";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** docker compose da homologação (arquivos e ambiente vindos de E2E_COMPOSE_*). */
function compose(...args: string[]): string {
  const files = env("E2E_COMPOSE_FILES")
    .split(";")
    .flatMap((f) => ["-f", f]);
  const base = [
    "compose",
    ...files,
    "--env-file",
    env("E2E_COMPOSE_ENV"),
    "--profile",
    "ferramentas",
    "--profile",
    "banco-local",
  ];
  return execFileSync("docker", [...base, ...args], { encoding: "utf-8", stdio: ["ignore", "pipe", "pipe"] });
}

function composeCode(...args: string[]): { code: number; out: string } {
  try {
    return { code: 0, out: compose(...args) };
  } catch (e) {
    const err = e as { status?: number; stdout?: string; stderr?: string };
    return { code: err.status ?? 1, out: `${err.stdout ?? ""}${err.stderr ?? ""}` };
  }
}

/** Roda um script de deploy/ (bash) e devolve saída e duração. */
function script(nome: string, ...args: string[]): Promise<{ code: number; out: string; ms: number }> {
  const t0 = Date.now();
  return new Promise((resolve) => {
    const p = spawn(process.env.E2E_BASH ?? "bash", [join(env("E2E_DEPLOY_DIR"), nome), ...args], {
      env: { ...process.env, FILA_SEM_BUILD: "1", MSYS_NO_PATHCONV: "1", MSYS2_ENV_CONV_EXCL: "*" },
    });
    let out = "";
    p.stdout.on("data", (d) => (out += d));
    p.stderr.on("data", (d) => (out += d));
    p.on("close", (code) => resolve({ code: code ?? 1, out, ms: Date.now() - t0 }));
  });
}

function psql(sql: string): string {
  return execFileSync(env("E2E_PSQL"), [env("E2E_DB_URL"), "-Atc", sql], { encoding: "utf-8" }).trim();
}

const evidencias: Record<string, unknown> = {};
test.afterAll(() => {
  const dir = process.env.E2E_EVIDENCIAS;
  if (!dir) return;
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "operacao.json"), JSON.stringify(evidencias, null, 2));
});

/** Visitante (pela API) entra na fila do Beto; devolve o id da participação. */
async function visitanteNaFila(loja: Loja, nick: string): Promise<string> {
  const v = await Api.nova();
  await v.ok("POST", `/v1/establishments/${loja.estId}/visitor-sessions`, { nick });
  const r = await v.ok("POST", `/v1/establishments/${loja.estId}/participations`, {
    queue_id: loja.filaBeto,
    service_ids: [loja.corte],
  });
  return r.participation.id;
}

/** Comando com a MESMA chave de idempotência em cada tentativa (rede caindo durante a troca). */
async function comando(api: Api, method: string, path: string, body?: unknown): Promise<any> {
  const key = crypto.randomUUID().replaceAll("-", "");
  for (let i = 0; i < 60; i++) {
    try {
      const s = await api.ctx.get("/v1/session");
      if (s.status() >= 500) throw new Error(`sessão ${s.status()}`);
      const csrf = (await s.json()).data.csrf_token;
      const res = await api.ctx.fetch(path, {
        method,
        headers: { "Idempotency-Key": key, ...(csrf ? { "X-CSRF-Token": csrf } : {}) },
        ...(body === undefined ? {} : { data: body }),
      });
      if (res.status() < 500) {
        expect(res.status(), `${method} ${path}: ${await res.text()}`).toBeLessThan(300);
        return (await res.json()).data;
      }
    } catch (e) {
      if (String(e).includes("toBeLessThan")) throw e;
    }
    await sleep(500);
  }
  throw new Error(`${method} ${path}: sem sucesso em 30 s`);
}

test("implantação com gente na fila: nada se perde, ninguém vê erro e as telas voltam sozinhas", async ({
  browser,
}) => {
  limparLimitesDoTeste();
  const loja = await montarLoja("impl");
  const ids = [
    await visitanteNaFila(loja, "Ana"),
    await visitanteNaFila(loja, "Bia"),
    await visitanteNaFila(loja, "Caio"),
  ];

  const v = await (
    await browser.newContext({ viewport: { width: 360, height: 780 }, ignoreHTTPSErrors })
  ).newPage();
  await v.goto(`/loja/${loja.slug}`);
  await v.getByLabel("Como quer ser chamado").fill("Lia");
  await v.getByRole("button", { name: "Continuar" }).click();
  const form = v.getByRole("form", { name: "Entrar na fila" });
  await form.getByRole("radio", { name: /Beto/ }).check();
  await form.getByRole("checkbox", { name: /Corte/ }).check();
  await form.getByRole("button", { name: "Entrar na fila" }).click();
  await expect(v.getByText(/você é o 4º da fila/)).toBeVisible();

  // Tráfego contínuo de leitura (público) durante toda a troca.
  const publico: APIRequestContext = await request.newContext({ baseURL, ignoreHTTPSErrors });
  const leituras = { total: 0, falhas: 0, codigos: {} as Record<string, number>, maxMs: 0 };
  let parar = false;
  const trafego = (async () => {
    while (!parar) {
      const t = Date.now();
      let code = "rede";
      try {
        code = String(
          (await publico.get(`/v1/public/establishments/${loja.estId}/queues`, { timeout: 30_000 })).status(),
        );
      } catch {
        /* conta como falha */
      }
      leituras.total++;
      leituras.codigos[code] = (leituras.codigos[code] ?? 0) + 1;
      if (code !== "200") leituras.falhas++;
      leituras.maxMs = Math.max(leituras.maxMs, Date.now() - t);
      await sleep(250);
    }
  })();

  const versao = env("E2E_IMPLANTAR_VERSAO");
  const deploy = script("implantar.sh", env("E2E_COMPOSE_ENV"), versao);
  // No meio da troca, Beto atende a primeira pessoa (comandos repetidos com a mesma chave).
  await sleep(4000);
  const est = `/v1/establishments/${loja.estId}`;
  const chamado = await comando(loja.beto, "POST", `${est}/queues/${loja.filaBeto}/call-next`);
  expect(chamado.participation.id).toBe(ids[0]);
  const iniciado = await comando(loja.beto, "POST", `${est}/participations/${ids[0]}/start`, {
    expected_version: chamado.participation.version,
  });
  const d = await deploy;
  parar = true;
  await trafego;
  expect(d.code, d.out).toBe(0);
  expect(d.out).toContain("trocando a API");

  // Ninguém sumiu da fila; a primeira pessoa está em atendimento; a tela da Lia andou sozinha.
  const filas = await (await publico.get(`/v1/public/establishments/${loja.estId}/queues`)).json();
  const fila = filas.data.find((q: any) => q.queue_id === loja.filaBeto) ?? filas.data[0];
  expect(JSON.stringify(fila)).toBeTruthy();
  expect(iniciado.participation.status).toBe("in_service");
  for (const id of ids.slice(1)) {
    const p = await loja.beto.ok("GET", `${est}/participations/${id}`);
    expect(p.status).toBe("waiting");
  }
  await expect(v.getByText(/você é o 3º da fila/)).toBeVisible({ timeout: 60_000 });
  await expect(v.getByText(/Reconectando/)).toHaveCount(0, { timeout: 30_000 });
  expect(leituras.falhas, JSON.stringify(leituras)).toBe(0);
  evidencias.implantacao = {
    duracao_ms: d.ms,
    leituras,
    saida: d.out.split("\n").filter((l) => /^\[\d+s\]/.test(l)),
  };
});

test("worker morto no meio do trabalho: ao voltar, faz tudo o que ficou parado (uma vez)", async () => {
  limparLimitesDoTeste();
  const loja = await montarLoja("work");
  const est = `/v1/establishments/${loja.estId}`;
  await loja.dona.ok("PUT", `${est}/loyalty-program`, { expected_version: 1, enabled: true });
  const premio = await loja.dona.ok("POST", `${est}/rewards`, {
    name: "Pomada",
    description: "Pomada modeladora",
    usage_instructions: "Peça na recepção.",
    cost_points: 10,
    stock: { kind: "limited", units_available: 3 },
  });
  const run = loja.run;
  // Pix pendente (Maria) e pontos com reserva ativa (João), antes de matar o worker.
  const maria = await clienteVerificado(`maria-${run}@exemplo.com`, "Maria");
  await maria.ok("POST", `${est}/participations`, { queue_id: loja.filaBeto, service_ids: [loja.corte] });
  const joao = await clienteVerificado(`joao-${run}@exemplo.com`, "João");
  await joao.ok("POST", `${est}/participations`, { queue_id: loja.filaBeto, service_ids: [loja.corte] });
  const om = await atenderProximo(loja);
  const pix = await maria.ok("POST", `${est}/orders/${om.id}/pix-charges`, { expected_version: om.version });
  const oj = await atenderProximo(loja);
  await receberDinheiro(loja, oj);
  const pontos = async () => (await joao.ok("GET", `/v1/me/loyalty/${loja.estId}`)).available;
  await expect.poll(pontos, { timeout: 30_000 }).toBe(10);
  const reserva = await joao.ok("POST", `${est}/reward-reservations`, {
    reward_id: premio.id,
    expected_reward_version: premio.version,
  });
  expect(await pontos()).toBe(0); // 10 pontos retidos pela reserva
  const sofia = await clienteVerificado(`sofia-${run}@exemplo.com`, "Sofia");

  // Mata o worker SEM encerramento gracioso (como queda de energia ou OOM).
  compose("kill", "-s", "SIGKILL", "worker");
  const morto = Date.now();
  await pixControle(`/control/set?externalId=${pix.id}&status=PAID`);
  psql(`UPDATE reward_reservation SET expires_at = now() - interval '1 minute' WHERE id = '${reserva.id}'`);
  await sofia.ok("POST", "/v1/auth/reauthenticate", { password: senha });
  const pedido = await sofia.ok("POST", "/v1/me/deletion-request", { confirm_effects: true });
  const outro = await Api.nova();
  await outro.ok("POST", "/v1/auth/password-reset/request", { email: `maria-${run}@exemplo.com` });
  await sleep(3000);
  // Nada andou com o worker morto.
  expect((await loja.beto.ok("GET", `${est}/orders/${om.id}`)).payment.charges[0].status).toBe("pending");

  compose("up", "-d", "--no-deps", "worker");
  const t0 = Date.now();
  await expect
    .poll(async () => (await loja.beto.ok("GET", `${est}/orders/${om.id}`)).payment.charges[0].status, {
      timeout: 90_000,
    })
    .toBe("paid");
  const pagoMs = Date.now() - t0;
  await expect.poll(pontos, { timeout: 90_000 }).toBe(10); // reserva vencida devolve os pontos
  await expect
    .poll(async () => psql(`SELECT status FROM reward_reservation WHERE id = '${reserva.id}'`), {
      timeout: 90_000,
    })
    .toBe("expired");
  const status = await Api.nova();
  await expect
    .poll(
      async () =>
        (await status.ok("POST", "/v1/deletion-requests/status", { protocol: pedido.protocol })).status,
      {
        timeout: 120_000,
      },
    )
    .toBe("completed");
  expect(await linkDoEmail(`maria-${run}@exemplo.com`, "Redefinição de senha")).toContain("#token=");
  const tudoMs = Date.now() - t0;
  // Uma vez só: um recebimento Pix, uma liberação de reserva.
  expect(psql(`SELECT count(*) FROM receipt WHERE pix_charge_id = '${pix.id}'`)).toBe("1");
  expect(
    psql(`SELECT count(*) FROM loyalty_ledger WHERE source_id = '${reserva.id}' AND kind = 'hold_release'`),
  ).toBe("1");
  evidencias.worker = { parado_ms: t0 - morto, pix_confirmado_ms: pagoMs, tudo_em_dia_ms: tudoMs };
});

test("restauração de backup: worker travado até conciliar; pagamento depois do backup não se perde", async () => {
  limparLimitesDoTeste();
  const loja = await montarLoja("rest");
  const est = `/v1/establishments/${loja.estId}`;
  const run = loja.run;
  const maria = await clienteVerificado(`maria-${run}@exemplo.com`, "Maria");
  await maria.ok("POST", `${est}/participations`, { queue_id: loja.filaBeto, service_ids: [loja.corte] });
  const om = await atenderProximo(loja);
  const davi = await clienteVerificado(`davi-${run}@exemplo.com`, "Davi");

  // 1. Backup (o mesmo comando do agendamento diário).
  const tb = Date.now();
  const b = composeCode("run", "--rm", "backup");
  const backupMs = Date.now() - tb;
  expect(b.code, b.out).toBe(0);
  const backup = readdirSync(env("E2E_BACKUPS_DIR"))
    .filter((f) => f.endsWith(".manifesto"))
    .sort()
    .at(-1)!
    .replace(".manifesto", "");
  const manifesto = readFileSync(join(env("E2E_BACKUPS_DIR"), `${backup}.manifesto`), "utf-8");

  // 2. Depois do backup: Maria paga por Pix (o worker registra), Davi pede exclusão, Lia entra na fila.
  const pix = await maria.ok("POST", `${est}/orders/${om.id}/pix-charges`, { expected_version: om.version });
  await pixControle(`/control/set?externalId=${pix.id}&status=PAID`);
  await expect
    .poll(async () => (await loja.beto.ok("GET", `${est}/orders/${om.id}`)).payment.charges[0].status, {
      timeout: 60_000,
    })
    .toBe("paid");
  await davi.ok("POST", "/v1/auth/reauthenticate", { password: senha });
  const pedido = await davi.ok("POST", "/v1/me/deletion-request", { confirm_effects: true });
  const lia = await visitanteNaFila(loja, "Lia");
  await sleep(7000); // o diário de exclusões é gravado em até 5 s

  // 3. Restauração (API e worker parados; banco trocado; worker travado).
  const r = await script("restaurar.sh", env("E2E_COMPOSE_ENV"), backup, `exercício ${run}`);
  expect(r.code, r.out).toBe(0);
  const noAr = Date.now();
  const v1 = composeCode("run", "--rm", "fila-admin", "verificar");
  expect(v1.code, v1.out).toBe(3);
  expect(v1.out).toContain("FilaWorkerTravadoRestauracao");

  // 4. Conciliação com o provedor e o diário.
  const tc = Date.now();
  const c = composeCode(
    "run",
    "--rm",
    "fila-admin",
    "restauracao",
    "reconciliar",
    "-diario",
    "/var/lib/fila/diario",
  );
  const conciliaMs = Date.now() - tc;
  expect(c.code, c.out).toBe(0);
  expect(c.out).toMatch(/pagamentos registrados agora: 1 · recriadas do provedor: 1/);
  expect(c.out).toMatch(/1 reaplicado\(s\)/);
  const l = composeCode("run", "--rm", "fila-admin", "restauracao", "liberar");
  expect(l.code, l.out).toBe(0);
  const liberado = Date.now();

  // Sessões do backup encerradas: a dona entra de novo e vê o Pix de Maria registrado.
  const dona = await Api.nova();
  await dona.login(loja.donaEmail, "staff");
  const ordem = await dona.ok("GET", `${est}/orders/${om.id}`);
  expect(ordem.payment.charges.map((x: any) => x.status)).toContain("paid");
  expect(ordem.payment.receipts.length).toBe(1);
  // Exclusão de Davi reaplicada com o mesmo protocolo e concluída depois de liberar.
  const status = await Api.nova();
  await expect
    .poll(
      async () =>
        (await status.ok("POST", "/v1/deletion-requests/status", { protocol: pedido.protocol })).status,
      {
        timeout: 120_000,
      },
    )
    .toBe("completed");
  // A entrada de Lia (depois do backup) se perdeu: é a perda máxima (RPO) medida aqui.
  const perdida = await dona.call("GET", `${est}/participations/${lia}`);
  expect(perdida.status).toBe(404);
  expect(compose("ps", "--status", "running", "--services")).toContain("worker");
  evidencias.restauracao = {
    backup,
    backup_ms: backupMs,
    manifesto,
    restaurar_script_ms: r.ms,
    restaurar_passos: r.out.split("\n").filter((l) => /^\[\d+s\]|restaurar: ok/.test(l)),
    conciliacao_ms: conciliaMs,
    conciliacao: c.out.split("\n").filter((x) => x.startsWith("·")),
    rto_api_no_ar_ms: r.ms,
    rto_worker_liberado_ms: r.ms + (liberado - noAr),
  };
});
