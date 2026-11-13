import { Link, NavLink, Navigate, Route, Routes, useParams } from "react-router";
import { useSession } from "../../session/context";
import { StreamProvider, StreamStatusNotice } from "../../stream/StreamProvider";
import { Notice } from "../../ui/ui";
import { Comandas } from "./Comandas";
import { Exclusoes } from "./Exclusoes";
import type { StaffContext } from "./comum";
import { Filas } from "./Filas";
import { LojaConfig } from "./LojaConfig";
import { Profissionais } from "./Profissionais";
import { Servicos } from "./Servicos";

/** Lista as lojas em que a conta tem vínculo de equipe. */
export function GestaoInicio() {
  const { state } = useSession();
  if (state.status !== "ready") return <p role="status">Carregando…</p>;
  const { session, memberships } = state.session;
  if (!session) return <Navigate to="/entrar" replace />;
  if (session.kind !== "staff") {
    return <Notice kind="info">Para gerenciar a loja, saia e entre escolhendo “Equipe da barbearia”.</Notice>;
  }
  const lojas = new Map<string, string>();
  for (const m of memberships) lojas.set(m.establishment_id, m.establishment_name);
  return (
    <section aria-labelledby="titulo-gestao">
      <h2 id="titulo-gestao">Gestão</h2>
      {lojas.size === 0 && <p>Esta conta não tem acesso de equipe ativo.</p>}
      <ul>
        {[...lojas].map(([id, name]) => (
          <li key={id}>
            <Link to={`/gestao/${id}/filas`}>{name}</Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Área da equipe de uma loja. As abas só escondem o que não se aplica; quem decide é o servidor. */
export function GestaoLoja() {
  const { estId = "" } = useParams();
  const { state } = useSession();
  if (state.status !== "ready") return <p role="status">Carregando…</p>;
  const { session, memberships } = state.session;
  if (!session || session.kind !== "staff") return <Navigate to="/gestao" replace />;
  const mine = memberships.filter((m) => m.establishment_id === estId);
  if (mine.length === 0) return <Notice kind="erro">Você não tem acesso a esta loja.</Notice>;
  const ctx: StaffContext = {
    estId,
    estName: mine[0]?.establishment_name ?? "",
    isOwner: mine.some((m) => m.role === "owner"),
    professionalId: mine.find((m) => m.professional_id)?.professional_id ?? null,
  };
  // Endereços completos: dentro de uma rota "/gestao/:estId/*", um caminho relativo é somado ao
  // endereço atual (react-router 7), e "filas" a partir de /filas/x viraria /filas/x/filas…
  const base = `/gestao/${estId}`;
  return (
    // Uma conexão de atualizações para toda a área da equipe desta loja.
    <StreamProvider estId={estId} channels={ctx.isOwner ? ["owner"] : ["professional"]}>
      <section aria-labelledby="titulo-loja-gestao">
        <h2 id="titulo-loja-gestao">{ctx.estName}</h2>
        <StreamStatusNotice />
        <nav aria-label="Seções da gestão">
          <ul className="tabs">
            <li>
              <NavLink to={`${base}/filas`}>Filas</NavLink>
            </li>
            <li>
              <NavLink to={`${base}/comandas`}>Comandas</NavLink>
            </li>
            <li>
              <NavLink to={`${base}/servicos`}>Serviços</NavLink>
            </li>
            {ctx.isOwner && (
              <>
                <li>
                  <NavLink to={`${base}/profissionais`}>Profissionais</NavLink>
                </li>
                <li>
                  <NavLink to={`${base}/loja`}>Loja</NavLink>
                </li>
                <li>
                  <NavLink to={`${base}/exclusoes`}>Exclusões</NavLink>
                </li>
              </>
            )}
          </ul>
        </nav>
        <Routes>
          <Route path="filas" element={<Filas ctx={ctx} />} />
          <Route path="comandas" element={<Comandas ctx={ctx} />} />
          <Route path="servicos" element={<Servicos ctx={ctx} />} />
          <Route
            path="profissionais"
            element={ctx.isOwner ? <Profissionais ctx={ctx} /> : <Navigate to={`${base}/filas`} replace />}
          />
          <Route
            path="loja"
            element={ctx.isOwner ? <LojaConfig ctx={ctx} /> : <Navigate to={`${base}/filas`} replace />}
          />
          <Route
            path="exclusoes"
            element={ctx.isOwner ? <Exclusoes ctx={ctx} /> : <Navigate to={`${base}/filas`} replace />}
          />
          <Route path="*" element={<Navigate to={`${base}/filas`} replace />} />
        </Routes>
      </section>
    </StreamProvider>
  );
}
