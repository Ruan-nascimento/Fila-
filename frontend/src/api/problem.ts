import type { components } from "./schema.gen";

export type Problem = components["schemas"]["Problem"];
export type ErrorCode = components["schemas"]["ErrorCode"];

/** Resultado de uma chamada: sucesso, erro do contrato (Problem) ou falha de rede. */
export type Outcome<T> = { ok: true; data: T } | { ok: false; problem: Problem | null; offline: boolean };

/** Mensagens por código estável (o servidor já manda title/detail em português; estas são reservas). */
const fallback: Partial<Record<ErrorCode, string>> = {
  invalid_credentials: "E-mail ou senha incorretos.",
  email_not_verified: "Confirme seu e-mail antes de entrar.",
  rate_limited: "Muitas tentativas. Aguarde um pouco e tente de novo.",
  csrf_failed: "A página ficou desatualizada. Atualize e tente de novo.",
  origin_not_allowed: "Pedido recusado por segurança. Abra o site pelo endereço oficial.",
  token_invalid: "Este link já foi usado, venceu ou não existe.",
  invite_invalid: "Este convite já foi usado, foi cancelado ou venceu.",
  recovery_code_invalid: "Código incorreto, já usado ou vencido.",
};

/** Texto para mostrar ao usuário. Nunca mostra detalhes técnicos. */
export function problemMessage(problem: Problem | null, offline = false): string {
  if (offline) return "Sem conexão com o servidor. Verifique a internet e tente de novo.";
  if (!problem) return "Algo deu errado. Tente de novo.";
  let msg = problem.detail ?? problem.title ?? fallback[problem.code] ?? "Algo deu errado. Tente de novo.";
  if (problem.code === "rate_limited" && problem.retry_after_seconds) {
    const min = Math.ceil(problem.retry_after_seconds / 60);
    msg += min > 1 ? ` Tente de novo em cerca de ${min} minutos.` : " Tente de novo em cerca de 1 minuto.";
  }
  if (problem.code === "internal_error" && problem.request_id) {
    msg += ` (código para suporte: ${problem.request_id})`;
  }
  return msg;
}

const fieldMessages: Record<string, string> = {
  required: "Preencha este campo.",
  invalid_format: "Formato inválido.",
  too_short: "Muito curto.",
  too_long: "Muito longo.",
  not_allowed: "Contém caracteres não permitidos.",
  unknown_field: "Campo não permitido.",
  out_of_range: "Valor fora do permitido.",
  duplicate: "Valor repetido.",
};

/** Erros por campo (chave = nome do campo sem a barra do JSON Pointer). */
export function fieldErrors(problem: Problem | null): Record<string, string> {
  const out: Record<string, string> = {};
  for (const e of problem?.errors ?? []) {
    const name = e.field.replace(/^\//, "").split("/")[0] ?? "";
    out[name] = e.message
      ? `${fieldMessages[e.code] ?? ""} ${e.message}`.trim()
      : (fieldMessages[e.code] ?? "Valor inválido.");
  }
  return out;
}

/** Converte o retorno do openapi-fetch em Outcome, tratando falha de rede. */
export async function call<T>(
  run: () => Promise<{ data?: T; error?: unknown; response: Response }>,
): Promise<Outcome<T>> {
  try {
    const { data, error, response } = await run();
    if (response.ok) return { ok: true, data: data as T };
    const problem = isProblem(error) ? error : null;
    return { ok: false, problem, offline: false };
  } catch {
    return { ok: false, problem: null, offline: true };
  }
}

function isProblem(v: unknown): v is Problem {
  return typeof v === "object" && v !== null && "code" in v && "status" in v;
}
