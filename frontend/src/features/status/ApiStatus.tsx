import { useEffect, useState } from "react";
import { api } from "../../api/client";

type Status = "checking" | "ready" | "not_ready" | "offline";

const labels: Record<Status, { icon: string; text: string }> = {
  checking: { icon: "…", text: "Verificando a API…" },
  ready: { icon: "✓", text: "API pronta" },
  not_ready: { icon: "!", text: "API no ar, mas ainda não está pronta (banco ou migrações)" },
  offline: { icon: "×", text: "Sem conexão com a API" },
};

// Mostra se a API responde ao readiness (operação getReadiness do contrato).
// O estado é informado por ícone e texto, não só por cor.
export function ApiStatus() {
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

  const { icon, text } = labels[status];
  return (
    <p className={`status status--${status}`} role="status" aria-live="polite">
      <span aria-hidden="true" className="status__icon">
        {icon}
      </span>
      {text}
    </p>
  );
}
