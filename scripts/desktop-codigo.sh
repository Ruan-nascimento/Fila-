#!/usr/bin/env bash
# Autorização de computadores do programa "Fila Gestão" (ADR-0027), sem exibir segredos.
#
#   scripts/desktop-codigo.sh <ambiente> emitir "<nome do computador>" <email-do-dono>
#   scripts/desktop-codigo.sh <ambiente> listar <email-do-dono>
#   scripts/desktop-codigo.sh <ambiente> revogar <id-da-instalacao>
#
# O código de ativação aparece SÓ no seu terminal: vale 15 minutos e uma única vez. Digite-o no
# programa, na tela "Autorizar este computador". Não envie o código por chat nem e-mail.
# Lê a configuração de ~/.config/fila-v1/<ambiente>.env (permissão 600).
set -euo pipefail
AMB="${1:?uso: desktop-codigo.sh homologacao|producao emitir|listar|revogar ...}"; ACAO="${2:?ação}"
case "$AMB" in homologacao) ENVV=homologacao; SITE=https://teste.fiveline.pro ;; producao) ENVV=production; SITE=https://app.fiveline.pro ;; *) echo "ambiente inválido"; exit 2 ;; esac
F="$HOME/.config/fila-v1/$AMB.env"; v() { grep -E "^$1=" "$F" | tail -1 | cut -d= -f2-; }
export FILA_ENV="$ENVV" FILA_COOKIE_SECURE=true FILA_LOG_FORMAT=text FILA_LOG_LEVEL=warn \
  FILA_PUBLIC_BASE_URL="$SITE" FILA_ALLOWED_ORIGINS="$SITE" FILA_DATABASE_URL="$(v FILA_DATABASE_URL)" FILA_SECRET_KEY="$(v FILA_SECRET_KEY)" \
  FILA_MAIL_MODE=resend FILA_RESEND_API_KEY="$(v FILA_RESEND_API_KEY)" FILA_MAIL_FROM="Fila <nao-responda@mail.fiveline.pro>" GOTOOLCHAIN=auto
cd "$(dirname "$0")/../backend"
case "$ACAO" in
  emitir)  go run ./cmd/fila-admin desktop emitir-codigo -nome "${3:?nome do computador}" -email "${4:?e-mail do dono}" ;;
  listar)  go run ./cmd/fila-admin desktop listar -email "${3:?e-mail do dono}" ;;
  revogar) go run ./cmd/fila-admin desktop revogar -instalacao "${3:?id da instalação}" ;;
  *) echo "ação inválida (emitir, listar, revogar)"; exit 2 ;;
esac
