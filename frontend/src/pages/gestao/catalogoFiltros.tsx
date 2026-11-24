import type { components } from "../../api/schema.gen";
import { CampoBusca } from "../../features/catalogo/CampoBusca";
import { byDisplay } from "../../features/catalogo/busca";
import type { useFiltros } from "./usarFiltros";
import { SelectField } from "../../ui/ui";

import type { Ordem, Situacao } from "./usarFiltros";

export type Category = components["schemas"]["CatalogCategory"];

export function BarraFiltros({
  f,
  noun,
  categories,
  withDuration,
}: {
  f: ReturnType<typeof useFiltros>;
  noun: string;
  categories: Category[];
  withDuration?: boolean;
}) {
  return (
    <div className="grid gap-3 rounded-xl border border-line bg-surface p-3 sm:grid-cols-2 lg:grid-cols-[2fr_1fr_1fr_1fr]">
      <CampoBusca
        label={`Buscar ${noun}`}
        placeholder={`Buscar ${noun}`}
        value={f.q}
        onChange={f.setQ}
        className="self-end"
      />
      <SelectField
        label="Situação"
        value={f.situacao}
        onChange={(e) => f.setSituacao(e.target.value as Situacao)}
      >
        <option value="active">Ativos</option>
        <option value="inactive">Inativos</option>
        <option value="all">Todos</option>
      </SelectField>
      <SelectField label="Categoria" value={f.categoria} onChange={(e) => f.setCategoria(e.target.value)}>
        <option value="">Todas</option>
        {[...categories].sort(byDisplay).map((c) => (
          <option key={c.id} value={c.id}>
            {c.name}
            {c.status === "inactive" ? " (inativa)" : ""}
          </option>
        ))}
        <option value="-">Sem categoria</option>
      </SelectField>
      <SelectField label="Ordenar por" value={f.ordem} onChange={(e) => f.setOrdem(e.target.value as Ordem)}>
        <option value="position">Posição</option>
        <option value="name">Nome</option>
        <option value="price">Preço</option>
        {withDuration && <option value="duration">Duração</option>}
      </SelectField>
      <p className="m-0 text-sm text-fg-muted sm:col-span-2 lg:col-span-4" aria-live="polite">
        {f.list.length === f.total
          ? `${f.total} ${f.total === 1 ? noun : `${noun}s`}`
          : `${f.list.length} de ${f.total} ${f.total === 1 ? noun : `${noun}s`}`}
        {f.list.length === 0 && f.total > 0 && " — nenhum resultado com estes filtros."}
      </p>
    </div>
  );
}

/** Campo de categoria dos formulários (só categorias do tipo; "sem categoria" sempre possível). */
export function CampoCategoria({
  categories,
  value,
  onChange,
  error,
}: {
  categories: Category[];
  value: string;
  onChange: (v: string) => void;
  error?: string | undefined;
}) {
  return (
    <SelectField label="Categoria" value={value} error={error} onChange={(e) => onChange(e.target.value)}>
      <option value="">Sem categoria</option>
      {[...categories].sort(byDisplay).map((c) => (
        <option key={c.id} value={c.id}>
          {c.name}
          {c.status === "inactive" ? " (inativa)" : ""}
        </option>
      ))}
    </SelectField>
  );
}
