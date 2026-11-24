#!/usr/bin/env bash
# Teste do segundo plano no Linux (ADR-0037), sem tocar na sessão da pessoa:
#   - X aninhado (Xephyr) e D-Bus isolado (dbus-run-session): nada de notificação ou bandeja real;
#   - painel falso local (servidor_sse.py) com fluxo SSE a cada 2 s;
#   - build de DEPURAÇÃO com ganchos por tempo (FILA_TESTE_FECHAR_APOS / FILA_TESTE_SAIR_APOS)
#     que passam pelo mesmo caminho do X e do "Sair do Fila".
# Verifica: a janela fecha → processo continua; a página escondida segue recebendo eventos
# (visibilidade "hidden"); segunda instância não cria outro processo; "Sair do Fila" encerra.
# Mede CPU e memória com a janela escondida. Uso: testes/segundo-plano.sh [segundos-escondida]
set -euo pipefail
cd "$(dirname "$0")/.."
ESCONDIDA=${1:-60}
PORTA=58731
DISP=:77
TMP=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$TMP"' EXIT

echo "== build de depuração apontando para http://127.0.0.1:$PORTA"
(cd src-tauri && FILA_PAINEL_URL="http://127.0.0.1:$PORTA" cargo build -q 2>&1 | grep -v "^warning" || true)
BIN="src-tauri/target/debug/fila-gestao"

python3 testes/servidor_sse.py "$PORTA" &
Xephyr "$DISP" -screen 800x600 -nolisten tcp -br >/dev/null 2>&1 &
sleep 2

# Preferência ligada só nesta configuração temporária (XDG_CONFIG_HOME isolado).
export XDG_CONFIG_HOME="$TMP/config" XDG_DATA_HOME="$TMP/data" XDG_CACHE_HOME="$TMP/cache"
mkdir -p "$XDG_CONFIG_HOME/br.fila.gestao"
echo '{"segundo_plano":true,"som":true,"explicado":true}' > "$XDG_CONFIG_HOME/br.fila.gestao/segundo-plano.json"

FECHAR=15
SAIR=$((FECHAR + ESCONDIDA + 10))
dbus-run-session -- env DISPLAY="$DISP" GDK_BACKEND=x11 WAYLAND_DISPLAY= \
  FILA_TESTE_FECHAR_APOS=$FECHAR FILA_TESTE_SAIR_APOS=$SAIR bash -c '
  "$0" & APP=$!
  mapa() { local id; id=$(xwininfo -root -tree 2>/dev/null | grep "\"Fila Gestão" | head -1 | awk "{print \$1}"); [ -n "$id" ] && xwininfo -id "$id" | sed -n "s/.*Map State: //p" || echo "sem janela"; }
  sleep 8; echo "== antes de fechar: janela $(mapa)"
  sleep '"$((FECHAR - 3))"'
  echo "== janela fechada há 5 s: janela $(mapa); processo vivo? $(kill -0 $APP 2>/dev/null && echo sim || echo NÃO)"
  # CPU instantânea (média do intervalo) e memória somando o processo e os filhos do WebKit.
  arvore() { echo $APP $(pgrep -P $APP); }
  jiffies() { local t=0; for p in $(arvore); do [ -r /proc/$p/stat ] && t=$((t + $(awk "{print \$14 + \$15}" /proc/$p/stat))); done; echo $t; }
  rss() { local t=0; for p in $(arvore); do [ -r /proc/$p/status ] && t=$((t + $(awk "/VmRSS/ {print \$2}" /proc/$p/status))); done; echo $t; }
  HZ=$(getconf CLK_TCK); PASSO='"$((ESCONDIDA / 4))"'
  echo "== escondida: CPU média por intervalo de ${PASSO}s e memória total (processo + WebKit)"
  for i in 1 2 3 4; do a=$(jiffies); sleep $PASSO; b=$(jiffies); awk -v d=$((b - a)) -v hz=$HZ -v s=$PASSO -v r=$(rss) "BEGIN {printf \"   cpu %.2f%% de um núcleo  rss %.0f MiB\n\", 100*d/hz/s, r/1024}"; done
  echo "== segunda instância (deve só reabrir a existente e sair)"
  timeout 15 "$0"; echo "   segunda instância terminou com código $?"; sleep 2
  echo "   depois dela: janela $(mapa)"
  wait $APP; echo "== processo encerrou com código $? (Sair do Fila)"
' "$BIN"

echo "== eventos vistos pela página"
curl -s "http://127.0.0.1:$PORTA/log" | python3 -c '
import json,sys
log=json.load(sys.stdin)
seen=[x for x in log if x["kind"]=="seen"]
hid=[x for x in seen if x["vis"]=="hidden"]
print(f"   eventos recebidos: {len(seen)}; com a página escondida: {len(hid)}")
if hid: print("   primeiro escondido: id %s; último: id %s" % (hid[0]["id"], hid[-1]["id"]))
ticks=[x for x in log if x["kind"]=="tick" and x["vis"]=="hidden"]
print(f"   temporizadores de 5 s com a página escondida: {len(ticks)}")
sys.exit(0 if len(hid) >= 5 else 1)
'
