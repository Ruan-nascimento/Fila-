#!/usr/bin/env bash
# Guarda um segredo no arquivo privado do ambiente, digitado SEM eco (não vai para a tela,
# para o histórico do terminal nem para argumentos de processo).
#
#   scripts/segredo.sh <ambiente|geral> NOME_DA_VARIAVEL
#
# Arquivo: ~/.config/fila-v1/<ambiente>.env (pasta 700, arquivo 600, fora do Git).
set -euo pipefail
AMB="${1:?uso: segredo.sh <ambiente|geral> NOME}"
NOME="${2:?informe o nome da variável}"
[[ "$NOME" =~ ^[A-Z][A-Z0-9_]{1,63}$ ]] || { echo "nome inválido"; exit 2; }
[[ "$AMB" =~ ^[a-z]{3,20}$ ]] || { echo "ambiente inválido"; exit 2; }
DIR="$HOME/.config/fila-v1"
ARQ="$DIR/$AMB.env"
umask 077
mkdir -p "$DIR" && chmod 700 "$DIR"
touch "$ARQ" && chmod 600 "$ARQ"
VALOR=""
read -rsp "Cole o valor de $NOME e tecle Enter (nada aparece na tela): " VALOR || true
printf '\n'
VALOR="$(printf '%s' "$VALOR" | tr -d '\r\n' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
[ -n "$VALOR" ] || { echo "vazio: nada foi gravado"; exit 1; }
tmp="$(mktemp "$DIR/.tmp.XXXX")"
grep -vE "^$NOME=" "$ARQ" > "$tmp" || true
printf '%s=%s\n' "$NOME" "$VALOR" >> "$tmp"
mv "$tmp" "$ARQ"
unset VALOR
echo "gravado: $NOME em $ARQ (o valor não é exibido)"
