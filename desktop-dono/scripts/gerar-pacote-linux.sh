#!/usr/bin/env bash
# Gera o pacote do Fila Gestão para Linux Mint 22 / Ubuntu 24.04 (.deb), numa máquina Linux de build.
# Uso (na pasta desktop-dono):
#   scripts/gerar-pacote-linux.sh https://fila.suabarbearia.com.br
# Sem argumento, usa FILA_PAINEL_URL ou a linha de painel.url. Com APPIMAGE=1, gera também o AppImage.
set -euo pipefail
cd "$(dirname "$0")/.."

for cmd in node npm cargo pkg-config; do
  command -v "$cmd" >/dev/null || { echo "Falta '$cmd'. Veja desktop-dono/README.md (Máquina de build Linux)." >&2; exit 2; }
done
if ! pkg-config --exists webkit2gtk-4.1 gtk+-3.0; then
  echo "Faltam as bibliotecas de desenvolvimento. Instale com:" >&2
  echo "  sudo apt install libwebkit2gtk-4.1-dev libgtk-3-dev librsvg2-dev libayatana-appindicator3-dev build-essential" >&2
  exit 2
fi
if [[ $# -ge 1 ]]; then export FILA_PAINEL_URL="$1"; fi
unset FILA_PAINEL_TESTE_LOCAL

# O Dashboard reaproveita estilos e componentes de frontend/src (e as dependências deles).
npm ci --prefix ../frontend
npm ci
npm run build:linux
if [[ "${APPIMAGE:-0}" == "1" ]]; then npm run build:linux:appimage; fi

echo
echo "Pacote gerado:"
ls -1 "$PWD"/src-tauri/target/release/bundle/deb/*.deb
if [[ "${APPIMAGE:-0}" == "1" ]]; then ls -1 "$PWD"/src-tauri/target/release/bundle/appimage/*.AppImage; fi
