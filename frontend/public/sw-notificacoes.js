// Service worker do Fila SÓ para notificações (o Chrome do Android exige mostrar por aqui).
// Não intercepta pedidos, não guarda cache e não recebe push: notifica enquanto a página da loja
// ou a gestão está aberta. Ao tocar na notificação, volta para a aba (ou abre o endereço).
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const alvo = new URL((event.notification.data && event.notification.data.url) || "/", self.location.origin);
  event.waitUntil(
    (async () => {
      const abertas = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      const mesma = abertas.find((c) => new URL(c.url).pathname.startsWith(alvo.pathname)) || abertas[0];
      if (mesma) return mesma.focus();
      return self.clients.openWindow(alvo.href);
    })(),
  );
});
