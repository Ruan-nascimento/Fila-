import type { components } from "../../api/schema.gen";

/** Identificador de paleta aceito pelo servidor (enum Palette do contrato). */
export type PaletteId = components["schemas"]["Palette"];

/** Tokens semânticos de uma paleta num tema (claro ou escuro). Todos hexadecimais estáticos. */
export type PaletteTokens = {
  /** Fundo do botão principal. */
  primary: string;
  primaryHover: string;
  /** Texto/ícone sobre o botão principal. */
  onPrimary: string;
  /** Texto e ícone de destaque (links, item selecionado) sobre as superfícies. */
  accent: string;
  /** Fundo suave de seleção (primary-soft). */
  select: string;
  /** Borda do item selecionado. */
  selectLine: string;
  /** Anel de foco. */
  focus: string;
};

export type Palette = {
  id: PaletteId;
  /** Nome acessível mostrado na interface. */
  name: string;
  light: PaletteTokens;
  dark: PaletteTokens;
};

const WHITE = "#ffffff";
const ZINC_950 = "#09090b";

/**
 * Monta os tokens a partir dos tons do Tailwind (50, 300, 400, base clara, hover claro, 950).
 * Claro: botão no tom base com texto branco; destaque = o próprio tom base.
 * Escuro: botão no tom 400 com texto zinc-950; destaque no 300; seleção no 950 translúcido.
 * Contrastes medidos em paletas.test.ts (WCAG 2.2 AA).
 */
function palette(
  id: PaletteId,
  name: string,
  t: { s50: string; s300: string; s400: string; base: string; hover: string; s950: string },
): Palette {
  return {
    id,
    name,
    light: {
      primary: t.base,
      primaryHover: t.hover,
      onPrimary: WHITE,
      accent: t.base,
      select: t.s50,
      selectLine: t.s300,
      focus: t.base,
    },
    dark: {
      primary: t.s400,
      primaryHover: t.s300,
      onPrimary: ZINC_950,
      accent: t.s300,
      select: `${t.s950}b3`,
      selectLine: t.s400,
      focus: t.s400,
    },
  };
}

/** Catálogo fechado das 16 paletas (ADR-0028). A ordem é a da interface. Padrão: purple. */
export const PALETTES: readonly Palette[] = [
  palette("purple", "Roxo", {
    s50: "#faf5ff",
    s300: "#d8b4fe",
    s400: "#c084fc",
    base: "#9333ea",
    hover: "#7e22ce",
    s950: "#3b0764",
  }),
  palette("violet", "Violeta", {
    s50: "#f5f3ff",
    s300: "#c4b5fd",
    s400: "#a78bfa",
    base: "#6d28d9",
    hover: "#5b21b6",
    s950: "#2e1065",
  }),
  palette("indigo", "Índigo", {
    s50: "#eef2ff",
    s300: "#a5b4fc",
    s400: "#818cf8",
    base: "#4338ca",
    hover: "#3730a3",
    s950: "#1e1b4b",
  }),
  palette("blue", "Azul", {
    s50: "#eff6ff",
    s300: "#93c5fd",
    s400: "#60a5fa",
    base: "#1d4ed8",
    hover: "#1e40af",
    s950: "#172554",
  }),
  palette("sky", "Azul-céu", {
    s50: "#f0f9ff",
    s300: "#7dd3fc",
    s400: "#38bdf8",
    base: "#0369a1",
    hover: "#075985",
    s950: "#082f49",
  }),
  palette("cyan", "Ciano", {
    s50: "#ecfeff",
    s300: "#67e8f9",
    s400: "#22d3ee",
    base: "#0e7490",
    hover: "#155e75",
    s950: "#083344",
  }),
  palette("teal", "Verde-petróleo", {
    s50: "#f0fdfa",
    s300: "#5eead4",
    s400: "#2dd4bf",
    base: "#0f766e",
    hover: "#115e59",
    s950: "#042f2e",
  }),
  palette("emerald", "Esmeralda", {
    s50: "#ecfdf5",
    s300: "#6ee7b7",
    s400: "#34d399",
    base: "#047857",
    hover: "#065f46",
    s950: "#022c22",
  }),
  palette("green", "Verde", {
    s50: "#f0fdf4",
    s300: "#86efac",
    s400: "#4ade80",
    base: "#15803d",
    hover: "#166534",
    s950: "#052e16",
  }),
  palette("lime", "Lima", {
    s50: "#f7fee7",
    s300: "#bef264",
    s400: "#a3e635",
    base: "#4d7c0f",
    hover: "#3f6212",
    s950: "#1a2e05",
  }),
  palette("amber", "Âmbar", {
    s50: "#fffbeb",
    s300: "#fcd34d",
    s400: "#fbbf24",
    base: "#b45309",
    hover: "#92400e",
    s950: "#451a03",
  }),
  palette("orange", "Laranja", {
    s50: "#fff7ed",
    s300: "#fdba74",
    s400: "#fb923c",
    base: "#c2410c",
    hover: "#9a3412",
    s950: "#431407",
  }),
  palette("red", "Vermelho", {
    s50: "#fef2f2",
    s300: "#fca5a5",
    s400: "#f87171",
    base: "#b91c1c",
    hover: "#991b1b",
    s950: "#450a0a",
  }),
  palette("rose", "Rosa", {
    s50: "#fff1f2",
    s300: "#fda4af",
    s400: "#fb7185",
    base: "#be123c",
    hover: "#9f1239",
    s950: "#4c0519",
  }),
  palette("pink", "Pink", {
    s50: "#fdf2f8",
    s300: "#f9a8d4",
    s400: "#f472b6",
    base: "#be185d",
    hover: "#9d174d",
    s950: "#500724",
  }),
  palette("fuchsia", "Fúcsia", {
    s50: "#fdf4ff",
    s300: "#f0abfc",
    s400: "#e879f9",
    base: "#a21caf",
    hover: "#86198f",
    s950: "#4a044e",
  }),
];

export const DEFAULT_PALETTE: PaletteId = "purple";

/** Paleta pelo id; desconhecido (dado antigo ou inválido) cai no padrão, nunca em CSS livre. */
export function paletteOf(id: string | null | undefined): Palette {
  return PALETTES.find((p) => p.id === id) ?? (PALETTES[0] as Palette);
}

const TOKEN_VARS: Record<keyof PaletteTokens, string> = {
  primary: "primary",
  primaryHover: "primary-hover",
  onPrimary: "on-primary",
  accent: "accent",
  select: "select",
  selectLine: "select-line",
  focus: "focus",
};

/**
 * Variáveis CSS da paleta (`--p-light-*` e `--p-dark-*`). O CSS (styles.css, `[data-brand]`)
 * escolhe o conjunto conforme o tema. Nada de classes Tailwind montadas dinamicamente.
 */
export function paletteVars(id: string | null | undefined): Record<string, string> {
  const p = paletteOf(id);
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(TOKEN_VARS)) {
    out[`--p-light-${v}`] = p.light[k as keyof PaletteTokens];
    out[`--p-dark-${v}`] = p.dark[k as keyof PaletteTokens];
  }
  return out;
}
