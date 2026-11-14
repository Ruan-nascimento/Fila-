import { useEffect, useState } from "react";
import { Link } from "react-router";
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

function AvisoSessaoTerminou() {
  return (
    <Notice kind="erro">
      Sua sessão terminou e a ação não foi feita. <Link to="/entrar">Entre de novo</Link> para continuar.
    </Notice>
  );
}

/** Aviso no topo da página enquanto a sessão encerrada durante o uso não for retomada. */
export function AvisoSessaoEncerrada() {
  const { state, expired } = useSession();
  if (!expired || (state.status === "ready" && state.session.account)) return null;
  return <AvisoSessaoTerminou />;
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
    <Notice kind="erro">
      Sem internet neste aparelho. O que já foi confirmado na tela está guardado; quando a conexão voltar,
      repita a última ação (ela não será aplicada duas vezes).
    </Notice>
  );
}
