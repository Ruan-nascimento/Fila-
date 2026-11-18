import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useSession } from "../session/context";
import { RefreshCw } from "lucide-react";
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

/**
 * Aviso de conexão: sem tempo real, os dados podem estar desatualizados (R-WEB-03). Durante a
 * reconexão a tela continua mostrando a última informação conhecida, marcada como tal; ao
 * reconectar, o snapshot do servidor reconcilia o estado.
 */
export function StreamStatusNotice() {
  const status = useStreamStatus();
  if (status === "live") return null;
  if (status === "unsupported")
    return (
      <Notice kind="info">
        <p>Este navegador não recebe atualizações automáticas. Use “Atualizar”.</p>
      </Notice>
    );
  if (status === "connecting")
    return (
      <p role="status" className="m-0 flex items-center gap-2 text-sm text-fg-muted">
        <RefreshCw aria-hidden="true" className="size-4 animate-spin" />
        Conectando para receber atualizações…
      </p>
    );
  return (
    <Notice kind="aviso" title="Reconectando…">
      <p>
        A conexão com a loja caiu. Os dados abaixo são a última informação conhecida e podem estar
        desatualizados.
      </p>
    </Notice>
  );
}
