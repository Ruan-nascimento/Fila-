import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Package, Pencil, Plus, Scissors, Tags } from "lucide-react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { centsToInput, formatCents, parseCents } from "../../money";
import {
  Badge,
  Button,
  Choice,
  ChoiceGroup,
  EmptyState,
  Field,
  LoadError,
  Loading,
  PageHeader,
  Segmented,
  Submit,
} from "../../ui/ui";
import { CategoriasGestao } from "./CategoriasGestao";
import { BarraFiltros, CampoCategoria, type Category } from "./catalogoFiltros";
import { useFiltros } from "./usarFiltros";
import { Produtos } from "./Produtos";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Service = components["schemas"]["Service"];
type Product = components["schemas"]["Product"];
type Aba = "servicos" | "produtos" | "categorias";
type Professional = components["schemas"]["Professional"];
type Pro = { id: string; name: string; active: boolean };

/**
 * Catálogo (serviços, produtos e categorias): a equipe lê; só o dono cria e altera (o servidor
 * confere). Busca, filtros e ordenação são locais: as listas vêm inteiras e são limitadas.
 */
export function Servicos({ ctx }: { ctx: StaffContext }) {
  const [services, setServices] = useState<Service[] | null>(null);
  const [products, setProducts] = useState<Product[]>([]);
  const [categories, setCategories] = useState<Category[]>([]);
  const [productsEnabled, setProductsEnabled] = useState<boolean | null>(null);
  const [pros, setPros] = useState<Pro[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [aba, setAba] = useState<Aba>("servicos");
  const novo = useInlinePanel();
  const f = useFiltros(services);

  const fetchAll = useCallback(async () => {
    const path = { establishmentId: ctx.estId };
    const [s, pr, ca] = await Promise.all([
      call(() => api.GET("/v1/establishments/{establishmentId}/services", { params: { path } })),
      call(() => api.GET("/v1/establishments/{establishmentId}/products", { params: { path } })),
      call(() => api.GET("/v1/establishments/{establishmentId}/catalog-categories", { params: { path } })),
    ]);
    // Só o dono lista profissionais e lê as configurações; para o profissional, não são necessários.
    const [p, st] = ctx.isOwner
      ? await Promise.all([
          call(() => api.GET("/v1/establishments/{establishmentId}/professionals", { params: { path } })),
          call(() => api.GET("/v1/establishments/{establishmentId}/settings", { params: { path } })),
        ])
      : [null, null];
    return { s, pr, ca, p, st };
  }, [ctx.estId, ctx.isOwner]);

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchAll>>) => {
    if (!r.s.ok) {
      setLoadError(problemMessage(r.s.problem, r.s.offline));
      return;
    }
    setLoadError(null);
    setServices(r.s.data.data);
    if (r.pr.ok) setProducts(r.pr.data.data);
    if (r.ca.ok) setCategories(r.ca.data.data);
    if (r.st?.ok) setProductsEnabled(r.st.data.data.catalog.products_enabled);
    if (r.p?.ok) {
      setPros(
        r.p.data.data.map((x: Professional) => ({
          id: x.id,
          name: x.display_name,
          active: x.status === "active",
        })),
      );
    }
  }, []);
  const load = useCallback(async () => apply(await fetchAll()), [apply, fetchAll]);

  useEffect(() => {
    let active = true;
    void fetchAll().then((r) => {
      if (active) apply(r);
    });
    return () => {
      active = false;
    };
  }, [fetchAll, apply]);

  const header = (
    <PageHeader
      title="Catálogo"
      description={
        ctx.isOwner
          ? "Serviços, produtos e categorias: preço, duração, pontos e quem atende cada serviço."
          : "Catálogo da loja (só leitura)."
      }
      actions={
        ctx.isOwner && aba === "servicos" && !novo.open ? (
          <Button id={novo.triggerId} icon={Plus} onClick={() => novo.show("aberto")}>
            Novo serviço
          </Button>
        ) : undefined
      }
    />
  );
  if (loadError && !services)
    return (
      <>
        {header}
        <LoadError message={loadError} onRetry={() => void load()} />
      </>
    );
  if (!services)
    return (
      <>
        {header}
        <Loading>Carregando serviços…</Loading>
      </>
    );
  const serviceCats = categories.filter((c) => c.kind === "service");
  return (
    <div className="grid gap-4">
      {header}
      <Segmented<Aba>
        legend="Parte do catálogo"
        name="aba-catalogo"
        value={aba}
        onChange={setAba}
        options={[
          { value: "servicos", label: "Serviços", icon: Scissors },
          { value: "produtos", label: "Produtos", icon: Package },
          { value: "categorias", label: "Categorias", icon: Tags },
        ]}
      />
      {aba === "produtos" && (
        <Produtos
          ctx={ctx}
          products={products}
          categories={categories}
          productsEnabled={productsEnabled}
          onChanged={load}
        />
      )}
      {aba === "categorias" && <CategoriasGestao ctx={ctx} categories={categories} onChanged={load} />}
      {aba === "servicos" && ctx.isOwner && (novo.open || services.length === 0) && (
        <ServicoForm
          ctx={ctx}
          pros={pros}
          categories={serviceCats}
          panelId={novo.panelId}
          onKeyDown={novo.onKeyDown}
          onDone={async () => {
            novo.hide();
            await load();
          }}
          {...(services.length > 0 ? { onCancel: novo.hide } : {})}
        />
      )}
      {aba === "servicos" &&
        (services.length === 0 ? (
          !ctx.isOwner && <EmptyState icon={Scissors} title="Nenhum serviço cadastrado" />
        ) : (
          <>
            <BarraFiltros f={f} noun="serviço" categories={serviceCats} withDuration />
            <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
              {f.list.map((s) => (
                <ServicoLinha key={s.id} ctx={ctx} s={s} pros={pros} categories={serviceCats} onDone={load} />
              ))}
            </ul>
          </>
        ))}
    </div>
  );
}

function ServicoLinha({
  ctx,
  s,
  pros,
  categories,
  onDone,
}: {
  ctx: StaffContext;
  s: Service;
  pros: Pro[];
  categories: Category[];
  onDone: () => Promise<void>;
}) {
  const edit = useInlinePanel();
  const cmd = useCommand();
  const names = s.professional_ids
    .map((id) => pros.find((p) => p.id === id)?.name ?? "profissional")
    .join(", ");
  async function toggle() {
    const op = s.status === "active" ? "deactivate" : "reactivate";
    const path =
      op === "deactivate"
        ? ("/v1/establishments/{establishmentId}/services/{serviceId}/deactivate" as const)
        : ("/v1/establishments/{establishmentId}/services/{serviceId}/reactivate" as const);
    const out = await cmd.run(`${op}:${s.id}:${s.version}`, (key) =>
      api.POST(path, {
        params: { path: { establishmentId: ctx.estId, serviceId: s.id }, header: { "Idempotency-Key": key } },
        body: { expected_version: s.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }
  return (
    <li className="grid gap-3 px-4 py-3">
      <div className="flex flex-wrap items-start gap-3">
        <Scissors aria-hidden="true" className="mt-1 size-5 shrink-0 text-fg-muted" />
        <div className="grid min-w-0 flex-1 gap-0.5">
          <h2 className="m-0 text-base font-semibold">{s.name}</h2>
          <p className="m-0 text-sm text-fg-muted tabular-nums">
            {[
              `${s.duration_minutes} min`,
              `${s.points} pontos`,
              // Só o que foi definido: sem categoria e posição 0 são o padrão e não poluem a lista.
              categories.find((c) => c.id === s.category_id)?.name,
              s.display_order ? `posição ${s.display_order}` : undefined,
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
          {ctx.isOwner && (
            <p className="m-0 text-sm text-fg-muted">Atendem: {names || "ninguém (ninguém pode atender)"}</p>
          )}
        </div>
        <div className="grid justify-items-end gap-1">
          <span className="font-semibold tabular-nums">{formatCents(s.price.amount_cents)}</span>
          <Badge tone={s.status === "active" ? "success" : "neutral"}>
            {s.status === "active" ? "Ativo" : "Inativo"}
          </Badge>
        </div>
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {ctx.isOwner && !edit.open && (
        <div className="flex flex-wrap gap-2">
          <Button id={edit.triggerId} variant="secondary" icon={Pencil} onClick={() => edit.show("aberto")}>
            Editar
          </Button>
          <Button variant="ghost" disabled={cmd.busy !== null} onClick={() => void toggle()}>
            {s.status === "active" ? "Desativar" : "Reativar"}
          </Button>
        </div>
      )}
      {edit.open && (
        <ServicoForm
          ctx={ctx}
          pros={pros}
          categories={categories}
          service={s}
          panelId={edit.panelId}
          onKeyDown={edit.onKeyDown}
          onDone={async () => {
            edit.hide();
            await onDone();
          }}
          onConflict={onDone}
          onCancel={edit.hide}
        />
      )}
    </li>
  );
}

/** Criação (sem `service`) ou edição. Preço digitado em reais e enviado em centavos inteiros. */
function ServicoForm({
  ctx,
  pros,
  categories,
  service,
  onDone,
  onConflict,
  onCancel,
  panelId,
  onKeyDown,
}: {
  ctx: StaffContext;
  pros: Pro[];
  categories: Category[];
  service?: Service;
  onDone: () => Promise<void>;
  /** Conflito de versão: relê a lista (o formulário continua aberto com o aviso). */
  onConflict?: () => Promise<void>;
  onCancel?: () => void;
  panelId?: string;
  onKeyDown?: (e: React.KeyboardEvent) => void;
}) {
  const cmd = useCommand();
  const [name, setName] = useState(service?.name ?? "");
  const [price, setPrice] = useState(service ? centsToInput(service.price.amount_cents) : "");
  const [duration, setDuration] = useState(service ? String(service.duration_minutes) : "");
  const [points, setPoints] = useState(service ? String(service.points) : "0");
  const [selected, setSelected] = useState<string[]>(service?.professional_ids ?? []);
  const [category, setCategory] = useState(service?.category_id ?? "");
  const [order, setOrder] = useState(String(service?.display_order ?? 0));
  const [localErrors, setLocalErrors] = useState<Record<string, string>>({});

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(price);
    const errs: Record<string, string> = {};
    if (cents === null || cents < 1) errs.price_cents = "Informe um valor maior que zero, ex.: 45,00";
    if (!/^\d+$/.test(duration)) errs.duration_minutes = "Informe os minutos (número inteiro).";
    if (!/^\d+$/.test(points)) errs.points = "Informe um número inteiro de pontos (0 ou mais).";
    if (!/^\d{1,4}$/.test(order)) errs.display_order = "Use um número de 0 a 9999.";
    setLocalErrors(errs);
    if (Object.keys(errs).length > 0 || cents === null) return;
    const fields = {
      name,
      price_cents: cents,
      duration_minutes: Number(duration),
      points: Number(points),
      category_id: category || null,
      display_order: Number(order),
    };
    const path = { establishmentId: ctx.estId };
    let ok: boolean;
    let conflict = false;
    if (!service) {
      const out = await cmd.run(`criar:${JSON.stringify(fields)}:${selected.join(",")}`, (key) =>
        api.POST("/v1/establishments/{establishmentId}/services", {
          params: { path, header: { "Idempotency-Key": key } },
          body: { ...fields, professional_ids: selected },
        }),
      );
      ok = out.ok;
    } else {
      const sp = { ...path, serviceId: service.id };
      const out = await cmd.run(`editar:${service.id}:${service.version}:${JSON.stringify(fields)}`, (key) =>
        api.PATCH("/v1/establishments/{establishmentId}/services/{serviceId}", {
          params: { path: sp, header: { "Idempotency-Key": key } },
          body: { expected_version: service.version, ...fields },
        }),
      );
      ok = out.ok;
      if (!out.ok && out.problem?.status === 409) conflict = true;
      const changedPros = [...selected].sort().join(",") !== [...service.professional_ids].sort().join(",");
      if (out.ok && changedPros) {
        const res = await cmd.run(`habilitar:${service.id}:${out.data.data.version}`, (key) =>
          api.PUT("/v1/establishments/{establishmentId}/services/{serviceId}/professionals", {
            params: { path: sp, header: { "Idempotency-Key": key } },
            body: { expected_version: out.data.data.version, professional_ids: selected },
          }),
        );
        ok = res.ok;
        if (!res.ok && res.problem?.status === 409) conflict = true;
      }
      if (conflict && onConflict) await onConflict();
    }
    if (ok) {
      if (!service) {
        setName("");
        setPrice("");
        setDuration("");
        setPoints("0");
        setSelected([]);
        setCategory("");
        setOrder("0");
      }
      await onDone();
    }
  }

  const errors = { ...fieldErrors(cmd.problem), ...localErrors };
  return (
    <form
      id={panelId}
      onKeyDown={onKeyDown}
      onSubmit={onSubmit}
      noValidate
      className="grid gap-4 rounded-xl border border-line bg-surface p-4 sm:p-5"
      aria-label={service ? `Editar ${service.name}` : "Novo serviço"}
    >
      {!service && <h2 className="m-0 text-base font-semibold">Novo serviço</h2>}
      {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
      <Field
        label="Nome"
        required
        maxLength={60}
        value={name}
        error={errors.name}
        onChange={(e) => setName(e.target.value)}
      />
      <div className="grid gap-4 sm:grid-cols-3">
        <Field
          label="Preço (R$)"
          inputMode="decimal"
          required
          value={price}
          error={errors.price_cents}
          onChange={(e) => setPrice(e.target.value)}
        />
        <Field
          label="Duração (minutos)"
          inputMode="numeric"
          required
          value={duration}
          error={errors.duration_minutes}
          onChange={(e) => setDuration(e.target.value)}
        />
        <Field
          label="Pontos de fidelidade"
          inputMode="numeric"
          required
          value={points}
          error={errors.points}
          onChange={(e) => setPoints(e.target.value)}
        />
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <CampoCategoria
          categories={categories}
          value={category}
          onChange={setCategory}
          error={errors.category_id}
        />
        <Field
          label="Posição"
          inputMode="numeric"
          value={order}
          error={errors.display_order}
          hint="Menor aparece primeiro (padrão 0)."
          onChange={(e) => setOrder(e.target.value)}
        />
      </div>
      <p className="m-0 -mt-2 text-sm text-fg-muted">
        Preço ex.: 45,00 — vale só para novos atendimentos; os já aceitos mantêm o preço registrado. Use 0
        pontos se o programa não estiver em uso.
      </p>
      {pros.length > 0 && (
        <ChoiceGroup legend="Profissionais que fazem este serviço">
          <div className="grid gap-2 sm:grid-cols-2">
            {pros.map((p) => (
              <Choice
                key={p.id}
                type="checkbox"
                checked={selected.includes(p.id)}
                onChange={(e) =>
                  setSelected((cur) => (e.target.checked ? [...cur, p.id] : cur.filter((x) => x !== p.id)))
                }
                label={p.name}
                description={p.active ? undefined : "desativado"}
              />
            ))}
          </div>
        </ChoiceGroup>
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>{service ? "Salvar" : "Criar serviço"}</Submit>
        {onCancel && (
          <Button variant="secondary" onClick={onCancel}>
            Cancelar
          </Button>
        )}
      </div>
    </form>
  );
}
