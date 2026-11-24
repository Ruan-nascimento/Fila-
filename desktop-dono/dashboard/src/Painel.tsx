import { useCallback, useEffect, useMemo, useState } from "react";
import { FilterX, LogOut, RefreshCw, UsersRound } from "lucide-react";
import { Button } from "@web/ui/ui";
import { cx } from "@web/ui/cx";
import { FilasAtendimento, Financeiro, Fidelidade, ProfissionaisServicos, VisaoGeral } from "./Abas";
import { DashCtx, useDados, type Ctx, type PedidoDetalhe } from "./dados";
import { Detalhe } from "./Detalhe";
import { hora, nomeFiltro, nomeMeio, periodoDe, textoValor, variacao, type Preset, type Unidade } from "./formato";
import { nativo, ouvir, type Contexto, type ErroNativo, type Filtros, type Valor } from "./nativo";

const ABAS = [
  { id: "geral", nome: "Visão geral", C: VisaoGeral },
  { id: "filas", nome: "Filas e atendimento", C: FilasAtendimento },
  { id: "financeiro", nome: "Financeiro", C: Financeiro },
  { id: "pessoas", nome: "Profissionais e serviços", C: ProfissionaisServicos },
  { id: "fidelidade", nome: "Fidelidade", C: Fidelidade },
] as const;

const PRESETS: { id: Preset; nome: string }[] = [
  { id: "hoje", nome: "Hoje" }, { id: "ontem", nome: "Ontem" }, { id: "7d", nome: "Últimos 7 dias" },
  { id: "30d", nome: "Últimos 30 dias" }, { id: "mes", nome: "Mês atual" }, { id: "personalizado", nome: "Personalizado" },
];

export function Painel({ contexto, lojaInicial, aoEncerrar }: { contexto: Contexto; lojaInicial: string | null; aoEncerrar: (e: ErroNativo) => void }) {
  const lojas = contexto.establishments;
  const [loja, setLoja] = useState(() => lojas.find((l) => l.id === lojaInicial)?.id ?? lojas[0]?.id ?? "");
  const tz = lojas.find((l) => l.id === loja)?.timezone ?? "America/Sao_Paulo";
  const [preset, setPreset] = useState<Preset>("hoje");
  const [custom, setCustom] = useState(() => periodoDe("7d", tz));
  const [compare, setCompare] = useState(true);
  const [prof, setProf] = useState<string | null>(null);
  const [serv, setServ] = useState<string | null>(null);
  const [meio, setMeio] = useState<"pix" | "cash" | "card" | null>(null);
  const [aba, setAba] = useState<(typeof ABAS)[number]["id"]>("geral");
  const [versoes, setVersoes] = useState<Record<string, number>>({});
  const [detalhe, setDetalhe] = useState<PedidoDetalhe | null>(null);
  const [aoVivo, setAoVivo] = useState<"conectando" | "ao_vivo" | "reconectando">("conectando");

  const periodo = preset === "personalizado" ? custom : periodoDe(preset, tz);
  const filtros: Filtros = useMemo(
    () => ({ from: periodo.from, to: periodo.to, compare, professional_id: prof, service_id: serv, method: meio }),
    [periodo.from, periodo.to, compare, prof, serv, meio],
  );

  // Troca de loja: filtros da loja anterior não valem (ids de profissional/serviço são da loja).
  const trocarLoja = (id: string) => {
    setDetalhe(null);
    setProf(null);
    setServ(null);
    setVersoes({});
    setLoja(id);
  };

  // Fluxo de invalidação da loja atual: recarrega só as áreas afetadas.
  useEffect(() => {
    if (!loja) return;
    setAoVivo("conectando");
    void nativo.iniciarFluxo(loja).catch(aoEncerrar);
    const soltar = ouvir<{ event: string; loja: string; data?: { areas?: string[]; reason?: string } }>("dashboard://fluxo", (m) => {
      if (m.loja !== loja) return; // aviso atrasado de outra loja
      if (m.event === "conectado" || m.event === "ready") setAoVivo("ao_vivo");
      if (m.event === "reconectando") setAoVivo("reconectando");
      if (m.event === "invalidate")
        setVersoes((v) => {
          const n = { ...v };
          for (const a of m.data?.areas ?? []) n[a] = (n[a] ?? 0) + 1;
          return n;
        });
      if (m.event === "session_ended")
        aoEncerrar({ status: 401, code: m.data?.reason === "owner_session_replaced" ? "owner_session_replaced" : "desktop_access_invalid", detail: "" });
    });
    return () => {
      soltar();
      void nativo.pararFluxo().catch(() => {});
    };
  }, [loja, aoEncerrar]);

  // Voltou de suspensão ou a rede voltou: confere o acesso antes de mostrar qualquer número novo.
  useEffect(() => {
    const conferir = () => {
      if (document.visibilityState !== "visible") return;
      void nativo.contexto().then(
        () => setVersoes((v) => ({ ...v, summary: (v.summary ?? 0) + 1 })),
        (e: ErroNativo) => aoEncerrar(e),
      );
    };
    window.addEventListener("online", conferir);
    document.addEventListener("visibilitychange", conferir);
    return () => {
      window.removeEventListener("online", conferir);
      document.removeEventListener("visibilitychange", conferir);
    };
  }, [aoEncerrar]);

  const abrirDetalhe = useCallback((d: PedidoDetalhe) => setDetalhe(d), []);
  const ctx: Ctx = { loja, tz, filtros, versoes, encerrar: aoEncerrar, abrirDetalhe };
  const Aba = ABAS.find((a) => a.id === aba)?.C ?? VisaoGeral;
  const ativos = [prof && "professional_id", serv && "service_id", meio && "method"].filter(Boolean) as string[];

  return (
    <DashCtx.Provider value={ctx}>
      <div className="min-h-dvh bg-bg">
        <header className="sticky top-0 z-30 grid gap-3 border-b border-line bg-surface/95 px-4 py-3 backdrop-blur sm:px-6">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <div className="grid min-w-0">
              <span className="text-xs font-semibold tracking-wide text-fg-muted uppercase">Dashboard</span>
              {lojas.length > 1 ? (
                <select aria-label="Loja" value={loja} onChange={(e) => trocarLoja(e.target.value)} className="min-h-10 rounded-lg border border-line bg-surface px-2 text-lg font-semibold">
                  {lojas.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
                </select>
              ) : (
                <h1 className="m-0 truncate text-lg font-semibold">{lojas[0]?.name}</h1>
              )}
            </div>
            <label className="grid text-sm">
              <span className="text-xs text-fg-muted">Período</span>
              <select value={preset} onChange={(e) => setPreset(e.target.value as Preset)} className="min-h-10 rounded-lg border border-line bg-surface px-2">
                {PRESETS.map((p) => <option key={p.id} value={p.id}>{p.nome}</option>)}
              </select>
            </label>
            {preset === "personalizado" && (
              <div className="flex items-end gap-2 text-sm">
                <label className="grid"><span className="text-xs text-fg-muted">De</span>
                  <input type="date" value={custom.from} max={custom.to} onChange={(e) => setCustom((c) => ({ ...c, from: e.target.value }))} className="min-h-10 rounded-lg border border-line bg-surface px-2" /></label>
                <label className="grid"><span className="text-xs text-fg-muted">Até</span>
                  <input type="date" value={custom.to} min={custom.from} onChange={(e) => setCustom((c) => ({ ...c, to: e.target.value }))} className="min-h-10 rounded-lg border border-line bg-surface px-2" /></label>
              </div>
            )}
            <label className="flex min-h-10 items-center gap-2 text-sm">
              <input type="checkbox" checked={compare} onChange={(e) => setCompare(e.target.checked)} className="size-4 accent-[var(--primary)]" />
              Comparar com o período anterior
            </label>
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <span role="status" className={cx("text-xs", aoVivo === "ao_vivo" ? "text-success" : "text-warning")}>
                {aoVivo === "ao_vivo" ? "Atualização automática ligada" : aoVivo === "reconectando" ? "Reconectando… (números podem estar desatualizados)" : "Conectando…"}
              </span>
              <Button variant="secondary" icon={UsersRound} onClick={() => void nativo.abrirGestao(loja, { tela: "filas" })}>Filas</Button>
              <Button variant="ghost" icon={LogOut} onClick={() => void nativo.sair()}>Sair do Dashboard</Button>
            </div>
          </div>
          <Filtros loja={loja} prof={prof} serv={serv} meio={meio} setProf={setProf} setServ={setServ} setMeio={setMeio} ativos={ativos} />
        </header>
        <main className="mx-auto grid max-w-[1600px] gap-4 px-4 py-4 sm:px-6">
          <Indicadores />
          <nav role="tablist" aria-label="Detalhes" className="flex gap-1 overflow-x-auto border-b border-line">
            {ABAS.map((a) => (
              <button key={a.id} role="tab" type="button" aria-selected={aba === a.id} onClick={() => setAba(a.id)}
                className={cx("min-h-11 shrink-0 border-b-2 px-3 text-sm font-medium", aba === a.id ? "border-[var(--primary)] text-accent" : "border-transparent text-fg-muted hover:text-fg")}>
                {a.nome}
              </button>
            ))}
          </nav>
          <div role="tabpanel" aria-label={ABAS.find((a) => a.id === aba)?.nome}>
            <Aba key={`${loja}-${aba}`} />
          </div>
        </main>
        {detalhe && <Detalhe pedido={detalhe} aoFechar={() => setDetalhe(null)} />}
      </div>
    </DashCtx.Provider>
  );
}

function Filtros(p: {
  loja: string; prof: string | null; serv: string | null; meio: "pix" | "cash" | "card" | null;
  setProf: (v: string | null) => void; setServ: (v: string | null) => void; setMeio: (v: "pix" | "cash" | "card" | null) => void; ativos: string[];
}) {
  // Opções vêm dos próprios detalhamentos da loja (sem filtro), nunca de outra loja.
  const profs = useDados<{ rows: { professional_id: string; name: string }[] }>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "professionals", filtros: { from: c.filtros.from, to: c.filtros.to, compare: false } }), ["professionals"]);
  const servs = useDados<{ rows: { service_id: string; name: string }[] }>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "services", filtros: { from: c.filtros.from, to: c.filtros.to, compare: false } }), ["services"]);
  const sel = "min-h-9 rounded-lg border border-line bg-surface px-2 text-sm";
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <select aria-label="Profissional" className={sel} value={p.prof ?? ""} onChange={(e) => p.setProf(e.target.value || null)}>
        <option value="">Todos os profissionais</option>
        {(profs.dados?.data.rows ?? []).map((r) => <option key={r.professional_id} value={r.professional_id}>{r.name}</option>)}
      </select>
      <select aria-label="Serviço" className={sel} value={p.serv ?? ""} onChange={(e) => p.setServ(e.target.value || null)}>
        <option value="">Todos os serviços</option>
        {(servs.dados?.data.rows ?? []).map((r) => <option key={r.service_id} value={r.service_id}>{r.name}</option>)}
      </select>
      <select aria-label="Meio de pagamento" className={sel} value={p.meio ?? ""} onChange={(e) => p.setMeio((e.target.value || null) as typeof p.meio)}>
        <option value="">Todos os meios</option>
        {Object.entries(nomeMeio).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
      </select>
      {p.ativos.length > 0 && (
        <>
          <span className="rounded-full bg-select px-3 py-1 font-medium text-accent" role="status">
            Filtrando por: {p.ativos.map((a) => nomeFiltro[a]).join(", ")}
          </span>
          <Button variant="ghost" icon={FilterX} onClick={() => { p.setProf(null); p.setServ(null); p.setMeio(null); }}>Limpar filtros</Button>
        </>
      )}
    </div>
  );
}

type Resumo = {
  current: { cash: Record<string, Valor>; completed: { count: Valor }; wait: { median_seconds: Valor; p90_seconds: Valor } };
  compare: Resumo["current"] | null;
  pending_now: { snapshot_at: string; total: number; open_financial_issues: number; open_financial_issues_cents: number; orders_awaiting_payment: number; orders_awaiting_payment_cents: number; orders_awaiting_confirmation: number; charges_unknown: number };
};

/** Primeira dobra: no máximo quatro números, com período, comparação e instante. */
function Indicadores() {
  const { dados, erro, carregando, recarregar } = useDados<Resumo>((c) => ({ tipo: "resumo", loja: c.loja, filtros: c.filtros }), ["summary", "cash", "movement", "wait", "pending"]);
  const d = dados?.data;
  const m = dados?.meta;
  const tile = (titulo: string, v: Valor | undefined, u: Unidade, antes: Valor | undefined, extra?: string) => (
    <div className="grid content-start gap-1 rounded-xl border border-line bg-surface p-4">
      <span className="text-sm text-fg-muted">{titulo}</span>
      <strong className={cx("font-semibold tabular-nums", v?.status === "ok" ? "text-3xl" : "text-base text-fg-muted")}>{textoValor(v, u)}</strong>
      {d?.compare && <span className="text-xs text-fg-muted">{variacao(v, antes, u) ?? "Comparação indisponível"}</span>}
      {extra && <span className="text-xs text-fg-muted">{extra}</span>}
    </div>
  );
  return (
    <section aria-label="Indicadores principais" className="grid gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-fg-muted">
        <span>
          {m ? `${m.establishment_name} · ${m.period.from === m.period.to ? m.period.from.split("-").reverse().join("/") : `${m.period.from.split("-").reverse().join("/")} a ${m.period.to.split("-").reverse().join("/")}`}` : "…"}
          {m?.compare_period && ` · comparado com ${m.compare_period.from.split("-").reverse().join("/")}${m.compare_period.to !== m.compare_period.from ? ` a ${m.compare_period.to.split("-").reverse().join("/")}` : ""} (mesmo tempo transcorrido)`}
        </span>
        <span className="flex items-center gap-2">
          {m && `Atualizado às ${hora(m.generated_at, m.timezone)} (${m.timezone})`}
          <button type="button" onClick={recarregar} aria-label="Atualizar agora" className="inline-flex size-9 items-center justify-center rounded-lg hover:bg-surface-2">
            <RefreshCw aria-hidden="true" className={cx("size-4", carregando && "animate-spin")} />
          </button>
        </span>
      </div>
      {erro && <p role="alert" className="m-0 text-sm text-danger">{erro.detail}</p>}
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {tile("Recebimentos líquidos", d?.current.cash.net_cents, "cents", d?.compare?.cash.net_cents,
          d && d.current.cash.received_cents?.status === "ok" ? `recebido ${textoValor(d.current.cash.received_cents, "cents")} − estornado ${textoValor(d.current.cash.refunded_cents, "cents")}` : undefined)}
        {tile("Recebido após custos", d?.current.cash.after_costs_cents, "cents", d?.compare?.cash.after_costs_cents,
          d && d.current.cash.fee_coverage
            ? `custos estimados ${textoValor(d.current.cash.fees_estimated_cents, "cents")} · informados ${textoValor(d.current.cash.fees_known_cents, "cents")} · ` +
              (d.current.cash.fee_coverage.status === "ok" && (d.current.cash.fee_coverage.value ?? 1) < 1
                ? `INCOMPLETO: ${Math.round((d.current.cash.fee_coverage.value ?? 0) * 100)}% dos recebimentos com custo conhecido`
                : "todos os recebimentos com custo conhecido") + " · não é lucro"
            : undefined)}
        {tile("Atendimentos concluídos", d?.current.completed.count, "count", d?.compare?.completed.count, "participações concluídas (uma comanda com 3 cortes conta 1)")}
        {tile("Espera mediana até a chamada", d?.current.wait.median_seconds, "seconds", d?.compare?.wait.median_seconds,
          d?.current.wait.median_seconds.sample !== undefined ? `${d.current.wait.median_seconds.sample} chamada(s) · p90 ${textoValor(d.current.wait.p90_seconds, "seconds")}` : undefined)}
        <div className="grid content-start gap-1 rounded-xl border border-line bg-surface p-4">
          <span className="flex items-center gap-2 text-sm text-fg-muted">Pendências financeiras <span className="rounded bg-surface-2 px-1.5 text-xs font-semibold text-fg">AGORA</span></span>
          <strong className="text-3xl font-semibold tabular-nums">{d ? d.pending_now.total : "—"}</strong>
          {d && <span className="text-xs text-fg-muted">situação às {hora(d.pending_now.snapshot_at, m?.timezone ?? "UTC")} · {d.pending_now.open_financial_issues} aberta(s), {d.pending_now.orders_awaiting_payment} comanda(s) a receber</span>}
        </div>
      </div>
    </section>
  );
}
