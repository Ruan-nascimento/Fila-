// Dinheiro sempre em centavos inteiros (ADR-0014). A conversão de texto para centavos é feita
// por texto, sem número de ponto flutuante (0,1 + 0,2 não pode virar 0,30000000000000004).

const brl = new Intl.NumberFormat("pt-BR", { style: "currency", currency: "BRL" });

/** 4550 → "R$ 45,50". */
export function formatCents(cents: number): string {
  return brl.format(cents / 100);
}

/**
 * Converte o que a pessoa digitou ("45", "45,5", "45,50", "1.234,56", "R$ 45,50") em centavos.
 * Devolve null se o texto não for um valor válido (mais de 2 casas decimais, letras, negativo…).
 */
export function parseCents(input: string): number | null {
  const text = input.trim().replace(/^R\$\s*/i, "");
  if (!/^\d{1,3}(\.\d{3})*(,\d{1,2})?$|^\d+(,\d{1,2})?$/.test(text)) return null;
  const [intPart = "0", decPart = ""] = text.replace(/\./g, "").split(",");
  const cents = Number(intPart) * 100 + Number(decPart.padEnd(2, "0"));
  return Number.isSafeInteger(cents) ? cents : null;
}

/** 4550 → "45,50" (para preencher o campo ao editar). */
export function centsToInput(cents: number): string {
  const reais = Math.floor(cents / 100);
  const resto = String(cents % 100).padStart(2, "0");
  return `${reais},${resto}`;
}
