import { createContext, useContext, useEffect, useRef } from "react";
import type { StreamMessage, StreamStatus } from "./client";

export type StreamContextValue = {
  status: StreamStatus;
  subscribe: (listener: (m: StreamMessage) => void) => () => void;
};

export const StreamContext = createContext<StreamContextValue>({
  status: "unsupported",
  subscribe: () => () => {},
});

export function useStreamStatus(): StreamStatus {
  return useContext(StreamContext).status;
}

/**
 * Recebe as mensagens do fluxo da página. O handler mais recente é usado sem reinscrever (assim a
 * tela pode usar o próprio estado dentro dele).
 */
export function useStreamMessages(handler: (m: StreamMessage) => void): void {
  const { subscribe } = useContext(StreamContext);
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(() => subscribe((m) => ref.current(m)), [subscribe]);
}

/**
 * Guarda a maior versão vista de cada recurso: o evento só vale se for mais novo. Assim receber
 * o mesmo evento duas vezes, ou fora de ordem, não muda o resultado (eventos-sse.md §2).
 */
export class Versions {
  private seen = new Map<string, number>();
  newer(key: string, version: number): boolean {
    if (version <= (this.seen.get(key) ?? -1)) return false;
    this.seen.set(key, version);
    return true;
  }
  /** Depois de um snapshot ou de reler pela API, a versão conhecida recomeça. */
  reset(): void {
    this.seen.clear();
  }
}
