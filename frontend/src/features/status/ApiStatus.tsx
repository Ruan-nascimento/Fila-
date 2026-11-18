import { useEffect, useState } from "react";
import { TriangleAlert } from "lucide-react";
import { api } from "../../api/client";

type Status = "checking" | "ready" | "not_ready" | "offline";

const labels: Record<Status, string> = {
  checking: "Verificando a API…",
  ready: "API pronta",
  not_ready: "API no ar, mas ainda não está pronta (banco ou migrações)",
  offline: "Sem conexão com a API",
};

/**
 * Mostra se a API responde ao readiness (operação getReadiness do contrato). Com `quietWhenReady`,
 * só aparece quando há problema: a tela inicial não carrega um indicador permanente de "online".
 * A região viva é sempre a mesma: o leitor de tela ouve a mudança, não um elemento novo.
 */
export function ApiStatus({ quietWhenReady = false }: { quietWhenReady?: boolean }) {
  const [status, setStatus] = useState<Status>("checking");

  useEffect(() => {
    const controller = new AbortController();
    api
      .GET("/healthz/ready", { signal: controller.signal })
      .then(({ response }) => setStatus(response.ok ? "ready" : "not_ready"))
      .catch(() => {
        if (!controller.signal.aborted) setStatus("offline");
      });
    return () => controller.abort();
  }, []);

  const problem = status === "not_ready" || status === "offline";
  const hidden = quietWhenReady && !problem;
  return (
    <p
      role="status"
      aria-live="polite"
      className={
        hidden
          ? "sr-only"
          : problem
            ? "m-0 flex items-start gap-2 rounded-xl border border-warning/40 bg-warning-soft px-4 py-3 text-left text-sm"
            : "m-0 text-sm text-fg-muted"
      }
    >
      {problem && <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-warning" />}
      {hidden ? "" : labels[status]}
    </p>
  );
}
