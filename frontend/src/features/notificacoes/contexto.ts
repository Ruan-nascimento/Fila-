import { createContext, useContext } from "react";
import type { Notificacao } from "./regras";
import type { Permissao } from "./sistema";

export type Papel = "cliente" | "equipe";

export type ItemNotificacao = Notificacao & { at: Date; lida: boolean; url: string };

export type NotificacoesValue = {
  papel: Papel;
  /** Painel do sino: o que chegou depois do último "Limpar". */
  lista: ItemNotificacao[];
  /** Tudo o que chegou neste aparelho (mesmo o que foi limpo do sino), mais novo primeiro. */
  historico: ItemNotificacao[];
  naoLidas: number;
  marcarLidas: () => void;
  /** Esvazia o painel do sino; o histórico continua com tudo. */
  limpar: () => void;
  /** Apaga o histórico deste aparelho (pedido explícito da pessoa). */
  apagarHistorico: () => void;
  permissao: Permissao;
  /** Notificações do sistema ligadas neste aparelho (permissão + preferência). */
  ligadas: boolean;
  /** Pede a permissão (precisa vir de um toque) e liga neste aparelho. */
  ativar: () => Promise<void>;
  desligar: () => void;
  /** Leva para onde a notificação aponta (ex.: a fila do barbeiro). */
  abrir: (n: ItemNotificacao) => void;
};

export const NotificacoesContext = createContext<NotificacoesValue | null>(null);

export function useNotificacoes(): NotificacoesValue | null {
  return useContext(NotificacoesContext);
}
