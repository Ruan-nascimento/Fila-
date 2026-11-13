import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useSession } from "../session/context";
import { Notice } from "../ui/ui";
import { StreamClient, type AudienceKind, type StreamMessage, type StreamStatus } from "./client";
import { StreamContext, useStreamStatus } from "./context";

/**
 * Abre UMA conexão SSE para a página (loja ou gestão) e repassa as mensagens às telas. Os
 * componentes nunca abrem conexão própria. Se a sessão for encerrada no servidor, relê a sessão;
 * os canais mudam e o fluxo é reaberto com as permissões novas.
 */
export function StreamProvider({
  estId,
  channels,
  children,
}: {
  estId: string;
  channels: AudienceKind[];
  children: ReactNode;
}) {
  const { refresh } = useSession();
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const listeners = useRef(new Set<(m: StreamMessage) => void>());
  const key = channels.join(",");

  useEffect(() => {
    const client = new StreamClient({
      estId,
      channels: key.split(",") as AudienceKind[],
      onStatus: setStatus,
      onMessage: (m) => listeners.current.forEach((l) => l(m)),
      onSessionEnded: () => void refresh(),
    });
    client.start();
    return () => client.stop();
  }, [estId, key, refresh]);

  const subscribe = useCallback((l: (m: StreamMessage) => void) => {
    listeners.current.add(l);
    return () => {
      listeners.current.delete(l);
    };
  }, []);
  const value = useMemo(() => ({ status, subscribe }), [status, subscribe]);
  return <StreamContext.Provider value={value}>{children}</StreamContext.Provider>;
}

/** Aviso de conexão: sem tempo real, os dados podem estar desatualizados (R-WEB-03). */
export function StreamStatusNotice() {
  const status = useStreamStatus();
  if (status === "live") return null;
  if (status === "unsupported")
    return <Notice kind="info">Este navegador não recebe atualizações automáticas. Use “Atualizar”.</Notice>;
  return (
    <Notice kind="info">
      {status === "connecting"
        ? "Conectando para receber atualizações…"
        : "Reconectando… os dados podem estar desatualizados."}
    </Notice>
  );
}
