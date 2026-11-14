// Telas em cada largura pedida (360, 390, 768, 1024, 1440) e no equivalente a zoom de 200% numa
// tela de 1280 px (640 px de largura útil): sem rolagem horizontal da página, alvos de toque de
// pelo menos 44×44, e sem violações automáticas de acessibilidade (axe: WCAG 2.1 A/AA, inclusive
// contraste e rótulos). Tira uma foto de cada tela para revisão humana.
import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import {
  atenderTudo,
  clienteVerificado,
  entrarNaFila,
  entrarNaTela,
  limparLimitesDoTeste,
  montarLoja,
  receberDinheiro,
  type Loja,
} from "./apoio";

const larguras = [
  { nome: "360", w: 360, h: 780 },
  { nome: "390", w: 390, h: 844 },
  { nome: "768", w: 768, h: 1024 },
  { nome: "1024", w: 1024, h: 768 },
  { nome: "1440", w: 1440, h: 900 },
  { nome: "zoom200", w: 640, h: 400 },
];

async function esperarPronta(page: Page) {
  await expect(page.locator("main")).toBeVisible();
  await expect(page.getByText(/^Carregando/)).toHaveCount(0);
  await page.waitForTimeout(300);
}

async function verificar(page: Page, tela: string, projeto: string) {
  for (const l of larguras) {
    await page.setViewportSize({ width: l.w, height: l.h });
    await esperarPronta(page);
    const largura = await page.evaluate(() => {
      const w = document.documentElement.clientWidth;
      const fora: string[] = [];
      for (const el of Array.from(document.querySelectorAll("body *"))) {
        const r = el.getBoundingClientRect();
        if (r.width > 0 && r.right > w + 1 && getComputedStyle(el).position !== "fixed") {
          fora.push(`${el.tagName.toLowerCase()}.${el.className} (${Math.round(r.right)}px)`);
        }
      }
      return { rolagem: document.documentElement.scrollWidth, w, fora: fora.slice(0, 6) };
    });
    expect(
      largura.rolagem,
      `${tela} @${l.nome}: rolagem horizontal ${JSON.stringify(largura.fora)}`,
    ).toBeLessThanOrEqual(largura.w);
    const pequenos = await page.evaluate(() =>
      Array.from(document.querySelectorAll("button, select, textarea, input, nav a, a.button"))
        .filter((el) => {
          const r = el.getBoundingClientRect();
          if (r.width === 0 && r.height === 0) return false;
          const type = el.getAttribute("type");
          if (type === "hidden") return false;
          if (type === "checkbox" || type === "radio") {
            const label = el.closest("label")?.getBoundingClientRect();
            return !label || label.height < 44;
          }
          return r.height < 44 || r.width < 44;
        })
        .map((el) => el.outerHTML.slice(0, 90)),
    );
    expect(pequenos, `${tela} @${l.nome}: alvos menores que 44×44`).toEqual([]);
    const axe = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
      .analyze();
    expect(
      axe.violations.map((v) => `${v.id} (${v.nodes.length}): ${v.help} → ${v.nodes[0]?.target.join(" ")}`),
      `${tela} @${l.nome}: acessibilidade`,
    ).toEqual([]);
    await page.screenshot({ path: `e2e-resultados/telas/${projeto}/${tela}-${l.nome}.png`, fullPage: true });
  }
}

let loja: Loja;
let clienteEmail: string;

test.beforeAll(async () => {
  limparLimitesDoTeste();
  loja = await montarLoja("telas");
  // Programa ligado, prêmio cadastrado, cliente com pontos e com visita em andamento.
  await loja.dona.ok("PUT", `/v1/establishments/${loja.estId}/loyalty-program`, {
    expected_version: 1,
    enabled: true,
  });
  await loja.dona.ok("POST", `/v1/establishments/${loja.estId}/rewards`, {
    name: "Pomada modeladora",
    description: "Pomada de 50 g, cortesia da casa",
    usage_instructions: "Peça na recepção. A cortesia não dá prioridade na fila.",
    cost_points: 10,
    stock: { kind: "limited", units_available: 3 },
  });
  clienteEmail = `cliente-${loja.run}@exemplo.com`;
  const cliente = await clienteVerificado(clienteEmail, "Maria Teste");
  const v1 = await entrarNaFila(loja, cliente, [loja.corte, loja.barba]);
  await receberDinheiro(loja, await atenderTudo(loja, v1.participation.id));
  await entrarNaFila(loja, cliente, [loja.corte]);
});

test("telas públicas", async ({ page }, info) => {
  for (const [tela, url] of [
    ["inicio", "/"],
    ["entrar", "/entrar"],
    ["cadastro", "/cadastro"],
    ["esqueci-senha", "/esqueci-senha"],
    ["consultar-exclusao", "/exclusao"],
    ["loja-anonimo", `/loja/${loja.slug}`],
    ["nao-encontrada", "/nao-existe"],
  ] as const) {
    await page.goto(url);
    await verificar(page, tela, info.project.name);
  }
});

test("telas do cliente", async ({ page }, info) => {
  await entrarNaTela(page, clienteEmail, false);
  await page.goto(`/loja/${loja.slug}`);
  await expect(page.getByRole("heading", { name: "Seus pontos nesta loja" })).toBeVisible();
  await verificar(page, "loja-cliente", info.project.name);
  await page.goto("/conta");
  await verificar(page, "conta", info.project.name);
});

test("telas da dona", async ({ page }, info) => {
  await entrarNaTela(page, loja.donaEmail, true);
  for (const aba of [
    "filas",
    "comandas",
    "servicos",
    "premios",
    "pendencias",
    "profissionais",
    "loja",
    "exclusoes",
    "auditoria",
  ]) {
    await page.goto(`/gestao/${loja.estId}/${aba}`);
    await verificar(page, `gestao-${aba}`, info.project.name);
  }
});

test("telas do profissional", async ({ page }, info) => {
  await entrarNaTela(page, loja.betoEmail, true);
  for (const aba of ["filas", "comandas", "premios"]) {
    await page.goto(`/gestao/${loja.estId}/${aba}`);
    await verificar(page, `profissional-${aba}`, info.project.name);
  }
});

test("só teclado: pular para o conteúdo, entrar e foco sempre visível", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/entrar");
  const focoVisivel = () =>
    page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      if (!el || el === document.body) return "nenhum";
      const st = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      const visivel = r.bottom > 0 && r.top < window.innerHeight && r.right > 0 && r.left < window.innerWidth;
      return st.outlineStyle !== "none" && parseFloat(st.outlineWidth) >= 2 && visivel
        ? "ok"
        : `${el.tagName} sem foco visível`;
    });
  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "Pular para o conteúdo" })).toBeFocused();
  expect(await focoVisivel()).toBe("ok");
  await page.keyboard.press("Enter");
  // Do conteúdo, Tab leva ao e-mail; tudo é preenchido e enviado sem mouse.
  await page.getByLabel("E-mail").focus();
  await page.keyboard.type(clienteEmail);
  await page.keyboard.press("Tab");
  expect(await focoVisivel()).toBe("ok");
  await page.keyboard.type("senha-e2e-forte-1");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("radio", { name: "Cliente" })).toBeFocused();
  expect(await focoVisivel()).toBe("ok");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "Entrar" })).toBeFocused();
  expect(await focoVisivel()).toBe("ok");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("link", { name: "Minha conta" })).toBeVisible();
});
