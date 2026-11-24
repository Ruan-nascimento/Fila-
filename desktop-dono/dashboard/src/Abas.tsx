import { useState } from "react";
import { Notice } from "@web/ui/ui";
import { Barras, type Linha } from "./graficos/Barras";
import { Cartao, Tabela, Vazio } from "./graficos/Cartao";
import { DIAS, MapaCalor, type Celula } from "./graficos/MapaCalor";
import { SerieTemporal, type Bucket, type DefSerie } from "./graficos/SerieTemporal";
import { useDados, useDash, type Ctx } from "./dados";
import { formatar, nomeFiltro, nomeMeio, textoValor, type Unidade } from "./formato";
import { nativo, type Meta, type Registro, type Valor } from "./nativo";

type Serie = { metric: string; series: DefSerie[]; buckets: Bucket[]; totals: Record<string, Valor>; compare_totals: Record<string, Valor> | null };
type Breakdown<T> = { dimension: string; status: "ok" | "not_applicable" | "not_collected"; rows: T };

function AvisoFiltro({ meta }: { meta: Meta | undefined }) {
  if (!meta?.ignored_filters.length) return null;
  return (
    <Notice kind="info">
      Filtro sem atribuição confiável neste gráfico: {meta.ignored_filters.map((f) => nomeFiltro[f] ?? f).join(", ")}. Os valores
      aparecem como “não se aplica” (nada é rateado).
    </Notice>
  );
}

function Estado({ carregando, erro }: { carregando: boolean; erro: { detail: string } | null }) {
  if (erro) return <Notice kind="erro">{erro.detail}</Notice>;
  if (carregando) return <p role="status" className="m-0 text-sm text-fg-muted">Carregando…</p>;
  return null;
}

/** Série temporal + tabela equivalente + clique → registros. */
function GraficoSerie({
  metrica, titulo, definicao, chaves, forma, area, registro, recorte,
}: {
  metrica: "cash" | "ticket" | "movement" | "wait" | "duration" | "loyalty";
  titulo: string;
  definicao: string;
  chaves?: string[];
  forma?: "linha" | "coluna";
  area: string;
  registro?: (serie: string) => { registro: Registro; extra?: Record<string, string> } | null;
  recorte?: string;
}) {
  const ctx = useDash();
  const { dados, erro, carregando } = useDados<Serie>((c: Ctx) => ({ tipo: "serie", loja: c.loja, metrica, filtros: c.filtros }), [area]);
  const series = (dados?.data.series ?? []).filter((s) => !chaves || chaves.includes(s.key)) as DefSerie[];
  const buckets = dados?.data.buckets ?? [];
  const vazio = buckets.length > 0 && buckets.every((b) => series.every((s) => b.values[s.key]?.status !== "ok" || !b.values[s.key]?.value));
  const principal = series[0];
  return (
    <Cartao
      titulo={titulo}
      definicao={definicao}
      aviso={<AvisoFiltro meta={dados?.meta} />}
      tabela={
        dados && (
          <Tabela
            cabecalho={["Período", ...series.map((s) => s.label)]}
            linhas={buckets.map((b) => [b.label + (b.partial ? " (parcial)" : ""), ...series.map((s) => textoValor(b.values[s.key], s.unit as Unidade))])}
          />
        )
      }
    >
      <Estado carregando={carregando && !dados} erro={erro} />
      {dados && (vazio ? <Vazio /> : (
        <SerieTemporal
          rotulo={titulo}
          buckets={buckets}
          series={series}
          forma={forma ?? "linha"}
          aoSelecionar={(b) => {
            const alvo = principal && registro?.(principal.key);
            if (!alvo) return;
            const v = principal ? b.values[principal.key] : undefined;
            ctx.abrirDetalhe({
              titulo: `${titulo} · ${b.label}`,
              registro: alvo.registro,
              recorte: { bucket_start: b.start, bucket_end: b.end, ...(recorte ? { ref: recorte } : {}), ...alvo.extra },
              ...(v?.status === "ok" && principal?.unit === "cents" ? { esperado: { texto: textoValor(v, "cents"), centavos: v.value ?? 0 } } : {}),
            });
          }}
        />
      ))}
      {dados && (
        <p className="m-0 text-xs text-fg-muted">
          Total do período: {series.map((s) => `${s.label} ${textoValor(dados.data.totals[s.key], s.unit as Unidade)}`).join(" · ")}
        </p>
      )}
    </Cartao>
  );
}

// ---------------------------------------------------------------------------------------------

export function VisaoGeral() {
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <GraficoSerie metrica="cash" area="cash" titulo="Caixa" forma="coluna"
        definicao="Recebido por data de confirmação; estornos efetivados por data de efetivação; líquido = recebido − estornado. Pix pendente e estorno em análise não entram."
        registro={(k) => (k === "refunded_cents" ? { registro: "refunds" } : { registro: "receipts" })} />
      <GraficoSerie metrica="movement" area="movement" titulo="Movimento" forma="coluna"
        definicao="Entradas na fila contadas pelo horário de ENTRADA e atendimentos concluídos pelo horário de CONCLUSÃO: não são a mesma coorte."
        registro={() => ({ registro: "participations" })} recorte="entered" />
    </div>
  );
}

export function FilasAtendimento() {
  const ctx = useDash();
  const bandas = useDados<Breakdown<{ band: string; count: number }[]>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "wait_bands", filtros: c.filtros }), ["wait_bands"]);
  const desf = useDados<Breakdown<{ denominator: string; denominator_count: number; open_count: number; items: { status: string; count: number; percent: number | null; open: boolean }[] }>>(
    (c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "outcomes", filtros: c.filtros }), ["outcomes"]);
  const pico = useDados<Breakdown<Celula[]>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "peak_hours", filtros: c.filtros }), ["peak_hours"]);
  const nomes: Record<string, string> = { completed: "Concluídos", interrupted: "Interrompidos", no_show: "Ausências", cancelled: "Cancelamentos", waiting: "Esperando agora", called: "Chamados agora", in_service: "Em atendimento agora" };
  const d = desf.dados?.data.rows;
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <GraficoSerie metrica="wait" area="wait" titulo="Espera até a chamada"
        definicao="chamada − entrada, só de quem foi chamado (sem chamada fica fora). Mediana e percentil 90; transferência não reinicia o relógio."
        registro={() => ({ registro: "participations" })} recorte="called" />
      <Cartao titulo="Distribuição da espera" definicao="Participações chamadas no período, por faixa de minutos."
        tabela={bandas.dados && <Tabela cabecalho={["Faixa", "Chamadas"]} linhas={(bandas.dados.data.rows ?? []).map((b) => [`${b.band} min`, b.count])} />}>
        <Estado carregando={bandas.carregando && !bandas.dados} erro={bandas.erro} />
        {bandas.dados && (bandas.dados.data.status !== "ok" ? <Vazio>{textoStatus(bandas.dados.data.status)}</Vazio> :
          (bandas.dados.data.rows.every((b) => !b.count) ? <Vazio /> :
          <Barras unidade="count" linhas={bandas.dados.data.rows.map((b) => ({ chave: b.band, rotulo: `${b.band} min`, valor: b.count }))}
            aoSelecionar={(l) => ctx.abrirDetalhe({ titulo: `Espera ${l.rotulo}`, registro: "participations", recorte: { ref: "called", band: l.chave }, esperado: { texto: `${l.valor} chamadas`, quantidade: l.valor } })} />))}
      </Cartao>
      <GraficoSerie metrica="duration" area="duration" titulo="Duração do atendimento"
        definicao="fim − início dos CONCLUÍDOS (mediana). Interrompidos contados à parte."
        registro={() => ({ registro: "participations", extra: { status: "completed" } })} recorte="finished" />
      <Cartao titulo="Desfechos" definicao={d ? `Taxas sobre ${d.denominator} (${d.denominator_count}). Abertas agora: ${d.open_count}, sem taxa.` : "Coorte por horário de entrada."}
        tabela={d && <Tabela cabecalho={["Desfecho", "Quantidade", "%"]} linhas={d.items.map((o) => [nomes[o.status] ?? o.status, o.count, o.percent === null ? "—" : formatar(o.percent, "percent")])} />}>
        <Estado carregando={desf.carregando && !desf.dados} erro={desf.erro} />
        {desf.dados && (desf.dados.data.status !== "ok" ? <Vazio>{textoStatus(desf.dados.data.status)}</Vazio> :
          <Barras unidade="count" linhas={d!.items.map((o) => ({ chave: o.status, rotulo: nomes[o.status] ?? o.status, valor: o.count, secundario: o.percent === null ? (o.open ? "aberta" : undefined) : formatar(o.percent, "percent") }))}
            aoSelecionar={(l) => ctx.abrirDetalhe({ titulo: `Desfecho: ${l.rotulo}`, registro: "participations", recorte: { ref: "entered", status: l.chave }, esperado: { texto: `${l.valor}`, quantidade: l.valor } })} />)}
      </Cartao>
      <div className="xl:col-span-2">
        <Cartao titulo="Horários de pico" definicao={pico.dados ? `Entradas por dia da semana e hora local (${pico.dados.meta.timezone}), de ${pico.dados.meta.period.from} a ${pico.dados.meta.period.to}.` : ""}
          tabela={pico.dados && pico.dados.data.status === "ok" && <Tabela cabecalho={["Dia", "Hora", "Entradas"]} linhas={pico.dados.data.rows.map((c) => [DIAS[c.dow - 1] ?? "", `${c.hour}h`, c.entries])} />}>
          <Estado carregando={pico.carregando && !pico.dados} erro={pico.erro} />
          {pico.dados && (pico.dados.data.status !== "ok" ? <Vazio>{textoStatus(pico.dados.data.status)}</Vazio> : pico.dados.data.rows.length === 0 ? <Vazio /> :
            <MapaCalor celulas={pico.dados.data.rows} aoSelecionar={(c) => ctx.abrirDetalhe({ titulo: `${DIAS[c.dow - 1]} às ${c.hour}h`, registro: "participations", recorte: { ref: "entered", dow: c.dow, hour: c.hour }, esperado: { texto: `${c.entries} entradas`, quantidade: c.entries } })} />)}
        </Cartao>
      </div>
    </div>
  );
}

type Metodo = { method: string; received_cents: number; receipts: number; refunded_cents: number; refunds: number; net_cents: number };
type Pend = { open_now: { kind: string; count: number; amount_cents: number | null }[]; age_bands_now: { band: string; count: number }[]; opened_in_period: number; resolved_in_period: number };

const nomePend: Record<string, string> = {
  order_awaiting_payment: "Comandas aguardando pagamento", order_awaiting_confirmation: "Recebimentos a conferir",
  pix_charge_unknown: "Pix com resultado desconhecido", duplicate_receipt: "Recebimento em duplicidade", late_receipt: "Pagamento tardio",
  amount_mismatch: "Valor divergente", charge_outcome_unresolved: "Cobrança sem desfecho", refund_under_review: "Estorno em análise",
  active_pix_on_method_switch: "Pix ativo na troca de meio", adjustment_excess: "Excedente após correção",
};

export function Financeiro() {
  const ctx = useDash();
  const meios = useDados<Breakdown<Metodo[]>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "payment_methods", filtros: c.filtros }), ["payment_methods"]);
  const pend = useDados<Breakdown<Pend>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "pending", filtros: { ...c.filtros, professional_id: null, service_id: null, method: null } }), ["pending"]);
  const m = meios.dados?.data;
  const p = pend.dados?.data.rows;
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <GraficoSerie metrica="cash" area="cash" titulo="Recebido, estornado e líquido"
        definicao="Valor recebido pela loja (o dinheiro entregue antes do troco não é receita). Clique num período para ver os recebimentos."
        registro={(k) => (k === "refunded_cents" ? { registro: "refunds" } : { registro: "receipts" })} />
      <Cartao titulo="Meios de pagamento" definicao="Recebimentos confirmados por meio (valor e quantidade) e estornos efetivados."
        aviso={<AvisoFiltro meta={meios.dados?.meta} />}
        tabela={m?.status === "ok" && <Tabela cabecalho={["Meio", "Recebido", "Qtd.", "Estornado", "Líquido"]} linhas={m.rows.map((r) => [nomeMeio[r.method] ?? r.method, formatar(r.received_cents, "cents"), r.receipts, formatar(r.refunded_cents, "cents"), formatar(r.net_cents, "cents")])} />}>
        <Estado carregando={meios.carregando && !m} erro={meios.erro} />
        {m && (m.status !== "ok" ? <Vazio>{textoStatus(m.status)}</Vazio> : m.rows.every((r) => !r.receipts && !r.refunds) ? <Vazio /> :
          <Barras unidade="cents" linhas={m.rows.map((r) => ({ chave: r.method, rotulo: nomeMeio[r.method] ?? r.method, valor: r.received_cents, secundario: `${r.receipts} recebimento(s)` }))}
            aoSelecionar={(l) => ctx.abrirDetalhe({ titulo: `Recebimentos: ${l.rotulo}`, registro: "receipts", recorte: {}, esperado: { texto: formatar(l.valor, "cents"), centavos: l.valor } })} />)}
      </Cartao>
      <GraficoSerie metrica="ticket" area="ticket" titulo="Ticket médio" chaves={["average_cents"]}
        definicao="Valor cobrado das comandas liquidadas ÷ quantidade dessas comandas, pela data de liquidação. Correções posteriores entram na data original."
        registro={() => ({ registro: "orders" })} />
      <Cartao titulo="Pendências financeiras" definicao={pend.dados?.meta.snapshot_at ? `Abertas AGORA (situação de ${new Date(pend.dados.meta.snapshot_at).toLocaleTimeString("pt-BR", { timeZone: ctx.tz, hour: "2-digit", minute: "2-digit" })}). No período: ${p?.opened_in_period ?? 0} aberta(s), ${p?.resolved_in_period ?? 0} resolvida(s).` : ""}
        tabela={p && <Tabela cabecalho={["Situação", "Qtd.", "Valor"]} linhas={p.open_now.map((r) => [nomePend[r.kind] ?? r.kind, r.count, r.amount_cents === null ? "—" : formatar(r.amount_cents, "cents")])} />}>
        <Estado carregando={pend.carregando && !p} erro={pend.erro} />
        {p && (p.open_now.length === 0 ? <Vazio>Nenhuma pendência aberta agora.</Vazio> : (
          <>
            <Barras unidade="count" cor="var(--serie-2)" linhas={p.open_now.map((r) => ({ chave: r.kind, rotulo: nomePend[r.kind] ?? r.kind, valor: r.count, secundario: r.amount_cents === null ? undefined : formatar(r.amount_cents, "cents") }))}
              aoSelecionar={(l) => (l.chave.startsWith("order_") || l.chave === "pix_charge_unknown"
                ? void nativo.abrirGestao(ctx.loja, { tela: "pendencias" })
                : ctx.abrirDetalhe({ titulo: `Pendências: ${l.rotulo}`, registro: "financial_issues", recorte: { state: "open", kind: l.chave }, esperado: { texto: `${l.valor}`, quantidade: l.valor } }))} />
            <p className="m-0 text-xs text-fg-muted">Idade (agora): {p.age_bands_now.map((b) => `${b.band}: ${b.count}`).join(" · ")}</p>
          </>
        ))}
      </Cartao>
    </div>
  );
}

type Prof = { professional_id: string; name: string; active: boolean; completed: number; wait_median_seconds: Valor; duration_median_seconds: Valor; services_performed: number; transferred_in: number };
type Serv = { service_id: string; name: string; requested: number; performed: number; performed_value_cents: number };
type Prod = { product_id: string; name: string; requested: number; delivered: number; delivered_value_cents: number };

export function ProfissionaisServicos() {
  const ctx = useDash();
  const [ordem, setOrdem] = useState<"completed" | "services_performed">("completed");
  const prof = useDados<Breakdown<Prof[]>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "professionals", filtros: c.filtros }), ["professionals"]);
  const serv = useDados<Breakdown<Serv[]>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "services", filtros: c.filtros }), ["services"]);
  const prod = useDados<Breakdown<Prod[]>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "products", filtros: { ...c.filtros, service_id: null } }), ["products"]);
  const pp = prod.dados?.data.status === "ok" ? prod.dados.data.rows : [];
  const ps = prof.dados?.data.status === "ok" ? [...prof.dados.data.rows].sort((a, b) => b[ordem] - a[ordem] || a.name.localeCompare(b.name)) : [];
  const ss = serv.dados?.data.status === "ok" ? serv.dados.data.rows : [];
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Cartao titulo="Profissionais" definicao="Atribuição ao profissional da fila FINAL (destino da última transferência). Amostras indicadas."
        acoes={
          <select aria-label="Ordenar por" value={ordem} onChange={(e) => setOrdem(e.target.value as typeof ordem)} className="min-h-9 rounded-lg border border-line bg-surface px-2 text-sm">
            <option value="completed">Concluídos</option>
            <option value="services_performed">Serviços realizados</option>
          </select>
        }
        tabela={<Tabela cabecalho={["Profissional", "Concluídos", "Espera mediana", "Duração mediana", "Serviços", "Recebeu transf."]}
          linhas={ps.map((p) => [p.name + (p.active ? "" : " (inativo)"), p.completed, textoValor(p.wait_median_seconds, "seconds") + (p.wait_median_seconds.sample ? ` (n=${p.wait_median_seconds.sample})` : ""), textoValor(p.duration_median_seconds, "seconds"), p.services_performed, p.transferred_in])} />}>
        <Estado carregando={prof.carregando && !prof.dados} erro={prof.erro} />
        {prof.dados && (prof.dados.data.status !== "ok" ? <Vazio>{textoStatus(prof.dados.data.status)}</Vazio> : ps.length === 0 ? <Vazio /> :
          <Barras unidade="count" linhas={ps.map((p): Linha => ({ chave: p.professional_id, rotulo: p.name, valor: p[ordem], secundario: `espera ${textoValor(p.wait_median_seconds, "seconds")}` }))}
            aoSelecionar={(l) => ctx.abrirDetalhe({ titulo: `Concluídos: ${l.rotulo}`, registro: "participations", recorte: { ref: "finished", status: "completed" } })} />)}
      </Cartao>
      <Cartao titulo="Serviços" definicao="UNIDADES solicitadas e realizadas (3 cortes de uma comanda = 3 unidades, 1 participação). Valor = preço registrado × unidades cobradas, não dinheiro recebido (sem rateio). Registros antigos valem 1 unidade."
        tabela={<Tabela cabecalho={["Serviço", "Solicitados", "Realizados", "Valor dos itens"]} linhas={ss.map((s) => [s.name, s.requested, s.performed, formatar(s.performed_value_cents, "cents")])} />}>
        <Estado carregando={serv.carregando && !serv.dados} erro={serv.erro} />
        {serv.dados && (serv.dados.data.status !== "ok" ? <Vazio>{textoStatus(serv.dados.data.status)}</Vazio> : ss.length === 0 ? <Vazio /> :
          <Barras unidade="cents" linhas={ss.map((s) => ({ chave: s.service_id, rotulo: s.name, valor: s.performed_value_cents, secundario: `${s.performed} realizados · ${s.requested} solicitados` }))}
            aoSelecionar={(l) => ctx.abrirDetalhe({ titulo: `Itens realizados: ${l.rotulo}`, registro: "service_items", recorte: { ref: "performed" } })} />)}
      </Cartao>
      <Cartao titulo="Produtos" definicao="Unidades pedidas e entregues e valor das unidades cobradas. Produtos não existiam antes da versão 1.0.0: períodos anteriores ficam vazios."
        tabela={<Tabela cabecalho={["Produto", "Pedidos", "Entregues", "Valor"]} linhas={pp.map((p) => [p.name, p.requested, p.delivered, formatar(p.delivered_value_cents, "cents")])} />}>
        <Estado carregando={prod.carregando && !prod.dados} erro={prod.erro} />
        {prod.dados && (prod.dados.data.status !== "ok" ? <Vazio>{textoStatus(prod.dados.data.status)}</Vazio> : pp.length === 0 ? <Vazio /> :
          <Barras unidade="cents" linhas={pp.map((p) => ({ chave: p.product_id, rotulo: p.name, valor: p.delivered_value_cents, secundario: `${p.delivered} entregues · ${p.requested} pedidos` }))} />)}
      </Cartao>
    </div>
  );
}

type Rewards = { program_enabled_now: boolean; items: { reward_id: string; name: string; reserved: number; delivered: number; cancelled: number; expired: number; points_used: number }[] };
type Rec = { window: string; identified_accounts: number; recurring_accounts: number; rate_percent: Valor; anonymous_visits_excluded: number };

export function Fidelidade() {
  const prem = useDados<Breakdown<Rewards>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "rewards", filtros: { ...c.filtros, professional_id: null, service_id: null, method: null } }), ["rewards"]);
  const rec = useDados<Breakdown<Rec>>((c) => ({ tipo: "detalhamento", loja: c.loja, dimensao: "recurrence", filtros: { ...c.filtros, method: null } }), ["recurrence"]);
  const r = prem.dados?.data.rows;
  const rc = rec.dados?.data.rows;
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      {r && !r.program_enabled_now && (
        <div className="xl:col-span-2">
          <Notice kind="info">A fidelidade está desligada nesta loja agora. O histórico abaixo continua disponível.</Notice>
        </div>
      )}
      <GraficoSerie metrica="loyalty" area="loyalty" titulo="Pontos" chaves={["credited", "used", "returned"]}
        definicao="Tipos reais do extrato: creditados, utilizados (consumo de reserva) e devolvidos. Ajustes (estorno de crédito e encerramento de conta) na tabela."
        registro={(k) => ({ registro: "loyalty_entries", extra: { kind: k } })} />
      <Cartao titulo="Prêmios" definicao="Reservas pela criação; entregas, cancelamentos e expirações pela data em que aconteceram."
        tabela={r && <Tabela cabecalho={["Prêmio", "Reservas", "Entregues", "Canceladas", "Expiradas", "Pontos usados"]} linhas={r.items.map((i) => [i.name, i.reserved, i.delivered, i.cancelled, i.expired, i.points_used])} />}>
        <Estado carregando={prem.carregando && !r} erro={prem.erro} />
        {r && (r.items.length === 0 ? <Vazio /> : <Barras unidade="count" linhas={r.items.map((i) => ({ chave: i.reward_id, rotulo: i.name, valor: i.delivered, secundario: `${i.reserved} reservas` }))} />)}
      </Cartao>
      <Cartao titulo="Recorrência" definicao="Contas identificadas com 2+ visitas concluídas no período. Visitantes anônimos ficam fora (apelido não identifica ninguém).">
        <Estado carregando={rec.carregando && !rc} erro={rec.erro} />
        {rec.dados && rec.dados.data.status !== "ok" && <Vazio>{textoStatus(rec.dados.data.status)}</Vazio>}
        {rc && rec.dados?.data.status === "ok" && (
          <dl className="m-0 grid grid-cols-2 gap-3 text-sm">
            <div><dt className="text-fg-muted">Taxa de recorrência</dt><dd className="m-0 text-2xl font-semibold tabular-nums">{textoValor(rc.rate_percent, "percent")}</dd></div>
            <div><dt className="text-fg-muted">Amostra</dt><dd className="m-0 tabular-nums">{rc.recurring_accounts} de {rc.identified_accounts} contas</dd></div>
            <div className="col-span-2"><dt className="text-fg-muted">Visitas anônimas excluídas</dt><dd className="m-0 tabular-nums">{rc.anonymous_visits_excluded}</dd></div>
          </dl>
        )}
      </Cartao>
    </div>
  );
}

function textoStatus(s: string): string {
  return s === "not_applicable" ? "Este detalhamento não se aplica ao filtro escolhido." : s === "not_collected" ? "Período anterior ao início da coleta." : "Sem dados para este período";
}
