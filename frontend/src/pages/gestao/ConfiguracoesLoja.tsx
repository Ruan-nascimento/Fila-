import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { Gift } from "lucide-react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { centsToInput, formatCents, parseCents } from "../../money";
import {
  Button,
  Choice,
  ConfirmPanel,
  Field,
  LoadError,
  Loading,
  Notice,
  Section,
  Submit,
} from "../../ui/ui";
import { useUnsavedWarning } from "../../ui/useUnsavedWarning";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Settings = components["schemas"]["EstablishmentSettings"];
type Fee = components["schemas"]["PaymentFee"];
type Costs = components["schemas"]["ReceiptCosts"];
export type SecaoConfig = "catalogo" | "filas" | "pagamentos" | "fidelidade" | "equipe";

/**
 * Configurações da loja por assunto (ADR-0034, docs/configuracoes-da-loja.md). Cada seção salva
 * sozinha, com a versão dela: duas pessoas mexendo em seções diferentes não se atrapalham. O
 * aviso de sucesso só aparece depois da confirmação do servidor.
 */
export function ConfiguracoesLoja({ ctx, secao }: { ctx: StaffContext; secao: SecaoConfig }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(async () => {
    const out = await call(() =>
      api.GET("/v1/establishments/{establishmentId}/settings", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    );
    if (out.ok) {
      setSettings(out.data.data);
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
  }, [ctx.estId]);
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/settings", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) setSettings(out.data.data);
      else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [ctx.estId]);

  if (error && !settings) return <LoadError message={error} onRetry={() => void load()} />;
  if (!settings) return <Loading>Carregando configurações…</Loading>;
  const props = { ctx, settings, onSaved: setSettings, onConflict: load };
  return (
    <div className="grid max-w-3xl gap-8">
      {secao === "catalogo" && <SecaoCatalogo {...props} />}
      {secao === "filas" && <SecaoFilas {...props} />}
      {secao === "equipe" && <SecaoEquipe {...props} />}
      {secao === "pagamentos" && (
        <>
          <SecaoPagamentos {...props} />
          <Custos ctx={ctx} />
          <ResumoCustos ctx={ctx} />
        </>
      )}
      {secao === "fidelidade" && (
        <Section title="Fidelidade" description="Programa de pontos e prêmios da loja.">
          <p className="m-0 text-sm">
            Pontos são por unidade de serviço cobrada e vão para o titular com conta. Produtos não geram
            pontos.
          </p>
          <Link
            to={`/gestao/${ctx.estId}/premios`}
            className="inline-flex min-h-11 items-center gap-2 font-semibold"
          >
            <Gift aria-hidden="true" className="size-5" /> Pontos e prêmios
          </Link>
        </Section>
      )}
    </div>
  );
}

type SectionProps = {
  ctx: StaffContext;
  settings: Settings;
  onSaved: (s: Settings) => void;
  onConflict: () => Promise<void>;
};

/** Esqueleto comum: formulário, estado de não salvo, sucesso após o servidor, conflito. */
function useSection<T extends Record<string, unknown>>(initial: T) {
  const [form, setForm] = useState<T>(initial);
  const [base, setBase] = useState<T>(initial);
  const [saved, setSaved] = useState(false);
  const [conflict, setConflict] = useState(false);
  const dirty = JSON.stringify(form) !== JSON.stringify(base);
  useUnsavedWarning(dirty);
  // Dados novos do servidor (salvou, outra aba): sem alteração local, acompanha.
  const [seen, setSeen] = useState(initial);
  if (JSON.stringify(seen) !== JSON.stringify(initial)) {
    setSeen(initial);
    setBase(initial);
    if (!dirty) setForm(initial);
  }
  return {
    form,
    set: (patch: Partial<T>) => {
      setSaved(false);
      setForm((f) => ({ ...f, ...patch }));
    },
    dirty,
    saved,
    setSaved,
    conflict,
    setConflict,
    reset: () => {
      setForm(base);
      setConflict(false);
    },
    changes: () =>
      Object.fromEntries(
        Object.entries(form).filter(([k, v]) => JSON.stringify(v) !== JSON.stringify(base[k])),
      ),
  };
}

function Status({ s, onDiscard }: { s: ReturnType<typeof useSection>; onDiscard: () => void }) {
  return (
    <>
      {s.conflict && (
        <Notice kind="info" title="Esta seção foi alterada em outra aba ou aparelho">
          <p>O que você escolheu foi mantido. Revise e salve de novo, ou descarte para ver a versão atual.</p>
          <p>
            <Button variant="secondary" onClick={onDiscard}>
              Descartar minhas alterações
            </Button>
          </p>
        </Notice>
      )}
      {s.saved && !s.dirty && (
        <p role="status" className="m-0 text-sm font-semibold text-success">
          Salvo.
        </p>
      )}
      {s.dirty && (
        <p className="m-0 text-sm text-warning" role="status">
          Alterações não salvas nesta seção.
        </p>
      )}
    </>
  );
}

function Toggle({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint: ReactNode;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <Choice
      type="checkbox"
      checked={checked}
      onChange={(e) => onChange(e.target.checked)}
      label={label}
      description={hint}
    />
  );
}

function intIn(v: string, lo: number, hi: number): number | null {
  if (!/^\d{1,3}$/.test(v)) return null;
  const n = Number(v);
  return n >= lo && n <= hi ? n : null;
}

// ---------------------------------------------------------------------------------- Catálogo

function SecaoCatalogo({ ctx, settings, onSaved, onConflict }: SectionProps) {
  const c = settings.catalog;
  const s = useSection({ max: String(c.max_service_quantity), products: c.products_enabled });
  const cmd = useCommand();
  const [local, setLocal] = useState<string | null>(null);
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const max = intIn(s.form.max, 1, 50);
    if (max === null) {
      setLocal("Use um número de 1 a 50.");
      return;
    }
    setLocal(null);
    const body = {
      expected_version: c.version,
      ...(max !== c.max_service_quantity ? { max_service_quantity: max } : {}),
      ...(s.form.products !== c.products_enabled ? { products_enabled: s.form.products } : {}),
    };
    const out = await cmd.run(`cfg:catalogo:${c.version}:${JSON.stringify(body)}`, (key) =>
      api.PATCH("/v1/establishments/{establishmentId}/settings/catalog", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      onSaved(out.data.data);
      s.setSaved(true);
    } else if (out.problem?.status === 409) {
      s.setConflict(true);
      await onConflict();
    }
  }
  const errors = fieldErrors(cmd.problem);
  return (
    <Section title="Catálogo" description="Como os clientes montam o pedido.">
      <form onSubmit={onSubmit} noValidate aria-label="Configurações do catálogo" className="grid gap-4">
        <Status s={s} onDiscard={s.reset} />
        {!cmd.problem?.errors?.length && cmd.problem?.status !== 409 && (
          <CommandError problem={cmd.problem} offline={cmd.offline} />
        )}
        <Field
          label="Unidades do mesmo item por pedido"
          inputMode="numeric"
          value={s.form.max}
          error={local ?? errors.max_service_quantity}
          hint="De 1 a 50 (padrão 10). Ex.: um responsável com dois filhos pede 3 cortes. Reduzir não invalida pedidos já aceitos."
          onChange={(e) => s.set({ max: e.target.value })}
          wrapperClassName="max-w-xs"
        />
        <Toggle
          label="Oferecer produtos como complemento da comanda"
          hint="Padrão: ligado. Desligar impede novas inclusões; produtos já incluídos em comandas continuam."
          checked={s.form.products}
          onChange={(v) => s.set({ products: v })}
        />
        <div>
          <Submit busy={cmd.busy !== null}>Salvar catálogo</Submit>
        </div>
      </form>
    </Section>
  );
}

// -------------------------------------------------------------------------- Filas e atendimento

function SecaoFilas({ ctx, settings, onSaved, onConflict }: SectionProps) {
  const q = settings.queue;
  const s = useSection({
    visitors: q.visitors_enabled,
    tolerance: String(q.call_tolerance_minutes),
    limit: q.default_queue_capacity != null,
    capacity: q.default_queue_capacity != null ? String(q.default_queue_capacity) : "",
  });
  const cmd = useCommand();
  const [local, setLocal] = useState<Record<string, string>>({});
  const [confirm, setConfirm] = useState(false);
  async function save() {
    const tol = intIn(s.form.tolerance, 1, 60);
    const cap = s.form.limit ? intIn(s.form.capacity, 1, 200) : null;
    const errs: Record<string, string> = {};
    if (tol === null) errs.tolerance = "Use de 1 a 60 minutos.";
    if (s.form.limit && cap === null) errs.capacity = "Use de 1 a 200 participações.";
    setLocal(errs);
    if (Object.keys(errs).length > 0 || tol === null) return;
    const body = {
      expected_version: q.version,
      ...(s.form.visitors !== q.visitors_enabled ? { visitors_enabled: s.form.visitors } : {}),
      ...(tol !== q.call_tolerance_minutes ? { call_tolerance_minutes: tol } : {}),
      ...(cap !== (q.default_queue_capacity ?? null) ? { default_queue_capacity: cap } : {}),
    };
    const out = await cmd.run(`cfg:filas:${q.version}:${JSON.stringify(body)}`, (key) =>
      api.PATCH("/v1/establishments/{establishmentId}/settings/queue", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    setConfirm(false);
    if (out.ok) {
      onSaved(out.data.data);
      s.setSaved(true);
    } else if (out.problem?.status === 409) {
      s.setConflict(true);
      await onConflict();
    }
  }
  function onSubmit(e: FormEvent) {
    e.preventDefault();
    // Mudança de impacto real: desligar a entrada como visitante pede confirmação.
    if (q.visitors_enabled && !s.form.visitors) setConfirm(true);
    else void save();
  }
  const errors = fieldErrors(cmd.problem);
  return (
    <Section title="Filas e atendimento" description="Entrada, tolerância depois da chamada e capacidade.">
      <form onSubmit={onSubmit} noValidate aria-label="Configurações das filas" className="grid gap-4">
        <Status s={s} onDiscard={s.reset} />
        {!cmd.problem?.errors?.length && cmd.problem?.status !== 409 && (
          <CommandError problem={cmd.problem} offline={cmd.offline} />
        )}
        <Toggle
          label="Permitir entrada como visitante"
          hint="Padrão: ligado. Desligado, ninguém novo entra como visitante; visitas já ativas continuam, inclusive pagamento e recuperação."
          checked={s.form.visitors}
          onChange={(v) => s.set({ visitors: v })}
        />
        <Field
          label="Tolerância depois da chamada (minutos)"
          inputMode="numeric"
          value={s.form.tolerance}
          error={local.tolerance ?? errors.call_tolerance_minutes}
          hint="De 1 a 60 (padrão 10). Vale para as próximas chamadas; quem já foi chamado mantém o prazo dele."
          onChange={(e) => s.set({ tolerance: e.target.value })}
          wrapperClassName="max-w-xs"
        />
        <Toggle
          label="Limitar participações pendentes por fila"
          hint="Padrão: sem limite. Aguardando e chamado ocupam vaga; em atendimento não. Um pedido com 3 cortes ocupa 1 vaga."
          checked={s.form.limit}
          onChange={(v) => s.set({ limit: v })}
        />
        {s.form.limit && (
          <Field
            label="Limite padrão por fila (participações)"
            inputMode="numeric"
            value={s.form.capacity}
            error={local.capacity ?? errors.default_queue_capacity}
            hint="De 1 a 200. Cada fila pode ter um limite próprio na tela da fila. Quando lota, a entrada volta sozinha ao liberar vaga."
            onChange={(e) => s.set({ capacity: e.target.value })}
            wrapperClassName="max-w-xs"
          />
        )}
        {confirm ? (
          <ConfirmPanel
            title="Desligar a entrada como visitante?"
            confirmLabel="Desligar"
            busy={cmd.busy !== null}
            onConfirm={() => void save()}
            onCancel={() => setConfirm(false)}
          >
            <p className="m-0">
              Quem não tem conta não consegue mais entrar na fila. Visitas já ativas continuam normalmente.
            </p>
          </ConfirmPanel>
        ) : (
          <div>
            <Submit busy={cmd.busy !== null}>Salvar filas e atendimento</Submit>
          </div>
        )}
      </form>
    </Section>
  );
}

// ------------------------------------------------------------------------ Equipe e permissões

function SecaoEquipe({ ctx, settings, onSaved, onConflict }: SectionProps) {
  const q = settings.queue;
  const s = useSection({ control: q.professionals_control_admission });
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const body = { expected_version: q.version, professionals_control_admission: s.form.control };
    const out = await cmd.run(`cfg:equipe:${q.version}:${String(s.form.control)}`, (key) =>
      api.PATCH("/v1/establishments/{establishmentId}/settings/queue", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      onSaved(out.data.data);
      s.setSaved(true);
    } else if (out.problem?.status === 409) {
      s.setConflict(true);
      await onConflict();
    }
  }
  return (
    <Section title="Equipe e permissões" description="O que os profissionais podem fazer na própria fila.">
      <form onSubmit={onSubmit} noValidate aria-label="Permissões da equipe" className="grid gap-4">
        <Status s={s} onDiscard={s.reset} />
        <CommandError problem={cmd.problem} offline={cmd.offline} />
        <Toggle
          label="Profissionais podem controlar a entrada da própria fila"
          hint="Padrão: ligado. Permite fechar e reabrir a entrada da própria fila. Não libera limite, transferências, preços, outras filas nem encerrar a operação."
          checked={s.form.control}
          onChange={(v) => s.set({ control: v })}
        />
        <div>
          <Submit busy={cmd.busy !== null}>Salvar permissões</Submit>
        </div>
      </form>
    </Section>
  );
}

// ----------------------------------------------------------------------------- Pagamentos

function SecaoPagamentos({ ctx, settings, onSaved, onConflict }: SectionProps) {
  const p = settings.payments;
  const s = useSection({ cash: p.cash_enabled, card: p.card_enabled, pix: p.pix_enabled });
  const cmd = useCommand();
  const usable = s.form.cash || s.form.card || (s.form.pix && p.pix_available);
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!usable) return;
    const body = {
      expected_version: p.version,
      ...(s.form.cash !== p.cash_enabled ? { cash_enabled: s.form.cash } : {}),
      ...(s.form.card !== p.card_enabled ? { card_enabled: s.form.card } : {}),
      ...(s.form.pix !== p.pix_enabled ? { pix_enabled: s.form.pix } : {}),
    };
    const out = await cmd.run(`cfg:pagamentos:${p.version}:${JSON.stringify(body)}`, (key) =>
      api.PATCH("/v1/establishments/{establishmentId}/settings/payments", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      onSaved(out.data.data);
      s.setSaved(true);
    } else if (out.problem?.status === 409 && out.problem.code === "version_conflict") {
      s.setConflict(true);
      await onConflict();
    }
  }
  return (
    <Section
      title="Pagamentos"
      description="Meios aceitos. Desligar não invalida cobranças nem recebimentos existentes."
    >
      <form onSubmit={onSubmit} noValidate aria-label="Meios de pagamento" className="grid gap-4">
        <Status s={s} onDiscard={s.reset} />
        <CommandError problem={cmd.problem} offline={cmd.offline} />
        <Toggle
          label="Dinheiro"
          hint="Padrão: ligado."
          checked={s.form.cash}
          onChange={(v) => s.set({ cash: v })}
        />
        <Toggle
          label="Cartão na maquininha (débito ou crédito)"
          hint="Padrão: ligado. A equipe informa a modalidade em cada recebimento."
          checked={s.form.card}
          onChange={(v) => s.set({ card: v })}
        />
        <Toggle
          label="Pix pela integração"
          hint={
            p.pix_available
              ? "Padrão: ligado. Só é oferecido com a integração em funcionamento."
              : "A integração Pix não está ligada neste servidor: mesmo ligado aqui, o Pix não aparece para os clientes."
          }
          checked={s.form.pix}
          onChange={(v) => s.set({ pix: v })}
        />
        {!usable && (
          <p role="alert" className="m-0 text-sm font-semibold text-danger">
            Mantenha pelo menos um meio que funcione.
          </p>
        )}
        <p className="m-0 text-xs text-fg-muted">
          Se o cliente já escolheu um meio que você desligar, a equipe ainda pode registrar o recebimento
          nele. Chaves do provedor nunca aparecem aqui.
        </p>
        <div>
          <Submit busy={cmd.busy !== null}>Salvar pagamentos</Submit>
        </div>
      </form>
    </Section>
  );
}

const feeName = { debit: "Cartão de débito", credit: "Cartão de crédito", pix: "Pix integrado" } as const;

/** Percentual em pontos-base → "2,5" (sem ponto flutuante no valor salvo). */
function bpToInput(bp: number): string {
  const s = String(bp).padStart(3, "0");
  const int = s.slice(0, -2);
  const dec = s.slice(-2).replace(/0+$/, "");
  return dec ? `${int},${dec}` : int;
}

/** "2,5" → 250 pontos-base; aceita até 2 casas; null se inválido. */
function inputToBp(v: string): number | null {
  const m = /^(\d{1,2})(?:[.,](\d{1,2}))?$/.exec(v.trim());
  if (!m) return null;
  return Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0") || "0");
}

/** Custos internos de recebimento (estimativas do dono). Nunca aparecem para clientes. */
function Custos({ ctx }: { ctx: StaffContext }) {
  const [fees, setFees] = useState<Fee[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const load = useCallback(async () => setAttempt((n) => n + 1), []);
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/payment-fees", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) setFees(out.data.data);
      else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [ctx.estId, attempt]);
  return (
    <Section
      title="Custos de recebimento"
      description="Estimativas cadastradas por você (não são tabela de provedor). Usadas só nos seus indicadores; o valor cobrado do cliente não muda."
    >
      {error && !fees && <LoadError message={error} onRetry={() => void load()} />}
      {!fees && !error && <Loading />}
      {fees && (
        <div className="grid gap-4 md:grid-cols-3">
          {fees.map((f) => (
            <CustoForm key={`${f.method}:${f.version}`} ctx={ctx} fee={f} onSaved={load} />
          ))}
        </div>
      )}
      <p className="m-0 text-xs text-fg-muted">
        Regra de cálculo: percentual sobre o valor recebido, arredondado ao centavo (metade para cima), mais o
        valor fixo por transação. Três cortes pagos numa transação têm um único valor fixo. Mudanças valem
        para recebimentos novos; os anteriores guardam o custo calculado na hora.
      </p>
    </Section>
  );
}

function CustoForm({ ctx, fee, onSaved }: { ctx: StaffContext; fee: Fee; onSaved: () => Promise<void> }) {
  const cmd = useCommand();
  const [pct, setPct] = useState(fee.current ? bpToInput(fee.current.percent_basis_points) : "");
  const [fixed, setFixed] = useState(fee.current ? centsToInput(fee.current.fixed_cents) : "");
  const [local, setLocal] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);
  async function send(body: { percent_basis_points?: number; fixed_cents?: number; clear?: boolean }) {
    const out = await cmd.run(`custo:${fee.method}:${fee.version}:${JSON.stringify(body)}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/payment-fees/{method}", {
        params: {
          path: { establishmentId: ctx.estId, method: fee.method },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: fee.version, ...body },
      }),
    );
    if (out.ok) setSaved(true);
    if (out.ok || out.problem?.status === 409) await onSaved();
  }
  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const bp = inputToBp(pct || "0");
    const cents = fixed.trim() === "" ? 0 : parseCents(fixed);
    const errs: Record<string, string> = {};
    if (bp === null || bp > 3000) errs.pct = "De 0 a 30%, com até 2 casas (ex.: 2,49).";
    if (cents === null || cents < 0 || cents > 10000) errs.fixed = "De R$ 0,00 a R$ 100,00.";
    setLocal(errs);
    if (Object.keys(errs).length > 0 || bp === null || cents === null) return;
    void send({ percent_basis_points: bp, fixed_cents: cents });
  }
  const example =
    inputToBp(pct || "0") !== null && parseCents(fixed || "0") !== null
      ? Math.floor((10000 * (inputToBp(pct || "0") ?? 0) + 5000) / 10000) + (parseCents(fixed || "0") ?? 0)
      : null;
  return (
    <form
      onSubmit={onSubmit}
      noValidate
      aria-label={`Custo de ${feeName[fee.method]}`}
      className="grid content-start gap-3 rounded-xl border border-line bg-surface p-4"
    >
      <h3 className="m-0 text-base font-semibold">{feeName[fee.method]}</h3>
      <p className="m-0 text-sm text-fg-muted">
        {fee.configured && fee.current
          ? `Vigente desde ${new Date(fee.current.effective_from).toLocaleDateString("pt-BR")} (versão ${fee.version}).`
          : "Não configurado: recebimentos ficam com custo desconhecido."}
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Field
        label="Percentual (%)"
        inputMode="decimal"
        value={pct}
        error={local.pct ?? fieldErrors(cmd.problem).percent_basis_points}
        hint="Ex.: 2,49"
        onChange={(e) => {
          setSaved(false);
          setPct(e.target.value);
        }}
      />
      <Field
        label="Valor fixo por transação (R$)"
        inputMode="decimal"
        value={fixed}
        error={local.fixed ?? fieldErrors(cmd.problem).fixed_cents}
        hint="Ex.: 0,50"
        onChange={(e) => {
          setSaved(false);
          setFixed(e.target.value);
        }}
      />
      {example !== null && (
        <p className="m-0 text-xs text-fg-muted">
          Exemplo: em R$ 100,00 o custo estimado é {formatCents(example)} (recebido após o custo:{" "}
          {formatCents(10000 - example)}).
        </p>
      )}
      {saved && (
        <p role="status" className="m-0 text-sm font-semibold text-success">
          Salvo.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>Salvar</Submit>
        {fee.configured && (
          <Button variant="ghost" disabled={cmd.busy !== null} onClick={() => void send({ clear: true })}>
            Voltar a “não configurado”
          </Button>
        )}
      </div>
    </form>
  );
}

/** Resultado dos últimos 30 dias para o dono (não é lucro; mostra a cobertura dos dados). */
function ResumoCustos({ ctx }: { ctx: StaffContext }) {
  const [costs, setCosts] = useState<Costs | null>(null);
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/receipt-costs", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (active && out.ok) setCosts(out.data.data);
    });
    return () => {
      active = false;
    };
  }, [ctx.estId]);
  if (!costs) return null;
  const rows: [string, number][] = [
    ["Recebido (bruto)", costs.gross_cents],
    ["Estornos", -costs.refunded_cents],
    ["Custos estimados", -costs.estimated_fees_cents],
    ["Custos informados pelo provedor", -costs.actual_fees_cents],
  ];
  if (costs.fee_returned_cents > 0) rows.push(["Taxas devolvidas em estornos", costs.fee_returned_cents]);
  return (
    <Section title="Últimos 30 dias" description={costs.note}>
      <dl className="m-0 grid gap-1 rounded-xl border border-line bg-surface p-4">
        {rows.map(([label, v]) => (
          <div key={label} className="flex justify-between gap-3 tabular-nums">
            <dt>{label}</dt>
            <dd className="m-0">{formatCents(v)}</dd>
          </div>
        ))}
        <div className="flex justify-between gap-3 border-t border-line pt-2 font-semibold tabular-nums">
          <dt>Recebido após custos conhecidos ou estimados</dt>
          <dd className="m-0">{formatCents(costs.after_known_costs_cents)}</dd>
        </div>
      </dl>
      {!costs.complete && (
        <Notice kind="aviso" title="Cálculo incompleto">
          <p>
            {costs.unknown_fee_count + costs.not_configured_count} recebimento(s) sem custo conhecido
            (anteriores aos custos, cartão sem modalidade ou modalidade não configurada). O valor acima não
            desconta esses custos.
          </p>
        </Notice>
      )}
      {costs.refunds_fee_return_unknown > 0 && (
        <p className="m-0 text-sm text-fg-muted">
          {costs.refunds_fee_return_unknown} estorno(s) com devolução de taxa desconhecida: a taxa original
          continua contada uma vez só.
        </p>
      )}
    </Section>
  );
}
