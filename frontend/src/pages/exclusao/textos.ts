import type { components } from "../../api/schema.gen";

export type DeletionRequest = components["schemas"]["DeletionRequest"];
export type DeletionBlocker = components["schemas"]["DeletionBlocker"];

/** O que cada bloqueio significa e quem resolve (a pessoa não precisa fazer nada). */
export const blockerText: Record<DeletionBlocker, string> = {
  service_in_progress: "Atendimento em andamento: a equipe da loja conclui ou interrompe.",
  financial_pending: "Pagamento pendente: a equipe da loja recebe, confere ou corrige o valor.",
  professional_queue_has_participants:
    "Ainda há clientes na fila deste profissional: o dono da loja transfere ou cancela.",
  sole_owner: "Única pessoa dona de loja ativa: é preciso transferir a administração ou encerrar a loja.",
};

/** Meta OPERACIONAL (R-EXC-05): nunca chamar de prazo legal. */
export const metaText = "A meta da operação é concluir em até 30 dias (meta interna, não é prazo legal).";

const day = 24 * 60 * 60 * 1000;

export function daysSince(iso: string, now = new Date()): number {
  return Math.floor((now.getTime() - new Date(iso).getTime()) / day);
}
