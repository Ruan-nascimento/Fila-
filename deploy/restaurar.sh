#!/usr/bin/env bash
# Restaura um backup (docs/operacao/backup-restauracao.md). Mede o tempo de cada passo.
#
#   deploy/restaurar.sh /etc/fila/producao.env fila-AAAAmmddTHHMMSSZ "motivo"
#
#   1. para API e worker (o site responde "indisponível" até o passo 5);
#   2. restaura num banco novo e troca pelo atual (o atual fica guardado como fila_antes_…);
#   3. migra (o backup pode ser de uma versão anterior) e aplica as permissões mínimas;
#   4. trava o worker por restauração (o banco novo também seria detectado sozinho);
#   5. sobe API e worker (o worker fica TRAVADO);
#   6. mostra o próximo passo: conciliar com o provedor e liberar (o operador confere antes).
set -euo pipefail

ENVF="${1:?uso: restaurar.sh <arquivo de ambiente> <backup> <motivo>}"
BACKUP="${2:?informe o backup (nome sem extensão)}"
MOTIVO="${3:?informe o motivo (vai para a auditoria)}"
AQUI="$(cd "$(dirname "$0")" && pwd)"
# Git Bash no Windows (exercício local): caminho no formato que o docker.exe entende.
command -v cygpath >/dev/null 2>&1 && AQUI="$(cygpath -m "$AQUI")"
set -a
# shellcheck disable=SC1090
. "$ENVF"
set +a
DC=(docker compose -f "$AQUI/compose.yaml" ${FILA_COMPOSE_EXTRA:+-f "$FILA_COMPOSE_EXTRA"} --env-file "$ENVF" --profile ferramentas)
[ "${FILA_BANCO_LOCAL:-1}" = "1" ] && DC+=(--profile banco-local)
t0=$(date +%s)
passo() { echo "[$(( $(date +%s) - t0 ))s] $*"; }

passo "parando API e worker"
"${DC[@]}" stop api worker
passo "restaurando $BACKUP"
"${DC[@]}" run --rm restore "$BACKUP"
passo "migrações e permissões"
"${DC[@]}" run --rm migrate up
"${DC[@]}" run --rm migrate permissoes -app fila_app
passo "travando o worker por restauração"
"${DC[@]}" run --rm fila-admin restauracao travar -motivo "$MOTIVO"
passo "subindo API e worker (worker travado)"
"${DC[@]}" up -d --no-deps --wait api
"${DC[@]}" up -d --no-deps worker
passo "pronto para conciliar. Agora:"
cat <<EOF
  ${DC[*]} run --rm fila-admin restauracao reconciliar -diario /var/lib/fila/diario
  (confira o relatório e as pendências no painel do dono)
  ${DC[*]} run --rm fila-admin restauracao liberar
EOF
