import type { components } from "../../api/schema.gen";
import { espera } from "../../tempo";

export type PublicQueue = components["schemas"]["PublicQueue"];
export type PublicService = components["schemas"]["PublicService"];
export type PublicProduct = components["schemas"]["PublicProduct"];
export type PublicCategory = components["schemas"]["PublicCatalogCategory"];
export type Ordering = components["schemas"]["PublicOrdering"];

/**
 * Escolha do cliente: fila, serviços (em ordem de escolha) com quantidade e produtos com
 * quantidade. Quantidade ausente = 1. Buscar ou trocar de categoria nunca mexe aqui.
 */
export type Selection = {
  queueId: string;
  serviceIds: string[];
  quantities?: Record<string, number>;
  products?: Record<string, number>;
};

export const emptySelection: Selection = { queueId: "", serviceIds: [], quantities: {}, products: {} };

export function qtyOf(sel: Selection, serviceId: string): number {
  return sel.quantities?.[serviceId] ?? 1;
}

/** Fila aceita entrar agora? (situação calculada pelo servidor; antiga sem o campo = status). */
export function accepting(q: PublicQueue): boolean {
  return (q.entry_state ?? (q.status === "open" ? "accepting" : "operation_closed")) === "accepting";
}

/** Situação de uma fila em texto (nunca só cor ou ícone). */
export function queueLine(q: PublicQueue): string {
  if (q.status === "paused") return "Fila pausada";
  if (q.status === "closed") return "Fila fechada";
  if (q.entry_state === "full") return "Fila cheia no momento · tente quando liberar vaga";
  if (q.entry_state === "closed_manual")
    return "Entrada encerrada por enquanto · quem está na fila será atendido";
  if (q.waiting_count === 0) return "Aberta · ninguém aguardando";
  return `Aberta · ${q.waiting_count} aguardando · estimativa de ${espera(q.estimated_wait.minutes)}`;
}

/** Serviços que o profissional da fila faz. */
export function servicesFor(services: PublicService[], queue: PublicQueue | undefined): PublicService[] {
  return queue ? services.filter((s) => s.professional_ids.includes(queue.professional.id)) : [];
}

/**
 * Confere a seleção com os dados atuais: fila que deixou de estar aberta sai; serviço que o
 * profissional não faz mais sai. Devolve a seleção válida e, se algo mudou, o motivo.
 */
export function reconcile(
  sel: Selection,
  queues: PublicQueue[] | null,
  services: PublicService[] | null,
): { next: Selection; note: string | null } {
  if (!sel.queueId || !queues || !services) return { next: sel, note: null };
  const q = queues.find((x) => x.queue_id === sel.queueId);
  if (!q || !accepting(q)) {
    const name = q?.professional.display_name;
    const why =
      q?.status === "paused"
        ? "foi pausada"
        : q?.entry_state === "full"
          ? "ficou cheia"
          : q?.entry_state === "closed_manual"
            ? "fechou a entrada por enquanto"
            : "foi fechada";
    return {
      next: { ...sel, queueId: "" },
      note: name
        ? `A fila de ${name} ${why}. Escolha outro profissional.`
        : "A fila escolhida não está mais disponível. Escolha outro profissional.",
    };
  }
  const offered = new Set(servicesFor(services, q).map((s) => s.id));
  const kept = sel.serviceIds.filter((id) => offered.has(id));
  if (kept.length === sel.serviceIds.length) return { next: sel, note: null };
  const dropped = sel.serviceIds
    .filter((id) => !offered.has(id))
    .map((id) => services.find((s) => s.id === id)?.name)
    .filter(Boolean);
  return {
    next: { ...sel, serviceIds: kept },
    note:
      dropped.length > 0
        ? `${dropped.join(", ")} não ${dropped.length > 1 ? "são feitos" : "é feito"} por ${q.professional.display_name} e saiu da seleção.`
        : null,
  };
}
