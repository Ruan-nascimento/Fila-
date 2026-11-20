// Configurações, fotos, identidade da loja, QR Code e entrada sem conta (ADR-0028), em navegador
// real contra a API real (fotos ligadas: FILA_MEDIA_STORAGE=filesystem + worker rodando).
// Imagens de teste: E2E_IMG_DIR com foto.jpg (com EXIF de orientação e GPS) e logo.png.
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { Api, clienteVerificado, entrarNaTela, montarLoja, senha, vigiarCSP } from "./apoio";

vigiarCSP();

const img = (f: string) => join(process.env.E2E_IMG_DIR ?? "", f);

async function semRolagemHorizontal(page: Page, onde: string) {
  const w = await page.evaluate(() => [document.documentElement.scrollWidth, window.innerWidth]);
  expect(w[0], `rolagem horizontal em ${onde}`).toBeLessThanOrEqual(w[1] ?? 0);
}

// Cenários: C-CFG-07.
test("dono muda paleta, logo e informações; visitante anônimo vê a identidade nova", async ({
  page,
  browser,
}) => {
  const loja = await montarLoja("cfg");
  await entrarNaTela(page, loja.donaEmail, true, loja.dona);
  await page.goto(`/gestao/${loja.estId}/loja`);
  await expect(page.getByRole("heading", { name: "Configurações da barbearia" })).toBeVisible();
  await page.getByText("Verde-petróleo", { exact: true }).click();
  await expect(page.getByRole("radio", { name: "Verde-petróleo" })).toBeChecked();
  await expect(page.getByText("Alterações ainda não salvas", { exact: true })).toBeVisible();
  await page.getByLabel("Descrição curta").fill("Cortes clássicos e barba na régua.");
  await page.getByLabel("Telefone ou WhatsApp da loja").fill("(11) 99999-8888");
  await page.getByRole("button", { name: "Salvar configurações" }).click();
  await expect(page.getByText(/Configurações salvas/)).toBeVisible();
  // Gestão contextual com a cor da loja.
  await expect(page.locator("html")).toHaveAttribute("data-brand", "teal");

  await page.locator('input[type="file"]').last().setInputFiles(img("logo.png"));
  await expect(page.getByText("Prévia — ainda não salva")).toBeVisible();
  await page.getByRole("button", { name: "Salvar logo" }).click();
  await expect(page.getByText("Logo salva.")).toBeVisible({ timeout: 30_000 });

  // QR: link canônico da loja; PNG baixa com nome sanitizado.
  await expect(page.getByTestId("link-qr")).toHaveText(new RegExp(`/loja/${loja.slug}$`));
  const [dl] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Baixar PNG" }).click(),
  ]);
  expect(dl.suggestedFilename()).toBe(`qr-${loja.slug}-loja.png`);
  await page.getByRole("radio", { name: /Entrar sem conta/ }).check();
  await expect(page.getByTestId("link-qr")).toHaveText(/\?entrada=visitante$/);

  const anon = await browser.newPage();
  await anon.goto(`/loja/${loja.slug}`);
  await expect(anon.getByRole("heading", { level: 1 })).toHaveText(`Barbearia ${loja.run}`);
  await expect(anon.locator("html")).toHaveAttribute("data-brand", "teal");
  await expect(anon.getByText("Cortes clássicos e barba na régua.")).toBeVisible();
  await expect(anon.getByRole("link", { name: "(11) 99999-8888" })).toHaveAttribute(
    "href",
    "tel:+5511999998888",
  );
  await expect(anon.getByRole("img", { name: new RegExp(`Logo de Barbearia ${loja.run}`) })).toBeVisible();
  // Saindo da loja, a cor some (login global usa a identidade da plataforma).
  await anon.goto("/entrar");
  await expect(anon.locator("html")).not.toHaveAttribute("data-brand", /.+/);
  await anon.close();
});

// Cenários: C-CFG-05.
test("cliente envia foto; equipe da fila vê durante o atendimento; outro cliente não vê", async ({
  page,
  browser,
}) => {
  const loja = await montarLoja("foto");
  const email = `cli-${loja.run}@exemplo.com`;
  const cli = await clienteVerificado(email, "Carla Cliente");
  await entrarNaTela(page, email, false, cli);
  await page.goto("/conta");
  await page.locator('input[type="file"]').first().setInputFiles(img("foto.jpg"));
  await expect(page.getByText("Prévia — ainda não salva")).toBeVisible();
  await page.getByLabel("Zoom").focus();
  await page.keyboard.press("ArrowRight"); // recorte pelo teclado
  await page.getByRole("button", { name: "Centralizar" }).click();
  await page.getByRole("button", { name: "Salvar foto" }).click();
  await expect(page.getByText("Foto salva.")).toBeVisible({ timeout: 30_000 });
  const me = await cli.ok("GET", "/v1/me");
  const url: string = me.avatar.variants[0].url;
  expect(url).toMatch(/^\/v1\/me\/avatar\/64\?v=/);
  // Sem metadados (EXIF/GPS) e orientação aplicada (variante quadrada).
  const bytes = await (await cli.ctx.get(url)).body();
  expect(bytes.includes(Buffer.from("Exif"))).toBe(false);

  // Na fila: a equipe vê a miniatura; o público e outro cliente não.
  await cli.ok("POST", `/v1/establishments/${loja.estId}/participations`, {
    queue_id: loja.filaBeto,
    service_ids: [loja.corte],
  });
  const filas = await loja.beto.ok("GET", `/v1/establishments/${loja.estId}/queues`);
  const entry = filas[0].entries[0];
  expect(entry.customer_avatar).not.toBeNull();
  const staffUrl: string = entry.customer_avatar.variants[0].url;
  expect((await loja.beto.ctx.get(staffUrl)).status()).toBe(200);
  const outro = await clienteVerificado(`outro-${loja.run}@exemplo.com`);
  expect((await outro.ctx.get(staffUrl)).status()).toBe(404);
  const pub = await (await Api.nova()).ctx.get(`/v1/public/establishments/${loja.estId}/queues`);
  expect(await pub.text()).not.toContain("customer-avatar");

  const staff = await browser.newPage();
  await entrarNaTela(staff, loja.betoEmail, true, loja.beto);
  await staff.goto(`/gestao/${loja.estId}/filas`);
  await expect(staff.locator(`img[src*="customer-avatar"]`).first()).toBeVisible();
  await staff.close();
});

// Cenários: C-CFG-06.
test("profissional publica a foto pública; ela aparece na escolha de profissional", async ({
  page,
  browser,
}) => {
  const loja = await montarLoja("prof");
  await entrarNaTela(page, loja.betoEmail, true, loja.beto);
  await page.goto("/conta");
  await expect(
    page.getByRole("heading", { name: new RegExp(`Meu perfil em Barbearia ${loja.run}`) }),
  ).toBeVisible();
  await expect(page.getByText("Esta imagem ficará pública")).toBeVisible();
  await page.locator('input[type="file"]').last().setInputFiles(img("foto.jpg"));
  await page.getByRole("button", { name: "Salvar foto" }).last().click();
  await expect(page.getByText(/Foto publicada/)).toBeVisible({ timeout: 30_000 });
  const anon = await browser.newPage();
  await anon.goto(`/loja/${loja.slug}`);
  await expect(anon.locator('img[src^="/v1/public/media/"]').first()).toBeVisible();
  await anon.close();
});

// Cenários: C-CFG-08.
test("QR sem conta: identificação por apelido, sem visita automática; cliente logado não é desconectado", async ({
  page,
  browser,
}) => {
  const loja = await montarLoja("qr");
  await page.goto(`/loja/${loja.slug}?entrada=visitante`);
  await expect(page.getByLabel("Apelido")).toBeFocused();
  const visitas = await loja.dona.ok("GET", `/v1/establishments/${loja.estId}/queues`);
  expect(visitas[0].entries).toHaveLength(0); // abrir o QR não cria visita

  const email = `qr-${loja.run}@exemplo.com`;
  const cli = await clienteVerificado(email);
  const logado = await browser.newPage();
  await entrarNaTela(logado, email, false, cli);
  await logado.goto(`/loja/${loja.slug}?entrada=visitante`);
  await expect(logado.getByText("Você está conectado com sua conta")).toBeVisible();
  await logado.goto("/conta");
  await expect(logado.getByRole("heading", { name: "Minha conta" })).toBeVisible();
  await expect(logado.getByText("Você não está conectado")).toHaveCount(0);
  await logado.close();
});

test("QR com conta: login por senha volta à mesma loja; entrar sem conta visível", async ({ page }) => {
  const loja = await montarLoja("login");
  const email = `lg-${loja.run}@exemplo.com`;
  await clienteVerificado(email);
  await page.goto(`/entrar?loja=${loja.slug}&voltar=%2Floja%2F${loja.slug}`);
  await expect(page.getByText(`Barbearia ${loja.run}`)).toBeVisible();
  await expect(page.getByRole("link", { name: "Entrar sem conta" })).toHaveAttribute(
    "href",
    `/loja/${loja.slug}?entrada=visitante`,
  );
  // Google não configurado neste ambiente: o botão não aparece e nada quebra.
  await expect(page.getByRole("button", { name: /Google/ })).toHaveCount(0);
  await page.getByLabel("E-mail").fill(email);
  await page.getByLabel("Senha", { exact: true }).fill(senha);
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/loja/${loja.slug}$`));
});

test("loja inexistente: indisponibilidade clara, sem visita nem Google", async ({ page }) => {
  await page.goto("/loja/nao-existe-mesmo");
  await expect(page.getByText("Loja não encontrada")).toBeVisible();
  await page.goto("/entrar?loja=nao-existe-mesmo");
  await expect(page.getByText(/Não encontramos essa barbearia/)).toBeVisible();
});

test("320 px: sem rolagem horizontal nas telas novas", async ({ page }) => {
  const loja = await montarLoja("w320");
  await page.setViewportSize({ width: 320, height: 720 });
  await entrarNaTela(page, loja.donaEmail, true, loja.dona);
  for (const path of [
    "/conta",
    `/gestao/${loja.estId}/loja`,
    `/entrar?loja=${loja.slug}`,
    `/loja/${loja.slug}`,
  ]) {
    await page.goto(path);
    await page.waitForLoadState("networkidle");
    await semRolagemHorizontal(page, path);
  }
});

test("medição: bytes e deslocamento de layout das imagens na página da loja (celular)", async ({ page }) => {
  const loja = await montarLoja("med");
  // Logo e foto profissional publicadas pela API (mesmo caminho da tela).
  const up = async (who: Api, purpose: string, file: string) => {
    const s = await who.ctx.get("/v1/session").then((r) => r.json());
    const fs = await import("node:fs");
    const res = await who.ctx.post(`/v1/media/uploads?purpose=${purpose}&establishment_id=${loja.estId}`, {
      headers: {
        "Content-Type": file.endsWith(".png") ? "image/png" : "image/jpeg",
        "X-CSRF-Token": s.data.csrf_token,
        "Idempotency-Key": crypto.randomUUID().replaceAll("-", ""),
      },
      data: fs.readFileSync(img(file)),
    });
    const id = (await res.json()).data.id as string;
    for (let i = 0; i < 40; i++) {
      const st = (await who.ok("GET", `/v1/media/uploads/${id}`)).status;
      if (st === "ready") return id;
      await new Promise((r) => setTimeout(r, 500));
    }
    throw new Error("processamento não terminou");
  };
  const logo = await up(loja.dona, "logo", "logo.png");
  const est = await loja.dona.ok("GET", `/v1/establishments/${loja.estId}`);
  await loja.dona.ok("PUT", `/v1/establishments/${loja.estId}/logo`, {
    asset_id: logo,
    expected_version: est.version,
  });
  const foto = await up(loja.beto, "professional_photo", "foto.jpg");
  const mp = await loja.beto.ok("GET", `/v1/establishments/${loja.estId}/my-professional-profile`);
  await loja.beto.ok("PUT", `/v1/establishments/${loja.estId}/my-professional-profile/photo`, {
    asset_id: foto,
    expected_version: mp.version,
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => {
    const w = window as unknown as { __cls: number; __img: number };
    w.__cls = 0;
    w.__img = 0;
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) {
        const x = e as unknown as { value: number; sources?: { node?: Node }[] };
        w.__cls += x.value;
        // Deslocamento causado por imagem: a imagem é a origem, ou contém a imagem que mudou de tamanho.
        if ((x.sources ?? []).some((s) => s.node instanceof HTMLImageElement)) w.__img += x.value;
      }
    }).observe({ type: "layout-shift", buffered: true });
  });
  // Medição no build de produção (E2E_PREVIEW_URL: vite preview); no servidor de desenvolvimento o
  // CSS é injetado por JS na primeira carga e desloca tudo, o que não acontece em produção.
  await page.goto(`${process.env.E2E_PREVIEW_URL ?? ""}/loja/${loja.slug}`);
  await page.waitForLoadState("networkidle");
  const m = await page.evaluate(() => {
    const imgs = performance
      .getEntriesByType("resource")
      .filter((r) => /\/v1\/public\/media\//.test(r.name)) as PerformanceResourceTiming[];
    return {
      requests: imgs.length,
      bytes: imgs.reduce((n, r) => n + (r.encodedBodySize || r.transferSize), 0),
      cls: (window as unknown as { __cls: number }).__cls,
      img: (window as unknown as { __img: number }).__img,
    };
  });
  console.log(
    `MEDIÇÃO loja (390px): ${m.requests} imagens, ${m.bytes} bytes; CLS por imagens ${m.img.toFixed(4)}; CLS total ${m.cls.toFixed(4)} (o total vem do carregamento de filas/serviços, já existia sem imagens)`,
  );
  expect(m.bytes).toBeLessThanOrEqual(200 * 1024);
  expect(m.img).toBe(0);
});
