import { useEffect, useState } from "react";
import { Link } from "react-router";
import { modoDono } from "../pages/dono/modo";
import { useSession } from "../session/context";
import { Notice } from "./ui";

/**
 * A sessão acabou (expirou, saiu em outro aparelho, senha trocada): o servidor respondeu 401 a uma
 * ação. Marca a sessão como encerrada (o aviso fica no topo da página e continua mesmo se esta tela
 * sumir) e relê a sessão (o menu muda). Nada foi aplicado.
 */
export function SessaoExpirada() {
  const { refresh, markExpired } = useSession();
  useEffect(() => {
    markExpired();
    void refresh();
    // O aviso fica no topo (AvisoSessaoEncerrada): leva a pessoa até ele.
    window.scrollTo?.({ top: 0 });
  }, [refresh, markExpired]);
  return null;
}

/** Aviso no topo da página enquanto a sessão encerrada durante o uso não for retomada. */
export function AvisoSessaoEncerrada() {
  const { state, expired } = useSession();
  if (state.status === "ready" && state.session.account) return null;
  // Sessão única do dono: o servidor diz quando o cookie deixou de valer porque a conta entrou em
  // outro aparelho (vale também ao voltar de suspensão ou recuperar a conexão: a releitura da
  // sessão traz o motivo). As telas de gestão saem de cena e levam junto os dados que mostravam.
  const motivo = state.status === "ready" ? state.session.ended_reason : undefined;
  if (motivo === "owner_session_replaced" || motivo === "owner_session_migration")
    return (
      <Notice kind="erro" title="Sua sessão foi encerrada porque você entrou em outro aparelho.">
        <p>
          {motivo === "owner_session_migration"
            ? "Com a atualização do sistema, só uma sessão de dono fica ativa por conta. "
            : "Nada desta tela foi alterado depois disso. "}
          <Link to={modoDono() ? "/dono" : "/entrar?como=equipe"}>Entrar de novo</Link> para continuar aqui.
        </p>
      </Notice>
    );
  if (!expired) return null;
  return (
    <Notice kind="erro" title="Sua sessão terminou e a ação não foi feita.">
      <p>
        <Link to={modoDono() ? "/dono" : "/entrar"}>Entre de novo</Link> para continuar.
      </p>
    </Notice>
  );
}

/**
 * Aviso de aparelho sem internet (eventos online/offline do navegador). Ações feitas enquanto isso
 * falham com aviso claro; ao repetir a mesma ação, a chave de idempotência é a mesma e o servidor
 * não aplica duas vezes.
 */
export function AvisoSemInternet() {
  const [online, setOnline] = useState(() => (typeof navigator === "undefined" ? true : navigator.onLine));
  useEffect(() => {
    const on = () => setOnline(true);
    const off = () => setOnline(false);
    window.addEventListener("online", on);
    window.addEventListener("offline", off);
    return () => {
      window.removeEventListener("online", on);
      window.removeEventListener("offline", off);
    };
  }, []);
  if (online) return null;
  return (
    <Notice kind="erro" title="Sem internet neste aparelho.">
      <p>
        O que já foi confirmado na tela está guardado; quando a conexão voltar, repita a última ação (ela não
        será aplicada duas vezes).
      </p>
    </Notice>
  );
}

/** Avisos que valem para qualquer tela (topo do conteúdo). */
export function AvisosGerais() {
  return (
    <div className="grid gap-3 empty:hidden">
      <AvisoSemInternet />
      <AvisoSessaoEncerrada />
    </div>
  );
}
