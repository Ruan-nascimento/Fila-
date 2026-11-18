import { useCallback, useEffect, useId, useRef, useState } from "react";

/**
 * Painel aberto/fechado no lugar (formulário de ação, confirmação): ao abrir, o foco vai para o
 * painel; ao fechar, volta para o botão que o abriu. Escape fecha.
 */
export function useInlinePanel<T extends string = "aberto">() {
  const [open, setOpen] = useState<T | null>(null);
  const base = useId();
  const triggerId = `${base}-origem`;
  const panelId = `${base}-painel`;
  const wasOpen = useRef(false);
  useEffect(() => {
    if (open) {
      wasOpen.current = true;
      const el = document.getElementById(panelId);
      const target =
        el?.querySelector<HTMLElement>("[data-autofocus]") ??
        el?.querySelector<HTMLElement>("input:not([type=hidden]), textarea, select") ??
        el;
      target?.focus();
    } else if (wasOpen.current) {
      wasOpen.current = false;
      document.getElementById(triggerId)?.focus();
    }
  }, [open, panelId, triggerId]);
  const show = useCallback((which: T) => setOpen(which), []);
  const hide = useCallback(() => setOpen(null), []);
  const onKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      setOpen(null);
    }
  }, []);
  // Ids (não refs): o painel e o botão de origem são achados no DOM só dentro do efeito.
  return { open, show, hide, triggerId, panelId, onKeyDown };
}
