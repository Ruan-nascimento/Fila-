import { createContext, useContext } from "react";
import type { PublicQueue, PublicService, Selection } from "./selecao";

export type StoreData = {
  shopId: string;
  queues: PublicQueue[] | null;
  queuesError: string | null;
  retryQueues: () => void;
  services: PublicService[] | null;
  servicesError: string | null;
  retryServices: () => void;
  /** Escolha do visitante (profissional e serviços): sobrevive à identificação e à troca de layout. */
  selection: Selection;
  setSelection: (s: Selection) => void;
  /** Por que a seleção mudou sozinha (fila pausou, serviço deixou de ser oferecido). */
  selectionNote: string | null;
  clearSelectionNote: () => void;
};

export const Ctx = createContext<StoreData | null>(null);

export function useStoreData(): StoreData {
  const v = useContext(Ctx);
  if (!v) throw new Error("useStoreData fora de StoreDataProvider");
  return v;
}
