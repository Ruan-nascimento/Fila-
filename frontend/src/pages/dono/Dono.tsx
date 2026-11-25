import { useState, type ReactNode } from "react";
import { Link, Navigate } from "react-router";
import { ChevronRight, LogOut, RefreshCw, Store } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useSession } from "../../session/context";
import { Button, Loading, Notice, PageHeader } from "../../ui/ui";
import { Entrar } from "../Entrar";
import { ativarModoDono, lojasDoDono } from "./modo";

/**
 * Entrada do dono (/dono), aberta pelo programa Fila Gestão. Usa o mesmo login e a mesma gestão
 * do site; aqui só se oferecem as lojas em que a conta tem vínculo ativo de dono. Sessão de equipe
 * sem vínculo de dono não basta: a tela explica e oferece “Trocar de conta”.
 */
export function AcessoDono() {
  const { state, refresh } = useSession();
  // Antes do primeiro desenho: as telas de gestão abertas depois já encontram o modo ligado.
  useState(ativarModoDono);

  if (state.status === "loading") return <Loading />;
  if (state.status === "offline")
    return (
      <Notice kind="erro" title="Não foi possível falar com o servidor.">
        <p>Confira a internet deste computador e tente de novo.</p>
        <p>
          <Button variant="secondary" icon={RefreshCw} onClick={() => void refresh()}>
            Tentar novamente
          </Button>
        </p>
      </Notice>
    );

  const { session } = state.session;
  if (!session)
    return (
      <Entrar
        equipe
        destino="/dono"
        titulo="Fila Gestão"
        descricao="Acesso do dono da barbearia. Entre com a conta de dono da loja."
      />
    );

  if (session.kind !== "staff")
    return (
      <NaoElegivel titulo="Esta conta entrou como cliente.">
        <p>Para gerenciar a loja, entre de novo com a conta de dono.</p>
      </NaoElegivel>
    );

  const lojas = lojasDoDono(state.session);
  if (lojas.length === 0)
    return (
      <NaoElegivel titulo="Esta conta não é dona de nenhuma loja.">
        <p>
          O Fila Gestão é para o dono da barbearia. Profissionais continuam atendendo pelo navegador, em{" "}
          <strong>Gestão</strong>.
        </p>
      </NaoElegivel>
    );
  if (lojas.length === 1) return <Navigate to={`/gestao/${lojas[0]?.id}`} replace />;

  return (
    <section aria-labelledby="titulo-dono" className="grid gap-4">
      <PageHeader id="titulo-dono" title="Fila Gestão" description="Escolha a loja que vai gerenciar." />
      <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
        {lojas.map((l) => (
          <li key={l.id}>
            <Link
              to={`/gestao/${l.id}`}
              className="flex min-h-11 items-center gap-3 px-4 py-3 text-fg no-underline hover:bg-surface-2"
            >
              <Store aria-hidden="true" className="size-5 text-fg-muted" />
              <span className="flex-1">
                <span className="block font-semibold">{l.name}</span>
                <span className="text-sm text-fg-muted">Dono</span>
              </span>
              <ChevronRight aria-hidden="true" className="size-5 text-fg-muted" />
            </Link>
          </li>
        ))}
      </ul>
      <TrocarDeConta variant="secondary" />
    </section>
  );
}

/** Conta sem direito ao modo dono (ou loja aberta por endereço sem vínculo de dono). */
export function NaoElegivel({
  titulo,
  children,
  verLojas,
}: {
  titulo: string;
  children: ReactNode;
  verLojas?: boolean;
}) {
  return (
    <section aria-labelledby="titulo-nao-elegivel" className="grid gap-4">
      <Notice kind="erro" title={<span id="titulo-nao-elegivel">{titulo}</span>}>
        {children}
      </Notice>
      <div className="flex flex-wrap gap-3">
        <TrocarDeConta />
        {verLojas && (
          <Link to="/dono" className="inline-flex min-h-11 items-center font-semibold">
            Ver minhas lojas
          </Link>
        )}
      </div>
    </section>
  );
}

/** Sai da conta atual e volta para o login do dono (o modo dono continua ligado nesta janela). */
function TrocarDeConta({ variant = "primary" }: { variant?: "primary" | "secondary" }) {
  const { refresh } = useSession();
  const [busy, setBusy] = useState(false);
  const [erro, setErro] = useState<string | null>(null);
  async function sair() {
    setBusy(true);
    setErro(null);
    const out = await call(() => api.POST("/v1/auth/logout"));
    setBusy(false);
    if (!out.ok && out.problem?.status !== 401) {
      setErro(problemMessage(out.problem, out.offline));
      return;
    }
    await refresh();
  }
  return (
    <div className="grid gap-2">
      <Button variant={variant} icon={LogOut} disabled={busy} onClick={() => void sair()}>
        {busy ? "Saindo…" : "Trocar de conta"}
      </Button>
      {erro && <Notice kind="erro">{erro}</Notice>}
    </div>
  );
}
