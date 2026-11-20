import { useEffect, useState } from "react";
import { ChartColumnBig } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useSession } from "../../session/context";
import { cx } from "../../ui/cx";
import { Notice, PageHeader } from "../../ui/ui";
import { noProgramaDesktop } from "./modo";

/** Endereço que o programa intercepta (nunca é carregado dentro da janela do programa). */
export const ROTA_DASHBOARD = "/dono/painel-analitico";

/**
 * Atalho "Dashboard" da navegação do dono, só na janela do programa e só para a sessão que ocupa a
 * vaga de dono. Pede ao servidor um ticket de 60 s (com o cookie desta sessão) e navega para o
 * endereço que o componente nativo intercepta: ele troca o ticket, com a prova da chave da
 * instalação, por um acesso preso à MESMA sessão, e abre a página local de gráficos. O cookie
 * HttpOnly nunca sai da WebView. Ticket no fragmento (#): não vai à rede nem a logs do servidor.
 */
export function BotaoDashboard({ estId, compact }: { estId: string; compact?: boolean }) {
  const { state } = useSession();
  const [busy, setBusy] = useState(false);
  const [erro, setErro] = useState<string | null>(null);
  if (!noProgramaDesktop()) return null;
  if (state.status !== "ready" || !state.session.session?.owner_authority) return null;
  async function abrir() {
    setBusy(true);
    setErro(null);
    const out = await call(() => api.POST("/v1/desktop/link-tickets"));
    setBusy(false);
    if (!out.ok) {
      setErro(problemMessage(out.problem, out.offline));
      return;
    }
    const f = new URLSearchParams({ t: out.data.data.ticket, est: estId });
    window.location.assign(`${ROTA_DASHBOARD}#${f.toString()}`);
  }
  return (
    <div className="grid gap-1">
      <button
        type="button"
        onClick={() => void abrir()}
        disabled={busy}
        className={cx(
          "flex min-h-11 w-full items-center gap-3 rounded-lg px-3 text-sm font-semibold text-accent hover:bg-surface-2",
          compact && "justify-center",
        )}
      >
        <ChartColumnBig aria-hidden="true" className="size-5" />
        {busy ? "Abrindo…" : "Dashboard"}
      </button>
      {erro && <Notice kind="erro">{erro}</Notice>}
    </div>
  );
}

/**
 * O endereço do Dashboard aberto num navegador comum (ou digitado): não há gráficos aqui e nenhuma
 * consulta analítica é feita. O fragmento com o ticket é apagado do histórico.
 */
export function DashboardSoNoPrograma() {
  useEffect(() => {
    if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
  }, []);
  return (
    <section aria-labelledby="titulo-dash" className="grid gap-4">
      <PageHeader id="titulo-dash" title="Dashboard" description="Gráficos da loja para o dono." />
      <Notice kind="info" title="O Dashboard abre só no programa Fila Gestão, num computador autorizado.">
        <p>No navegador, a gestão da loja continua disponível normalmente.</p>
      </Notice>
    </section>
  );
}
