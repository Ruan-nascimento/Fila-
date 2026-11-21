#!/usr/bin/env python3
"""Prepara o Cloudflare R2 de um ambiente (docs/operacao/publicacao-v1.md §4.2) SEM exibir segredos.

    python3 -I scripts/r2-preparar.py homologacao|producao

Lê CLOUDFLARE_ACCOUNT_ID e CLOUDFLARE_SETUP_API_TOKEN de ~/.config/fila-v1/geral.env
(gravados com scripts/segredo.sh). Faz, sem recriar o que já existe:
  1. buckets privados fila-<amb>-midia, fila-<amb>-diario e fila-<amb>-backup;
  2. ciclo de vida só em backup/banco/ (35 dias) e limpeza de uploads incompletos;
  3. credencial S3 da API/worker (leitura e escrita só em midia e diario) e
     credencial do workflow Backup (leitura em midia e diario, escrita em backup);
  4. verifica: grava, lê e apaga um objeto com cada credencial, confirma que a
     credencial da API NÃO acessa o backup e que nada responde sem assinatura.
Grava em ~/.config/fila-v1/<amb>.env (600). Nunca imprime chaves.
"""
import datetime
import hashlib
import hmac
import json
import os
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

AMB = sys.argv[1] if len(sys.argv) > 1 else ""
CURTO = {"homologacao": "hml", "producao": "prod"}.get(AMB)
if not CURTO:
    sys.exit("uso: r2-preparar.py homologacao|producao")
DIR = os.path.expanduser("~/.config/fila-v1")
os.umask(0o077)


def ler(arq):
    d = {}
    if os.path.exists(arq):
        for linha in open(arq):
            if "=" in linha:
                k, v = linha.rstrip("\n").split("=", 1)
                d[k] = v
    return d


def gravar(arq, novos):
    d = ler(arq)
    d.update(novos)
    tmp = arq + ".tmp"
    with open(tmp, "w") as f:
        for k, v in d.items():
            f.write(f"{k}={v}\n")
    os.chmod(tmp, 0o600)
    os.replace(tmp, arq)


GERAL = ler(os.path.join(DIR, "geral.env"))
ACC = GERAL["CLOUDFLARE_ACCOUNT_ID"]
TOKEN = GERAL["CLOUDFLARE_SETUP_API_TOKEN"]
ARQ = os.path.join(DIR, f"{AMB}.env")
API = f"https://api.cloudflare.com/client/v4/accounts/{ACC}"
ENDPOINT = f"https://{ACC}.r2.cloudflarestorage.com"
MIDIA, DIARIO, BACKUP = (f"fila-{CURTO}-{s}" for s in ("midia", "diario", "backup"))


def cf(metodo, caminho, corpo=None):
    req = urllib.request.Request(API + caminho, method=metodo,
                                 data=None if corpo is None else json.dumps(corpo).encode(),
                                 headers={"Authorization": "Bearer " + TOKEN,
                                          "Content-Type": "application/json",
                                          "User-Agent": "fila-setup/1"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        d = json.load(e)
        raise SystemExit(f"Cloudflare recusou {metodo} {caminho.split('?')[0]}: HTTP {e.code} {d.get('errors')}")


def passo(m):
    print("·", m, flush=True)


# 1. buckets
existentes = {b["name"] for b in cf("GET", "/r2/buckets")["result"]["buckets"]}
for b in (MIDIA, DIARIO, BACKUP):
    if b in existentes:
        passo(f"bucket {b} já existe; mantido")
    else:
        cf("POST", "/r2/buckets", {"name": b, "locationHint": "enam"})
        passo(f"bucket {b} criado (privado, América do Norte leste)")
    pub = cf("GET", f"/r2/buckets/{b}/domains/managed")["result"]
    if pub.get("enabled"):
        raise SystemExit(f"{b} tem acesso público r2.dev ligado; desligue no painel antes de seguir")

# 2. ciclo de vida
SEMANA, DIAS35 = 7 * 86400, 35 * 86400
multipart = {"id": "uploads-incompletos-7d", "enabled": True, "conditions": {"prefix": ""},
             "abortMultipartUploadsTransition": {"condition": {"type": "Age", "maxAge": SEMANA}}}
for b in (MIDIA, DIARIO):
    cf("PUT", f"/r2/buckets/{b}/lifecycle", {"rules": [multipart]})
cf("PUT", f"/r2/buckets/{BACKUP}/lifecycle", {"rules": [multipart, {
    "id": "dumps-35-dias", "enabled": True, "conditions": {"prefix": "banco/"},
    "deleteObjectsTransition": {"condition": {"type": "Age", "maxAge": DIAS35}}}]})
passo("ciclo de vida: só backup/banco/ expira (35 dias); fotos e diário nunca expiram")

# 3. credenciais S3 (token de conta restrito por bucket; segredo S3 = sha256 do valor)
grupos = {g["name"]: g["id"] for g in cf("GET", "/tokens/permission_groups")["result"]}
ESCRITA, LEITURA = grupos["Workers R2 Storage Bucket Item Write"], grupos["Workers R2 Storage Bucket Item Read"]


def rec(*bs):
    return {f"com.cloudflare.edge.r2.bucket.{ACC}_default_{b}": "*" for b in bs}


atual = ler(ARQ)
tokens = {t["name"]: t["id"] for t in cf("GET", "/tokens?per_page=50")["result"]}


def credencial(nome, politicas, var_id, var_seg):
    if atual.get(var_id) and atual.get(var_seg) and nome in tokens:
        passo(f"credencial {nome} já existe; mantida")
        return
    if nome in tokens:
        raise SystemExit(f"o token {nome} existe na Cloudflare mas não no arquivo local; "
                         f"apague-o no painel (API Tokens) e rode de novo")
    r = cf("POST", "/tokens", {"name": nome, "policies": politicas})["result"]
    gravar(ARQ, {var_id: r["id"], var_seg: hashlib.sha256(r["value"].encode()).hexdigest()})
    passo(f"credencial {nome} criada (valor não exibido)")


credencial(f"fila-{CURTO}-api", [
    {"effect": "allow", "permission_groups": [{"id": ESCRITA}], "resources": rec(MIDIA, DIARIO)}],
    "FILA_S3_ACCESS_KEY_ID", "FILA_S3_SECRET_ACCESS_KEY")
credencial(f"fila-{CURTO}-backup", [
    {"effect": "allow", "permission_groups": [{"id": LEITURA}], "resources": rec(MIDIA, DIARIO)},
    {"effect": "allow", "permission_groups": [{"id": ESCRITA}], "resources": rec(BACKUP)}],
    "R2_BACKUP_ACCESS_KEY_ID", "R2_BACKUP_SECRET_ACCESS_KEY")
gravar(ARQ, {"FILA_MEDIA_STORAGE": "s3", "FILA_S3_ENDPOINT": ENDPOINT, "FILA_S3_BUCKET": MIDIA,
             "FILA_S3_REGION": "auto", "FILA_DELETION_JOURNAL_BUCKET": DIARIO,
             "R2_ENDPOINT": ENDPOINT, "R2_MEDIA_BUCKET": MIDIA, "R2_JOURNAL_BUCKET": DIARIO,
             "R2_BACKUP_BUCKET": BACKUP})


# 4. verificação com SigV4 (sem SDK)
def s3(metodo, bucket, chave, ak, sk, corpo=b"", assinar=True, query=""):
    host = urllib.parse.urlsplit(ENDPOINT).netloc
    caminho = "/" + bucket + ("/" + urllib.parse.quote(chave) if chave else "")
    agora = datetime.datetime.now(datetime.timezone.utc)
    amz, dia = agora.strftime("%Y%m%dT%H%M%SZ"), agora.strftime("%Y%m%d")
    hp = hashlib.sha256(corpo).hexdigest()
    cab = {"host": host, "x-amz-content-sha256": hp, "x-amz-date": amz}
    if assinar:
        nomes = ";".join(sorted(cab))
        canon = "\n".join([metodo, caminho, query, *(f"{k}:{cab[k]}" for k in sorted(cab)), "", nomes, hp])
        escopo = f"{dia}/auto/s3/aws4_request"
        sts = "\n".join(["AWS4-HMAC-SHA256", amz, escopo, hashlib.sha256(canon.encode()).hexdigest()])
        k = ("AWS4" + sk).encode()
        for parte in (dia, "auto", "s3", "aws4_request"):
            k = hmac.new(k, parte.encode(), hashlib.sha256).digest()
        sig = hmac.new(k, sts.encode(), hashlib.sha256).hexdigest()
        cab["authorization"] = f"AWS4-HMAC-SHA256 Credential={ak}/{escopo}, SignedHeaders={nomes}, Signature={sig}"
    url = ENDPOINT + caminho + ("?" + query if query else "")
    req = urllib.request.Request(url, method=metodo, data=corpo if metodo == "PUT" else None,
                                 headers={k: v for k, v in cab.items() if k != "host"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, b""


atual = ler(ARQ)
API_K = (atual["FILA_S3_ACCESS_KEY_ID"], atual["FILA_S3_SECRET_ACCESS_KEY"])
BKP_K = (atual["R2_BACKUP_ACCESS_KEY_ID"], atual["R2_BACKUP_SECRET_ACCESS_KEY"])
falhas = []


def espera(desc, cond):
    print(f"  {'ok ' if cond else 'FALHOU'} {desc}")
    if not cond:
        falhas.append(desc)


def ciclo(desc, bucket, cred):
    chave = f"verificacao/{secrets.token_hex(8)}.txt"
    dado = b"verificacao fila " + chave.encode()
    for _ in range(12):  # o token novo leva alguns segundos para valer
        st, _b = s3("PUT", bucket, chave, *cred, corpo=dado)
        if st != 401 and st != 403:
            break
        time.sleep(5)
    st2, lido = s3("GET", bucket, chave, *cred)
    st3, _b = s3("DELETE", bucket, chave, *cred)
    espera(f"{desc}: grava ({st}), lê igual ({st2}), apaga ({st3})", st == 200 and lido == dado and st3 == 204)


passo("verificando")
ciclo("API em midia", MIDIA, API_K)
ciclo("API em diario", DIARIO, API_K)
ciclo("Backup em backup", BACKUP, BKP_K)
st, _b = s3("GET", BACKUP, "", *API_K, query="list-type=2")
espera(f"API NÃO lista o backup ({st})", st in (401, 403))
st, _b = s3("PUT", BACKUP, "verificacao/negado.txt", *API_K, corpo=b"x")
espera(f"API NÃO grava no backup ({st})", st in (401, 403))
st, _b = s3("PUT", MIDIA, "verificacao/negado.txt", *BKP_K, corpo=b"x")
espera(f"Backup NÃO grava em midia ({st})", st in (401, 403))
st, _b = s3("GET", MIDIA, "", *BKP_K, query="list-type=2")
espera(f"Backup lista midia para copiar ({st})", st == 200)
for b in (MIDIA, DIARIO, BACKUP):
    st, _b = s3("GET", b, "", "", "", assinar=False, query="list-type=2")
    espera(f"{b} sem assinatura é recusado ({st})", st in (400, 401, 403))
if falhas:
    sys.exit(f"{len(falhas)} verificação(ões) falharam")
passo(f"pronto. Variáveis em {ARQ} (permissão 600). Nada foi exibido.")
