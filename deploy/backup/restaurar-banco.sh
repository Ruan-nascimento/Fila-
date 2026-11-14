#!/bin/sh
# Restaura um backup num banco NOVO e o coloca no lugar do atual (contêiner "restore").
# Quem chama é deploy/restaurar.sh, com a API e o worker PARADOS.
#
#   restaurar-banco.sh fila-AAAAmmddTHHMMSSZ
#
#   1. confere o HMAC do arquivo cifrado (integridade) e decifra;
#   2. cria fila_restaurando e restaura com o papel fila_migrador como dono;
#   3. renomeia o banco atual para fila_antes_<carimbo> (guardado: nada é apagado) e o
#      restaurado para fila. O banco novo tem outro oid: o worker trava sozinho (ADR-0026).
set -eu

NOME="${1:?informe o backup, ex.: fila-20261114T030000Z}"
ARQ="/backups/$NOME.dump.enc"
MAN="/backups/$NOME.manifesto"
CHAVE=/run/secrets/backup/backup_chave
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
umask 077

if [ ! -f "$ARQ" ] || [ ! -f "$MAN" ]; then
	echo "restaurar: backup $NOME não encontrado em /backups" >&2
	exit 2
fi
ESPERADO=$(sed -n 's/^hmac_sha256=//p' "$MAN")
CALCULADO=$(openssl dgst -sha256 -hmac "$(cat "$CHAVE")" -r "$ARQ" | cut -d' ' -f1)
[ "$ESPERADO" = "$CALCULADO" ] || { echo "restaurar: HMAC NÃO confere (arquivo alterado ou chave errada); nada foi feito" >&2; exit 1; }
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass "file:$CHAVE" -in "$ARQ" -out "$TMP/fila.dump"

printf '*:*:*:fila_dba:%s\n' "$(cat /run/secrets/banco/pg_senha_superusuario)" > "$TMP/pgpass"
export PGPASSFILE="$TMP/pgpass" PGUSER=fila_dba
CARIMBO=$(date -u +%Y%m%dT%H%M%SZ)

psql -v ON_ERROR_STOP=1 -d postgres -c "DROP DATABASE IF EXISTS fila_restaurando" \
	-c "CREATE DATABASE fila_restaurando OWNER fila_migrador ENCODING 'UTF8' TEMPLATE template0" \
	-c "REVOKE ALL ON DATABASE fila_restaurando FROM PUBLIC" \
	-c "GRANT CONNECT ON DATABASE fila_restaurando TO fila_migrador, fila_app, fila_backup"
INICIO=$(date +%s)
pg_restore --exit-on-error --no-owner --role=fila_migrador --dbname=fila_restaurando "$TMP/fila.dump"
FIM=$(date +%s)

# Troca: ninguém pode estar conectado (API e worker parados). Conexões que sobrarem são encerradas.
psql -v ON_ERROR_STOP=1 -d postgres \
	-c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'fila' AND pid <> pg_backend_pid()" \
	-c "ALTER DATABASE fila RENAME TO fila_antes_$CARIMBO" \
	-c "ALTER DATABASE fila_restaurando RENAME TO fila"
echo "restaurar: ok ($NOME em $((FIM - INICIO)) s); banco anterior guardado como fila_antes_$CARIMBO"
