import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Em desenvolvimento, o Vite repassa /v1 e /healthz para a API Go. Assim o navegador vê
// frontend e API na MESMA origem, como em produção (ADR-0006: cookies __Host- e sem CORS).
// FILA_API_PROXY_TARGET muda o destino (padrão: API local em 127.0.0.1:8080).
// Atenção: nenhuma variável com prefixo VITE_ pode conter segredo — ela vai para o bundle público.
const apiTarget = process.env.FILA_API_PROXY_TARGET ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/v1": { target: apiTarget, changeOrigin: false },
      "/healthz": { target: apiTarget, changeOrigin: false },
    },
  },
  build: {
    sourcemap: false,
  },
  test: {
    // Só os testes de componente; os de navegador real (e2e/) rodam com o Playwright.
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    restoreMocks: true,
  },
});
