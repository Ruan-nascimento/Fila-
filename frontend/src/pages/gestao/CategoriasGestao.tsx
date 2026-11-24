import { useState, type FormEvent } from "react";
import { Pencil, Plus, Tags } from "lucide-react";
import { api } from "../../api/client";
import { fieldErrors } from "../../api/problem";
import { byDisplay } from "../../features/catalogo/busca";
import { Badge, Button, EmptyState, Field, Submit } from "../../ui/ui";
import { useInlinePanel } from "../../ui/useInlinePanel";
import type { Category } from "./catalogoFiltros";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

const kindName = { service: "serviços", product: "produtos" } as const;

/**
 * Categorias de serviços e de produtos (ADR-0032): nome, posição (número; sem arrastar) e
 * situação. Desativar não apaga nem esconde os itens: eles passam a "sem categoria" para o cliente.
 */
export function CategoriasGestao({
  ctx,
  categories,
  onChanged,
}: {
  ctx: StaffContext;
  categories: Category[];
  onChanged: () => Promise<void>;
}) {
  return (
    <div className="grid gap-6">
      {(["service", "product"] as const).map((kind) => (
        <section key={kind} className="grid gap-3" aria-labelledby={`cat-${kind}`}>
          <h2 id={`cat-${kind}`} className="m-0 text-base font-semibold">
            Categorias de {kindName[kind]}
          </h2>
          <Lista
            ctx={ctx}
            kind={kind}
            list={categories.filter((c) => c.kind === kind).sort(byDisplay)}
            onChanged={onChanged}
          />
        </section>
      ))}
    </div>
  );
}

function Lista({
  ctx,
  kind,
  list,
  onChanged,
}: {
  ctx: StaffContext;
  kind: "service" | "product";
  list: Category[];
  onChanged: () => Promise<void>;
}) {
  const novo = useInlinePanel();
  return (
    <div className="grid gap-3">
      {list.length === 0 && !novo.open && (
        <EmptyState icon={Tags} title={`Nenhuma categoria de ${kindName[kind]}`}>
          <p>Itens sem categoria continuam aparecendo normalmente.</p>
        </EmptyState>
      )}
      {list.length > 0 && (
        <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
          {list.map((c) => (
            <Linha key={c.id} ctx={ctx} c={c} onChanged={onChanged} />
          ))}
        </ul>
      )}
      {ctx.isOwner &&
        (novo.open ? (
          <Form
            ctx={ctx}
            kind={kind}
            panelId={novo.panelId}
            onKeyDown={novo.onKeyDown}
            onCancel={novo.hide}
            onDone={async () => {
              novo.hide();
              await onChanged();
            }}
          />
        ) : (
          <div>
            <Button id={novo.triggerId} variant="secondary" icon={Plus} onClick={() => novo.show("aberto")}>
              Nova categoria de {kindName[kind]}
            </Button>
          </div>
        ))}
    </div>
  );
}

function Linha({ ctx, c, onChanged }: { ctx: StaffContext; c: Category; onChanged: () => Promise<void> }) {
  const edit = useInlinePanel();
  const cmd = useCommand();
  async function toggle() {
    const path =
      c.status === "active"
        ? ("/v1/establishments/{establishmentId}/catalog-categories/{categoryId}/deactivate" as const)
        : ("/v1/establishments/{establishmentId}/catalog-categories/{categoryId}/reactivate" as const);
    const out = await cmd.run(`categoria:${c.status}:${c.id}:${c.version}`, (key) =>
      api.POST(path, {
        params: {
          path: { establishmentId: ctx.estId, categoryId: c.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: c.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onChanged();
  }
  return (
    <li className="grid gap-2 px-4 py-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="min-w-0 flex-1 font-medium break-words">{c.name}</span>
        <span className="text-sm text-fg-muted tabular-nums">posição {c.display_order}</span>
        <Badge tone={c.status === "active" ? "success" : "neutral"}>
          {c.status === "active" ? "Ativa" : "Inativa"}
        </Badge>
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {ctx.isOwner && !edit.open && (
        <div className="flex flex-wrap gap-2">
          <Button id={edit.triggerId} variant="secondary" icon={Pencil} onClick={() => edit.show("aberto")}>
            Editar<span className="sr-only"> {c.name}</span>
          </Button>
          <Button variant="ghost" disabled={cmd.busy !== null} onClick={() => void toggle()}>
            {c.status === "active" ? "Desativar" : "Reativar"}
          </Button>
        </div>
      )}
      {edit.open && (
        <Form
          ctx={ctx}
          kind={c.kind}
          category={c}
          panelId={edit.panelId}
          onKeyDown={edit.onKeyDown}
          onCancel={edit.hide}
          onDone={async () => {
            edit.hide();
            await onChanged();
          }}
        />
      )}
    </li>
  );
}

function Form({
  ctx,
  kind,
  category,
  onDone,
  onCancel,
  panelId,
  onKeyDown,
}: {
  ctx: StaffContext;
  kind: "service" | "product";
  category?: Category;
  onDone: () => Promise<void>;
  onCancel: () => void;
  panelId: string;
  onKeyDown: (e: React.KeyboardEvent) => void;
}) {
  const cmd = useCommand();
  const [name, setName] = useState(category?.name ?? "");
  const [order, setOrder] = useState(String(category?.display_order ?? 0));
  const [local, setLocal] = useState<string | null>(null);
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!/^\d{1,4}$/.test(order)) {
      setLocal("Use um número de 0 a 9999.");
      return;
    }
    setLocal(null);
    const fields = { name, display_order: Number(order) };
    const out = category
      ? await cmd.run(
          `categoria:editar:${category.id}:${category.version}:${JSON.stringify(fields)}`,
          (key) =>
            api.PATCH("/v1/establishments/{establishmentId}/catalog-categories/{categoryId}", {
              params: {
                path: { establishmentId: ctx.estId, categoryId: category.id },
                header: { "Idempotency-Key": key },
              },
              body: { expected_version: category.version, ...fields },
            }),
        )
      : await cmd.run(`categoria:criar:${kind}:${JSON.stringify(fields)}`, (key) =>
          api.POST("/v1/establishments/{establishmentId}/catalog-categories", {
            params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
            body: { kind, ...fields },
          }),
        );
    if (out.ok) await onDone();
  }
  const errors = fieldErrors(cmd.problem);
  return (
    <form
      id={panelId}
      onKeyDown={onKeyDown}
      onSubmit={onSubmit}
      noValidate
      aria-label={category ? `Editar categoria ${category.name}` : `Nova categoria de ${kindName[kind]}`}
      className="grid gap-3 rounded-xl border border-line bg-surface-2 p-4 sm:grid-cols-[2fr_1fr]"
    >
      {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
      <Field
        label="Nome"
        required
        maxLength={40}
        value={name}
        error={errors.name}
        onChange={(e) => setName(e.target.value)}
      />
      <Field
        label="Posição"
        inputMode="numeric"
        value={order}
        error={local ?? errors.display_order}
        hint="Menor aparece primeiro (padrão 0)."
        onChange={(e) => setOrder(e.target.value)}
      />
      <div className="flex flex-wrap gap-2 sm:col-span-2">
        <Submit busy={cmd.busy !== null}>{category ? "Salvar" : "Criar categoria"}</Submit>
        <Button variant="secondary" onClick={onCancel}>
          Cancelar
        </Button>
      </div>
    </form>
  );
}
