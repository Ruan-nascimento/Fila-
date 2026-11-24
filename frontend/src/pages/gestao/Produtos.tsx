import { useState, type FormEvent } from "react";
import { Package, Pencil, Plus } from "lucide-react";
import { api } from "../../api/client";
import { fieldErrors } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { ImageUploadField } from "../../features/media/ImageUploadField";
import { centsToInput, formatCents, parseCents } from "../../money";
import { Avatar, Badge, Button, EmptyState, Field, Submit, TextArea } from "../../ui/ui";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { BarraFiltros, CampoCategoria, type Category } from "./catalogoFiltros";
import { useFiltros } from "./usarFiltros";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Product = components["schemas"]["Product"];

/**
 * Produtos simples (ADR-0032): pomada, shampoo e afins, como complemento da visita. Sem estoque:
 * disponível = ativo. Desativar bloqueia novas inclusões e não tira o produto das comandas.
 */
export function Produtos({
  ctx,
  products,
  categories,
  productsEnabled,
  onChanged,
}: {
  ctx: StaffContext;
  products: Product[];
  categories: Category[];
  productsEnabled: boolean | null;
  onChanged: () => Promise<void>;
}) {
  const novo = useInlinePanel();
  const f = useFiltros(products);
  const cats = categories.filter((c) => c.kind === "product");
  return (
    <div className="grid gap-4">
      {productsEnabled === false && (
        <p className="m-0 rounded-xl border border-warning/40 bg-warning-soft p-3 text-sm">
          Produtos estão desligados nas configurações da loja: clientes e equipe não conseguem incluí-los em
          novas comandas.
        </p>
      )}
      {ctx.isOwner &&
        (novo.open ? (
          <ProdutoForm
            ctx={ctx}
            categories={cats}
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
            <Button id={novo.triggerId} icon={Plus} onClick={() => novo.show("aberto")}>
              Novo produto
            </Button>
          </div>
        ))}
      {products.length === 0 ? (
        <EmptyState icon={Package} title="Nenhum produto cadastrado">
          <p>Produtos aparecem para o cliente como complemento opcional do pedido.</p>
        </EmptyState>
      ) : (
        <>
          <BarraFiltros f={f} noun="produto" categories={cats} />
          <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
            {f.list.map((p) => (
              <ProdutoLinha key={p.id} ctx={ctx} p={p} categories={cats} onChanged={onChanged} />
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function ProdutoLinha({
  ctx,
  p,
  categories,
  onChanged,
}: {
  ctx: StaffContext;
  p: Product;
  categories: Category[];
  onChanged: () => Promise<void>;
}) {
  const edit = useInlinePanel();
  const cmd = useCommand();
  const cat = categories.find((c) => c.id === p.category_id);
  async function toggle() {
    const path =
      p.status === "active"
        ? ("/v1/establishments/{establishmentId}/products/{productId}/deactivate" as const)
        : ("/v1/establishments/{establishmentId}/products/{productId}/reactivate" as const);
    const out = await cmd.run(`produto:${p.status}:${p.id}:${p.version}`, (key) =>
      api.POST(path, {
        params: { path: { establishmentId: ctx.estId, productId: p.id }, header: { "Idempotency-Key": key } },
        body: { expected_version: p.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onChanged();
  }
  return (
    <li className="grid gap-3 px-4 py-3">
      <div className="flex flex-wrap items-start gap-3">
        <Avatar name={p.name} image={p.image} size={40} className="size-10 rounded-lg text-xs" />
        <div className="grid min-w-0 flex-1 gap-0.5">
          <h2 className="m-0 text-base font-semibold break-words">{p.name}</h2>
          <p className="m-0 text-sm text-fg-muted">
            {cat ? cat.name : "Sem categoria"} · posição {p.display_order}
            {p.description ? ` · ${p.description}` : ""}
          </p>
        </div>
        <div className="grid justify-items-end gap-1">
          <span className="font-semibold tabular-nums">{formatCents(p.price.amount_cents)}</span>
          <Badge tone={p.status === "active" ? "success" : "neutral"}>
            {p.status === "active" ? "Ativo" : "Inativo"}
          </Badge>
        </div>
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {ctx.isOwner && !edit.open && (
        <div className="flex flex-wrap gap-2">
          <Button
            id={edit.triggerId}
            variant="secondary"
            icon={Pencil}
            aria-label={`Editar ${p.name}`}
            onClick={() => edit.show("aberto")}
          >
            Editar
          </Button>
          <Button variant="ghost" disabled={cmd.busy !== null} onClick={() => void toggle()}>
            {p.status === "active" ? "Desativar" : "Reativar"}
          </Button>
        </div>
      )}
      {edit.open && (
        <ProdutoForm
          ctx={ctx}
          product={p}
          categories={categories}
          panelId={edit.panelId}
          onKeyDown={edit.onKeyDown}
          onCancel={edit.hide}
          onDone={async () => {
            edit.hide();
            await onChanged();
          }}
          onConflict={onChanged}
        />
      )}
    </li>
  );
}

/** Criação (sem `product`) ou edição. Preço em reais, enviado em centavos inteiros. */
function ProdutoForm({
  ctx,
  product,
  categories,
  onDone,
  onCancel,
  onConflict,
  panelId,
  onKeyDown,
}: {
  ctx: StaffContext;
  product?: Product;
  categories: Category[];
  onDone: () => Promise<void>;
  onCancel: () => void;
  onConflict?: () => Promise<void>;
  panelId: string;
  onKeyDown: (e: React.KeyboardEvent) => void;
}) {
  const cmd = useCommand();
  const [name, setName] = useState(product?.name ?? "");
  const [description, setDescription] = useState(product?.description ?? "");
  const [price, setPrice] = useState(product ? centsToInput(product.price.amount_cents) : "");
  const [category, setCategory] = useState(product?.category_id ?? "");
  const [order, setOrder] = useState(String(product?.display_order ?? 0));
  const [local, setLocal] = useState<Record<string, string>>({});

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(price);
    const errs: Record<string, string> = {};
    if (cents === null || cents < 1) errs.price_cents = "Informe um valor maior que zero, ex.: 35,00";
    if (!/^\d{1,4}$/.test(order)) errs.display_order = "Use um número de 0 a 9999.";
    setLocal(errs);
    if (Object.keys(errs).length > 0 || cents === null) return;
    const fields = {
      name,
      description: description.trim() ? description : null,
      price_cents: cents,
      category_id: category || null,
      display_order: Number(order),
    };
    const path = { establishmentId: ctx.estId };
    const out = product
      ? await cmd.run(`produto:editar:${product.id}:${product.version}:${JSON.stringify(fields)}`, (key) =>
          api.PATCH("/v1/establishments/{establishmentId}/products/{productId}", {
            params: { path: { ...path, productId: product.id }, header: { "Idempotency-Key": key } },
            body: { expected_version: product.version, ...fields },
          }),
        )
      : await cmd.run(`produto:criar:${JSON.stringify(fields)}`, (key) =>
          api.POST("/v1/establishments/{establishmentId}/products", {
            params: { path, header: { "Idempotency-Key": key } },
            body: fields,
          }),
        );
    if (out.ok) await onDone();
    else if (out.problem?.status === 409 && onConflict) await onConflict();
  }

  async function setImage(assetId: string | null): Promise<boolean> {
    if (!product) return false;
    const out = await cmd.run(`produto:foto:${product.id}:${product.version}:${assetId ?? "sem"}`, (key) =>
      api.PATCH("/v1/establishments/{establishmentId}/products/{productId}", {
        params: {
          path: { establishmentId: ctx.estId, productId: product.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: product.version, image_asset_id: assetId },
      }),
    );
    if (out.ok) await onDone();
    return out.ok;
  }

  const errors = { ...fieldErrors(cmd.problem), ...local };
  return (
    <form
      id={panelId}
      onKeyDown={onKeyDown}
      onSubmit={onSubmit}
      noValidate
      className="grid gap-4 rounded-xl border border-line bg-surface p-4 sm:p-5"
      aria-label={product ? `Editar ${product.name}` : "Novo produto"}
    >
      {!product && <h2 className="m-0 text-base font-semibold">Novo produto</h2>}
      {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
      <Field
        label="Nome"
        required
        maxLength={60}
        value={name}
        error={errors.name}
        onChange={(e) => setName(e.target.value)}
      />
      <TextArea
        label="Descrição curta (opcional)"
        maxLength={200}
        rows={2}
        value={description}
        error={errors.description}
        onChange={(e) => setDescription(e.target.value)}
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
          hint="Menor aparece primeiro."
          onChange={(e) => setOrder(e.target.value)}
        />
      </div>
      <p className="m-0 -mt-2 text-sm text-fg-muted">
        Sem controle de estoque: o produto fica disponível enquanto estiver ativo. Preço novo vale só para
        novas inclusões.
      </p>
      {product && (
        <ImageUploadField
          purpose="product_photo"
          establishmentId={ctx.estId}
          current={product.image}
          currentLabel={`Foto de ${product.name}`}
          shape="square"
          saving={cmd.busy !== null}
          onReady={(id) => setImage(id)}
          onRemove={product.image ? () => void setImage(null) : undefined}
        />
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>{product ? "Salvar" : "Criar produto"}</Submit>
        <Button variant="secondary" onClick={onCancel}>
          Cancelar
        </Button>
      </div>
    </form>
  );
}
