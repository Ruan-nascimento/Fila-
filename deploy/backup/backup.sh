#!/bin/sh
# Backup lógico do banco (pg_dump, formato custom), CIFRADO e CONFERIDO. Roda no contêiner
# "backup" do compose (docker compose ... run --rm backup). docs/operacao/backup-restauracao.md
#
#   1. pg_dump com o papel fila_backup (só leitura), por TLS;
#   2. confere o dump (pg_restore --list precisa ler o arquivo inteiro);
#   3. cifra com AES-256 (chave em /run/secrets/backup_chave) e grava HMAC-SHA256 do cifrado;
#   4. copia o diário de exclusões junto (para restaurar e reaplicar);
#   5. só então grava ULTIMO_BACKUP_OK (fila-admin verificar -backups alerta se envelhecer);
#   6. apaga backups com mais de FILA_BACKUP_RETENCAO_DIAS dias (padrão 14; prazo legal a validar).
# Nada de senha em argumento de linha de comando (fica visível em ps): PGPASSFILE.
set -eu

DIR=/backups
CHAVE=/run/secrets/backup_chave
RET="${FILA_BACKUP_RETENCAO_DIAS:-14}"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BASE="$DIR/fila-$STAMP"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

[ -s "$CHAVE" ] || { echo "backup: falta a chave de cifra ($CHAVE)" >&2; exit 2; }
umask 077
printf '*:*:*:fila_backup:%s\n' "$(cat /run/secrets/pg_senha_backup)" > "$TMP/pgpass"
export PGPASSFILE="$TMP/pgpass" PGUSER=fila_backup PGDATABASE=fila

INICIO=$(date +%s)
pg_dump --format=custom --compress=6 --no-owner --no-privileges --file="$TMP/fila.dump"
# Conferência: o arquivo precisa ser legível do começo ao fim.
pg_restore --list "$TMP/fila.dump" > "$TMP/conteudo.txt"
TABELAS=$(grep -c ' TABLE DATA ' "$TMP/conteudo.txt" || true)
[ "$TABELAS" -gt 0 ] || { echo "backup: dump sem dados de tabela; algo está errado" >&2; exit 1; }

openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass "file:$CHAVE" -in "$TMP/fila.dump" -out "$BASE.dump.enc"
HMAC=$(openssl dgst -sha256 -hmac "$(cat "$CHAVE")" -r "$BASE.dump.enc" | cut -d' ' -f1)
if [ -f /diario/exclusoes.jsonl ]; then
	cp /diario/exclusoes.jsonl "$BASE.diario.jsonl"
fi
FIM=$(date +%s)
cat > "$BASE.manifesto" <<EOF
arquivo=fila-$STAMP.dump.enc
criado_utc=$STAMP
hmac_sha256=$HMAC
tamanho_bytes=$(wc -c < "$BASE.dump.enc" | tr -d ' ')
tabelas_com_dados=$TABELAS
duracao_segundos=$((FIM - INICIO))
EOF
date -u +%Y-%m-%dT%H:%M:%SZ > "$DIR/ULTIMO_BACKUP_OK"

# Retenção: só depois de um backup novo conferido.
find "$DIR" -maxdepth 1 -name 'fila-*' -mtime +"$RET" -print -exec rm -f {} \; | sed 's/^/backup: apagado por retenção: /'
echo "backup: ok fila-$STAMP ($TABELAS tabelas, $((FIM - INICIO)) s)"
