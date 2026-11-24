import { useMemo, useState } from "react";
import { Package, Plus, Scissors } from "lucide-react";
import { formatCents } from "../../money";
import { Button } from "../../ui/ui";
import { byDisplay, matches } from "../../features/catalogo/busca";
import { CampoBusca } from "../../features/catalogo/CampoBusca";
import { Categorias } from "../../features/catalogo/Categorias";
import { Quantidade } from "../../features/catalogo/Quantidade";
import type { PublicCategory, PublicProduct, PublicService } from "./selecao";

/** Item que o seletor mostra (serviço ou produto do catálogo público). */
type Item = {
  id: string;
  name: string;
  price: number;
  duration?: number | undefined;
  description?: string | null | undefined;
  category_id?: string | null | undefined;
  display_order?: number | undefined;
};

import type { Escolha } from "./pedidoTotais";

export type { Escolha };

/**
 * Seleção com quantidade (ADR-0033): busca local (lista inteira carregada), categorias em chips,
 * diminuir/aumentar/editar a quantidade e subtotal da linha. Buscar ou trocar de categoria não
 * mexe no que já foi escolhido. `min` protege unidades que não podem sair (já realizadas);
 * `priceOf` mostra o preço já aceito na comanda (snapshot) em vez do catálogo atual.
 */
function Lista({
  kind,
  items,
  categories,
  chosen,
  onChange,
  max,
  min,
  priceOf,
}: {
  kind: "service" | "product";
  items: Item[];
  categories: PublicCategory[];
  chosen: Escolha;
  onChange: (id: string, n: number) => void;
  max: number;
  min?: ((id: string) => number) | undefined;
  priceOf?: ((id: string) => number | undefined) | undefined;
}) {
  const [q, setQ] = useState("");
  const [cat, setCat] = useState("");
  const noun = kind === "service" ? "serviço" : "produto";
  const Icon = kind === "service" ? Scissors : Package;
  const cats = useMemo(() => {
    const active = new Set(categories.filter((c) => c.kind === kind).map((c) => c.id));
    return {
      list: categories
        .filter((c) => c.kind === kind)
        .sort(byDisplay)
        .map((c) => ({ id: c.id, name: c.name, count: items.filter((i) => i.category_id === c.id).length }))
        .filter((c) => c.count > 0),
      // Categoria inativa ou ausente: o item continua acessível em "Sem categoria".
      none: items.filter((i) => !i.category_id || !active.has(i.category_id)).length,
      active,
    };
  }, [categories, items, kind]);
  const shown = items
    .filter((i) => {
      if (cat === "-") return !i.category_id || !cats.active.has(i.category_id);
      return !cat || i.category_id === cat;
    })
    .filter((i) => matches(`${i.name} ${i.description ?? ""}`, q))
    .sort(byDisplay);
  const searching = q.trim() !== "" || cat !== "";

  return (
    <div className="grid gap-3">
      {items.length > 6 && (
        <CampoBusca label={`Buscar ${noun}`} placeholder={`Buscar ${noun}`} value={q} onChange={setQ} />
      )}
      <Categorias
        label={`Categorias de ${noun}s`}
        options={cats.list}
        value={cat}
        onChange={setCat}
        uncategorized={cats.list.length > 0 ? cats.none : 0}
      />
      {searching && (
        <p className="m-0 text-sm text-fg-muted" aria-live="polite">
          {shown.length === 0
            ? `Nenhum ${noun} encontrado${q.trim() ? ` para “${q.trim()}”` : " nesta categoria"}.`
            : `${shown.length} ${shown.length === 1 ? noun : `${noun}s`} encontrado${shown.length === 1 ? "" : "s"}.`}
        </p>
      )}
      <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
        {shown.map((i) => {
          const n = chosen[i.id] ?? 0;
          const unit = priceOf?.(i.id) ?? i.price;
          const floor = min?.(i.id) ?? 0;
          return (
            <li key={i.id} className="grid gap-2 px-4 py-3 sm:grid-cols-[1fr_auto] sm:items-center">
              <div className="flex min-w-0 items-start gap-3">
                <Icon aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-fg-muted" />
                <div className="min-w-0">
                  <p className="m-0 font-medium break-words">{i.name}</p>
                  <p className="m-0 text-sm text-fg-muted">
                    {formatCents(unit)}
                    {kind === "service" && i.duration ? ` · cerca de ${i.duration} min cada` : ""}
                    {i.description ? ` · ${i.description}` : ""}
                  </p>
                  {n > 0 && (
                    <p className="m-0 text-sm font-semibold tabular-nums">
                      {n} × {formatCents(unit)} = {formatCents(n * unit)}
                      {kind === "service" && i.duration ? ` · ~${n * i.duration} min` : ""}
                    </p>
                  )}
                  {floor > 0 && (
                    <p className="m-0 text-xs text-fg-muted">
                      {floor} {kind === "service" ? "já realizada(s)" : "já entregue(s)"}: não dá para tirar.
                    </p>
                  )}
                </div>
              </div>
              <div className="justify-self-end">
                {n === 0 ? (
                  <Button
                    variant="secondary"
                    icon={Plus}
                    aria-label={`Adicionar ${i.name}`}
                    onClick={() => onChange(i.id, 1)}
                  >
                    Adicionar
                  </Button>
                ) : (
                  <Quantidade
                    label={`Quantidade de ${i.name}`}
                    value={n}
                    min={floor}
                    max={Math.max(max, n)}
                    onChange={(v) => onChange(i.id, v)}
                  />
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

export function SeletorItens({
  services,
  products,
  categories,
  services_chosen,
  products_chosen,
  onService,
  onProduct,
  max,
  min,
  priceOf,
  title,
}: {
  services: PublicService[];
  products: PublicProduct[] | null;
  categories: PublicCategory[];
  services_chosen: Escolha;
  products_chosen: Escolha;
  onService: (id: string, n: number) => void;
  onProduct: (id: string, n: number) => void;
  max: number;
  min?: ((kind: "service" | "product", id: string) => number) | undefined;
  priceOf?: ((kind: "service" | "product", id: string) => number | undefined) | undefined;
  title: string;
}) {
  const [showProducts, setShowProducts] = useState(Object.keys(products_chosen).length > 0);
  return (
    <div className="grid gap-5">
      <fieldset className="m-0 grid min-w-0 gap-3 border-0 p-0">
        <legend className="mb-2 p-0 text-sm font-semibold">{title}</legend>
        <Lista
          kind="service"
          items={services.map((s) => ({
            id: s.id,
            name: s.name,
            price: s.price.amount_cents,
            duration: s.duration_minutes,
            category_id: s.category_id,
            display_order: s.display_order,
          }))}
          categories={categories}
          chosen={services_chosen}
          onChange={onService}
          max={max}
          min={min && ((id) => min("service", id))}
          priceOf={priceOf && ((id) => priceOf("service", id))}
        />
      </fieldset>
      {products && products.length > 0 && (
        <fieldset className="m-0 grid min-w-0 gap-3 border-0 p-0">
          <legend className="mb-2 p-0 text-sm font-semibold">Produtos (opcional)</legend>
          {!showProducts ? (
            <div>
              <Button variant="secondary" icon={Package} onClick={() => setShowProducts(true)}>
                Ver produtos ({products.length})
              </Button>
            </div>
          ) : (
            <Lista
              kind="product"
              items={products.map((p) => ({
                id: p.id,
                name: p.name,
                price: p.price.amount_cents,
                description: p.description,
                category_id: p.category_id,
                display_order: p.display_order,
              }))}
              categories={categories}
              chosen={products_chosen}
              onChange={onProduct}
              max={max}
              min={min && ((id) => min("product", id))}
              priceOf={priceOf && ((id) => priceOf("product", id))}
            />
          )}
          <p className="m-0 text-xs text-fg-muted">
            Produtos complementam a visita e não aumentam a espera. Não dá para entrar na fila só para comprar
            produtos.
          </p>
        </fieldset>
      )}
    </div>
  );
}
