import { useEffect, useRef, useState } from "react";
import { ExternalLink, X } from "lucide-react";
import { Button, Notice } from "@web/ui/ui";
import { destinoDe, nativo, type ErroNativo, type Registro, type Recorte } from "./nativo";
import { encerraSessao, useDash, type PedidoDetalhe } from "./dados";
import { dataHora, duracao, formatar, nomeMeio } from "./formato";

type Item = Record<string, unknown>;
type Pagina = { kind: string; total: number; total_cents: number | null; items: Item[]; next_cursor: string | null; window: { start: string; end: string } };

const STATUS: Record<string, string> = {
  waiting: "Esperando", called: "Chamado", in_service: "Em atendimento", completed: "Concluído",
  cancelled: "Cancelado", no_show: "Ausente", interrupted: "Interrompido", performed: "Realizado",
  accepted: "Aceito", not_performed: "Não realizado", open: "Aberta", resolved: "Resolvida",
};

/**
 * Painel lateral com os registros que formaram o valor clicado. Paginação no servidor (cursor),
 * mesmo recorte e filtros do gráfico; o total do recorte aparece para conferir com o gráfico.
 * Fechar (Esc ou botão) volta ao gráfico com os filtros mantidos.
 */
export function Detalhe({ pedido, aoFechar }: { pedido: PedidoDetalhe; aoFechar: () => void }) {
  const ctx = useDash();
  const [paginas, setPaginas] = useState<Pagina[]>([]);
  const [erro, setErro] = useState<ErroNativo | null>(null);
  const [carregando, setCarregando] = useState(false);
  const titulo = useRef<HTMLHeadingElement>(null);

  async function carregar(cursor?: string) {
    setCarregando(true);
    try {
      const recorte: Recorte = { ...pedido.recorte, ...(cursor ? { cursor } : {}) };
      const r = await nativo.consultar<Pagina>({
        tipo: "registros", loja: ctx.loja, registro: pedido.registro as Registro, filtros: { ...ctx.filtros, compare: false }, recorte,
      });
      if (r.meta.establishment_id !== ctx.loja) return;
      setPaginas((p) => (cursor ? [...p, r.data] : [r.data]));
      setErro(null);
    } catch (e) {
      const er = e as ErroNativo;
      if (encerraSessao(er)) ctx.encerrar(er);
      setErro(er);
    } finally {
      setCarregando(false);
    }
  }

  useEffect(() => {
    setPaginas([]);
    void carregar();
    titulo.current?.focus();
    const esc = (e: KeyboardEvent) => e.key === "Escape" && aoFechar();
    window.addEventListener("keydown", esc);
    return () => window.removeEventListener("keydown", esc);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(pedido), ctx.loja]);

  const primeira = paginas[0];
  const itens = paginas.flatMap((p) => p.items);
  const ultima = paginas[paginas.length - 1];
  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-labelledby="titulo-detalhe"
      className="fixed inset-y-0 right-0 z-40 grid w-full max-w-md grid-rows-[auto_1fr_auto] border-l border-line bg-surface shadow-2xl"
    >
      <header className="grid gap-1 border-b border-line p-4">
        <div className="flex items-start justify-between gap-2">
          <h2 id="titulo-detalhe" ref={titulo} tabIndex={-1} className="m-0 text-lg font-semibold">{pedido.titulo}</h2>
          <button type="button" onClick={aoFechar} aria-label="Fechar detalhamento" className="inline-flex size-10 items-center justify-center rounded-lg hover:bg-surface-2">
            <X aria-hidden="true" className="size-5" />
          </button>
        </div>
        {primeira && (
          <p className="m-0 text-sm text-fg-muted">
            {dataHora(primeira.window.start, ctx.tz)} até {dataHora(primeira.window.end, ctx.tz)} ·{" "}
            <strong className="text-fg">{primeira.total} registro(s)</strong>
            {primeira.total_cents !== null && <> · {formatar(primeira.total_cents, "cents")}</>}
          </p>
        )}
        {pedido.esperado && primeira && <Conferencia esperado={pedido.esperado} pagina={primeira} />}
      </header>
      <div className="overflow-y-auto p-4">
        {erro && <Notice kind="erro">{erro.detail}</Notice>}
        {!erro && primeira && itens.length === 0 && <p className="m-0 text-sm text-fg-muted">Nenhum registro neste recorte.</p>}
        <ul className="m-0 grid list-none gap-2 p-0">
          {itens.map((it, i) => (
            <ItemRegistro key={String(it.id ?? i)} item={it} />
          ))}
        </ul>
      </div>
      <footer className="flex flex-wrap gap-2 border-t border-line p-4">
        {ultima?.next_cursor && (
          <Button variant="secondary" disabled={carregando} onClick={() => void carregar(ultima.next_cursor ?? undefined)}>
            {carregando ? "Carregando…" : "Carregar mais"}
          </Button>
        )}
        <Button variant="ghost" onClick={aoFechar}>Voltar ao gráfico</Button>
      </footer>
    </aside>
  );
}

function Conferencia({ esperado, pagina }: { esperado: NonNullable<PedidoDetalhe["esperado"]>; pagina: Pagina }) {
  const okC = esperado.centavos === undefined || esperado.centavos === pagina.total_cents;
  const okN = esperado.quantidade === undefined || esperado.quantidade === pagina.total;
  return (
    <p className={`m-0 text-xs ${okC && okN ? "text-success" : "text-warning"}`}>
      {okC && okN ? `Confere com o gráfico (${esperado.texto}).` : `Atenção: o gráfico mostrou ${esperado.texto}.`}
    </p>
  );
}

function ItemRegistro({ item }: { item: Item }) {
  const ctx = useDash();
  const destino = destinoDe(item.gestao_path as string | undefined, ctx.loja);
  const linhas: string[] = [];
  const t = (k: string) => (typeof item[k] === "string" ? dataHora(item[k] as string, ctx.tz) : null);
  if (item.amount_cents !== undefined && item.amount_cents !== null) linhas.push(formatar(Number(item.amount_cents), "cents"));
  if (item.total_cents !== undefined) linhas.push(formatar(Number(item.total_cents), "cents"));
  if (item.method) linhas.push(nomeMeio[String(item.method)] ?? String(item.method));
  if (item.cash_tendered_cents) linhas.push(`entregue ${formatar(Number(item.cash_tendered_cents), "cents")}, troco ${formatar(Number(item.change_cents ?? 0), "cents")}`);
  if (item.status) linhas.push(STATUS[String(item.status)] ?? String(item.status));
  if (item.kind) linhas.push(String(item.kind).replaceAll("_", " "));
  if (item.wait_seconds !== undefined && item.wait_seconds !== null) linhas.push(`espera ${duracao(Number(item.wait_seconds))}`);
  if (item.duration_seconds !== undefined && item.duration_seconds !== null) linhas.push(`duração ${duracao(Number(item.duration_seconds))}`);
  if (item.professional_name) linhas.push(String(item.professional_name));
  if (item.name) linhas.push(String(item.name));
  if (item.points_delta !== undefined) linhas.push(`${item.points_delta} pts`);
  if (item.transferred) linhas.push("transferido");
  if (item.adjusted_after_close) linhas.push("corrigida depois de fechar");
  const quando = t("at") ?? t("entered_at") ?? t("performed_at") ?? t("created_at") ?? t("added_at");
  return (
    <li className="grid gap-1 rounded-lg border border-line p-3 text-sm">
      <div className="flex items-baseline justify-between gap-2">
        <strong className="font-semibold">{(item.ticket_code as string) ?? "Registro"}</strong>
        {quando && <span className="text-xs text-fg-muted">{quando}</span>}
      </div>
      <p className="m-0 text-fg-muted">{linhas.join(" · ")}</p>
      {destino && (
        <button
          type="button"
          onClick={() => void nativo.abrirGestao(ctx.loja, destino)}
          className="inline-flex min-h-9 w-fit items-center gap-1.5 rounded-lg px-2 text-sm font-medium text-accent hover:bg-select"
        >
          <ExternalLink aria-hidden="true" className="size-4" /> Abrir na gestão
        </button>
      )}
    </li>
  );
}
