// Jornadas completas em navegador real contra a API real (com worker, SSE e simulador de Pix).
// Cliente e visitante no celular (360–390 px); equipe e dona no computador ou tablet.
import { expect, test, type Page, type Request } from "@playwright/test";
import {
  Api,
  vigiarCSP,
  atenderProximo,
  clienteVerificado,
  entrarNaTela,
  limparLimitesDoTeste,
  linkDoEmail,
  montarLoja,
  pixControle,
  senha,
  type Loja,
} from "./apoio";

vigiarCSP();

const celular = { viewport: { width: 360, height: 780 }, hasTouch: true };

async function entrarNaFilaPelaTela(page: Page) {
  const form = page.getByRole("form", { name: "Entrar na fila" });
  await form.getByRole("radio", { name: /Beto/ }).check();
  await form.getByRole("checkbox", { name: /Corte/ }).check();
  await form.getByRole("button", { name: "Entrar na fila" }).click();
  await expect(page.getByRole("heading", { name: /Sua senha:/ })).toBeVisible();
}

let loja: Loja;

test.beforeAll(async () => {
  limparLimitesDoTeste();
  loja = await montarLoja("jorn");
  await loja.dona.ok("PUT", `/v1/establishments/${loja.estId}/loyalty-program`, {
    expected_version: 1,
    enabled: true,
  });
  await loja.dona.ok("POST", `/v1/establishments/${loja.estId}/rewards`, {
    name: "Pomada",
    description: "Pomada modeladora",
    usage_instructions: "Peça na recepção. Não dá prioridade na fila.",
    cost_points: 10,
    stock: { kind: "limited", units_available: 5 },
  });
});

test("visitante no celular e Beto no computador: do link da loja ao pago, atualizando sozinho", async ({
  browser,
}) => {
  const vctx = await browser.newContext(celular);
  const sctx = await browser.newContext({ viewport: { width: 1024, height: 768 } });
  const v = await vctx.newPage();
  const s = await sctx.newPage();

  await v.goto(`/loja/${loja.slug}`);
  await v.getByLabel("Como quer ser chamado").fill("Lia");
  await v.getByRole("button", { name: "Continuar" }).click();
  await expect(v.getByText("Guarde seu código de recuperação.")).toBeVisible();
  await entrarNaFilaPelaTela(v);
  await expect(
    v
      .getByRole("status")
      .filter({ hasText: /Aguardando/ })
      .first(),
  ).toBeVisible();

  await entrarNaTela(s, loja.betoEmail, true);
  await s.goto(`/gestao/${loja.estId}/filas`);
  await s.getByRole("button", { name: "Chamar próximo" }).click();
  await expect(v.getByText(/Chamado! Dirija-se ao profissional/)).toBeVisible(); // pelo SSE, sem recarregar
  await s.getByRole("button", { name: "Iniciar atendimento" }).click();
  await expect(v.getByText("Em atendimento")).toBeVisible();
  await s.getByRole("button", { name: "Concluir atendimento" }).click();
  const concluir = s.getByRole("form", { name: "Concluir atendimento" });
  await concluir.getByRole("radio", { name: "realizado", exact: true }).check();
  await concluir.getByRole("button", { name: "Confirmar conclusão" }).click();

  await expect(v.getByText("Como você vai pagar?")).toBeVisible();
  // O meio só aparece escolhido depois da resposta do servidor (nada é marcado antes).
  await v.getByRole("radio", { name: "Dinheiro" }).click();
  await expect(v.getByRole("radio", { name: "Dinheiro" })).toBeChecked();
  await expect(v.getByText(/Pague R\$\s*45,00 na loja/)).toBeVisible();
  // Nada de "pago" antes do registro pela equipe.
  await expect(v.getByText("Pago. Obrigado!")).toHaveCount(0);

  await s.goto(`/gestao/${loja.estId}/comandas`);
  await s.getByLabel("Mostrar").selectOption("awaiting_payment");
  const receber = s.getByRole("form", { name: "Receber na loja" });
  await receber.getByLabel(/Valor entregue/).fill("50");
  await expect(receber.getByText(/Troco:/)).toContainText("5,00");
  await receber.getByRole("button", { name: /Registrar R\$\s*45,00 recebido e conferido/ }).click();
  await expect(v.getByText("Pago. Obrigado!")).toBeVisible();
  await vctx.close();
  await sctx.close();
});

test("cliente: cadastro, e-mail, Pix com rede caindo (sem cobrar duas vezes), pontos e prêmio", async ({
  browser,
}) => {
  const email = `maria-${loja.run}@exemplo.com`;
  const cctx = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true });
  const c = await cctx.newPage();

  await c.goto("/cadastro");
  await c.getByLabel("Nome").fill("Maria Cliente");
  await c.getByLabel("E-mail").fill(email);
  await c.getByLabel("Senha").fill(senha);
  await c.getByRole("button", { name: "Criar conta" }).click();
  await expect(
    c
      .getByRole("status")
      .filter({ hasText: /e-mail/i })
      .first(),
  ).toBeVisible();
  await c.goto(await linkDoEmail(email, "Confirme seu e-mail"));
  await expect(c.getByText("E-mail confirmado! Sua conta está ativa.")).toBeVisible();
  await entrarNaTela(c, email, false);

  await c.goto(`/loja/${loja.slug}`);
  await entrarNaFilaPelaTela(c);
  const order = await atenderProximo(loja);
  await c.getByRole("radio", { name: "Pix" }).click();
  await expect(c.getByRole("radio", { name: "Pix" })).toBeChecked();
  const gerar = c.getByRole("button", { name: /Gerar Pix de R\$\s*45,00/ });
  await expect(gerar).toBeVisible();

  // Toda tentativa de criar o Pix leva a mesma chave de idempotência.
  const keys: string[] = [];
  const onReq = (r: Request) => {
    if (r.method() === "POST" && r.url().endsWith("/pix-charges"))
      keys.push(r.headers()["idempotency-key"] ?? "");
  };
  c.on("request", onReq);

  // 1) Aparelho sem internet: aviso claro, comanda intacta.
  await cctx.setOffline(true);
  await gerar.click();
  await expect(c.getByText(/Sem internet neste aparelho/)).toBeVisible();
  await expect(c.getByRole("alert").filter({ hasText: /Sem conexão com o servidor/ })).toBeVisible();
  await expect(c.getByText(/Reconectando… os dados podem estar desatualizados/)).toBeVisible();
  await cctx.setOffline(false);
  await expect(c.getByText(/Sem internet neste aparelho/)).toHaveCount(0);

  // 2) A resposta se perde DEPOIS de o servidor criar a cobrança (pior caso).
  await c.route(
    "**/pix-charges",
    async (route) => {
      await route.fetch(); // o servidor processou
      await route.abort("connectionreset"); // mas a resposta não chegou ao celular
    },
    { times: 1 },
  );
  await gerar.click();

  // 3) Recuperação: ou a atualização em tempo real já traz o Pix criado, ou a pessoa repete e a
  //    MESMA chave faz o servidor devolver a MESMA cobrança. Nos dois casos: um Pix só.
  const qr = c.getByRole("img", { name: /QR Code do Pix de R\$\s*45,00/ });
  await expect(qr.or(gerar).first()).toBeVisible();
  if (await gerar.isVisible()) await gerar.click();
  await expect(qr).toBeVisible();
  await expect(c.getByRole("alert").filter({ hasText: /Sem conexão com o servidor/ })).toHaveCount(0);
  c.off("request", onReq);
  expect(keys.length).toBeGreaterThanOrEqual(2);
  expect(new Set(keys).size, `chaves usadas: ${keys.join(", ")}`).toBe(1);
  const atual = await loja.dona.ok("GET", `/v1/establishments/${loja.estId}/orders/${order.id}`);
  expect(atual.payment.charges).toHaveLength(1);
  await expect(c.getByText(/Reconectando/)).toHaveCount(0); // SSE voltou

  // O banco confirma (simulador) → o worker registra → a tela muda sozinha. Clique não confirma nada.
  await expect(c.getByText(/Pagamento recebido pelo banco/)).toHaveCount(0);
  await pixControle(`/control/set?externalId=${atual.payment.charges[0].id}&status=PAID`);
  await expect(c.getByText(/Pagamento recebido pelo banco/)).toBeVisible({ timeout: 40_000 });
  const pago = await loja.beto.ok("GET", `/v1/establishments/${loja.estId}/orders/${order.id}`);
  await loja.beto.ok(
    "POST",
    `/v1/establishments/${loja.estId}/orders/${order.id}/receipts/${pago.payment.receipts[0].id}/confer`,
    {},
  );
  await expect(c.getByText("Pago. Obrigado!")).toBeVisible();

  const saldo = c.getByRole("list", { name: "Saldo" });
  await expect(saldo.getByText("disponíveis").locator("..")).toContainText("10");
  await c.getByRole("button", { name: "Reservar Pomada" }).click();
  await expect(c.getByRole("status", { name: "Prêmio reservado" })).toContainText("Pomada");

  const sctx = await browser.newContext({ viewport: { width: 768, height: 1024 } });
  const s = await sctx.newPage();
  await entrarNaTela(s, loja.betoEmail, true);
  await s.goto(`/gestao/${loja.estId}/premios`);
  await s.getByRole("button", { name: "Confirmar entrega de Pomada" }).click();
  await expect(c.getByRole("list", { name: "Extrato de pontos" })).toContainText("Prêmio entregue");
  await expect(c.getByRole("status", { name: "Prêmio reservado" })).toHaveCount(0);
  await sctx.close();
  await cctx.close();
});

test("conta: senha esquecida, sessão encerrada em outro aparelho e exclusão com protocolo", async ({
  browser,
}) => {
  const email = `joao-${loja.run}@exemplo.com`;
  await clienteVerificado(email, "João Cliente");
  const ctx = await browser.newContext(celular);
  const p = await ctx.newPage();

  await p.goto("/esqueci-senha");
  await p.getByLabel("E-mail").fill(email);
  await p.getByRole("button", { name: "Enviar link" }).click();
  await p.goto(await linkDoEmail(email, "Redefinição de senha"));
  await p.getByLabel("Senha nova", { exact: true }).fill(senha);
  await p.getByRole("button", { name: "Salvar senha" }).click();
  await expect(p.getByText(/Senha alterada/)).toBeVisible();
  await entrarNaTela(p, email, false);

  // A sessão acaba em outro aparelho (troca de senha lá): a próxima ação aqui explica e não aplica.
  await p.goto("/conta");
  const outro = await Api.nova();
  await outro.login(email, "customer");
  await outro.ok("POST", "/v1/auth/password/change", { current_password: senha, new_password: senha + "x" });
  await outro.ok("POST", "/v1/auth/password/change", { current_password: senha + "x", new_password: senha });
  await p.getByLabel("Nome exibido").fill("João Novo");
  await p.getByRole("button", { name: "Salvar nome" }).click();
  await expect(p.getByText(/Sua sessão terminou e a ação não foi feita/)).toBeVisible();
  await expect(p.getByRole("link", { name: "Entrar", exact: true }).first()).toBeVisible();

  await entrarNaTela(p, email, false);
  await p.goto("/conta");
  await p.getByLabel("Nome exibido").fill("João Novo");
  await p.getByRole("button", { name: "Salvar nome" }).click();
  await expect(p.getByText("Nome atualizado.")).toBeVisible();

  await p.getByRole("button", { name: /Excluir conta|Quero excluir/ }).click();
  await p.getByRole("checkbox", { name: /Li e entendi/ }).check();
  await p.getByLabel("Sua senha (confirmação)").fill(senha);
  await p.getByRole("button", { name: "Excluir minha conta" }).click();
  await expect(p.getByRole("heading", { name: "Pedido de exclusão recebido" })).toBeVisible();
  const protocolo = (await p.getByLabel("Protocolo").textContent())?.trim() ?? "";
  expect(protocolo.length).toBeGreaterThan(15);
  await p.goto("/exclusao");
  await p.getByLabel("Protocolo").fill(protocolo);
  await p.getByRole("button", { name: "Consultar" }).click();
  // Sem pendências: "em processamento" até o worker concluir (em até 1 minuto), nunca "concluída" antes.
  await expect(p.getByText(/Em processamento|Concluída/).first()).toBeVisible();
  await ctx.close();
});

test("dona no tablet: convida e cancela convite (com senha), vê pendências e auditoria", async ({
  browser,
}) => {
  const ctx = await browser.newContext({ viewport: { width: 768, height: 1024 } });
  const p = await ctx.newPage();
  await loja.dona.ok("POST", `/v1/establishments/${loja.estId}/professionals`, {
    display_name: "Caio",
    queue_letter: "B",
  });
  await entrarNaTela(p, loja.donaEmail, true);
  await p.goto(`/gestao/${loja.estId}/profissionais`);
  const caio = p.getByRole("listitem").filter({ has: p.getByRole("heading", { name: /Caio/ }) });
  await caio.getByRole("button", { name: "Convidar" }).click();
  await caio.getByLabel("E-mail do profissional").fill(`caio-${loja.run}@exemplo.com`);
  await caio.getByRole("button", { name: "Enviar convite" }).click();
  // Convite exige senha recente: a tela pede a senha e repete a MESMA ação (mesma chave).
  const senhaDeNovo = caio.getByLabel("Sua senha", { exact: true });
  await expect(senhaDeNovo.or(caio.getByText(/convite pendente/)).first()).toBeVisible();
  if (await senhaDeNovo.isVisible()) {
    await senhaDeNovo.fill(senha);
    await caio.getByRole("button", { name: "Confirmar e continuar" }).click();
  }
  await expect(caio.getByText(/convite pendente/)).toBeVisible();
  await caio.getByRole("button", { name: "Cancelar convite" }).click();
  await expect(caio.getByText("sem acesso")).toBeVisible();

  await p.getByRole("link", { name: "Pendências" }).click();
  await expect(p.getByRole("list", { name: "Resumo" })).toBeVisible();
  await p.getByRole("link", { name: "Auditoria" }).click();
  await expect(p.getByText("T-IN-03").first()).toBeVisible();
  // Profissional não vê as abas do dono.
  const pctx = await browser.newContext({ viewport: { width: 1024, height: 768 } });
  const pp = await pctx.newPage();
  await entrarNaTela(pp, loja.betoEmail, true);
  await pp.goto(`/gestao/${loja.estId}/filas`);
  await expect(pp.getByRole("link", { name: "Auditoria" })).toHaveCount(0);
  await pp.goto(`/gestao/${loja.estId}/auditoria`);
  await expect(pp).toHaveURL(/\/filas$/);
  await pctx.close();
  await ctx.close();
});
