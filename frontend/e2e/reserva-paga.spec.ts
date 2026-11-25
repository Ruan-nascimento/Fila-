// Reserva com Pix da Fila 2.0 (ADR-0039) em navegador real contra a API real e o SIMULADOR local de
// Pix (E2E_PIX_CONTROL; nunca a AbacatePay). Precisa de FILA_AGENDA, FILA_AGENDA_COBRANCA e o Pix
// apontando para o simulador.
import { expect, test } from "@playwright/test";
import { entrarNaTela, montarLoja, pixControle, vigiarCSP } from "./apoio";

vigiarCSP();

// Cenários: C-BK-01, C-BK-08.
test("sinal pelo Pix: dona liga, cliente reserva, paga e a equipe vê o sinal", async ({ page, browser }) => {
  const loja = await montarLoja("sinal");
  const est = `/v1/establishments/${loja.estId}`;
  // Preparação pela API: "os dois", jornada 09–18 todos os dias e Pix ligado na loja.
  let st = await loja.dona.ok("GET", `${est}/settings`);
  await loja.dona.ok("PATCH", `${est}/settings/agenda`, {
    expected_version: st.agenda.version,
    service_mode: "hybrid",
  });
  st = await loja.dona.ok("GET", `${est}/settings`);
  if (!st.payments.pix_enabled)
    await loja.dona.ok("PATCH", `${est}/settings/payments`, {
      expected_version: st.payments.version,
      pix_enabled: true,
    });
  const hoje = new Date().toLocaleDateString("en-CA", { timeZone: "America/Sao_Paulo" });
  await loja.dona.ok("PUT", `${est}/agenda/hours`, {
    effective_from: hoje,
    shifts: [0, 1, 2, 3, 4, 5, 6].map((d) => ({
      weekday: d,
      start_minute: 540,
      end_minute: 1080,
      modality: "both",
    })),
  });

  // Dona liga o sinal de R$ 15 pela interface.
  await entrarNaTela(page, loja.donaEmail, true, loja.dona);
  await page.goto(`/gestao/${loja.estId}/loja?secao=agenda`);
  await page.getByLabel("Sinal pelo Pix").check();
  await page.getByLabel("Valor do sinal (R$)").fill("15");
  await page.getByRole("button", { name: "Salvar agenda" }).click();
  await expect(page.getByText("Salvo.")).toBeVisible();

  // Cliente sem conta: revisão mostra o que paga agora e o que fica para a barbearia.
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true });
  const c = await ctx.newPage();
  await c.goto(`/loja/${loja.slug}/agendar`);
  await c.getByRole("button", { name: /Beto/ }).click();
  await c.getByRole("button", { name: /Corte/ }).first().click();
  await c.getByRole("button", { name: "Escolher dia e horário" }).click();
  await c.getByRole("button", { name: "Amanhã" }).click();
  await c.getByRole("group", { name: "Horário" }).getByRole("button").first().click();
  await expect(c.getByText("Agora, pelo Pix: R$ 15,00")).toBeVisible();
  await expect(c.getByText(/Esse valor será descontado do atendimento/)).toBeVisible();
  await c.getByLabel("Apelido").fill("Ana");
  await c.getByRole("button", { name: "Continuar" }).click();
  await c.getByRole("button", { name: /e pagar R\$ 15,00/ }).click();
  await expect(c.getByText("Falta pagar para garantir o horário")).toBeVisible();
  await c.getByRole("button", { name: "Gerar Pix de R$ 15,00" }).click();
  await expect(c.getByRole("button", { name: "Copiar código" })).toBeVisible();

  // O banco confirma (simulador); a tela atualiza sozinha pelo evento da reserva.
  const minhas = await (await c.request.get("/v1/my-appointments?scope=upcoming")).json();
  const chargeId = minhas.data[0].appointment.payment.booking.charge.id;
  await pixControle(`/control/set?externalId=${chargeId}&status=PAID`);
  await expect(c.getByText("Horário confirmado")).toBeVisible({ timeout: 30_000 });
  await expect(c.getByText("Sinal de R$ 15,00 pago pelo Pix.")).toBeVisible();

  // Minhas reservas: cancelar dentro da janela explica a devolução antes de confirmar.
  await c.getByRole("link", { name: "Ver minhas reservas" }).click();
  await c.getByRole("button", { name: "Cancelar" }).click();
  await expect(c.getByText(/é devolvido pelo Pix/)).toBeVisible();
  await c.getByRole("button", { name: "Manter" }).click();

  // Equipe: a agenda mostra o sinal pago e quanto fica para a barbearia.
  await page.goto(`/gestao/${loja.estId}/agenda`);
  await page.getByRole("tab", { name: "Por dia" }).click();
  await page.getByRole("button", { name: "Amanhã" }).click();
  const cartao = page.getByRole("article").filter({ hasText: "Ana" });
  await expect(cartao.getByText(/Sinal de R\$\s15,00 pago · na barbearia/)).toBeVisible();
  await ctx.close();
});
