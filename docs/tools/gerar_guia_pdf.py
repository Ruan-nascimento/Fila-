"""Gera docs/Guia-Pratico-Fila-1.0.0.pdf a partir de docs/guia-pratico.md.

Uso (na raiz do repositório):

    python docs/tools/gerar_guia_pdf.py

Precisa do pacote Python `markdown` e do Google Chrome ou Chromium instalado
(o PDF é impresso pelo navegador em modo headless). Defina CHROME=<caminho>
se o navegador não estiver no PATH.
"""

from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
import tempfile
from datetime import date
from pathlib import Path

import markdown
from markdown.extensions.toc import slugify_unicode

RAIZ = Path(__file__).resolve().parents[2]
ORIGEM = RAIZ / "docs" / "guia-pratico.md"
DESTINO = RAIZ / "docs" / "Guia-Pratico-Fila-1.0.0.pdf"
REPO = "https://github.com/Ruan-nascimento/Fila-/blob/main/docs/"
VERSAO = "1.0.0"

CSS = """
@page { size: A4; margin: 18mm 16mm 18mm 16mm; }
* { box-sizing: border-box; }
html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
body { font-family: "Inter", "Segoe UI", "Noto Sans", "DejaVu Sans", Arial, sans-serif;
       font-size: 10.3pt; line-height: 1.5; color: #1d232b; margin: 0; }
.capa { height: 255mm; display: flex; flex-direction: column; justify-content: center;
        page-break-after: always; padding: 0 6mm; }
.capa .marca { width: 22mm; height: 22mm; border-radius: 6mm; background: #1f6f5c;
               color: #fff; font-size: 26pt; font-weight: 800; display: flex;
               align-items: center; justify-content: center; margin-bottom: 12mm; }
.capa h1 { font-size: 34pt; margin: 0 0 4mm; border: 0; color: #12191f; }
.capa .sub { font-size: 14pt; color: #4a5561; margin: 0 0 14mm; max-width: 140mm; }
.capa .meta { font-size: 10pt; color: #6b7682; border-top: 1px solid #d5dbe1; padding-top: 5mm; }
h1 { font-size: 19pt; color: #12191f; margin: 0 0 4mm; }
h2 { font-size: 15pt; color: #1f6f5c; margin: 9mm 0 3mm; padding-bottom: 1.5mm;
     border-bottom: 2px solid #1f6f5c; page-break-after: avoid; }
h2.nova-pagina { page-break-before: always; margin-top: 0; }
h3 { font-size: 11.5pt; color: #12191f; margin: 6mm 0 2mm; page-break-after: avoid; }
p, ul, ol { margin: 0 0 2.6mm; }
li { margin: 0.6mm 0; }
a { color: #1f6f5c; text-decoration: none; }
code { font-family: "JetBrains Mono", "DejaVu Sans Mono", Consolas, monospace; font-size: 8.8pt;
       background: #eef2f4; padding: 0.3mm 1.2mm; border-radius: 1mm; word-break: break-word; }
pre { background: #eef2f4; padding: 3mm; border-radius: 2mm; overflow-wrap: anywhere; white-space: pre-wrap; }
table { width: 100%; border-collapse: collapse; margin: 2mm 0 4mm; font-size: 9.2pt;
        page-break-inside: auto; }
tr { page-break-inside: avoid; }
th { background: #e3efeb; color: #12191f; text-align: left; font-weight: 700; }
th, td { border: 1px solid #ccd5dc; padding: 1.6mm 2.2mm; vertical-align: top; }
tbody tr:nth-child(even) td { background: #f7f9fa; }
blockquote { margin: 3mm 0; padding: 2.5mm 4mm; background: #fff6e0; border-left: 3px solid #d79a00;
             border-radius: 1mm; }
blockquote p { margin: 0; }
hr { display: none; }
.sumario-ocultar { display: none; }
"""


def ajustar_links(md: str) -> str:
    """Links relativos (outros .md, pastas) viram endereços do GitHub; âncoras ficam."""

    def troca(m: re.Match[str]) -> str:
        texto, alvo = m.group(1), m.group(2)
        if alvo.startswith(("http://", "https://", "#", "mailto:")):
            return m.group(0)
        if alvo.endswith(".pdf"):
            return texto  # o próprio PDF: só o texto
        return f"[{texto}]({REPO}{alvo})"

    return re.sub(r"\[([^\]]+)\]\(([^)\s]+)\)", troca, md)


def montar_html(md: str) -> str:
    md = ajustar_links(md)
    # O bloco que aponta para o PDF não faz sentido dentro dele.
    md = re.sub(r"^> \*\*Versão em PDF:\*\*.*$\n?", "", md, flags=re.M)
    titulo, _, corpo = md.partition("\n")
    corpo_html = markdown.markdown(
        corpo,
        extensions=["tables", "toc", "sane_lists", "attr_list"],
        # Mesmos ids do GitHub (mantém acentos), para o sumário funcionar no PDF.
        extension_configs={"toc": {"slugify": slugify_unicode}},
        output_format="html",
    )
    # Cada seção numerada começa numa página nova (exceto a primeira, que segue o sumário).
    primeira = [True]

    def secao(m: re.Match[str]) -> str:
        if re.match(r'<h2 id="\d', m.group(0)) and not primeira[0]:
            return m.group(0).replace("<h2 ", '<h2 class="nova-pagina" ', 1)
        if re.match(r'<h2 id="\d', m.group(0)):
            primeira[0] = False
        return m.group(0)

    corpo_html = re.sub(r"<h2 [^>]*>", secao, corpo_html)
    hoje = date.today().strftime("%d/%m/%Y")
    capa = f"""
<section class="capa">
  <div class="marca">F</div>
  <h1>Fila {VERSAO}</h1>
  <p class="sub">Guia prático: fila, atendimento, pagamento e fidelidade para barbearias, passo a passo para cliente, profissional, dono e operador.</p>
  <p class="meta">Versão {VERSAO} · gerado em {hoje} · github.com/Ruan-nascimento/Fila-</p>
</section>"""
    return f"""<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><title>Fila {VERSAO} — Guia prático</title>
<style>{CSS}</style></head>
<body>{capa}
<h1>{titulo.lstrip('# ').strip()}</h1>
{corpo_html}
</body></html>"""


def achar_chrome() -> str:
    if os.environ.get("CHROME"):
        return os.environ["CHROME"]
    for nome in ("google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"):
        caminho = shutil.which(nome)
        if caminho:
            return caminho
    sys.exit("Chrome/Chromium não encontrado. Instale ou defina CHROME=<caminho>.")


def main() -> int:
    html = montar_html(ORIGEM.read_text(encoding="utf-8"))
    with tempfile.TemporaryDirectory() as tmp:
        pagina = Path(tmp) / "guia.html"
        pagina.write_text(html, encoding="utf-8")
        subprocess.run(
            [
                achar_chrome(),
                "--headless=new",
                "--disable-gpu",
                "--no-sandbox",
                f"--user-data-dir={tmp}/perfil",
                "--no-pdf-header-footer",
                f"--print-to-pdf={DESTINO}",
                pagina.as_uri(),
            ],
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    print(f"gerado: {DESTINO.relative_to(RAIZ)} ({DESTINO.stat().st_size // 1024} KiB)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
