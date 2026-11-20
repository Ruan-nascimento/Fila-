import type { ItemNotificacao } from "./contexto";

/**
 * Histórico de notificações NESTE aparelho (localStorage). Nada vai ao servidor: as notificações
 * nascem no navegador, a partir dos eventos que a página já recebe. A chave inclui quem está
 * usando (conta ou visitante) e a loja, então outra pessoa no mesmo computador não vê o histórico
 * de quem saiu. Guarda no máximo 300 avisos e 30 dias.
 *
 * "Limpar" no sino não apaga nada daqui: só marca até quando o painel deve ficar vazio
 * (`limpoAte`). O histórico continua mostrando tudo até a pessoa apagá-lo de propósito.
 */

const PREFIXO = "fila-notificacoes:";
const MAX = 300;
const VALIDADE_MS = 30 * 24 * 60 * 60 * 1000;

type Salvo = { itens: (Omit<ItemNotificacao, "at"> & { at: string })[]; limpoAte: string | null };

export type Historico = { itens: ItemNotificacao[]; limpoAte: Date | null };

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null; // bloqueado (janela privada): o histórico vale só enquanto a página está aberta
  }
}

export function chaveHistorico(escopo: string): string {
  return PREFIXO + escopo;
}

export function lerHistorico(escopo: string): Historico {
  try {
    const raw = storage()?.getItem(chaveHistorico(escopo));
    if (!raw) return { itens: [], limpoAte: null };
    const s = JSON.parse(raw) as Salvo;
    const limite = Date.now() - VALIDADE_MS;
    return {
      itens: (s.itens ?? [])
        .map((x) => ({ ...x, at: new Date(x.at) }))
        .filter((x) => !Number.isNaN(x.at.getTime()) && x.at.getTime() >= limite),
      limpoAte: s.limpoAte ? new Date(s.limpoAte) : null,
    };
  } catch {
    return { itens: [], limpoAte: null }; // histórico corrompido: recomeça
  }
}

export function gravarHistorico(escopo: string, h: Historico): void {
  try {
    const s: Salvo = {
      itens: h.itens.slice(0, MAX).map((x) => ({ ...x, at: x.at.toISOString() })),
      limpoAte: h.limpoAte ? h.limpoAte.toISOString() : null,
    };
    storage()?.setItem(chaveHistorico(escopo), JSON.stringify(s));
  } catch {
    // sem espaço ou bloqueado: segue só na memória
  }
}

export function apagarHistorico(escopo: string): void {
  try {
    storage()?.removeItem(chaveHistorico(escopo));
  } catch {
    // nada a fazer
  }
}

/** Painel do sino: só o que chegou depois do último "Limpar", e só o mais novo de cada grupo. */
export function visiveisNoPainel(h: Historico): ItemNotificacao[] {
  const grupos = new Set<string>();
  return h.itens.filter((x) => {
    if (h.limpoAte && x.at <= h.limpoAte) return false;
    if (x.group) {
      if (grupos.has(x.group)) return false;
      grupos.add(x.group);
    }
    return true;
  });
}
