// Agenda da Fila 2.0 (ADR-0038) em navegador real contra a API real: os três modos de
// atendimento, a reserva pela interface e a foto do dono na gestão. Precisa da agenda liberada
// (FILA_AGENDA, padrão em desenvolvimento) e, para a foto, de FILA_MEDIA_STORAGE=filesystem.
import { expect, test, type Page } from "@playwright/test";
import { entrarNaTela, montarLoja, vigiarCSP, type Loja } from "./apoio";

vigiarCSP();

async function abrirSegundaACopiar(page: Page) {
  await page
    .getByRole("group", { name: /Segunda/ })
    .getByRole("button", { name: "Abrir neste dia" })
    .click();
  await page.getByRole("button", { name: /Copiar segunda/ }).click();
  for (const dia of [/Domingo/]) {
    await page.getByRole("group", { name: dia }).getByRole("button", { name: "Abrir neste dia" }).click();
  }
}

async function modo(loja: Loja, m: "queue" | "schedule" | "hybrid") {
  const st = await loja.dona.ok("GET", `/v1/establishments/${loja.estId}/settings`);
  await loja.dona.ok("PATCH", `/v1/establishments/${loja.estId}/settings/agenda`, {
    expected_version: st.agenda.version,
    service_mode: m,
  });
}

// Cenários: C-AG-01, C-AG-02.
test("três modos: fila (padrão), os dois e só horário marcado, com reserva pela interface", async ({
  page,
  browser,
}) => {
  const loja = await montarLoja("agenda");
  await entrarNaTela(page, loja.donaEmail, true, loja.dona);

  // Modo fila (padrão): a gestão abre em Filas e a página pública não oferece horário.
  await page.goto(`/gestao/${loja.estId}`);
  await expect(page).toHaveURL(/\/filas/);
  const cliente = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true });
  const c = await cliente.newPage();
  await c.goto(`/loja/${loja.slug}`);
  await expect(c.getByRole("heading", { name: `Barbearia ${loja.run}` })).toBeVisible();
  await expect(c.getByRole("link", { name: "Agendar horário" })).toHaveCount(0);

  // Configurações → Agenda: escolher "Os dois" mostra o impacto antes de salvar.
  await page.goto(`/gestao/${loja.estId}/loja?secao=agenda`);
  await page.getByLabel("Os dois").check();
  await page.getByRole("button", { name: "Salvar agenda" }).click();
  await expect(page.getByText(/reserva\(s\) futura\(s\)/)).toBeVisible();
  await page.getByRole("button", { name: "Mudar a forma de atendimento" }).click();
  await expect(page.getByText("Salvo.")).toBeVisible();

  // Horários: segunda 09–18, copiar para terça a sábado, abrir domingo.
  await page.goto(`/gestao/${loja.estId}/horarios`);
  await abrirSegundaACopiar(page);
  await page.getByRole("button", { name: "Salvar jornada" }).click();
  await expect(page.getByRole("button", { name: "Salvar jornada" })).toBeEnabled();
  await expect
    .poll(
      async () =>
        (await loja.dona.ok("GET", `/v1/establishments/${loja.estId}/agenda/hours`)).store.current?.shifts
          .length,
    )
    .toBe(7);

  // Cliente (sem conta) agenda pela interface: profissional → serviço → amanhã → horário → revisão.
  await c.goto(`/loja/${loja.slug}`);
  await expect(c.getByRole("link", { name: "Entrar na fila" })).toBeVisible();
  await c.getByRole("link", { name: "Agendar horário" }).first().click();
  await c.getByRole("button", { name: /Beto/ }).click();
  await c.getByRole("button", { name: /Corte/ }).first().click();
  await c.getByRole("button", { name: "Escolher dia e horário" }).click();
  await c.getByRole("button", { name: "Amanhã" }).click();
  await c.getByRole("group", { name: "Horário" }).getByRole("button").first().click();
  await expect(c.getByRole("heading", { name: "Revise antes de confirmar" })).toBeVisible();
  await expect(c.getByText("Reserva gratuita: nada a pagar agora.")).toBeVisible();
  await c.getByLabel("Apelido").fill("Ana");
  await c.getByRole("button", { name: "Continuar" }).click();
  await c.getByRole("button", { name: /^Confirmar / }).click();
  await expect(c.getByText("Horário confirmado")).toBeVisible();
  await c.getByRole("link", { name: "Ver minhas reservas" }).click();
  await expect(c.getByRole("heading", { name: "Próximos horários" })).toBeVisible();
  await expect(c.getByRole("button", { name: "Cancelar" })).toBeVisible();

  // A gestão (modo "os dois") abre na Agenda; amanhã aparece a reserva com as ações.
  await page.goto(`/gestao/${loja.estId}`);
  await expect(page).toHaveURL(/\/agenda/);
  await page.getByRole("tab", { name: "Por dia" }).click();
  await page.getByRole("button", { name: "Amanhã" }).click();
  const cartao = page.getByRole("article").filter({ hasText: "Ana" });
  await expect(cartao.getByRole("button", { name: "Registrar chegada" })).toBeVisible();
  await expect(cartao.getByRole("button", { name: "Chamar" })).toBeVisible();

  // Só horário marcado: a página pública abre direto no agendamento e a fila fecha à entrada.
  await modo(loja, "schedule");
  const outro = await browser.newContext();
  const o = await outro.newPage();
  await o.goto(`/loja/${loja.slug}`);
  await expect(o.getByRole("heading", { name: "Com quem?" })).toBeVisible();
  await expect(o.getByRole("link", { name: "Entrar na fila" })).toHaveCount(0);
  await outro.close();
  await cliente.close();
});

// Cenários: C-AG-13.
test("foto do dono aparece nos dois links Minha conta e some ao remover", async ({ page }) => {
  const loja = await montarLoja("avatar");
  await modo(loja, "hybrid");
  await entrarNaTela(page, loja.donaEmail, true, loja.dona);
  const png = await page.evaluate(() => {
    const c = document.createElement("canvas");
    c.width = c.height = 400;
    const x = c.getContext("2d");
    if (x) {
      x.fillStyle = "#2a7";
      x.fillRect(0, 0, 400, 400);
    }
    return c.toDataURL("image/png").split(",")[1] ?? "";
  });
  await page.goto("/conta");
  await page
    .locator('input[type="file"]')
    .first()
    .setInputFiles({ name: "foto.png", mimeType: "image/png", buffer: Buffer.from(png, "base64") });
  await page.getByRole("button", { name: "Salvar foto" }).click({ timeout: 30_000 });
  await expect(page.getByText("Foto salva.")).toBeVisible({ timeout: 30_000 });
  await page.goto(`/gestao/${loja.estId}/agenda`);
  await expect(page.locator('aside a[href="/conta"] img')).toBeVisible();
  await page.reload();
  await expect(page.locator('aside a[href="/conta"] img')).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('header a[href="/conta"] img')).toBeVisible();
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/conta");
  await page
    .getByRole("button", { name: /Remover foto/ })
    .first()
    .click();
  const confirmar = page.getByRole("button", { name: /^Remover/ });
  if ((await confirmar.count()) > 1) await confirmar.last().click();
  await page.goto(`/gestao/${loja.estId}/agenda`);
  await expect(page.getByRole("heading", { name: "Agenda" })).toBeVisible();
  await expect(page.locator('aside a[href="/conta"] img')).toHaveCount(0);
});
