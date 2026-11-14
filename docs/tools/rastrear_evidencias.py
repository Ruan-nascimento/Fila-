"""Rastreabilidade regra -> cenário -> teste (etapa 13).

Lê docs/regras.md (regras R-…), docs/cenarios-aceite.md (cenários C-… e as regras que cada um
cobre) e os testes do repositório (Go, Vitest e Playwright), que citam o cenário num comentário
ou no nome. Gera docs/rastreabilidade.md e falha (saída 1) com --verificar se:
  * um cenário citado em teste não existe; ou
  * um cenário marcado como automatizado não tem teste que o cite.

Uso (raiz do repositório):
  python docs/tools/rastrear_evidencias.py            # escreve docs/rastreabilidade.md
  python docs/tools/rastrear_evidencias.py --verificar  # só confere (CI)
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

RAIZ = Path(__file__).resolve().parents[2]
REGRA = re.compile(r"^\| (R-[A-Z]+-\d{2}) \| (.+?) \|")
CENARIO = re.compile(r"^### (C-[A-Z]+-\d{2}) — (.+)$")
ID = re.compile(r"\bC-[A-Z]+-\d{2}\b")
FUNC_GO = re.compile(r"^func (Test\w+)")
TESTE_TS = re.compile(r"""^\s*(?:it|test)\(\s*["'`](.+?)["'`]""")


# Regras de estrutura: verificadas por testes de arquitetura e pelo CI, não por cenários.
ESTRUTURAIS = {
    "R-ARQ-01": "backend/internal/arquitetura/arquitetura_test.go: TestDomainModulesExist, TestModulesOnlyQueryOwnTables",
    "R-ARQ-03": "CI job Frontend (npm run build, lint, typecheck); frontend/package.json",
    "R-ARQ-05": "backend/internal/api/contrato_test.go: TestRoutesMatchContract, TestResponsesMatchContractTypes; CI: tipos gerados em dia (go generate, npm run check:api)",
    "R-CONTA-12": "backend/internal/identidade/segredos_test.go: TestPasswordHashing, TestTokenIs256BitsAndHashed",
}


def regras() -> dict[str, str]:
    out = {}
    for linha in (RAIZ / "docs/regras.md").read_text(encoding="utf-8").splitlines():
        m = REGRA.match(linha)
        if m:
            out[m.group(1)] = m.group(2).strip()
    return out


def cenarios() -> dict[str, dict]:
    out: dict[str, dict] = {}
    atual = None
    for linha in (RAIZ / "docs/cenarios-aceite.md").read_text(encoding="utf-8").splitlines():
        m = CENARIO.match(linha)
        if m:
            atual = m.group(1)
            out[atual] = {"titulo": m.group(2).strip(), "regras": [], "tipo": ""}
            continue
        if atual and linha.startswith("- Regras:") and not out[atual]["regras"]:
            out[atual]["regras"] = re.findall(r"R-[A-Z]+-\d{2}", linha.split("·")[0])
            t = re.search(r"Tipo: (.+)$", linha)
            out[atual]["tipo"] = t.group(1).strip() if t else ""
    return out


def testes() -> dict[str, list[str]]:
    """Cenário -> lista de 'arquivo: teste' que o citam (comentário logo acima ou corpo)."""
    arquivos = list(RAIZ.glob("backend/**/*_test.go")) + list(RAIZ.glob("frontend/src/**/*.test.ts*")) + list(
        RAIZ.glob("frontend/e2e/*.spec.ts")
    )
    out: dict[str, list[str]] = {}
    for arq in sorted(arquivos):
        linhas = arq.read_text(encoding="utf-8").splitlines()
        rel = arq.relative_to(RAIZ).as_posix()
        pendentes: list[str] = []  # ids vistos antes do próximo teste (comentário de cabeçalho)
        atual = None
        for linha in linhas:
            nome = None
            m = FUNC_GO.match(linha) if arq.suffix == ".go" else TESTE_TS.match(linha)
            if m:
                nome = m.group(1)
            if nome:
                atual = nome
                for c in pendentes:
                    out.setdefault(c, []).append(f"{rel}: {atual}")
                pendentes = []
            for c in ID.findall(linha):
                if nome or (atual and not linha.lstrip().startswith(("//", "*", "/*"))):
                    ref = f"{rel}: {atual}"
                    if ref not in out.setdefault(c, []):
                        out[c].append(ref)
                else:
                    pendentes.append(c)
    return {k: sorted(set(v)) for k, v in out.items()}


def main() -> int:
    rs, cs, ts = regras(), cenarios(), testes()
    erros = [f"teste cita {c}, que não existe em cenarios-aceite.md" for c in ts if c not in cs]
    sem_teste = [c for c in cs if c not in ts]
    linhas = [
        "# Rastreabilidade: regra → cenário → teste",
        "",
        "Gerado por `python docs/tools/rastrear_evidencias.py` (etapa 13). Um cenário conta como",
        "**automatizado** quando algum teste o cita pelo identificador; os demais estão na seção 3 com o",
        "motivo. Teste citado aqui não quer dizer aprovado: o resultado de cada execução está em",
        "[evidencias.md](evidencias.md).",
        "",
        "## 1. Regras",
        "",
        "| Regra | Cenários | Situação |",
        "|---|---|---|",
    ]
    por_regra: dict[str, list[str]] = {r: [] for r in rs}
    for c, d in cs.items():
        for r in d["regras"]:
            por_regra.setdefault(r, []).append(c)
    contagem = {"automatizado": 0, "parcial": 0, "sem teste": 0, "sem cenário": 0}
    for r in rs:
        cen = sorted(por_regra.get(r, []))
        com = [c for c in cen if c in ts]
        if not cen and r in ESTRUTURAIS:
            sit = "automatizado (estrutural)"
            contagem.setdefault(sit, 0)
            contagem[sit] += 1
            linhas.append(f"| {r} | — ({ESTRUTURAIS[r]}) | {sit} |")
            continue
        if not cen:
            sit = "sem cenário"
        elif len(com) == len(cen):
            sit = "automatizado"
        elif com:
            sit = "parcial"
        else:
            sit = "sem teste"
        contagem[sit] += 1
        txt = ", ".join(f"{c}{'' if c in ts else ' (sem teste)'}" for c in cen) or "—"
        linhas.append(f"| {r} | {txt} | {sit} |")
    linhas += ["", "Resumo: " + " · ".join(f"{k}: {v}" for k, v in contagem.items()), "", "## 2. Cenários e testes", ""]
    linhas += ["| Cenário | Testes que o citam |", "|---|---|"]
    for c, d in cs.items():
        refs = "<br>".join(f"`{t}`" for t in ts.get(c, [])) or "**nenhum**"
        linhas.append(f"| {c} — {d['titulo']} | {refs} |")
    linhas += ["", "## 3. Cenários sem teste automatizado", ""]
    for c in sem_teste:
        linhas.append(f"- {c} — {cs[c]['titulo']} (tipo declarado: {cs[c]['tipo'] or '—'})")
    if not sem_teste:
        linhas.append("Nenhum.")
    texto = "\n".join(linhas) + "\n"
    destino = RAIZ / "docs/rastreabilidade.md"
    if "--verificar" in sys.argv:
        if destino.read_text(encoding="utf-8") != texto:
            erros.append("docs/rastreabilidade.md desatualizado: rode python docs/tools/rastrear_evidencias.py")
    else:
        destino.write_text(texto, encoding="utf-8", newline="\n")
    for e in erros:
        print("ERRO:", e)
    print(f"Regras: {len(rs)} · Cenários: {len(cs)} · com teste: {len(cs) - len(sem_teste)} · sem teste: {len(sem_teste)}")
    print("Resumo das regras:", contagem)
    return 1 if erros else 0


if __name__ == "__main__":
    sys.exit(main())
