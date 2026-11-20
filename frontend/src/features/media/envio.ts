import { getCsrfToken } from "../../api/client";
import type { Problem } from "../../api/problem";
import type { components } from "../../api/schema.gen";

export type MediaPurpose = components["schemas"]["MediaPurpose"];
export type MediaUpload = components["schemas"]["MediaUpload"];

/** Limites conferidos também no servidor (aqui só para avisar antes de enviar). */
export const MAX_UPLOAD_BYTES = 5 * 1024 * 1024;
export const ACCEPTED_TYPES = ["image/jpeg", "image/png", "image/webp"] as const;

/** Recorte quadrado normalizado (0..1) sobre a imagem como o navegador a mostra (já orientada). */
export type Crop = { x: number; y: number; size: number };

export type UploadResult =
  { ok: true; assetId: string } | { ok: false; message: string; problem: Problem | null; offline: boolean };

function newKey(): string {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

const failureText: Record<string, string> = {
  invalid_image: "Não foi possível ler esta imagem. Tente outra foto.",
  unsupported_format: "Formato não aceito. Use JPEG, PNG ou WebP sem animação.",
  dimensions: "Imagem grande demais (até 8.192 px de lado e 12 megapixels).",
  processing_error: "Não foi possível preparar a foto. Tente de novo.",
};

async function problemOf(res: Response): Promise<Problem | null> {
  try {
    return (await res.json()) as Problem;
  } catch {
    return null;
  }
}

/**
 * Envia a imagem (bytes crus, rota própria) e acompanha o processamento com consulta curta e
 * limitada (no máximo ~40 s, só enquanto este envio está ativo). Enviar NÃO salva o perfil: quem
 * chama associa o id devolvido com o comando da tela (avatar, logo ou foto profissional).
 */
export async function uploadImage(
  file: Blob,
  purpose: MediaPurpose,
  opts: {
    establishmentId?: string;
    crop?: Crop | null;
    signal?: AbortSignal;
    onProcessing?: () => void;
  } = {},
): Promise<UploadResult> {
  const q = new URLSearchParams({ purpose });
  if (opts.establishmentId) q.set("establishment_id", opts.establishmentId);
  if (opts.crop) {
    q.set("crop_x", opts.crop.x.toFixed(4));
    q.set("crop_y", opts.crop.y.toFixed(4));
    q.set("crop_size", opts.crop.size.toFixed(4));
  }
  const headers: Record<string, string> = { "Content-Type": file.type, "Idempotency-Key": newKey() };
  const csrf = getCsrfToken();
  if (csrf) headers["X-CSRF-Token"] = csrf;
  let res: Response;
  try {
    res = await fetch(`/v1/media/uploads?${q.toString()}`, {
      method: "POST",
      body: file,
      headers,
      credentials: "same-origin",
      signal: opts.signal ?? null,
    });
  } catch {
    return { ok: false, message: "Sem conexão. A foto não foi enviada.", problem: null, offline: true };
  }
  if (res.status !== 202) {
    const p = await problemOf(res);
    return { ok: false, message: p?.detail ?? "Não foi possível enviar a foto.", problem: p, offline: false };
  }
  const created = ((await res.json()) as { data: MediaUpload }).data;
  opts.onProcessing?.();
  for (let i = 0; i < 40; i++) {
    await new Promise((r) => setTimeout(r, i < 5 ? 500 : 1000));
    if (opts.signal?.aborted)
      return { ok: false, message: "Envio cancelado.", problem: null, offline: false };
    let st: Response;
    try {
      st = await fetch(`/v1/media/uploads/${created.id}`, {
        credentials: "same-origin",
        signal: opts.signal ?? null,
      });
    } catch {
      continue; // queda momentânea: consulta de novo (o envio já foi aceito)
    }
    if (!st.ok) continue;
    const u = ((await st.json()) as { data: MediaUpload }).data;
    if (u.status === "ready" || u.status === "attached") return { ok: true, assetId: u.id };
    if (u.status === "failed")
      return {
        ok: false,
        message: failureText[u.failure_reason ?? "processing_error"] ?? "Não foi possível preparar a foto.",
        problem: null,
        offline: false,
      };
  }
  return {
    ok: false,
    message: "A foto ainda está em processamento. Tente salvar de novo em instantes.",
    problem: null,
    offline: false,
  };
}

/** Lê o arquivo como data URL para a prévia local (a CSP aceita data: em imagens). */
export function readAsDataURL(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error);
    r.readAsDataURL(file);
  });
}

/** Validação local (o servidor confere de novo pelo conteúdo real). */
export function checkFile(file: File): string | null {
  if (!(ACCEPTED_TYPES as readonly string[]).includes(file.type))
    return "Formato não aceito. Use uma foto JPEG, PNG ou WebP.";
  if (file.size > MAX_UPLOAD_BYTES) return "A imagem passa de 5 MB. Escolha uma foto menor.";
  if (file.size === 0) return "O arquivo está vazio.";
  return null;
}

/** Maior quadrado central (normalizado). */
export function centered(w: number, h: number): Crop {
  const side = Math.min(w, h);
  return { x: (w - side) / 2 / w, y: (h - side) / 2 / h, size: 1 };
}

/** Posição máxima (normalizada) para o recorte de tamanho `size` caber na imagem. */
export function limits(w: number, h: number, size: number) {
  const s = size * Math.min(w, h);
  return { x: Math.max(0, (w - s) / w), y: Math.max(0, (h - s) / h) };
}
