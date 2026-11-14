import { defineConfig } from "@playwright/test";

// Testes em navegador REAL (Chrome e Edge instalados na máquina; nada é baixado) contra a API
// real. O ambiente (API, worker, Vite, banco e simulador de Pix) é preparado fora daqui: veja
// docs/web-validacao.md. E2E_BASE_URL aponta para o Vite desse ambiente.
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:15173";

export default defineConfig({
  testDir: "./e2e",
  timeout: 120_000,
  expect: { timeout: 20_000 },
  // Um teste por vez: o ambiente é compartilhado (filas, limites de tentativa por IP).
  workers: 1,
  fullyParallel: false,
  reporter: [["list"], ["html", { outputFolder: "e2e-relatorio", open: "never" }]],
  outputDir: "e2e-resultados",
  use: {
    baseURL,
    locale: "pt-BR",
    timezoneId: "America/Sao_Paulo",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "chrome", use: { channel: "chrome" } },
    { name: "edge", use: { channel: "msedge" } },
  ],
});
