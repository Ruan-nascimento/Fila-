#!/usr/bin/env python3
"""Verifica a coerência entre regras, matriz de transições, cenários e contrato OpenAPI.

Uso (a partir da raiz do repositório):
    python docs/tools/verificar_contrato.py          # só verifica (CI)
    python docs/tools/verificar_contrato.py --write  # também regenera a tabela de matriz-permissoes.md

Requer Python 3.10+ e PyYAML. Sai com código 1 se encontrar erros.

O que é verificado:
  1. Toda operação do OpenAPI tem operationId único e `x-authz` com atores/escopos conhecidos.
  2. Comandos (POST/PUT/PATCH/DELETE) exigem Idempotency-Key, salvo as exceções documentadas.
  3. Operações que mudam estado não são abertas ao público, salvo as rotas pré-sessão documentadas.
  4. `x-events` usa só tipos de EventType; EventType bate com o catálogo de eventos-sse.md.
  5. Toda transição T-… da matriz tem ator; operações citadas existem; `x-transitions` e a matriz concordam.
  6. Toda regra R-… é coberta por pelo menos um cenário C-… ou por um ADR.
  7. Todas as referências entre crases (operações, esquemas, eventos, códigos) existem no contrato.
  8. A tabela de permissões por operação em matriz-permissoes.md está atualizada.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover
    sys.exit("PyYAML não encontrado. Instale com: python -m pip install pyyaml")

DOCS = Path(__file__).resolve().parent.parent
OPENAPI = DOCS / "contrato" / "openapi.yaml"
REGRAS = DOCS / "regras.md"
MATRIZ = DOCS / "matriz-transicoes.md"
PERMISSOES = DOCS / "matriz-permissoes.md"
CENARIOS = DOCS / "cenarios-aceite.md"
EVENTOS = DOCS / "contrato" / "eventos-sse.md"
ADR_DIR = DOCS / "adr"

ACTORS = ["public", "visitor", "customer", "professional", "owner", "provider"]
SCOPES = {
    "none", "self", "visit_owner", "own_queue", "own_attendance",
    "establishment_member", "establishment", "provider_signature", "protocol_holder",
}
MUTATING = {"post", "put", "patch", "delete"}
IDEMPOTENCY_EXEMPT = ("/v1/auth/", "/visitor-sessions", "/v1/webhooks/", "/v1/deletion-requests/status")
PUBLIC_MUTATION_ALLOWED = ("/v1/auth/", "/visitor-sessions", "/v1/deletion-requests/status", "/healthz/")
MARK_START = "<!-- gerado:permissoes:inicio -->"
MARK_END = "<!-- gerado:permissoes:fim -->"

RE_RULE_ROW = re.compile(r"^\|\s*(R-[A-Z]+-\d{2})\s*\|(.*)$", re.M)
RE_TRANS_ROW = re.compile(r"^\|\s*(T-[A-Z]{1,2}-\d{2})\s*\|(.*)$", re.M)
RE_RULE = re.compile(r"\bR-[A-Z]+-\d{2}\b")
RE_TRANS = re.compile(r"\bT-[A-Z]{1,2}-\d{2}\b")
RE_ADR = re.compile(r"\bADR-(\d{4})\b")
RE_TICK = re.compile(r"`([^`]+)`")
RE_SCENARIO = re.compile(r"^### (C-[A-Z]+-\d{2}) — (.+)$", re.M)


class Report:
    def __init__(self) -> None:
        self.errors: list[str] = []
        self.warnings: list[str] = []

    def err(self, msg: str) -> None:
        self.errors.append(msg)

    def warn(self, msg: str) -> None:
        self.warnings.append(msg)


def load_openapi() -> dict:
    with OPENAPI.open(encoding="utf-8") as f:
        return yaml.safe_load(f)


def collect_operations(spec: dict, rep: Report) -> dict[str, dict]:
    ops: dict[str, dict] = {}
    for path, item in spec.get("paths", {}).items():
        for method, op in item.items():
            if method not in {"get", "post", "put", "patch", "delete"}:
                continue
            oid = op.get("operationId")
            if not oid:
                rep.err(f"{method.upper()} {path}: sem operationId")
                continue
            if oid in ops:
                rep.err(f"operationId duplicado: {oid}")
            params = [p.get("$ref", p.get("name", "")) for p in op.get("parameters", [])]
            ops[oid] = {
                "method": method,
                "path": path,
                "authz": op.get("x-authz"),
                "transitions": op.get("x-transitions", []),
                "events": op.get("x-events", []),
                "reauth": bool(op.get("x-reauth")),
                "idem": any(p.endswith("/IdempotencyKey") for p in params),
            }
    return ops


def check_operations(ops: dict[str, dict], event_types: set[str], rep: Report) -> None:
    for oid, op in ops.items():
        authz = op["authz"]
        if not authz:
            rep.err(f"{oid}: sem x-authz (negação por padrão exige lista explícita)")
            continue
        for rule in authz:
            if rule.get("actor") not in ACTORS:
                rep.err(f"{oid}: ator desconhecido {rule.get('actor')!r}")
            if rule.get("scope") not in SCOPES:
                rep.err(f"{oid}: escopo desconhecido {rule.get('scope')!r}")
        actors = {r.get("actor") for r in authz}
        mutating = op["method"] in MUTATING
        exempt = any(x in op["path"] for x in IDEMPOTENCY_EXEMPT)
        if mutating and not exempt and not op["idem"]:
            rep.err(f"{oid}: comando sem Idempotency-Key")
        if mutating and "public" in actors and not any(x in op["path"] for x in PUBLIC_MUTATION_ALLOWED):
            rep.err(f"{oid}: comando aberto ao público fora das rotas pré-sessão")
        if op["transitions"] and actors == {"public"} and not any(x in op["path"] for x in PUBLIC_MUTATION_ALLOWED):
            rep.err(f"{oid}: muda estado sem ator autenticado")
        for ev in op["events"]:
            if ev not in event_types:
                rep.err(f"{oid}: x-events cita tipo inexistente {ev!r}")


def schema_index(spec: dict) -> tuple[dict, set[str], set[str], set[str]]:
    schemas = spec["components"]["schemas"]
    enum_values: set[str] = set()
    prop_names: set[str] = set()

    def walk(node):
        if isinstance(node, dict):
            for k, v in node.items():
                if k == "enum" and isinstance(v, list):
                    enum_values.update(str(x) for x in v)
                elif k == "const" and isinstance(v, str):
                    enum_values.add(v)
                elif k == "properties" and isinstance(v, dict):
                    prop_names.update(v.keys())
                walk(v)
        elif isinstance(node, list):
            for x in node:
                walk(x)

    walk(spec)
    param_names = {p["name"] for p in spec["components"]["parameters"].values()}
    return schemas, enum_values, prop_names, param_names


def check_refs(text: str, where: str, ctx: dict, rep: Report) -> None:
    """Confere referências entre crases contra o contrato."""
    for raw in RE_TICK.findall(text):
        tok = raw.strip()
        if (" " in tok or "/" in tok or tok.startswith(("x-", "__Host", "X-", "-"))
                or tok.endswith(".md") or tok in ctx["ignore"]
                or re.fullmatch(r"[A-Z]+|[A-Z][0-9]+", tok)):  # verbos HTTP e valores de exemplo (A001, K1)
            continue
        if re.fullmatch(r"[a-z]+(?:[A-Z][a-z0-9]*)+", tok):  # camelCase → operação ou esquema de segurança
            if tok not in ctx["ops"] and tok not in ctx["security"]:
                rep.err(f"{where}: `{tok}` não é operationId nem esquema de segurança")
        elif re.fullmatch(r"[A-Z][A-Za-z0-9]+(?:\.[a-z_]+)?", tok):  # Esquema ou Esquema.campo
            name, _, prop = tok.partition(".")
            if name in ctx["schemas"]:
                if prop and prop not in ctx["schemas"][name].get("properties", {}):
                    rep.err(f"{where}: `{tok}` — campo inexistente em {name}")
            elif name not in ctx["headers"] and name not in ctx["responses"]:
                rep.err(f"{where}: `{tok}` não existe nos componentes do contrato")
        elif re.fullmatch(r"[a-z_]+(?:\.[a-z_]+)+", tok):  # tipo de evento
            if tok not in ctx["events"]:
                rep.err(f"{where}: evento `{tok}` não existe em EventType")
        elif re.fullmatch(r"[a-z][a-z0-9_]*", tok) and "_" in tok:  # código/estado/campo
            if tok not in ctx["known_words"]:
                rep.warn(f"{where}: `{tok}` não encontrado no contrato (código, enum ou campo)")


def parse_event_catalog(rep: Report) -> set[str]:
    text = EVENTOS.read_text(encoding="utf-8")
    section = text.split("## 4.", 1)[1].split("\n## ", 1)[0]
    return set(re.findall(r"^\|\s*`([a-z_.]+)`\s*\|", section, re.M))


def parse_matrix(rep: Report, ops: dict) -> dict[str, dict]:
    text = MATRIZ.read_text(encoding="utf-8")
    rows: dict[str, dict] = {}
    for tid, rest in RE_TRANS_ROW.findall(text):
        cells = [c.strip() for c in rest.strip().strip("|").split("|")]
        if tid in rows:
            rep.err(f"matriz: transição duplicada {tid}")
        if len(cells) < 8:
            rep.err(f"matriz: {tid} com {len(cells)} colunas (esperado 8 após o ID)")
            continue
        actor, contract = cells[1], cells[7]
        if not actor or actor in {"—", "-"}:
            rep.err(f"matriz: {tid} sem ator")
        for col, name in ((cells[5], "auditoria"), (cells[4], "idempotência")):
            if not col:
                rep.err(f"matriz: {tid} sem {name}")
        contract_ops = {t for t in RE_TICK.findall(contract) if t in ops}
        rows[tid] = {"actor": actor, "ops": contract_ops, "text": "|".join(cells)}
    return rows


def parse_rules() -> dict[str, str]:
    text = REGRAS.read_text(encoding="utf-8")
    return {rid: rest for rid, rest in RE_RULE_ROW.findall(text)}


def parse_scenarios(rep: Report) -> dict[str, str]:
    text = CENARIOS.read_text(encoding="utf-8")
    heads = list(RE_SCENARIO.finditer(text))
    out: dict[str, str] = {}
    for i, m in enumerate(heads):
        end = heads[i + 1].start() if i + 1 < len(heads) else len(text)
        body = text[m.start():end]
        if m.group(1) in out:
            rep.err(f"cenários: ID duplicado {m.group(1)}")
        if "- Regras:" not in body:
            rep.err(f"cenários: {m.group(1)} sem linha '- Regras:'")
        if not re.search(r"^(Dado|Para cada|Então)", body, re.M):
            rep.err(f"cenários: {m.group(1)} sem passos Dado/Quando/Então")
        out[m.group(1)] = body
    return out


def render_permissions(ops: dict[str, dict]) -> str:
    cols = ["public", "visitor", "customer", "professional", "owner", "provider"]
    head = ("| Operação | Método e rota | Público | Visitante | Cliente | Profissional | Dono | Provedor | Reauth | Transições |\n"
            "|---|---|---|---|---|---|---|---|---|---|\n")
    lines = []
    for oid, op in sorted(ops.items(), key=lambda kv: (kv[1]["path"], kv[1]["method"])):
        by_actor = {r["actor"]: r["scope"] for r in (op["authz"] or [])}
        cells = [by_actor.get(a, "—") for a in cols]
        cells = ["sim" if c == "none" else c for c in cells]
        trans = ", ".join(op["transitions"]) or "—"
        lines.append(
            f"| `{oid}` | `{op['method'].upper()} {op['path']}` | " + " | ".join(cells)
            + f" | {'sim' if op['reauth'] else '—'} | {trans} |"
        )
    return head + "\n".join(lines) + "\n"


def main() -> int:
    write = "--write" in sys.argv
    rep = Report()
    spec = load_openapi()
    ops = collect_operations(spec, rep)
    schemas, enum_values, prop_names, param_names = schema_index(spec)
    event_types = set(schemas["EventType"]["enum"])
    error_codes = set(schemas["ErrorCode"]["enum"])
    ctx = {
        "ops": ops,
        "schemas": schemas,
        "events": event_types,
        "security": set(spec["components"]["securitySchemes"]),
        "headers": set(spec["components"].get("headers", {})),
        "responses": set(spec["components"].get("responses", {})),
        "known_words": error_codes | enum_values | prop_names | param_names | SCOPES | {"audit_event"},
        # Nomes de parâmetro de rota e termos de navegador citados em texto, que não são do contrato.
        "ignore": param_names | {"operationId", "localStorage", "sessionStorage", "dangerouslySetInnerHTML"},
    }

    check_operations(ops, event_types, rep)

    catalog = parse_event_catalog(rep)
    for missing in sorted(event_types - catalog):
        rep.err(f"eventos-sse.md: EventType `{missing}` sem linha no catálogo")
    for extra in sorted(catalog - event_types):
        rep.err(f"eventos-sse.md: catálogo cita `{extra}` que não existe em EventType")

    matrix = parse_matrix(rep, ops)
    check_refs(MATRIZ.read_text(encoding="utf-8"), "matriz-transicoes.md", ctx, rep)
    for oid, op in ops.items():
        for tid in op["transitions"]:
            if tid not in matrix:
                rep.err(f"{oid}: x-transitions cita {tid}, ausente da matriz")
            elif oid not in matrix[tid]["ops"]:
                rep.err(f"{oid}: declara {tid}, mas a linha {tid} da matriz não cita esta operação")
    for tid, row in matrix.items():
        for oid in row["ops"]:
            if tid not in ops[oid]["transitions"]:
                rep.err(f"matriz {tid}: cita `{oid}`, mas a operação não declara {tid} em x-transitions")

    adrs = {p.name[:4] for p in ADR_DIR.glob("[0-9][0-9][0-9][0-9]-*.md")}
    rules = parse_rules()
    check_refs(REGRAS.read_text(encoding="utf-8"), "regras.md", ctx, rep)
    for rid, rest in rules.items():
        for tid in RE_TRANS.findall(rest):
            if tid not in matrix:
                rep.err(f"regras.md {rid}: cita {tid} inexistente")
        for num in RE_ADR.findall(rest):
            if num not in adrs:
                rep.err(f"regras.md {rid}: cita ADR-{num} inexistente")

    scenarios = parse_scenarios(rep)
    check_refs(CENARIOS.read_text(encoding="utf-8"), "cenarios-aceite.md", ctx, rep)
    covered_rules: set[str] = set()
    covered_trans: set[str] = set()
    for cid, body in scenarios.items():
        meta = body.split("\n\n", 1)[0]
        for rid in RE_RULE.findall(meta):
            if rid not in rules:
                rep.err(f"{cid}: cita regra inexistente {rid}")
            covered_rules.add(rid)
        for tid in RE_TRANS.findall(meta):
            if tid not in matrix:
                rep.err(f"{cid}: cita transição inexistente {tid}")
            covered_trans.add(tid)
    for rid, rest in rules.items():
        if rid not in covered_rules and not RE_ADR.search(rest):
            rep.err(f"{rid}: sem cenário de aceite e sem ADR")
    for tid in sorted(set(matrix) - covered_trans):
        rep.warn(f"{tid}: nenhuma cena cita esta transição diretamente")

    adr_index = (ADR_DIR / "README.md").read_text(encoding="utf-8")
    for num in sorted(adrs):
        if f"ADR-{num}" not in adr_index:
            rep.err(f"adr/README.md: ADR-{num} fora do índice")

    table = render_permissions(ops)
    perm_text = PERMISSOES.read_text(encoding="utf-8")
    if MARK_START not in perm_text or MARK_END not in perm_text:
        rep.err("matriz-permissoes.md: marcadores da tabela gerada ausentes")
    else:
        before, rest = perm_text.split(MARK_START, 1)
        current, after = rest.split(MARK_END, 1)
        wanted = "\n" + table
        if current != wanted:
            if write:
                PERMISSOES.write_text(before + MARK_START + wanted + MARK_END + after, encoding="utf-8", newline="\n")
                print("matriz-permissoes.md: tabela regenerada")
            else:
                rep.err("matriz-permissoes.md: tabela desatualizada (rode com --write)")

    print(f"Operações: {len(ops)} | Transições: {len(matrix)} | Regras: {len(rules)} | "
          f"Cenários: {len(scenarios)} | ADRs: {len(adrs)} | Eventos: {len(event_types)}")
    print(f"Regras cobertas por cenário: {len(covered_rules & set(rules))}/{len(rules)} | "
          f"Transições citadas em cenário: {len(covered_trans & set(matrix))}/{len(matrix)}")
    for w in rep.warnings:
        print(f"AVISO: {w}")
    for e in rep.errors:
        print(f"ERRO: {e}")
    print("OK" if not rep.errors else f"{len(rep.errors)} erro(s)")
    return 1 if rep.errors else 0


if __name__ == "__main__":
    sys.exit(main())
