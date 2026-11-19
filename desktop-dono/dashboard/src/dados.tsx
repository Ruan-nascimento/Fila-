import { createContext, useContext, useEffect, useRef, useState } from "react";
import { nativo, type Consulta, type ErroNativo, type Filtros, type Recorte, type Registro, type Resposta } from "./nativo";

/** Contexto do Dashboard aberto: loja, filtros, versões por área (invalidação) e fim de sessão. */
export type Ctx = {
  loja: string;
  tz: string;
  filtros: Filtros;
  versoes: Record<string, number>;
  /** Chamado quando o servidor diz que o acesso acabou: limpa TUDO e mostra o motivo. */
  encerrar: (e: ErroNativo) => void;
  abrirDetalhe: (d: PedidoDetalhe) => void;
};

export type PedidoDetalhe = {
  titulo: string;
  registro: Registro;
  recorte: Recorte;
  /** Valor que o gráfico mostrou, para conferir com o total dos registros. */
  esperado?: { texto: string; centavos?: number; quantidade?: number };
};

export const DashCtx = createContext<Ctx | null>(null);
export function useDash(): Ctx {
  const c = useContext(DashCtx);
  if (!c) throw new Error("fora do Dashboard");
  return c;
}

const fimDeSessao = new Set([
  "owner_session_replaced",
  "desktop_access_invalid",
  "desktop_proof_invalid",
  "desktop_installation_invalid",
  "unauthenticated",
]);
export function encerraSessao(e: ErroNativo): boolean {
  return e.status === 401 || fimDeSessao.has(e.code) || e.code === "desktop_not_activated";
}

type Estado<T> = { dados: Resposta<T> | null; erro: ErroNativo | null; carregando: boolean };

/**
 * Carrega uma consulta quando o componente aparece (aba aberta) e quando a área é invalidada.
 * Resposta de outra loja ou de um pedido já substituído é ignorada: dados da loja A nunca
 * aparecem sob o cabeçalho da loja B.
 */
export function useDados<T>(montar: (c: Ctx) => Consulta | null, areas: string[]): Estado<T> & { recarregar: () => void } {
  const ctx = useDash();
  const consulta = montar(ctx);
  const chave = JSON.stringify(consulta);
  const versao = areas.reduce((s, a) => s + (ctx.versoes[a] ?? 0), 0);
  const [manual, setManual] = useState(0);
  const [st, setSt] = useState<Estado<T>>({ dados: null, erro: null, carregando: true });
  const seq = useRef(0);
  // Loja ATUAL (não a do momento do pedido): uma resposta que chega depois da troca é descartada.
  const lojaAtual = useRef(ctx.loja);
  lojaAtual.current = ctx.loja;
  useEffect(() => {
    if (!consulta) return;
    const meu = ++seq.current;
    setSt((s) => ({ dados: s.dados?.meta.establishment_id === ctx.loja ? s.dados : null, erro: null, carregando: true }));
    nativo
      .consultar<T>(consulta)
      .then((r) => {
        if (meu !== seq.current || r.meta.establishment_id !== lojaAtual.current || r.meta.establishment_id !== consulta.loja) return; // atrasada ou de outra loja
        setSt({ dados: r, erro: null, carregando: false });
      })
      .catch((e: ErroNativo) => {
        if (meu !== seq.current) return;
        if (encerraSessao(e)) ctx.encerrar(e);
        setSt({ dados: null, erro: e, carregando: false });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chave, versao, manual]);
  return { ...st, recarregar: () => setManual((n) => n + 1) };
}
