import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import {
  Api,
  clienteVerificado,
  confirmarTrocaSeHouver,
  entrarNaFila,
  entrarNaTela,
  limparLimitesDoTeste,
  montarLoja,
  senha,
  tokenDe,
  vigiarCSP,
  type Loja,
} from "./apoio";

// Entrada do dono (/dono), a mesma que o programa Fila Gestão abre. Ana é dona da loja A e
// profissional da loja B; Beto é só profissional da loja A.
vigiarCSP();

let a: Loja;
let b: Loja;

test.beforeAll(async () => {
  limparLimitesDoTeste();
  a = await montarLoja("donoa");
  b = await montarLoja("donob");
  // A dona de B convida a dona de A (conta existente) como profissional de B.
  const pro = await b.dona.ok("POST", `/v1/establishments/${b.estId}/professionals`, {
    display_name: "Ana",
    queue_letter: "C",
  });
  await b.dona.ok("POST", "/v1/auth/reauthenticate", { password: senha });
  const antes = new Set(readdirSync(pastaEmails()));
  await b.dona.ok("POST", `/v1/establishments/${b.estId}/professionals/${pro.id}/invites`, {
    email: a.donaEmail,
  });
  const convite = await emailNovo(antes, a.donaEmail);
  const ana = await Api.nova();
  await ana.ok("POST", "/v1/auth/invites/accept", { token: tokenDe(convite), password: senha });
});

function pastaEmails(): string {
  const d = process.env.E2E_MAIL_DIR;
  if (!d) throw new Error("defina E2E_MAIL_DIR");
  return d;
}

/** Link do convite que chegou DEPOIS de `antes` (a dona de A já recebeu outro convite antes). */
async function emailNovo(antes: Set<string>, para: string): Promise<string> {
  for (let i = 0; i < 60; i++) {
    for (const f of readdirSync(pastaEmails())) {
      if (antes.has(f) || !f.endsWith(".txt")) continue;
      const texto = readFileSync(join(pastaEmails(), f), "utf-8");
      const link = /https?:\/\/\S+#token=[A-Za-z0-9_-]{43}/.exec(texto)?.[0];
      if (texto.includes(`Para: ${para}`) && link) return link;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`convite para ${para} não chegou`);
}

async function entrarComoDono(page: Page, email: string): Promise<boolean> {
  await page.goto("/dono");
  await expect(page.getByRole("heading", { name: "Fila Gestão" })).toBeVisible();
  // Login de equipe fixo: sem escolha de cliente nem cadastro de cliente.
  await expect(page.getByRole("radio", { name: /Cliente/ })).toHaveCount(0);
  await expect(page.getByText("Criar conta de cliente")).toHaveCount(0);
  await page.getByLabel("E-mail").fill(email);
  await page.getByLabel("Senha", { exact: true }).fill(senha);
  await page.getByRole("button", { name: "Entrar" }).click();
  return confirmarTrocaSeHouver(page);
}

test("dona de A e profissional de B: só A, URL direta de B recusada, fila de A em tempo real", async ({
  page,
}) => {
  // A preparação deixou a sessão de dono da Ana aberta pela API: o login pede confirmação.
  expect(await entrarComoDono(page, a.donaEmail)).toBe(true);
  await expect(page).toHaveURL(new RegExp(`/gestao/${a.estId}/filas`));
  await expect(page.getByText(`Barbearia ${a.run}`).first()).toBeVisible();
  // Uma só loja de dono: não há troca de loja, e B não aparece.
  await expect(page.getByRole("link", { name: "Trocar de loja" })).toHaveCount(0);
  await expect(page.getByText(`Barbearia ${b.run}`)).toHaveCount(0);
  // Abas exclusivas do dono estão lá (mesma gestão do site).
  await expect(page.getByRole("link", { name: "Profissionais" }).first()).toBeVisible();

  // Endereço direto da loja B (em que é só profissional): recusado nesta janela.
  await page.goto(`/gestao/${b.estId}/filas`);
  await expect(page.getByText("Nesta janela só aparecem lojas em que você é dono.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Chamar próximo" })).toHaveCount(0);
  // Troca de loja (/gestao) também cai na escolha do dono.
  await page.goto("/gestao");
  await expect(page).toHaveURL(new RegExp(`/gestao/${a.estId}/filas`));

  // Tempo real: um cliente entra na fila do Beto e a tela da dona mostra sem recarregar.
  await expect(page.getByRole("heading", { name: /Beto/ }).first()).toBeVisible();
  const cliente = await clienteVerificado(`cli-${a.run}@exemplo.com`, "Carla Tempo Real");
  await entrarNaFila(a, cliente, [a.corte]);
  await expect(page.getByText("Carla Tempo Real").first()).toBeVisible();

  // Sair (Minha conta) volta ao acesso do dono, não à página inicial do site.
  await page.goto("/conta");
  await page.getByRole("button", { name: "Sair desta conta" }).click();
  await expect(page).toHaveURL(/\/dono$/);
  await expect(page.getByLabel("E-mail")).toBeVisible();
});

test("conta só profissional é recusada com mensagem clara e Trocar de conta", async ({ page }) => {
  await entrarComoDono(page, a.betoEmail);
  await expect(page.getByText("Esta conta não é dona de nenhuma loja.")).toBeVisible();
  await expect(page).toHaveURL(/\/dono$/);
  await page.goto(`/gestao/${a.estId}/filas`);
  await expect(page.getByText("Nesta janela só aparecem lojas em que você é dono.")).toBeVisible();
  await page.getByRole("button", { name: "Trocar de conta" }).click();
  await expect(page).toHaveURL(/\/dono$/);
  await expect(page.getByLabel("E-mail")).toBeVisible();
  // A sessão foi mesmo encerrada no servidor.
  const s = await page.request.get("/v1/session").then((r) => r.json());
  expect(s.data.session).toBeNull();
});

test("navegador comum continua igual: profissional entra por /entrar e opera a loja", async ({ page }) => {
  await entrarNaTela(page, a.donaEmail, true, a.dona);
  // Fora do modo dono, a Ana tem as duas lojas (dona de A, profissional de B).
  await page.goto("/gestao");
  await expect(page.getByRole("heading", { name: "Gestão" })).toBeVisible();
  await page.getByRole("link", { name: new RegExp(`Barbearia ${b.run}`) }).click();
  await expect(page).toHaveURL(new RegExp(`/gestao/${b.estId}/filas`));
  await expect(page.getByText("Nesta janela só aparecem lojas em que você é dono.")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Profissionais" })).toHaveCount(0);
});
