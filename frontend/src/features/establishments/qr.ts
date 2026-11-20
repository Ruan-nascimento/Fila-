import qrcode from "qrcode-generator";

/** Margem silenciosa (módulos) exigida pela especificação do QR: pelo menos 4. */
export const QUIET_ZONE = 4;

export type QrMatrix = { size: number; dark: (r: number, c: number) => boolean };

/** Matriz do QR (correção M), gerada localmente: nenhum serviço externo, nada sai do navegador. */
export function qrMatrix(text: string): QrMatrix {
  const qr = qrcode(0, "M");
  qr.addData(text, "Byte");
  qr.make();
  const n = qr.getModuleCount();
  return { size: n, dark: (r, c) => qr.isDark(r, c) };
}

/** Caminho SVG dos módulos (só números do gerador; nenhum texto da loja entra no SVG). */
export function qrPath(m: QrMatrix): string {
  let d = "";
  for (let r = 0; r < m.size; r++)
    for (let c = 0; c < m.size; c++) if (m.dark(r, c)) d += `M${c + QUIET_ZONE} ${r + QUIET_ZONE}h1v1h-1z`;
  return d;
}

/** SVG preto sobre branco, com margem silenciosa, pronto para baixar. */
export function qrSvg(m: QrMatrix): string {
  const s = m.size + QUIET_ZONE * 2;
  return `<?xml version="1.0" encoding="UTF-8"?>\n<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${s} ${s}" width="1024" height="1024" shape-rendering="crispEdges"><rect width="${s}" height="${s}" fill="#ffffff"/><path d="${qrPath(m)}" fill="#000000"/></svg>`;
}

/** PNG de ~1024 px com módulos nítidos (cada módulo vira um número inteiro de pixels). */
export function qrPngDataUrl(m: QrMatrix, target = 1024): string {
  const s = m.size + QUIET_ZONE * 2;
  const scale = Math.max(1, Math.floor(target / s));
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = s * scale;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("canvas indisponível");
  ctx.fillStyle = "#ffffff";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.fillStyle = "#000000";
  for (let r = 0; r < m.size; r++)
    for (let c = 0; c < m.size; c++)
      if (m.dark(r, c)) ctx.fillRect((c + QUIET_ZONE) * scale, (r + QUIET_ZONE) * scale, scale, scale);
  return canvas.toDataURL("image/png");
}
