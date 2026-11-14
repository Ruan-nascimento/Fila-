#!/bin/sh
# Publica o site desta imagem no volume servido pelo Caddy, sem derrubar nada:
#   1. copia os assets (nomes com hash: nunca colidem) — os da versão anterior FICAM, para quem
#      está com a página antiga aberta continuar carregando o que falta;
#   2. troca o index.html de uma vez (rename atômico no mesmo sistema de arquivos);
#   3. apaga assets com mais de 30 dias (nenhuma página aberta vive tanto).
set -eu
DEST=/site
mkdir -p "$DEST/assets"
cp -R /srv/assets/. "$DEST/assets/"
for f in /srv/*; do
	nome=$(basename "$f")
	case "$nome" in assets | index.html) continue ;; esac
	cp -R "$f" "$DEST/$nome.novo" && rm -rf "${DEST:?}/$nome" && mv "$DEST/$nome.novo" "$DEST/$nome"
done
cp /srv/index.html "$DEST/index.html.novo"
mv "$DEST/index.html.novo" "$DEST/index.html"
find "$DEST/assets" -type f -mtime +30 -delete
echo "site: publicado ($(find /srv/assets -type f | wc -l) assets desta versão)"
