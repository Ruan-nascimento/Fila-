#!/usr/bin/env bash
# Prepara o banco Neon de um ambiente (docs/operacao/publicacao-v1.md §4.1), SEM exibir segredos.
#
#   scripts/neon-preparar.sh homologacao|producao
#
# Pré-requisito: login feito pelo fluxo oficial (npx neonctl@8.23.2 auth), no mesmo usuário.
# Faz:
#   1. cria o projeto fila-<ambiente> (PostgreSQL 18, AWS us-east-1), se ainda não existir;
#   2. guarda a URL DIRETA do dono do banco (migrador) e cria o papel restrito fila_app por SQL;
#   3. gera FILA_SECRET_KEY uma única vez (nunca troca se já existir);
#   4. confere versão e uuidv7, aplica migrations e permissões mínimas e confere.
# Tudo vai para ~/.config/fila-v1/<ambiente>.env (permissão 600, fora do Git). Nada é impresso.
# Repetível: não recria projeto, papel nem chave que já existam.
set -euo pipefail

AMB="${1:?uso: neon-preparar.sh homologacao|producao}"
case "$AMB" in homologacao) FILA_ENV_V=homologacao ;; producao) FILA_ENV_V=production ;; *) echo "ambiente inválido"; exit 2 ;; esac
RAIZ="$(cd "$(dirname "$0")/.." && pwd)"
NEON=(npx -y neonctl@8.23.2)
DIR="$HOME/.config/fila-v1"
ARQ="$DIR/$AMB.env"
umask 077
mkdir -p "$DIR" && chmod 700 "$DIR"
touch "$ARQ" && chmod 600 "$ARQ"

ler() { grep -E "^$1=" "$ARQ" | tail -1 | cut -d= -f2- || true; }
gravar() { # gravar NOME VALOR (substitui a linha, sem eco)
  local tmp; tmp="$(mktemp "$DIR/.tmp.XXXX")"
  grep -vE "^$1=" "$ARQ" > "$tmp" || true
  printf '%s=%s\n' "$1" "$2" >> "$tmp"
  mv "$tmp" "$ARQ"
}
passo() { echo "· $*"; }

PROJ="fila-$AMB"
ID="$("${NEON[@]}" projects list --output json | python3 -c "
import json,sys
d=json.load(sys.stdin); ps=d.get('projects',d) if isinstance(d,dict) else d
print(next((p['id'] for p in ps if p.get('name')=='$PROJ'),''))")"
if [ -z "$ID" ]; then
  passo "criando o projeto $PROJ (PostgreSQL 18, AWS us-east-1)"
  ID="$("${NEON[@]}" projects create --name "$PROJ" --region-id aws-us-east-1 --pg-version 18 --output json |
    python3 -c "import json,sys; print(json.load(sys.stdin)['project']['id'])")"
else
  passo "projeto $PROJ já existe; reaproveitando"
fi
gravar NEON_PROJECT_ID "$ID"

if [ -z "$(ler FILA_MIGRATOR_DATABASE_URL)" ]; then
  URL="$("${NEON[@]}" connection-string --project-id "$ID" --database-name neondb --role-name neondb_owner --ssl require)"
  case "$URL" in *-pooler.*) echo "erro: a URL veio com pooling; esperado conexão direta"; exit 1 ;; esac
  gravar FILA_MIGRATOR_DATABASE_URL "$URL"
  passo "URL direta do migrador guardada"
fi
MIG="$(ler FILA_MIGRATOR_DATABASE_URL)"

psql_mig() { # SQL pelo cliente psql 18 do contêiner oficial; a URL vai por variável de ambiente
  docker run --rm -i -e DBURL="$MIG" postgres:18.7 sh -c 'psql "$DBURL" -v ON_ERROR_STOP=1 -qAt'
}

if [ -z "$(ler FILA_DATABASE_URL)" ]; then
  SENHA="$(openssl rand -hex 24)"
  passo "criando o papel restrito fila_app"
  psql_mig <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fila_app') THEN
    CREATE ROLE fila_app LOGIN PASSWORD '$SENHA' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
  ELSE
    ALTER ROLE fila_app PASSWORD '$SENHA';
  END IF;
END \$\$;
ALTER ROLE fila_app SET statement_timeout = '30s';
ALTER ROLE fila_app SET lock_timeout = '10s';
ALTER ROLE fila_app SET idle_in_transaction_session_timeout = '60s';
GRANT CONNECT ON DATABASE neondb TO fila_app;
SQL
  APP="$(python3 -c "
import sys,urllib.parse as u
p=u.urlsplit(sys.argv[1]); host=p.hostname+(':%d'%p.port if p.port else '')
print(u.urlunsplit((p.scheme,'fila_app:'+sys.argv[2]+'@'+host,p.path,p.query,'')))" "$MIG" "$SENHA")"
  gravar FILA_DATABASE_URL "$APP"
  unset SENHA APP
fi

if [ -z "$(ler FILA_SECRET_KEY)" ]; then
  gravar FILA_SECRET_KEY "$(cd "$RAIZ/backend" && GOTOOLCHAIN=auto go run ./cmd/fila-admin gerar-chave 2>/dev/null | tail -1)"
  passo "FILA_SECRET_KEY gerada (uma única vez para este ambiente)"
fi

passo "conferindo versão e uuidv7"
echo "SELECT 'versão: '||current_setting('server_version'); SELECT 'uuidv7 ok: '||(uuidv7() IS NOT NULL);" | psql_mig

passo "migrations e permissões mínimas"
(
  cd "$RAIZ/backend"
  export GOTOOLCHAIN=auto FILA_ENV="$FILA_ENV_V" FILA_COOKIE_SECURE=true FILA_LOG_FORMAT=text \
    FILA_PUBLIC_BASE_URL="https://placeholder.invalid" FILA_ALLOWED_ORIGINS="https://placeholder.invalid" \
    FILA_DATABASE_URL="$MIG"
  go run ./cmd/migrate up
  go run ./cmd/migrate permissoes -app fila_app
  go run ./cmd/migrate permissoes -app fila_app -verificar
  go run ./cmd/migrate status | tail -2
)
passo "pronto. Segredos em $ARQ (permissão 600). Nada foi exibido."
