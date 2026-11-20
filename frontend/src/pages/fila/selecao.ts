import type { components } from "../../api/schema.gen";
import { espera } from "../../tempo";

export type PublicQueue = components["schemas"]["PublicQueue"];
export type PublicService = components["schemas"]["PublicService"];
export type Selection = { queueId: string; serviceIds: string[] };

/** Situação de uma fila em texto (nunca só cor ou ícone). */
export function queueLine(q: PublicQueue): string {
  if (q.status === "paused") return "Fila pausada";
  if (q.status === "closed") return "Fila fechada";
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
  if (!q || q.status !== "open") {
    const name = q?.professional.display_name;
    return {
      next: { queueId: "", serviceIds: sel.serviceIds },
      note: name
        ? `A fila de ${name} ${q?.status === "paused" ? "foi pausada" : "foi fechada"}. Escolha outro profissional.`
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
    next: { queueId: sel.queueId, serviceIds: kept },
    note:
      dropped.length > 0
        ? `${dropped.join(", ")} não ${dropped.length > 1 ? "são feitos" : "é feito"} por ${q.professional.display_name} e saiu da seleção.`
        : null,
  };
}
