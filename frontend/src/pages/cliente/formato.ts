import type { components } from "../../api/schema.gen";

type Visit = components["schemas"]["Visit"];

const km1 = new Intl.NumberFormat("pt-BR", { maximumFractionDigits: 1 });
const km0 = new Intl.NumberFormat("pt-BR", { maximumFractionDigits: 0 });

/** Distância em linha reta, curta: "350 m", "1,2 km", "85 km". */
export function distancia(m: number): string {
  if (m < 1000) return `${Math.max(10, Math.round(m / 10) * 10)}\u00a0m`;
  const km = m / 1000;
  return `${(km < 10 ? km1 : km0).format(km)}\u00a0km`;
}

/**
 * A localização do cliente vai arredondada (~100 m) e só na consulta: o bastante para ordenar por
 * proximidade sem mandar a posição exata da pessoa.
 */
export function arredondar(v: number): number {
  return Math.round(v * 1000) / 1000;
}

export type Situacao = { texto: string; tom: "primary" | "warning" | "neutral"; destaque: boolean };

/** Situação da visita em uma frase (cartão do painel). */
export function situacao(v: Visit): Situacao {
  const p = v.participation;
  switch (p.status) {
    case "called":
      return { texto: "Chegou a sua vez!", tom: "primary", destaque: true };
    case "in_service":
      return { texto: "Em atendimento", tom: "primary", destaque: false };
    case "waiting":
      return {
        texto: p.position === 1 ? "Você é o próximo" : p.position ? `${p.position}º na fila` : "Aguardando",
        tom: "neutral",
        destaque: false,
      };
    default:
      break;
  }
  switch (v.order.status) {
    case "awaiting_payment":
      return { texto: "Pagamento pendente", tom: "warning", destaque: false };
    case "awaiting_confirmation":
      return { texto: "Confirmando pagamento", tom: "neutral", destaque: false };
    case "pending_review":
      return { texto: "Comanda em revisão pela loja", tom: "neutral", destaque: false };
    case "settled":
      return { texto: "Pago — obrigado!", tom: "neutral", destaque: false };
    default:
      return { texto: "Visita encerrada", tom: "neutral", destaque: false };
  }
}
