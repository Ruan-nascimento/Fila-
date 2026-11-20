import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import jsQR from "jsqr";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it } from "vitest";
import { Entrar } from "../pages/Entrar";
import { SessionProvider } from "../session/session";
import { anonymousSession, fakeApi } from "../test/fake-api";
import { Avatar } from "../ui/ui";
import { accessUrl, formatPhone, slugFromContext, slugFromInput } from "./auth/acessoLoja";
import { resetPublicConfig } from "./config/publicConfig";
import { QUIET_ZONE, qrMatrix, qrSvg } from "./establishments/qr";
import { StoreQrPanel } from "./establishments/StoreQrPanel";
import { ThemePalettePicker } from "./establishments/ThemePalettePicker";
import { centered, checkFile, limits } from "./media/envio";
import { DEFAULT_PALETTE, PALETTES, paletteOf, paletteVars } from "./theme/paletas";
import { useStoreBrand } from "./theme/tema";

afterEach(() => resetPublicConfig());

// ---------------------------------------------------------------- contraste (WCAG 2.2)

function lum(hex: string): number {
  const h = hex.replace("#", "").slice(0, 6);
  const c = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16) / 255);
  const [r, g, b] = c.map((x) => (x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4)) as [
    number,
    number,
    number,
  ];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
function ratio(a: string, b: string): number {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p) as [number, number];
  return (x + 0.05) / (y + 0.05);
}
/** Compõe uma cor com alfa (#rrggbbaa) sobre um fundo opaco. */
function over(fg: string, bg: string): string {
  const a = fg.length === 9 ? parseInt(fg.slice(7, 9), 16) / 255 : 1;
  const ch = (s: string, i: number) => parseInt(s.slice(1 + i * 2, 3 + i * 2), 16);
  return (
    "#" +
    [0, 1, 2]
      .map((i) => Math.round(ch(fg, i) * a + ch(bg, i) * (1 - a)))
      .map((v) => v.toString(16).padStart(2, "0"))
      .join("")
  );
}

// Superfícies reais (styles.css): claro zinc-50/branco/zinc-100; escuro zinc-950/zinc-900/≈zinc-800.
const LIGHT_SURFACES = ["#fafafa", "#ffffff", "#f4f4f5"];
const DARK_SURFACES = ["#09090b", "#18181b", "#27272a"];

// Cenários: C-CFG-07.
describe("Paletas da loja", () => {
  it("catálogo fechado de 16 identificadores, iguais ao contrato, padrão purple", () => {
    expect(PALETTES.map((p) => p.id)).toEqual([
      "purple",
      "violet",
      "indigo",
      "blue",
      "sky",
      "cyan",
      "teal",
      "emerald",
      "green",
      "lime",
      "amber",
      "orange",
      "red",
      "rose",
      "pink",
      "fuchsia",
    ]);
    expect(new Set(PALETTES.map((p) => p.name)).size).toBe(16);
    expect(DEFAULT_PALETTE).toBe("purple");
    // Desconhecido (ou tentativa de CSS) cai no padrão; nunca vira cor livre.
    expect(paletteOf("#ff0000").id).toBe("purple");
    expect(paletteOf("red; background: url(x)").id).toBe("purple");
    for (const v of Object.values(paletteVars("teal"))) expect(v).toMatch(/^#[0-9a-f]{6}([0-9a-f]{2})?$/);
  });

  it.each(PALETTES.map((p) => [p.id, p] as const))("%s: contrastes AA em claro e escuro", (_id, p) => {
    for (const [t, surfaces] of [
      [p.light, LIGHT_SURFACES],
      [p.dark, DARK_SURFACES],
    ] as const) {
      // Texto do botão principal (normal e hover): 4,5:1.
      expect(ratio(t.onPrimary, t.primary)).toBeGreaterThanOrEqual(4.5);
      expect(ratio(t.onPrimary, t.primaryHover)).toBeGreaterThanOrEqual(4.5);
      for (const s of surfaces) {
        // Links e item selecionado sobre as superfícies: 4,5:1.
        expect(ratio(t.accent, s)).toBeGreaterThanOrEqual(4.5);
        // Botão e anel de foco como componente: 3:1.
        expect(ratio(t.primary, s)).toBeGreaterThanOrEqual(3);
        expect(ratio(t.focus, s)).toBeGreaterThanOrEqual(3);
        // Texto de destaque sobre o fundo suave de seleção.
        expect(ratio(t.accent, over(t.select, s))).toBeGreaterThanOrEqual(4.5);
      }
    }
  });
});

describe("Escolha de paleta", () => {
  it("rádios com nome acessível; a seleção não depende só da cor", () => {
    let chosen = "purple";
    const { rerender } = render(<ThemePalettePicker value="purple" onChange={(p) => (chosen = p)} />);
    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(16);
    expect(screen.getByRole("radio", { name: "Verde-petróleo" })).not.toBeChecked();
    expect(screen.getByRole("radio", { name: "Roxo" })).toBeChecked();
    fireEvent.click(screen.getByRole("radio", { name: "Verde-petróleo" }));
    expect(chosen).toBe("teal");
    rerender(<ThemePalettePicker value="teal" onChange={() => {}} />);
    expect(screen.getByRole("radio", { name: "Verde-petróleo" })).toBeChecked();
    expect(screen.getByRole("group", { name: "Cor principal" })).toBeInTheDocument();
  });
});

function Marca({ p }: { p: string | null }) {
  useStoreBrand(p);
  return null;
}

describe("Identidade da loja no documento", () => {
  it("aplica a paleta, troca de loja sem misturar e limpa ao sair", () => {
    const root = document.documentElement;
    const { rerender, unmount } = render(<Marca p="teal" />);
    expect(root.getAttribute("data-brand")).toBe("teal");
    expect(root.style.getPropertyValue("--p-light-primary")).toBe("#0f766e");
    rerender(<Marca p="red" />);
    expect(root.getAttribute("data-brand")).toBe("red");
    expect(root.style.getPropertyValue("--p-light-primary")).toBe("#b91c1c");
    unmount();
    expect(root.hasAttribute("data-brand")).toBe(false);
    expect(root.style.getPropertyValue("--p-light-primary")).toBe("");
  });
});

// ---------------------------------------------------------------- QR Code e links

/** Rasteriza a matriz (sem canvas) e decodifica com uma biblioteca independente (jsQR). */
function decode(text: string): string | null {
  const m = qrMatrix(text);
  const scale = 4;
  const side = (m.size + QUIET_ZONE * 2) * scale;
  const data = new Uint8ClampedArray(side * side * 4).fill(255);
  for (let r = 0; r < m.size; r++)
    for (let c = 0; c < m.size; c++)
      if (m.dark(r, c))
        for (let y = 0; y < scale; y++)
          for (let x = 0; x < scale; x++) {
            const i = (((r + QUIET_ZONE) * scale + y) * side + (c + QUIET_ZONE) * scale + x) * 4;
            data[i] = data[i + 1] = data[i + 2] = 0;
          }
  return jsQR(data, side, side)?.data ?? null;
}

// Cenários: C-CFG-08.
describe("QR Code da loja", () => {
  const origin = "https://fila.exemplo.com.br";

  it("três destinos tipados, montados com URL/URLSearchParams, sem segredo", () => {
    expect(accessUrl(origin, "barbearia-do-ze", "loja")).toBe(
      "https://fila.exemplo.com.br/loja/barbearia-do-ze",
    );
    expect(accessUrl(origin, "barbearia-do-ze", "visitante")).toBe(
      "https://fila.exemplo.com.br/loja/barbearia-do-ze?entrada=visitante",
    );
    expect(accessUrl(origin, "barbearia-do-ze", "conta")).toBe(
      "https://fila.exemplo.com.br/entrar?loja=barbearia-do-ze&voltar=%2Floja%2Fbarbearia-do-ze",
    );
    expect(() => accessUrl(origin, "../x", "loja")).toThrow();
  });

  it.each(["loja", "visitante", "conta"] as const)("o QR (%s) decodifica exatamente o link", (t) => {
    const url = accessUrl(origin, "barbearia-do-ze", t);
    expect(decode(url)).toBe(url);
  });

  it("SVG só com o desenho do gerador: preto sobre branco, sem texto da loja", () => {
    const svg = qrSvg(qrMatrix(accessUrl(origin, "x-y", "loja")));
    expect(svg).toContain('fill="#000000"');
    expect(svg).toContain('fill="#ffffff"');
    expect(svg).not.toMatch(/<script|<text|<image|href=/);
  });

  it("trocar o destino atualiza link e código juntos; nome da loja fica fora do código", () => {
    render(
      <StoreQrPanel origin={origin} slug="barbearia-do-ze" storeName="Zé <b>Barbearia</b>" logo={null} />,
    );
    expect(screen.getByTestId("link-qr")).toHaveTextContent(
      "https://fila.exemplo.com.br/loja/barbearia-do-ze",
    );
    const before = document.querySelector("figure path")?.getAttribute("d");
    fireEvent.click(screen.getByRole("radio", { name: /Entrar sem conta/ }));
    expect(screen.getByTestId("link-qr")).toHaveTextContent("?entrada=visitante");
    expect(document.querySelector("figure path")?.getAttribute("d")).not.toBe(before);
    // Texto da loja é texto (nunca HTML) e não entra no QR.
    expect(screen.getByText("Zé <b>Barbearia</b>")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /QR Code: Entrar sem conta/ })).toBeInTheDocument();
  });
});

describe("Entrada pelo link da loja", () => {
  it("lê o slug do contexto e do que a pessoa colou, sem adivinhar", () => {
    expect(slugFromContext("barbearia-do-ze", null)).toBe("barbearia-do-ze");
    expect(slugFromContext(null, "/loja/barbearia-do-ze/historico")).toBe("barbearia-do-ze");
    expect(slugFromContext("../x", "//evil.com")).toBeNull();
    expect(slugFromInput("https://fila.exemplo.com.br/loja/barbearia-do-ze?entrada=visitante")).toBe(
      "barbearia-do-ze",
    );
    expect(slugFromInput("Barbearia-Do-Ze")).toBe("barbearia-do-ze");
    expect(slugFromInput("qualquer coisa")).toBeNull();
    expect(formatPhone("+5511999998888")).toBe("(11) 99999-8888");
  });
});

// ---------------------------------------------------------------- login: Google e visitante

function shopData() {
  return {
    data: {
      id: "e1",
      slug: "barbearia-do-ze",
      name: "Barbearia do Zé",
      timezone: "America/Sao_Paulo",
      palette: "teal",
      description: null,
      address: null,
      phone: null,
      logo: null,
    },
  };
}

function renderEntrar(url: string, props: Parameters<typeof Entrar>[0] = {}) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <SessionProvider>
        <Routes>
          <Route path="/entrar" element={<Entrar {...props} />} />
          <Route path="/loja/:slug" element={<p>página da loja</p>} />
        </Routes>
      </SessionProvider>
    </MemoryRouter>,
  );
}

describe("Login de cliente", () => {
  it("Google aparece só para cliente; entrar sem conta leva à loja do contexto", async () => {
    const { requests } = fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/config": () =>
        Response.json({ data: { google_login: true, media_uploads: false, public_base_url: "https://x" } }),
      "GET /v1/public/slugs/barbearia-do-ze": () => Response.json(shopData()),
    });
    renderEntrar("/entrar?loja=barbearia-do-ze&voltar=%2Floja%2Fbarbearia-do-ze");
    expect(await screen.findByText("Barbearia do Zé")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Continuar com o Google" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Entrar sem conta" })).toHaveAttribute(
      "href",
      "/loja/barbearia-do-ze?entrada=visitante",
    );
    expect(document.documentElement.getAttribute("data-brand")).toBe("teal");
    // Trocar para equipe: o Google some (nunca no acesso administrativo).
    fireEvent.click(screen.getByRole("radio", { name: /Equipe da barbearia/ }));
    expect(screen.queryByRole("button", { name: "Continuar com o Google" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Entrar sem conta" })).toBeNull();
    expect(requests.some((r) => r.path === "/v1/auth/google/start")).toBe(false);
  });

  it("acesso do dono (equipe fixa) nunca mostra Google", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/config": () =>
        Response.json({ data: { google_login: true, media_uploads: true, public_base_url: "https://x" } }),
    });
    renderEntrar("/entrar", { equipe: true, destino: "/dono" });
    await screen.findByRole("button", { name: "Entrar" });
    await waitFor(() => expect(screen.queryByRole("button", { name: /Google/ })).toBeNull());
  });

  it("sem Google configurado: entrar sem conta continua e pede a loja antes", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/config": () =>
        Response.json({ data: { google_login: false, media_uploads: false, public_base_url: "https://x" } }),
      "GET /v1/public/slugs/barbearia-do-ze": () => Response.json(shopData()),
    });
    renderEntrar("/entrar");
    fireEvent.click(await screen.findByRole("button", { name: "Entrar sem conta" }));
    expect(screen.queryByRole("button", { name: /Google/ })).toBeNull();
    fireEvent.change(screen.getByLabelText("Link ou código da barbearia"), {
      target: { value: "https://fila/loja/barbearia-do-ze" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Continuar" }));
    expect(await screen.findByText("página da loja")).toBeInTheDocument();
  });

  it("Google cancelado: mensagem discreta e nova tentativa possível", async () => {
    fakeApi({
      "GET /v1/session": () => Response.json(anonymousSession),
      "GET /v1/public/config": () =>
        Response.json({ data: { google_login: true, media_uploads: false, public_base_url: "https://x" } }),
    });
    renderEntrar("/entrar?google=cancelado");
    expect(await screen.findByText(/Você cancelou no Google/)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Continuar com o Google" })).toBeEnabled();
  });
});

// ---------------------------------------------------------------- fotos

describe("Fotos", () => {
  it("avatar com espaço reservado, lazy e iniciais quando a imagem falha", () => {
    const img = {
      variants: [
        { width: 64, height: 64, url: "/v1/me/avatar/64?v=1" },
        { width: 128, height: 128, url: "/v1/me/avatar/128?v=1" },
      ],
    };
    render(<Avatar name="Maria Souza" image={img} size={64} />);
    const el = document.querySelector("img");
    expect(el).toHaveAttribute("width", "64");
    expect(el).toHaveAttribute("height", "64");
    expect(el).toHaveAttribute("loading", "lazy");
    expect(el).toHaveAttribute("alt", "");
    expect(el?.getAttribute("srcset")).toContain("128w");
    fireEvent.error(el as HTMLImageElement);
    expect(screen.getByText("MS")).toBeInTheDocument();
  });

  it("validação local de arquivo e enquadramento", () => {
    const f = (type: string, size: number) => new File([new Uint8Array(size)], "x", { type });
    expect(checkFile(f("image/svg+xml", 10))).toMatch(/Formato/);
    expect(checkFile(f("image/gif", 10))).toMatch(/Formato/);
    expect(checkFile(f("image/png", 6 * 1024 * 1024))).toMatch(/5 MB/);
    expect(checkFile(f("image/webp", 10))).toBeNull();
    expect(centered(400, 200)).toEqual({ x: 0.25, y: 0, size: 1 });
    expect(limits(400, 200, 0.5)).toEqual({ x: 0.75, y: 0.5 });
  });
});
