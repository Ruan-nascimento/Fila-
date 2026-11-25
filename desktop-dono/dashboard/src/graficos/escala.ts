/** Escala "bonita" a partir de ZERO (eixo nunca truncado): 4 divisões arredondadas. */
export function escala(
  max: number,
  inteiro = false,
): { topo: number; marcas: number[] } {
  if (!(max > 0)) return { topo: 1, marcas: [0, 1] };
  const bruto = inteiro ? Math.max(1, max / 4) : max / 4; // contagens: passo inteiro (sem 0,25 pessoa)
  const pot = 10 ** Math.floor(Math.log10(bruto));
  const passo =
    [1, 2, 2.5, 5, 10].map((m) => m * pot).find((p) => p >= bruto) ?? bruto;
  const topo = Math.ceil(max / passo) * passo;
  const marcas: number[] = [];
  for (let v = 0; v <= topo + passo / 2; v += passo) marcas.push(v);
  return { topo, marcas };
}

export const CORES = ["var(--serie-1)", "var(--serie-2)", "var(--serie-3)"];
