import { useState } from "react";
import { byDisplay, fold, matches } from "../../features/catalogo/busca";

export type Situacao = "active" | "inactive" | "all";
export type Ordem = "position" | "name" | "price" | "duration";

type Filtravel = {
  id: string;
  name: string;
  status: string;
  category_id?: string | null | undefined;
  display_order?: number | undefined;
  price: { amount_cents: number };
  duration_minutes?: number | undefined;
  description?: string | null | undefined;
};

/**
 * Busca e organização da gestão (lista inteira carregada: filtro local, imediato). Ordem estável:
 * o critério escolhido, depois posição, nome e id.
 */
export function useFiltros<T extends Filtravel>(items: T[] | null) {
  const [q, setQ] = useState("");
  const [situacao, setSituacao] = useState<Situacao>("active");
  const [categoria, setCategoria] = useState("");
  const [ordem, setOrdem] = useState<Ordem>("position");
  const list = (items ?? [])
    .filter((i) => situacao === "all" || i.status === situacao)
    .filter((i) =>
      categoria === "" ? true : categoria === "-" ? !i.category_id : i.category_id === categoria,
    )
    .filter((i) => matches(`${i.name} ${i.description ?? ""}`, q))
    .sort((a, b) => {
      const by =
        ordem === "name"
          ? fold(a.name).localeCompare(fold(b.name), "pt-BR")
          : ordem === "price"
            ? a.price.amount_cents - b.price.amount_cents
            : ordem === "duration"
              ? (a.duration_minutes ?? 0) - (b.duration_minutes ?? 0)
              : 0;
      return by || byDisplay(a, b);
    });
  return {
    q,
    setQ,
    situacao,
    setSituacao,
    categoria,
    setCategoria,
    ordem,
    setOrdem,
    list,
    total: items?.length ?? 0,
  };
}
