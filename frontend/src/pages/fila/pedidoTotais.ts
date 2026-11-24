import type { PublicProduct, PublicService } from "./selecao";

export type Escolha = Record<string, number>;

/** Totais do pedido calculados no aparelho só para mostrar (o servidor confirma). */
export function totais(
  services: PublicService[],
  products: PublicProduct[] | null,
  s: Escolha,
  p: Escolha,
  priceOf?: (kind: "service" | "product", id: string) => number | undefined,
) {
  let cents = 0;
  let minutes = 0;
  let serviceUnits = 0;
  let productUnits = 0;
  const lines: { key: string; name: string; qty: number; subtotal: number }[] = [];
  for (const sv of services) {
    const n = s[sv.id] ?? 0;
    if (n <= 0) continue;
    const unit = priceOf?.("service", sv.id) ?? sv.price.amount_cents;
    cents += n * unit;
    minutes += n * sv.duration_minutes;
    serviceUnits += n;
    lines.push({ key: `s:${sv.id}`, name: sv.name, qty: n, subtotal: n * unit });
  }
  for (const pr of products ?? []) {
    const n = p[pr.id] ?? 0;
    if (n <= 0) continue;
    const unit = priceOf?.("product", pr.id) ?? pr.price.amount_cents;
    cents += n * unit;
    productUnits += n;
    lines.push({ key: `p:${pr.id}`, name: pr.name, qty: n, subtotal: n * unit });
  }
  return { cents, minutes, serviceUnits, productUnits, lines };
}

/** "3 unidades de serviço · 1 produto" (unidade de serviço não é pessoa). */
export function unidades(serviceUnits: number, productUnits: number): string {
  const parts = [];
  if (serviceUnits > 0) parts.push(`${serviceUnits} ${serviceUnits === 1 ? "serviço" : "serviços"}`);
  if (productUnits > 0) parts.push(`${productUnits} ${productUnits === 1 ? "produto" : "produtos"}`);
  return parts.join(" · ");
}
