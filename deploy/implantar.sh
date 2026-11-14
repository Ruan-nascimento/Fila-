#!/usr/bin/env bash
# Implanta uma versão nova sem perder fila (docs/operacao/implantacao.md).
#
#   deploy/implantar.sh /etc/fila/producao.env <versao>
#
# Ordem (cada passo para tudo se falhar; nada é apagado):
#   1. constrói as imagens da versão (ou usa as já publicadas, com FILA_SEM_BUILD=1);
#   2. backup ANTES de migrar (reversão de migração = restaurar este backup);
#   3. migrações (só para frente, compatíveis com a versão anterior) e permissões mínimas;
#   4. troca a API (o Caddy segura as requisições por até 20 s) e depois o worker;
#   5. confere a saúde pela porta pública.
# Reversão da APLICAÇÃO: rode de novo com a versão anterior (o banco migrado continua
# compatível com ela). Reversão de MIGRAÇÃO com defeito: deploy/restaurar.sh com o backup do passo 2.
set -euo pipefail

ENVF="${1:?uso: implantar.sh <arquivo de ambiente> <versao>}"
VERSAO="${2:?informe a versão (ex.: o commit)}"
AQUI="$(cd "$(dirname "$0")" && pwd)"
# Git Bash no Windows (exercício local): caminho no formato que o docker.exe entende.
command -v cygpath >/dev/null 2>&1 && AQUI="$(cygpath -m "$AQUI")"
RAIZ="$(cd "$AQUI/.." && pwd)"
command -v cygpath >/dev/null 2>&1 && RAIZ="$(cygpath -m "$RAIZ")"
set -a
# shellcheck disable=SC1090
. "$ENVF"
set +a
export FILA_IMAGEM_BACKEND="fila-backend:$VERSAO" FILA_IMAGEM_WEB="fila-web:$VERSAO"
DC=(docker compose -f "$AQUI/compose.yaml" ${FILA_COMPOSE_EXTRA:+-f "$FILA_COMPOSE_EXTRA"} --env-file "$ENVF")
PERFIS=(--profile ferramentas)
[ "${FILA_BANCO_LOCAL:-1}" = "1" ] && PERFIS+=(--profile banco-local)
t0=$(date +%s)
passo() { echo "[$(( $(date +%s) - t0 ))s] $*"; }

if [ "${FILA_SEM_BUILD:-0}" != "1" ]; then
	passo "construindo imagens $VERSAO"
	docker build -q -f "$AQUI/Dockerfile.backend" --build-arg VERSAO="$VERSAO" -t "$FILA_IMAGEM_BACKEND" "$RAIZ" >/dev/null
	docker build -q -f "$AQUI/Dockerfile.web" -t "$FILA_IMAGEM_WEB" "$RAIZ" >/dev/null
fi

if [ "${FILA_BANCO_LOCAL:-1}" = "1" ]; then
	passo "banco"
	"${DC[@]}" "${PERFIS[@]}" up -d --wait postgres
fi
if "${DC[@]}" "${PERFIS[@]}" ps --status running --services 2>/dev/null | grep -qx api; then
	passo "backup antes de migrar"
	"${DC[@]}" "${PERFIS[@]}" run --rm backup
else
	passo "primeira implantação: sem backup prévio"
fi

passo "migrações"
"${DC[@]}" "${PERFIS[@]}" run --rm migrate up
"${DC[@]}" "${PERFIS[@]}" run --rm migrate permissoes -app fila_app

passo "trocando a API"
"${DC[@]}" up -d --no-deps --wait api
passo "trocando o worker"
"${DC[@]}" up -d --no-deps worker
"${DC[@]}" up -d --no-deps --wait web

passo "conferindo pela porta pública"
for _ in $(seq 1 30); do
	if curl -fsS ${FILA_CURL_INSEGURO:+-k} "https://${FILA_DOMINIO}:${FILA_PORTA_HTTPS:-443}/healthz/ready" >/dev/null; then
		passo "ok: versão $VERSAO no ar"
		exit 0
	fi
	sleep 2
done
echo "FALHOU: /healthz/ready não respondeu 200 pela porta pública; veja: docker compose logs api" >&2
exit 1
