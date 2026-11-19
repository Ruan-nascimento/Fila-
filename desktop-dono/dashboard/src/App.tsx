import { useCallback, useEffect, useState, type FormEvent } from "react";
import { KeyRound, LogOut, MonitorSmartphone } from "lucide-react";
import { Button, Field, Loading, Notice } from "@web/ui/ui";
import { Painel } from "./Painel";
import { nativo, noPrograma, type Contexto, type ErroNativo, type EstadoDashboard } from "./nativo";

type Tela =
  | { t: "carregando" }
  | { t: "ativar"; ticket: boolean; erro?: string }
  | { t: "sem_vinculo" }
  | { t: "painel"; ctx: Contexto; loja: string | null }
  | { t: "encerrada"; motivo: string; revogado?: boolean }
  | { t: "erro"; msg: string };

const MOTIVO_TROCA = "Sua sessão foi encerrada porque você entrou em outro aparelho.";

/**
 * Página local do Dashboard. Estados:
 *  1. computador sem chave → ativação com o código do operador;
 *  2. ticket entregue pela gestão → troca pela mesma sessão (vincular);
 *  3. painel com os gráficos (tudo em memória);
 *  4. acesso encerrado → limpa tudo e explica.
 */
export function App() {
  const [tela, setTela] = useState<Tela>({ t: "carregando" });

  const encerrar = useCallback((e: ErroNativo) => {
    // Limpa TODOS os dados privados: o painel inteiro sai da árvore (gráficos, detalhes, caches).
    void nativo.pararFluxo().catch(() => {});
    if (e.code === "owner_session_replaced") setTela({ t: "encerrada", motivo: MOTIVO_TROCA });
    else if (e.code === "desktop_installation_invalid") setTela({ t: "encerrada", motivo: "Este computador não está mais autorizado.", revogado: true });
    else setTela({ t: "encerrada", motivo: e.detail || "O acesso do Dashboard terminou. Abra de novo pela gestão." });
  }, []);

  const iniciar = useCallback(async () => {
    if (!noPrograma()) {
      setTela({ t: "erro", msg: "O Dashboard abre só no programa Fila Gestão." });
      return;
    }
    let st: EstadoDashboard;
    try {
      st = await nativo.estado();
    } catch (e) {
      setTela({ t: "erro", msg: (e as ErroNativo).detail });
      return;
    }
    if (!st.instalacao) {
      setTela({ t: "ativar", ticket: st.ticket_pendente });
      return;
    }
    try {
      if (st.ticket_pendente) {
        const ctx = await nativo.vincular();
        setTela({ t: "painel", ctx, loja: st.loja_pedida });
      } else if (st.vinculado) {
        setTela({ t: "painel", ctx: await nativo.contexto(), loja: null });
      } else setTela({ t: "sem_vinculo" });
    } catch (e) {
      encerrar(e as ErroNativo);
    }
  }, [encerrar]);

  useEffect(() => {
    void iniciar();
  }, [iniciar]);

  switch (tela.t) {
    case "carregando":
      return <Centro><Loading>Abrindo o Dashboard…</Loading></Centro>;
    case "erro":
      return <Centro><Notice kind="erro">{tela.msg}</Notice></Centro>;
    case "ativar":
      return <Centro><Ativacao ticket={tela.ticket} aoAtivar={() => void iniciar()} /></Centro>;
    case "sem_vinculo":
      return (
        <Centro>
          <Notice kind="info" title="Abra o Dashboard pela gestão.">
            <p>Entre como dono na gestão e use o menu “Dashboard”. A mesma sessão vale para os dois.</p>
          </Notice>
          <Button icon={LogOut} onClick={() => void nativo.sair()}>Ir para a gestão</Button>
        </Centro>
      );
    case "encerrada":
      return (
        <Centro>
          <Notice kind="erro" title={tela.motivo}>
            <p>Os números deste computador foram apagados da tela. Entre de novo para continuar aqui.</p>
          </Notice>
          <div className="flex flex-wrap gap-2">
            <Button icon={LogOut} onClick={() => void nativo.sair()}>Ir para o acesso</Button>
            {tela.revogado && (
              <Button variant="secondary" onClick={() => void nativo.esquecer().then(iniciar)}>Ativar de novo com um código</Button>
            )}
          </div>
        </Centro>
      );
    case "painel":
      return tela.ctx.establishments.length === 0 ? (
        <Centro><Notice kind="info">Esta conta não é dona de nenhuma loja ativa.</Notice></Centro>
      ) : (
        <Painel key={tela.ctx.account.id} contexto={tela.ctx} lojaInicial={tela.loja} aoEncerrar={encerrar} />
      );
  }
}

function Centro({ children }: { children: React.ReactNode }) {
  return <main className="mx-auto grid min-h-dvh max-w-lg content-center gap-4 p-6">{children}</main>;
}

function Ativacao({ ticket, aoAtivar }: { ticket: boolean; aoAtivar: () => void }) {
  const [codigo, setCodigo] = useState("");
  const [busy, setBusy] = useState(false);
  const [erro, setErro] = useState<string | null>(null);
  async function enviar(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErro(null);
    try {
      await nativo.ativar(codigo);
      aoAtivar();
    } catch (er) {
      setErro((er as ErroNativo).detail);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-labelledby="titulo-ativar" className="grid gap-4">
      <h1 id="titulo-ativar" className="m-0 flex items-center gap-2 text-xl font-semibold">
        <MonitorSmartphone aria-hidden="true" className="size-6 text-accent" /> Autorizar este computador
      </h1>
      <p className="m-0 text-fg-muted">
        Os gráficos só abrem num computador autorizado. Peça ao responsável pelo sistema um <strong>código de ativação</strong>
        {" "}(vale 15 minutos e uma única vez) e digite abaixo.
        {!ticket && " Depois, abra o Dashboard pelo menu da gestão."}
      </p>
      {erro && <Notice kind="erro">{erro}</Notice>}
      <form onSubmit={(e) => void enviar(e)} className="grid gap-3">
        <Field label="Código de ativação" value={codigo} autoComplete="off" spellCheck={false} placeholder="XXXX-XXXX-XXXX-XXXX"
          onChange={(e) => setCodigo(e.target.value.toUpperCase())} />
        <Button type="submit" icon={KeyRound} disabled={busy || codigo.replace(/[^A-Z0-9]/g, "").length < 16}>
          {busy ? "Ativando…" : "Ativar"}
        </Button>
      </form>
    </section>
  );
}
