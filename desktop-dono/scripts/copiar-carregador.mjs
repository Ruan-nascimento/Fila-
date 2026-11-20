// Junta a tela local de carregamento (carregador/) ao Dashboard construído em dist/: o Tauri
// empacota dist/ inteiro (frontendDist).
import { cpSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
const raiz = fileURLToPath(new URL("..", import.meta.url));
for (const f of readdirSync(`${raiz}/carregador`)) cpSync(`${raiz}/carregador/${f}`, `${raiz}/dist/${f}`);
console.log("carregador copiado para dist/");
