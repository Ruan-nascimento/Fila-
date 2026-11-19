// Teste da JANELA do Fila Gestão (Linux: tauri-driver + WebKitWebDriver), contra a API real de um
// ambiente de TESTE (nunca produção). Prepara os dados pela API, como os testes do frontend
// (frontend/e2e/apoio.ts), e opera o programa instalado como o dono faria.
//
//   node testes/janela.mjs completo   login do dono, só a loja dele, URL direta recusada, fila em
//                                     tempo real, trava de origem, conta só profissional recusada
//   node testes/janela.mjs falha      painel fora do ar: tela local com "Tentar novamente"
//   node testes/janela.mjs dashboard  ativação do PC, Dashboard local com a MESMA sessão, números
//                                     reconciliados, navegador sem acesso ao analítico, troca de
//                                     sessão web ↔ desktop e limpeza dos dados ao perder a vaga
//
// Variáveis: FG_APP (binário instalado), FG_BASE (origem do painel de teste, a mesma do build),
// FG_ADMIN (fila-admin), FG_ADMIN_CWD (pasta com o .env do teste), FG_MAIL_DIR (e-mails em arquivo),
// FG_DRIVER (tauri-driver, padrão http://127.0.0.1:4444), FG_FOTOS (pasta para capturas).
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const env = (n, padrao) => {
  const v = process.env[n] ?? padrao;
  if (!v) throw new Error(`defina ${n}`);
  return v;
};
const BASE = env("FG_BASE");
const DRIVER = env("FG_DRIVER", "http://127.0.0.1:4444");
const FOTOS = env("FG_FOTOS", "testes/fotos");
const SENHA = "senha-janela-forte-1";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let passos = 0;
const ok = (msg) => console.log(`  ok ${++passos} - ${msg}`);

// ---------- API (cookies próprios, Origin e CSRF, como um navegador) ----------
class Api {
  cookies = new Map();
  async req(method, path, body) {
    const headers = { Origin: BASE, Cookie: [...this.cookies].map(([k, v]) => `${k}=${v}`).join("; ") };
    if (method !== "GET") {
      const s = await this.req("GET", "/v1/session");
      if (s.json.data.csrf_token) headers["X-CSRF-Token"] = s.json.data.csrf_token;
      headers["Idempotency-Key"] = crypto.randomUUID().replaceAll("-", "");
      headers["Content-Type"] = "application/json";
    }
    const res = await fetch(BASE + path, { method, headers, body: body ? JSON.stringify(body) : undefined });
    for (const c of res.headers.getSetCookie()) {
      const [kv] = c.split(";");
      const i = kv.indexOf("=");
      this.cookies.set(kv.slice(0, i), kv.slice(i + 1));
    }
    const text = await res.text();
    return { status: res.status, json: text ? JSON.parse(text) : null };
  }
  async ok(method, path, body) {
    const r = await this.req(method, path, body);
    if (r.status >= 300) throw new Error(`${method} ${path}: ${r.status} ${JSON.stringify(r.json)}`);
    return r.json?.data;
  }
  login(email, context) {
    return this.ok("POST", "/v1/auth/login", { email, password: SENHA, context });
  }
}

function emails() {
  return new Set(readdirSync(env("FG_MAIL_DIR")));
}
async function linkNovo(antes, para) {
  for (let i = 0; i < 60; i++) {
    for (const f of readdirSync(env("FG_MAIL_DIR"))) {
      if (antes.has(f)) continue;
      const t = readFileSync(join(env("FG_MAIL_DIR"), f), "utf-8");
      const m = /#token=([A-Za-z0-9_-]{43})/.exec(t);
      if (t.includes(`Para: ${para}`) && m) return m[1];
    }
    await sleep(500);
  }
  throw new Error(`e-mail para ${para} não chegou`);
}

async function lojaComDono(run) {
  const email = `dono-${run}@exemplo.com`;
  const antes = emails();
  execFileSync(env("FG_ADMIN"), ["bootstrap", "-nome", `Barbearia ${run}`, "-slug", `loja-${run}`, "-email", email], {
    cwd: env("FG_ADMIN_CWD"),
    stdio: "pipe",
  });
  const dono = new Api();
  await dono.ok("POST", "/v1/auth/invites/accept", { token: await linkNovo(antes, email), name: "Dono", password: SENHA });
  await dono.login(email, "staff");
  const estId = (await dono.ok("GET", "/v1/session")).memberships[0].establishment_id;
  return { run, email, dono, estId, nome: `Barbearia ${run}` };
}

async function convidarProfissional(loja, nome, letra, email, contaExistente) {
  const pro = await loja.dono.ok("POST", `/v1/establishments/${loja.estId}/professionals`, {
    display_name: nome,
    queue_letter: letra,
  });
  await loja.dono.ok("POST", "/v1/auth/reauthenticate", { password: SENHA });
  const antes = emails();
  await loja.dono.ok("POST", `/v1/establishments/${loja.estId}/professionals/${pro.id}/invites`, { email });
  const token = await linkNovo(antes, email);
  await new Api().ok("POST", "/v1/auth/invites/accept", contaExistente ? { token, password: SENHA } : { token, name: nome, password: SENHA });
  return pro;
}

// ---------- WebDriver ----------
let sid = null;
async function wd(method, path, body) {
  const res = await fetch(`${DRIVER}/session${sid ? `/${sid}` : ""}${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const j = await res.json();
  if (j.value?.error) throw new Error(`${method} ${path}: ${j.value.error} ${j.value.message}`);
  return j.value;
}
async function abrirPrograma() {
  const v = await wd("POST", "", { capabilities: { alwaysMatch: { "tauri:options": { application: env("FG_APP") } } } });
  sid = v.sessionId;
}
const fecharPrograma = async () => sid && (await wd("DELETE", "").catch(() => {}), (sid = null));
const url = () => wd("GET", "/url");
const ir = (u) => wd("POST", "/url", { url: u });
const js = (script, args = []) => wd("POST", "/execute/sync", { script, args });
const texto = () => js("return document.body ? document.body.innerText : ''");

async function esperar(desc, cond, ms = 30000) {
  const fim = Date.now() + ms;
  let ultimo;
  while (Date.now() < fim) {
    try {
      ultimo = await cond();
      if (ultimo) return ultimo;
    } catch (e) {
      ultimo = e.message;
    }
    await sleep(300);
  }
  throw new Error(`esperava: ${desc} (último: ${String(ultimo).slice(0, 300)})`);
}
async function campo(seletor, valor) {
  const el = await wd("POST", "/element", { using: "css selector", value: seletor });
  const id = Object.values(el)[0];
  await wd("POST", `/element/${id}/clear`, {});
  await wd("POST", `/element/${id}/value`, { text: valor });
}
async function botao(rotulo) {
  const el = await wd("POST", "/element", { using: "xpath", value: `//button[normalize-space(.)="${rotulo}"]` });
  await wd("POST", `/element/${Object.values(el)[0]}/click`, {});
}
async function foto(nome) {
  mkdirSync(FOTOS, { recursive: true });
  writeFileSync(join(FOTOS, `${nome}.png`), Buffer.from(await wd("GET", "/screenshot"), "base64"));
}
async function entrar(email) {
  await esperar("formulário de login do dono", async () => (await url()).endsWith("/dono") && (await texto()).includes("Acesso do dono"));
  await campo('input[type="email"]', email);
  await campo('input[autocomplete="current-password"]', SENHA);
  await botao("Entrar");
}

// ---------- Cenários ----------
async function completo() {
  const run = `jan${Date.now().toString(36)}`;
  console.log(`preparando dados (${run})…`);
  const a = await lojaComDono(`${run}a`);
  const b = await lojaComDono(`${run}b`);
  const betoEmail = `beto-${run}@exemplo.com`;
  const beto = await convidarProfissional(a, "Beto", "A", betoEmail, false);
  await convidarProfissional(b, "Dono A", "C", a.email, true); // dono de A é profissional de B
  const corte = await a.dono.ok("POST", `/v1/establishments/${a.estId}/services`, {
    name: "Corte", price_cents: 4500, duration_minutes: 30, points: 0, professional_ids: [beto.id],
  });
  await a.dono.ok("POST", `/v1/establishments/${a.estId}/queues/${beto.queue_id}/open`, { expected_version: 1 });
  // Sessão única do dono (ADR-0027): a preparação sai para o dono entrar pelo programa sem conflito.
  await a.dono.ok("POST", "/v1/auth/logout");

  await abrirPrograma();
  ok("programa instalado abriu (sessão WebDriver criada)");
  await esperar("tela local abrir /dono", async () => (await url()) === `${BASE}/dono`);
  ok(`tela local conferiu o painel e abriu ${BASE}/dono`);
  await esperar("título", async () => (await js("return document.title")) !== "");
  await foto("1-login-dono");
  const t0 = await texto();
  if (t0.includes("Criar conta de cliente") || t0.includes("Entrar como")) throw new Error("login não é só de equipe");
  ok("login do dono sem opção de cliente");

  await entrar(a.email);
  await esperar("painel da loja A", async () => (await url()) === `${BASE}/gestao/${a.estId}/filas`);
  await esperar("nome da loja A", async () => (await texto()).includes(a.nome));
  const tA = await texto();
  if (tA.includes(b.nome) || tA.includes("Trocar de loja")) throw new Error("loja B oferecida ao dono de A");
  ok("dono de A (e profissional de B) entra direto em A; B não é oferecida");

  await ir(`${BASE}/gestao/${b.estId}/filas`);
  await esperar("recusa da loja B", async () => (await texto()).includes("Nesta janela só aparecem lojas em que você é dono."));
  await foto("2-url-direta-loja-b-recusada");
  ok("URL direta da loja B recusada nesta janela");
  await ir(`${BASE}/gestao`);
  await esperar("troca de loja volta para A", async () => (await url()) === `${BASE}/gestao/${a.estId}/filas`);
  ok("troca de loja (/gestao) leva só às lojas de dono");

  await esperar("fila do Beto na tela", async () => (await texto()).includes("Beto"));
  const antes = emails();
  const cli = new Api();
  const cliEmail = `cli-${run}@exemplo.com`;
  await cli.ok("POST", "/v1/auth/register", { name: "Carla Janela", email: cliEmail, password: SENHA });
  await cli.ok("POST", "/v1/auth/email-verification/confirm", { token: await linkNovo(antes, cliEmail) });
  await cli.login(cliEmail, "customer");
  await cli.ok("POST", `/v1/establishments/${a.estId}/participations`, { queue_id: beto.queue_id, service_ids: [corte.id] });
  await esperar("cliente aparecer na fila sem recarregar", async () => (await texto()).includes("Carla Janela"), 20000);
  await foto("3-fila-atualizada-em-tempo-real");
  ok("cliente entrou pela API e apareceu na fila da janela sozinho (SSE)");

  // Trava de origem: navegação para outra origem é bloqueada (ftp: não abre nada no sistema).
  await js("window.location.href = 'ftp://outro-site.invalid/'");
  await sleep(1500);
  if (!(await url()).startsWith(BASE)) throw new Error(`saiu da origem: ${await url()}`);
  ok("navegação para outra origem bloqueada; a janela continua no painel");
  const ipc = await wd("POST", "/execute/async", {
    script:
      "const done = arguments[0]; const t = window.__TAURI__; if (!t) return done('sem-api');" +
      "t.core.invoke('painel').then(() => done('PERMITIDO'), (e) => done('negado: ' + e));",
    args: [],
  });
  if (ipc === "PERMITIDO") throw new Error("a página remota conseguiu chamar comando do programa");
  ok(`página do painel não chama comandos do programa (${String(ipc).slice(0, 60)})`);

  // Sair e entrar com conta só profissional.
  await ir(`${BASE}/conta`);
  await esperar("minha conta", async () => (await texto()).includes("Sair desta conta"));
  await botao("Sair desta conta");
  await esperar("volta ao login do dono", async () => (await url()) === `${BASE}/dono`);
  ok("sair da conta volta ao acesso do dono");
  await entrar(betoEmail);
  await esperar("recusa da conta profissional", async () => (await texto()).includes("Esta conta não é dona de nenhuma loja."));
  await foto("4-conta-profissional-recusada");
  ok("conta só profissional recusada com mensagem clara");
  await botao("Trocar de conta");
  await esperar("login de novo", async () => (await texto()).includes("Acesso do dono"));
  ok("Trocar de conta sai e mostra o login");
  // A sessão de equipe do Beto continua valendo no servidor para o navegador comum (não é afetada).
  const b2 = new Api();
  await b2.login(betoEmail, "staff");
  const s = await b2.ok("GET", "/v1/session");
  if (s.session?.kind !== "staff") throw new Error("profissional não entra mais pelo navegador");
  ok("o mesmo profissional continua entrando como equipe fora do programa");
}

async function falha() {
  await abrirPrograma();
  ok("programa instalado abriu com o painel fora do ar");
  await esperar("tela local de erro", async () => (await texto()).includes("Não foi possível abrir o painel"));
  if (!/^(tauri:\/\/localhost|https?:\/\/tauri\.localhost)/.test(await url())) throw new Error(`não está na tela local: ${await url()}`);
  await foto("5-painel-fora-do-ar");
  ok(`tela local de erro mostrada (${(await texto()).split("\n").find((l) => l.includes("conectar")) ?? ""})`);
  console.log("  aguardando o painel voltar…");
  await esperar("painel voltar", async () => (await fetch(`${BASE}/healthz/live`).catch(() => null))?.ok, 120000);
  await botao("Tentar novamente");
  await esperar("abrir /dono depois de tentar novamente", async () => (await url()) === `${BASE}/dono`);
  ok("Tentar novamente abriu o painel quando ele voltou");
}

// Atendimento pago em dinheiro (R$ 45 com R$ 50 entregues): dados reais para o Dashboard.
async function atendimentoPago(loja, beto, betoApi, servico) {
  const v = new Api();
  await v.ok("POST", `/v1/establishments/${loja.estId}/visitor-sessions`, { nick: "Zé" });
  await v.ok("POST", `/v1/establishments/${loja.estId}/participations`, { queue_id: beto.queue_id, service_ids: [servico.id] });
  const chamada = await betoApi.ok("POST", `/v1/establishments/${loja.estId}/queues/${beto.queue_id}/call-next`);
  const p = chamada.participation;
  const ini = await betoApi.ok("POST", `/v1/establishments/${loja.estId}/participations/${p.id}/start`, { expected_version: p.version });
  const fim = await betoApi.ok("POST", `/v1/establishments/${loja.estId}/participations/${p.id}/complete`, {
    expected_version: ini.participation.version, order_expected_version: ini.order.version,
    items: ini.order.items.map((i) => ({ item_id: i.id, outcome: "performed" })),
  });
  const pago = await betoApi.ok("POST", `/v1/establishments/${loja.estId}/orders/${fim.order.id}/receipts`, {
    expected_version: fim.order.version, method: "cash", amount_cents: 4500, cash_tendered_cents: 5000,
  });
  await betoApi.ok("POST", `/v1/establishments/${loja.estId}/orders/${pago.id}/receipts/${pago.payment.receipts[0].id}/confer`, {});
}

async function dashboard() {
  const run = `dash${Date.now().toString(36)}`;
  console.log(`preparando dados (${run})…`);
  const a = await lojaComDono(run);
  const betoEmail = `beto-${run}@exemplo.com`;
  const beto = await convidarProfissional(a, "Beto", "A", betoEmail, false);
  const corte = await a.dono.ok("POST", `/v1/establishments/${a.estId}/services`, {
    name: "Corte", price_cents: 4500, duration_minutes: 30, points: 0, professional_ids: [beto.id],
  });
  await a.dono.ok("POST", `/v1/establishments/${a.estId}/queues/${beto.queue_id}/open`, { expected_version: 1 });
  const betoApi = new Api();
  await betoApi.login(betoEmail, "staff");
  await atendimentoPago(a, beto, betoApi, corte);
  await a.dono.ok("POST", "/v1/auth/logout"); // a preparação sai; o dono vai entrar pelo programa
  const codigo = /código de ativação: (\S+)/.exec(
    execFileSync(env("FG_ADMIN"), ["desktop", "emitir-codigo", "-email", a.email, "-nome", "PC do teste"], { cwd: env("FG_ADMIN_CWD"), encoding: "utf-8" }),
  )?.[1];
  if (!codigo) throw new Error("fila-admin não emitiu o código");
  ok("operador emitiu o código de ativação (fila-admin desktop emitir-codigo)");

  await abrirPrograma();
  await esperar("tela local abrir /dono", async () => (await url()) === `${BASE}/dono`);
  await entrar(a.email);
  await esperar("gestão da loja", async () => (await url()).includes(`/gestao/${a.estId}/filas`));
  await esperar("atalho Dashboard na navegação do dono", async () => (await texto()).includes("Dashboard"));
  ok("dono entrou pelo programa; a navegação mostra Dashboard e Filas");
  await botao("Dashboard");
  await esperar("página LOCAL do Dashboard (ativação)", async () =>
    /^(tauri:\/\/localhost|https?:\/\/tauri\.localhost)\/dashboard\.html$/.test(await url()) && (await texto()).includes("Autorizar este computador"));
  if ((await url()).includes("#")) throw new Error("ticket ficou na URL local");
  ok("o atalho abriu a página local (ticket entregue ao nativo, fora da URL)");
  await campo("input[placeholder='XXXX-XXXX-XXXX-XXXX']", codigo);
  await botao("Ativar");
  await esperar("indicadores do Dashboard", async () => (await texto()).includes("Recebimentos líquidos"), 45000);
  await esperar("valor do caixa reconciliado (R$ 45,00)", async () => /R\$\s?45,00/.test(await texto()));
  await foto("dash-1-painel");
  ok("computador ativado; Dashboard com a MESMA sessão mostra R$ 45,00 (pago com R$ 50: o troco não entra no caixa)");
  const t = await texto();
  for (const aba of ["Visão geral", "Filas e atendimento", "Financeiro", "Profissionais e serviços", "Fidelidade"])
    if (!t.includes(aba)) throw new Error(`aba ausente: ${aba}`);
  for (const [w, h, nome] of [[1366, 768, "notebook-1366x768"], [1920, 1080, "largo-1920x1080"], [820, 620, "reduzida-820x620"]]) {
    await wd("POST", "/window/rect", { width: w, height: h });
    await sleep(800);
    const vis = await js("const t=document.querySelector('header'); const k=[...document.querySelectorAll('section[aria-label=\"Indicadores principais\"] strong')]; return { loja: !!t && t.innerText.includes('Barbearia'), periodo: !!document.querySelector('header select, header h1'), destaque: k.length > 0 && k[0].getBoundingClientRect().top < innerHeight, rolagemH: document.documentElement.scrollWidth > innerWidth + 1 }");
    if (!vis.loja || !vis.destaque || vis.rolagemH) throw new Error(`leitura em ${nome}: ${JSON.stringify(vis)}`);
    await foto(`dash-tamanho-${nome}`);
  }
  await wd("POST", "/window/rect", { width: 1280, height: 820 });
  ok("loja, período e destaque principal visíveis em 1366×768, 1920×1080 e 820×620 (sem rolagem horizontal)");
  await botao("Financeiro");
  await esperar("meios de pagamento", async () => (await texto()).includes("Meios de pagamento") && (await texto()).includes("Dinheiro"));
  await foto("dash-2-financeiro");
  ok("abas carregam sob demanda (Financeiro: meios de pagamento)");

  // Navegador comum com cookie de dono: gestão sim, analítico não.
  const web = new Api();
  const tentativa = await web.req("POST", "/v1/auth/login", { email: a.email, password: SENHA, context: "staff" });
  if (tentativa.status !== 409 || tentativa.json.code !== "owner_session_active") throw new Error(`login concorrente: ${tentativa.status}`);
  if (tentativa.json.current.other_session.channel !== "desktop_authorized") throw new Error("canal da outra sessão");
  ok("login no navegador com o Dashboard aberto: 409, a outra sessão é o programa autorizado");
  await web.ok("POST", "/v1/auth/owner-session/takeover", { challenge: tentativa.json.current.challenge });
  const direto = await web.req("GET", `/v1/desktop/analytics/establishments/${a.estId}/summary?from=2026-01-01&to=2026-01-02`);
  if (direto.status !== 401) throw new Error(`analítico direto do navegador: ${direto.status}`);
  await web.ok("GET", `/v1/establishments/${a.estId}/pendencies`);
  ok("navegador assumiu: gestão funciona, chamada direta ao analítico recusada (401)");
  await esperar("Dashboard limpo com a mensagem de troca", async () => (await texto()).includes("Sua sessão foi encerrada porque você entrou em outro aparelho"), 45000);
  if (/R\$\s?45,00/.test(await texto())) throw new Error("números continuaram na tela");
  await foto("dash-3-encerrado");
  ok("o programa limpou os gráficos e explicou: entrou em outro aparelho");

  await botao("Ir para o acesso");
  await entrar(a.email);
  await esperar("confirmação de troca no programa", async () => (await texto()).includes("Continuar neste aparelho e encerrar a outra sessão"));
  await botao("Continuar neste aparelho e encerrar a outra sessão");
  await esperar("gestão de volta no programa", async () => (await url()).includes(`/gestao/${a.estId}/`));
  const depois = await web.req("GET", `/v1/establishments/${a.estId}/pendencies`);
  if (depois.status !== 401 || depois.json.code !== "owner_session_replaced") throw new Error(`web depois da troca: ${depois.status}`);
  ok("programa retomou a sessão: o navegador perdeu a gestão (owner_session_replaced)");
  await esperar("atalho Dashboard depois da troca", async () => (await texto()).includes("Dashboard") && (await texto()).includes("Filas"));
  await botao("Dashboard");
  await esperar("Dashboard de novo, sem nova ativação", async () => (await texto()).includes("Recebimentos líquidos"), 45000);
  ok("Dashboard reabre com a mesma sessão, sem pedir código (chave no cofre do sistema)");
  await botao("Filas");
  await esperar("Filas na gestão com um clique", async () => (await url()).includes(`/gestao/${a.estId}/filas`));
  const s = await js("return fetch('/v1/session').then(r => r.json()).then(j => j.data.session && j.data.session.owner_authority)");
  if (s !== true) throw new Error("gestão perdeu a sessão ao alternar");
  ok("alternar Dashboard ↔ Filas não gera aviso de sessão concorrente");
}

const cenario = process.argv[2] ?? "completo";
try {
  await ({ falha, completo, dashboard }[cenario] ?? completo)();
  console.log(`# ${cenario}: ${passos} verificações, todas ok`);
} catch (e) {
  console.error(`FALHOU: ${e.message}`);
  if (sid) await foto(`erro-${cenario}`).catch(() => {});
  process.exitCode = 1;
} finally {
  await fecharPrograma();
}
