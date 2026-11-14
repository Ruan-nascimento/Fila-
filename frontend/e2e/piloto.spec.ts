// Roteiro real do piloto (etapa 13): um dia de barbearia em navegador real contra a API real
// (homologação ou ambiente local). Cada passo confere o que a PESSOA vê na tela. Ações da equipe
// pela tela quando é o que ela faria no balcão; algumas pela API (mesmas regras), marcadas aqui.
//
// Passos: visitante entra e recupera em outro aparelho · recarregar mantém o estado · conta entra
// e muda o pedido · nick com HTML aparece como texto · chamada e ausência · rede cai e volta na
// chamada · atendimento, Pix recebido e conferido · pontos creditados e prêmio resgatado ·
// interrupção revisada pela dona · Pix tardio vira pendência que a dona resolve.
import { execFileSync } from "node:child_process";
import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";
import {
  Api,
  clienteVerificado,
  entrarNaTela,
  limparLimitesDoTeste,
  montarLoja,
  pixControle,
  vigiarCSP,
  type Loja,
} from "./apoio";

vigiarCSP();
test.describe.configure({ mode: "serial", timeout: 300_000 });

const celular = {
  viewport: { width: 360, height: 780 },
  hasTouch: true,
  ignoreHTTPSErrors: process.env.E2E_IGNORAR_TLS === "1",
};
const balcao = {
  viewport: { width: 1024, height: 768 },
  ignoreHTTPSErrors: process.env.E2E_IGNORAR_TLS === "1",
};

function psql(sql: string): string {
  const psqlBin = process.env.E2E_PSQL;
  const db = process.env.E2E_DB_URL;
  if (!psqlBin || !db)
    throw new Error("defina E2E_PSQL e E2E_DB_URL (simular a passagem dos 10 minutos de tolerância)");
  return execFileSync(psqlBin, [db, "-Atc", sql], { encoding: "utf-8" }).trim();
}

async function abrir(browser: Browser, opts: object): Promise<[BrowserContext, Page]> {
  const ctx = await browser.newContext(opts);
  const page = await ctx.newPage();
  // Nenhum script injetado pode rodar (nick com HTML): qualquer alert/confirm reprova.
  page.on("dialog", (d) => {
    throw new Error(`diálogo inesperado (script executado?): ${d.message()}`);
  });
  return [ctx, page];
}

async function entrarNaFila(page: Page, servicos: RegExp[]) {
  const form = page.getByRole("form", { name: "Entrar na fila" });
  await form.getByRole("radio", { name: /Beto/ }).check();
  for (const s of servicos) await form.getByRole("checkbox", { name: s }).check();
  await form.getByRole("button", { name: "Entrar na fila" }).click();
  await expect(page.getByRole("heading", { name: /Sua senha:/ })).toBeVisible();
}

let loja: Loja;

test.beforeAll(async () => {
  limparLimitesDoTeste();
  loja = await montarLoja("pil");
  const est = `/v1/establishments/${loja.estId}`;
  await loja.dona.ok("PUT", `${est}/loyalty-program`, { expected_version: 1, enabled: true });
  await loja.dona.ok("POST", `${est}/rewards`, {
    name: "Pomada",
    description: "Pomada modeladora",
    usage_instructions: "Peça na recepção. Não dá prioridade na fila.",
    cost_points: 10,
    stock: { kind: "limited", units_available: 5 },
  });
});

// Cenários: C-VIS-01, C-VIS-04, C-SSE-03, C-SSE-04, C-SEG-07, C-FILA-09, C-ATD-03.
test("roteiro do piloto: do primeiro cliente às pendências da dona", async ({ browser }) => {
  const est = `/v1/establishments/${loja.estId}`;
  const passos: string[] = [];
  const ok = (p: string) => passos.push(p);

  // 1. Visitante entra pelo link da loja e guarda o código de recuperação.
  const [lctx, lia] = await abrir(browser, celular);
  await lia.goto(`/loja/${loja.slug}`);
  await lia.getByLabel("Como quer ser chamado").fill("Lia");
  await lia.getByRole("button", { name: "Continuar" }).click();
  await expect(lia.getByText("Guarde seu código de recuperação.")).toBeVisible();
  const codigo = (await lia
    .getByText(/^[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$/)
    .textContent())!.trim();
  await entrarNaFila(lia, [/Corte/]);
  const senhaLia = (await lia.getByRole("heading", { name: /Sua senha:/ }).textContent())!
    .replace(/.*Sua senha:\s*/, "")
    .trim();
  ok("visitante entrou com nick e recebeu código");

  // 2. Recupera a visita em OUTRO aparelho com o código; o aparelho antigo perde o acesso.
  const [l2ctx, lia2] = await abrir(browser, celular);
  await lia2.goto(`/loja/${loja.slug}`);
  await lia2.getByLabel("Código de recuperação").fill(codigo);
  await lia2.getByRole("button", { name: "Recuperar" }).click();
  await expect(lia2.getByRole("heading", { name: `Sua senha: ${senhaLia}` })).toBeVisible();
  await lctx.close();
  // Recarregar a página reconstrói o mesmo estado a partir do banco (C-SSE-03).
  await lia2.reload();
  await expect(lia2.getByRole("heading", { name: `Sua senha: ${senhaLia}` })).toBeVisible();
  await expect(lia2.getByText(/você é o 1º da fila/)).toBeVisible();
  ok("visita recuperada em outro aparelho; recarregar mostra o mesmo estado");

  // 3. Conta entra pela tela e muda o pedido enquanto aguarda (Corte → Corte + Barba).
  const emailMaria = `maria-${loja.run}@exemplo.com`;
  await clienteVerificado(emailMaria, "Maria");
  const [mctx, maria] = await abrir(browser, { ...celular, viewport: { width: 390, height: 844 } });
  await entrarNaTela(maria, emailMaria, false);
  await maria.goto(`/loja/${loja.slug}`);
  await entrarNaFila(maria, [/Corte/]);
  await expect(maria.getByText(/você é o 2º da fila/)).toBeVisible();
  await maria.getByRole("button", { name: "Alterar serviços" }).click();
  const alterar = maria.getByRole("form", { name: "Alterar serviços" });
  await alterar.getByRole("checkbox", { name: /Barba/ }).check();
  await alterar.getByRole("button", { name: "Salvar serviços" }).click();
  await expect(maria.getByText(/R\$\s*75,00/).first()).toBeVisible();
  ok("conta entrou e mudou o pedido (R$ 45 → R$ 75) enquanto aguardava");

  // 4. Nick com HTML: aparece como texto na tela da equipe; nenhum script roda.
  const xss = await Api.nova();
  await xss.ok("POST", `${est}/visitor-sessions`, { nick: "<img src=x onerror=alert(1)>" });
  await xss.ok("POST", `${est}/participations`, { queue_id: loja.filaBeto, service_ids: [loja.corte] });
  const [sctx, beto] = await abrir(browser, balcao);
  await entrarNaTela(beto, loja.betoEmail, true);
  await beto.goto(`/gestao/${loja.estId}/filas`);
  await expect(beto.getByText("<img src=x onerror=alert(1)>", { exact: false }).first()).toBeVisible();
  expect(await beto.locator("img[src='x']").count()).toBe(0);
  ok("nick com HTML exibido como texto, sem executar");

  // 5. Beto chama a Lia; ela não aparece; depois dos 10 minutos de tolerância, ausência.
  await beto.getByRole("button", { name: "Chamar próximo" }).click();
  await expect(lia2.getByText(/Chamado! Dirija-se ao profissional/)).toBeVisible();
  // Simula a passagem do tempo no banco da homologação (a regra de 10 minutos é do servidor).
  psql(
    `UPDATE participation SET called_at = now() - interval '11 minutes', tolerance_ends_at = now() - interval '1 minute'
     WHERE establishment_id = '${loja.estId}' AND status = 'called'`,
  );
  await beto.reload();
  await beto.getByRole("button", { name: "Marcar ausência" }).click();
  await expect(lia2.getByText(/ausente|Ausente|ausência/).first()).toBeVisible();
  ok("chamada chegou sozinha; ausência só depois da tolerância");

  // 6. Rede da Maria cai; Beto a chama; a rede volta e a tela mostra a chamada sozinha.
  await mctx.setOffline(true);
  await beto.getByRole("button", { name: "Chamar próximo" }).click();
  await expect(maria.getByText(/Reconectando/)).toBeVisible();
  await mctx.setOffline(false);
  await expect(maria.getByText(/Chamado! Dirija-se ao profissional/)).toBeVisible({ timeout: 40_000 });
  await expect(maria.getByText(/Reconectando/)).toHaveCount(0);
  ok("rede caiu e voltou: a chamada apareceu depois de reconectar, sem recarregar");

  // 7. Atendimento concluído pela tela do Beto; Maria paga por Pix; o banco confirma; Beto confere.
  await beto.getByRole("button", { name: "Iniciar atendimento" }).click();
  await beto.getByRole("button", { name: "Concluir atendimento" }).click();
  const concluir = beto.getByRole("form", { name: "Concluir atendimento" });
  for (const r of await concluir.getByRole("radio", { name: "realizado", exact: true }).all())
    await r.check();
  await concluir.getByRole("button", { name: "Confirmar conclusão" }).click();
  await maria.getByRole("radio", { name: "Pix" }).click();
  await maria.getByRole("button", { name: /Gerar Pix de R\$\s*75,00/ }).click();
  await expect(maria.getByRole("img", { name: /QR Code do Pix de R\$\s*75,00/ })).toBeVisible();
  const visita = await loja.dona.ok("GET", `${est}/orders?status=awaiting_payment`);
  const ordem = visita.find((o: any) => o.total.amount_cents === 7500);
  const cobranca = (await loja.dona.ok("GET", `${est}/orders/${ordem.id}`)).payment.charges[0];
  await pixControle(`/control/set?externalId=${cobranca.id}&status=PAID`);
  await expect(maria.getByText(/Pagamento recebido pelo banco/)).toBeVisible({ timeout: 40_000 });
  const pago = await loja.beto.ok("GET", `${est}/orders/${ordem.id}`);
  await loja.beto.ok("POST", `${est}/orders/${ordem.id}/receipts/${pago.payment.receipts[0].id}/confer`, {}); // pela API
  await expect(maria.getByText("Pago. Obrigado!")).toBeVisible();
  ok("atendimento concluído; Pix recebido pelo banco e conferido pela equipe");

  // 8. Pontos creditados (Corte 10 + Barba 5) e prêmio resgatado e entregue.
  const saldo = maria.getByRole("list", { name: "Saldo" });
  await expect(saldo.getByText("disponíveis").locator("..")).toContainText("15");
  await maria.getByRole("button", { name: "Reservar Pomada" }).click();
  await expect(maria.getByRole("status", { name: "Prêmio reservado" })).toContainText("Pomada");
  await beto.goto(`/gestao/${loja.estId}/premios`);
  await beto.getByRole("button", { name: "Confirmar entrega de Pomada" }).click();
  await expect(maria.getByRole("list", { name: "Extrato de pontos" })).toContainText("Prêmio entregue");
  ok("15 pontos creditados uma vez; prêmio reservado e entregue");

  // 9. Interrupção (pela API, como a tela do Beto faz) e revisão da dona (pela API): Corte feito
  //    inteiro, cobra só ele; a visita do nick com HTML.
  const chamado = await loja.beto.ok("POST", `${est}/queues/${loja.filaBeto}/call-next`);
  const iniciado = await loja.beto.ok("POST", `${est}/participations/${chamado.participation.id}/start`, {
    expected_version: chamado.participation.version,
  });
  const oi = await loja.beto.ok("GET", `${est}/orders/${iniciado.participation.order_id}`);
  await loja.beto.ok("POST", `${est}/participations/${chamado.participation.id}/interrupt`, {
    expected_version: iniciado.participation.version,
    order_expected_version: oi.version,
    reason: "cliente precisou sair",
    items: oi.items.map((i: any) => ({ item_id: i.id, outcome: "fully_performed" })),
  });
  ok("atendimento interrompido com motivo; cadeira liberada");

  // 10. Pix tardio: Rui gera Pix, paga em dinheiro (troca de meio) e depois o Pix cai no banco.
  const rui = await Api.nova();
  await rui.ok("POST", `${est}/visitor-sessions`, { nick: "Rui" });
  await rui.ok("POST", `${est}/participations`, { queue_id: loja.filaBeto, service_ids: [loja.corte] });
  const cr = await loja.beto.ok("POST", `${est}/queues/${loja.filaBeto}/call-next`);
  const ir = await loja.beto.ok("POST", `${est}/participations/${cr.participation.id}/start`, {
    expected_version: cr.participation.version,
  });
  const or = await loja.beto.ok("GET", `${est}/orders/${ir.participation.order_id}`);
  const fr = await loja.beto.ok("POST", `${est}/participations/${cr.participation.id}/complete`, {
    expected_version: ir.participation.version,
    order_expected_version: or.version,
    items: or.items.map((i: any) => ({ item_id: i.id, outcome: "performed" })),
  });
  const pixRui = await rui.ok("POST", `${est}/orders/${fr.order.id}/pix-charges`, {
    expected_version: fr.order.version,
  });
  const ord2 = await loja.beto.ok("GET", `${est}/orders/${fr.order.id}`);
  const troca = await loja.beto.ok("POST", `${est}/orders/${fr.order.id}/payment-method`, {
    expected_version: ord2.version,
    method: "cash",
    acknowledge_active_pix: true,
  });
  await loja.beto.ok("POST", `${est}/orders/${fr.order.id}/receipts`, {
    expected_version: troca.version,
    method: "cash",
    amount_cents: 4500,
    cash_tendered_cents: 5000,
  });
  await pixControle(`/control/set?externalId=${pixRui.id}&status=PAID`);

  // 11. A dona revisa as pendências pela tela: vê a interrupção para revisar e o Pix tardio.
  const [dctx, dona] = await abrir(browser, { ...balcao, viewport: { width: 1440, height: 900 } });
  await entrarNaTela(dona, loja.donaEmail, true);
  await dona.goto(`/gestao/${loja.estId}/pendencias`);
  await expect(dona.getByText(/Pix pago depois de trocar o meio/).first()).toBeVisible({ timeout: 60_000 });
  const resumo = dona.getByRole("list", { name: "Resumo" });
  await expect(resumo).toContainText(/Interrupç/);
  // Há duas pendências do Rui (troca de meio com Pix em aberto, e o Pix tardio): a dona trata a do Pix tardio.
  const tardio = dona.getByRole("listitem").filter({ hasText: /Pix pago depois de trocar o meio/ });
  await tardio.getByLabel(/Como foi tratada/).fill("devolvido em dinheiro no balcão");
  await tardio.getByRole("button", { name: "Marcar como resolvida" }).click();
  // Resolvida: some da lista de abertas (ou aparece como resolvida), e o servidor guarda a nota.
  await expect(tardio.getByRole("button", { name: "Marcar como resolvida" })).toHaveCount(0);
  await expect
    .poll(async () => {
      const lista = await loja.dona.ok("GET", `${est}/financial-issues?status=resolved`);
      return lista.find((i: any) => i.kind === "late_receipt")?.resolution_note ?? null;
    })
    .toBe("devolvido em dinheiro no balcão");
  // Revisão da interrupção (pela API): só o Corte feito inteiro é cobrado.
  const revisao = await loja.dona.ok("GET", `${est}/orders/${iniciado.participation.order_id}`);
  await loja.dona.ok("POST", `${est}/orders/${revisao.id}/close-interruption`, {
    expected_version: revisao.version,
    items: revisao.items.map((i: any) => ({ item_id: i.id, outcome: "fully_performed" })),
    review_note: "conferido com o Beto",
  });
  ok("dona viu e resolveu o Pix tardio pela tela; revisou a interrupção");

  for (const c of [l2ctx, mctx, sctx, dctx]) await c.close();
  test.info().annotations.push({ type: "passos", description: passos.join(" | ") });
  console.log("ROTEIRO:\n- " + passos.join("\n- "));
});
