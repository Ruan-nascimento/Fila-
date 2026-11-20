import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Dashboard local do programa (ADR-0027): empacotado no instalador, fora do bundle web público.
// Reaproveita os tokens e componentes do frontend (alias @web → frontend/src). React e afins vêm
// SEMPRE deste pacote (dedupe), para não haver duas cópias quando um componente do frontend é usado.
const aqui = (p: string) => fileURLToPath(new URL(p, import.meta.url));

export default defineConfig({
  root: aqui("."),
  base: "./",
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@web": aqui("../../frontend/src") },
    dedupe: ["react", "react-dom", "react-router", "lucide-react"],
  },
  build: {
    outDir: aqui("../dist"),
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: { input: { dashboard: aqui("dashboard.html") } },
  },
  test: {
    root: aqui("."),
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "jsdom",
    setupFiles: [aqui("src/test-setup.ts")],
    restoreMocks: true,
  },
});
