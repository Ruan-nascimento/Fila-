#!/usr/bin/env python3
"""Preenche no Render as variáveis secretas e de domínio de um ambiente, SEM exibir valores.

    python3 -I scripts/render-variaveis.py homologacao|producao

Lê RENDER_API_KEY de ~/.config/fila-v1/geral.env e os valores de ~/.config/fila-v1/<amb>.env.
Gera FILA_PROXY_SHARED_SECRET uma única vez (o mesmo valor vai depois para a Vercel).
Só grava variáveis que existem no arquivo do ambiente; as demais (Google, AbacatePay,
monitor) ficam para a etapa própria. Não toca em serviços de outros projetos.
"""
import json, os, secrets, sys, urllib.request, urllib.error

AMB = sys.argv[1] if len(sys.argv) > 1 else ""
CURTO = {"homologacao": "hml", "producao": "prod"}.get(AMB) or sys.exit("uso: render-variaveis.py homologacao|producao")
SITE = {"homologacao": "https://teste.fiveline.pro", "producao": "https://app.fiveline.pro"}[AMB]
DIR = os.path.expanduser("~/.config/fila-v1"); os.umask(0o077)

def ler(arq):
    d = {}
    for l in open(arq):
        if "=" in l:
            k, v = l.rstrip("\n").split("=", 1); d[k] = v
    return d

ARQ = os.path.join(DIR, f"{AMB}.env")
env = ler(ARQ)
if not env.get("FILA_PROXY_SHARED_SECRET"):
    with open(ARQ, "a") as f:
        f.write("FILA_PROXY_SHARED_SECRET=" + secrets.token_urlsafe(48) + "\n")
    env = ler(ARQ); print("· FILA_PROXY_SHARED_SECRET gerado (uma única vez)")
KEY = ler(os.path.join(DIR, "geral.env"))["RENDER_API_KEY"]

def render(metodo, caminho, corpo=None):
    req = urllib.request.Request("https://api.render.com/v1" + caminho, method=metodo,
        data=None if corpo is None else json.dumps(corpo).encode(),
        headers={"Authorization": "Bearer " + KEY, "Accept": "application/json",
                 "Content-Type": "application/json", "User-Agent": "fila-setup/1"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            b = r.read(); return json.loads(b) if b else None
    except urllib.error.HTTPError as e:
        sys.exit(f"Render recusou {metodo} {caminho}: HTTP {e.code}")

svcs = {x["service"]["name"]: x["service"]["id"] for x in render("GET", "/services?limit=100")}
grupos = {x.get("envGroup", x)["name"]: x.get("envGroup", x)["id"] for x in render("GET", "/env-groups?limit=100")}
GRUPO, API, WORKER = grupos[f"fila-{CURTO}-comum"], svcs[f"fila-api-{CURTO}"], svcs[f"fila-worker-{CURTO}"]

fixos = {"FILA_PUBLIC_BASE_URL": SITE, "FILA_ALLOWED_ORIGINS": SITE,
         "FILA_MAIL_FROM": "Fila <nao-responda@mail.fiveline.pro>"}
# Pix só liga quando a chave do ambiente existe (teste em homologação; produção só após validação).
if env.get("FILA_ABACATEPAY_API_KEY"):
    fixos["FILA_PIX_PROVIDER"] = "abacatepay"
plano = [
    ("grupo", GRUPO, ["FILA_PUBLIC_BASE_URL", "FILA_ALLOWED_ORIGINS", "FILA_DATABASE_URL", "FILA_SECRET_KEY",
                      "FILA_S3_ENDPOINT", "FILA_S3_ACCESS_KEY_ID", "FILA_S3_SECRET_ACCESS_KEY",
                      "FILA_ABACATEPAY_API_KEY", "FILA_PIX_PROVIDER"]),
    ("api", API, ["FILA_PROXY_SHARED_SECRET", "FILA_GOOGLE_CLIENT_ID", "FILA_GOOGLE_CLIENT_SECRET",
                  "FILA_ABACATEPAY_WEBHOOK_SECRET"]),
    ("worker", WORKER, ["FILA_RESEND_API_KEY", "FILA_MAIL_FROM", "FILA_MAIL_REPLY_TO", "FILA_HEARTBEAT_URL"]),
]
for alvo, ident, chaves in plano:
    for k in chaves:
        v = fixos.get(k) or env.get(k)
        if not v:
            print(f"  {alvo}: {k} ainda sem valor; fica para a etapa própria"); continue
        base = f"/env-groups/{ident}" if alvo == "grupo" else f"/services/{ident}"
        render("PUT", f"{base}/env-vars/{k}", {"value": v})
        print(f"  {alvo}: {k} gravado")
print("· pronto (nenhum valor exibido)")
