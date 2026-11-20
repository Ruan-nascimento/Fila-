#!/usr/bin/env bash
# Ensaio de restauração ISOLADO de um backup cifrado (docs/operacao/backup-e-recuperacao-v1.md).
#
#   scripts/ensaio-restauracao.sh <backup .dump.age> <arquivo da chave PRIVADA age> [pasta do diário] [desde]
#
#   [desde] (AAAA-MM-DD) só é preciso se o backup não tiver sinal de vida do worker.
#
# O que faz (e mede o tempo de cada passo):
#   1. sobe um PostgreSQL 18 descartável em Docker (porta só local, senha aleatória);
#   2. decifra o backup num arquivo temporário (permissão só do usuário) e restaura;
#   3. aplica migrations pendentes (o backup pode ser de versão anterior);
#   4. trava o worker por restauração (sem confiar só na detecção automática);
#   5. reconcilia: encerra sessões, troca a época do fluxo e reaplica as exclusões do diário;
#   6. mostra indicadores e alertas; apaga o banco descartável e o arquivo decifrado.
#
# Isolamento: e-mails vão para ARQUIVOS numa pasta temporária (nada é enviado), Pix e fotos ficam
# DESLIGADOS (nenhuma chamada a provedor real) e nada é ligado a domínio público.
# A conciliação com a AbacatePay NÃO é ensaiada aqui (exigiria o provedor real): -sem-pix é usado
# só porque esta cópia é descartável. Numa restauração de verdade, siga o runbook.
#
# FILA_SECRET_KEY do ambiente é pedida sem eco (não vai para histórico nem argumentos): sem ela os
# dados cifrados (e-mails na fila, códigos) não podem ser lidos. Deixe vazio para pular o passo 5.
set -euo pipefail

BACKUP="${1:?uso: ensaio-restauracao.sh <backup.dump.age> <chave-privada-age> [pasta-do-diario]}"
IDENT="${2:?informe o arquivo da chave privada age}"
DIARIO="${3:-}"
DESDE="${4:-}"
RAIZ="$(cd "$(dirname "$0")/.." && pwd)"
command -v age >/dev/null || { echo "instale o age (sudo apt install age)"; exit 1; }
command -v docker >/dev/null || { echo "Docker é necessário"; exit 1; }

t0=$(date +%s)
passo() { echo "[$(($(date +%s) - t0))s] $*"; }

umask 077
TMP="$(mktemp -d)"
NOME="fila-ensaio-$$"
SENHA="$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)"
limpar() {
  docker rm -f "$NOME" >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap limpar EXIT

CHAVE=""
read -rsp "FILA_SECRET_KEY do ambiente do backup (Enter para pular a conciliação): " CHAVE || true
printf '\n'

passo "subindo PostgreSQL 18 descartável"
docker run -d --name "$NOME" -e POSTGRES_USER=ensaio -e POSTGRES_PASSWORD="$SENHA" -e POSTGRES_DB=fila \
  -p 127.0.0.1::5432 postgres:18.7 >/dev/null
until docker exec "$NOME" pg_isready -U ensaio -d fila >/dev/null 2>&1; do sleep 1; done
sleep 2
PORTA="$(docker port "$NOME" 5432/tcp | head -1 | sed 's/.*://')"

passo "decifrando e restaurando"
age -d -i "$IDENT" -o "$TMP/fila.dump" "$BACKUP"
docker cp "$TMP/fila.dump" "$NOME:/tmp/fila.dump" >/dev/null
rm -f "$TMP/fila.dump"
docker exec "$NOME" pg_restore --no-owner --no-privileges -U ensaio -d fila /tmp/fila.dump
docker exec "$NOME" rm -f /tmp/fila.dump

passo "compilando as ferramentas"
(cd "$RAIZ/backend" && GOTOOLCHAIN=auto go build -trimpath -o "$TMP/bin/" ./cmd/migrate ./cmd/fila-admin)

# Ambiente isolado: roda numa pasta vazia (nenhum .env do projeto é lido).
export FILA_ENV=development FILA_LOG_FORMAT=text \
  FILA_DATABASE_URL="postgres://ensaio:${SENHA}@127.0.0.1:${PORTA}/fila?sslmode=disable" \
  FILA_MAIL_MODE=file FILA_MAIL_DIR="$TMP/correio" FILA_PIX_PROVIDER=disabled FILA_MEDIA_STORAGE=disabled
[ -n "$CHAVE" ] && export FILA_SECRET_KEY="$CHAVE"
mkdir -p "$TMP/vazio" && cd "$TMP/vazio"

passo "migrations"
"$TMP/bin/migrate" status | tail -3
"$TMP/bin/migrate" up

passo "travando o worker por restauração"
"$TMP/bin/fila-admin" restauracao travar -motivo "ensaio de restauração $(date -u +%FT%TZ)"

if [ -n "$CHAVE" ]; then
  passo "conciliação (sessões, época do fluxo, diário de exclusões)"
  if [ -n "$DIARIO" ]; then
    "$TMP/bin/fila-admin" restauracao reconciliar -diario "$DIARIO" -sem-pix ${DESDE:+-desde "$DESDE"}
  else
    echo "  (sem pasta do diário: use 'fila-admin diario baixar' para trazê-lo do bucket)"
    "$TMP/bin/fila-admin" restauracao reconciliar -sem-diario -sem-pix ${DESDE:+-desde "$DESDE"}
  fi
  "$TMP/bin/fila-admin" restauracao liberar
fi

passo "indicadores e alertas da cópia restaurada"
"$TMP/bin/fila-admin" verificar || true
docker exec "$NOME" psql -U ensaio -d fila -Atc \
  "SELECT 'lojas: '||count(*) FROM establishment UNION ALL SELECT 'contas: '||count(*) FROM account UNION ALL SELECT 'comandas: '||count(*) FROM service_order"
n="$(find "$TMP/correio" -type f 2>/dev/null | wc -l)"
echo "e-mails gerados durante o ensaio (gravados em arquivo, NADA enviado): $n"
passo "ensaio concluído; apagando banco descartável e arquivos temporários"
