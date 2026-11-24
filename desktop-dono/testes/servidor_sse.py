#!/usr/bin/env python3
"""Servidor local mínimo para o teste de segundo plano do Fila Gestão (só 127.0.0.1).

Imita o painel: /healthz/live responde; /dono é uma página que abre um EventSource em /events e,
a cada evento recebido, avisa /seen com o id do evento e a visibilidade da página. Assim o teste
prova, pelo lado do servidor, que a página escondida continua recebendo o fluxo. Não envia
notificação nenhuma (o teste roda com um D-Bus isolado).
"""
import json, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

LOG = []
LOCK = threading.Lock()
PAGE = b"""<!doctype html><meta charset=utf-8><title>painel de teste</title>
<script>
const es = new EventSource('/events');
es.onmessage = (e) => navigator.sendBeacon('/seen?id=' + e.lastEventId + '&vis=' + document.visibilityState);
setInterval(() => navigator.sendBeacon('/tick?vis=' + document.visibilityState), 5000);
</script><p>painel de teste</p>"""


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/healthz/live":
            self._send(200, b'{"status":"ok"}', "application/json")
        elif u.path in ("/dono", "/"):
            self._send(200, PAGE, "text/html; charset=utf-8")
        elif u.path == "/events":
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            n = 0
            try:
                while True:
                    n += 1
                    self.wfile.write(f"id: {n}\ndata: evento {n}\n\n".encode())
                    self.wfile.flush()
                    time.sleep(2)
            except (BrokenPipeError, ConnectionResetError):
                return
        elif u.path == "/log":
            with LOCK:
                self._send(200, json.dumps(LOG).encode(), "application/json")
        else:
            self._send(404, b"{}", "application/json")

    def do_POST(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        with LOCK:
            LOG.append({"t": round(time.time(), 1), "kind": u.path.strip("/"), "id": q.get("id", [""])[0], "vis": q.get("vis", [""])[0]})
        self._send(204, b"", "text/plain")

    def _send(self, code, body, ctype):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)


if __name__ == "__main__":
    port = int(sys.argv[1])
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
