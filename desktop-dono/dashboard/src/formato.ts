import type { Valor } from "./nativo";

const brl = new Intl.NumberFormat("pt-BR", {
  style: "currency",
  currency: "BRL",
});
const inteiro = new Intl.NumberFormat("pt-BR");

export type Unidade = "cents" | "count" | "seconds" | "points" | "percent";

export function formatar(v: number, unidade: Unidade): string {
  switch (unidade) {
    case "cents":
      return brl.format(v / 100);
    case "seconds":
      return duracao(v);
    case "percent":
      return `${v.toLocaleString("pt-BR", { maximumFractionDigits: 1 })}%`;
    case "points":
      return `${inteiro.format(Math.round(v))} pts`;
    default:
      return inteiro.format(Math.round(v));
  }
}

export function duracao(s: number): string {
  if (s < 60) return `${Math.round(s)} s`;
  const min = Math.round(s / 60);
  if (min < 60) return `${min} min`;
  return `${Math.floor(min / 60)} h ${String(min % 60).padStart(2, "0")} min`;
}

/** Texto de um valor com a semântica de ausência explícita. */
export function textoValor(v: Valor | undefined, unidade: Unidade): string {
  if (!v) return "—";
  switch (v.status) {
    case "ok":
      return formatar(v.value ?? 0, unidade);
    case "empty":
      return "Sem dados para este período";
    case "not_collected":
      return "Antes do início da coleta";
    case "not_applicable":
      return "Não se aplica ao filtro";
  }
}

export function dataHora(iso: string, tz: string): string {
  return new Intl.DateTimeFormat("pt-BR", {
    timeZone: tz,
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

export function hora(iso: string, tz: string): string {
  return new Intl.DateTimeFormat("pt-BR", {
    timeZone: tz,
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

/** Data (AAAA-MM-DD) de hoje no fuso da loja. */
export function hojeNoFuso(tz: string, agora = new Date()): string {
  const p = new Intl.DateTimeFormat("en-CA", {
    timeZone: tz,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(agora);
  const g = (t: string) => p.find((x) => x.type === t)?.value ?? "";
  return `${g("year")}-${g("month")}-${g("day")}`;
}

export function somarDias(data: string, n: number): string {
  const d = new Date(`${data}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

export type Preset = "hoje" | "ontem" | "7d" | "30d" | "mes" | "personalizado";

export function periodoDe(
  p: Preset,
  tz: string,
  agora = new Date(),
): { from: string; to: string } {
  const hoje = hojeNoFuso(tz, agora);
  switch (p) {
    case "hoje":
      return { from: hoje, to: hoje };
    case "ontem":
      return { from: somarDias(hoje, -1), to: somarDias(hoje, -1) };
    case "7d":
      return { from: somarDias(hoje, -6), to: hoje };
    case "30d":
      return { from: somarDias(hoje, -29), to: hoje };
    case "mes":
    default: {
      const inicio = `${hoje.slice(0, 8)}01`;
      const fim = somarDias(`${somarDias(inicio, 32).slice(0, 8)}01`, -1);
      return { from: inicio, to: fim };
    }
  }
}

/** Variação contra o período anterior. Base zero: só valores absolutos (sem %). */
export function variacao(
  atual: Valor | undefined,
  anterior: Valor | undefined,
  unidade: Unidade,
): string | null {
  if (anterior?.status === "not_collected")
    return "Período anterior: antes do início da coleta";
  if (anterior?.status === "empty") return "Período anterior: sem dados";
  if (atual?.status !== "ok" || anterior?.status !== "ok") return null;
  const a = atual.value ?? 0;
  const b = anterior.value ?? 0;
  if (b === 0) return `antes: ${formatar(b, unidade)} (base zero)`;
  const pct = ((a - b) / Math.abs(b)) * 100;
  const sinal = pct > 0 ? "+" : "";
  return `${sinal}${pct.toLocaleString("pt-BR", { maximumFractionDigits: 1 })}% vs. ${formatar(b, unidade)}`;
}

export const nomeFiltro: Record<string, string> = {
  professional_id: "Profissional",
  service_id: "Serviço",
  method: "Meio de pagamento",
};

export const nomeMeio: Record<string, string> = {
  pix: "Pix",
  cash: "Dinheiro",
  card: "Cartão",
};
